// Package agent is the daemon on every Node (ADR 0001, ADR 0002). It enrolls
// once, then long-polls the Coordinator for its Node's snapshots and applies
// each under commit-confirm: if it cannot complete a fresh round trip to the
// Coordinator within the confirm window, it reverts to the previous snapshot
// and reports the version as apply_failed.
//
// The last confirmed snapshot is kept on disk and re-applied on boot, with no
// expiry, so a Node keeps working while the Coordinator is unreachable.
package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// Applier puts a Node's desired state into effect. Applying the same state
// twice must be harmless.
type Applier interface {
	Apply(ctx context.Context, s protocol.State) error
}

// Reporter is implemented by an Applier that adds to every health report,
// such as the Link layer with its Link key and measurements.
type Reporter interface {
	Report(h *protocol.Health)
}

// Acker is implemented by an Applier that needs to know a health report
// reached the Coordinator, such as the meter, which then drops the usage it
// carried.
type Acker interface {
	Acked(h *protocol.Health)
}

// Fetch fetches a path of the Agent API, such as /v1/sets/{hash}, and
// returns its body.
type Fetch func(ctx context.Context, path string) ([]byte, error)

// FetchUser is implemented by an Applier that fetches content-addressed
// data, such as Destination Sets, from the Coordinator. The Agent hands it
// a Fetch once it holds a certificate.
type FetchUser interface {
	UseFetch(Fetch)
}

// Appliers applies each Applier in turn, and passes on reports and Fetch
// to those that want them.
type Appliers []Applier

func (as Appliers) Apply(ctx context.Context, s protocol.State) error {
	var errs []error
	for _, a := range as {
		errs = append(errs, a.Apply(ctx, s))
	}
	return errors.Join(errs...)
}

func (as Appliers) Report(h *protocol.Health) {
	for _, a := range as {
		if r, ok := a.(Reporter); ok {
			r.Report(h)
		}
	}
}

func (as Appliers) Acked(h *protocol.Health) {
	for _, a := range as {
		if k, ok := a.(Acker); ok {
			k.Acked(h)
		}
	}
}

func (as Appliers) UseFetch(f Fetch) {
	for _, a := range as {
		if u, ok := a.(FetchUser); ok {
			u.UseFetch(f)
		}
	}
}

// Config configures an Agent.
type Config struct {
	// Coordinators are base URLs of the Agent API, such as
	// https://4miz.ir:8443. On failure the Agent moves to the next.
	Coordinators []string
	// CACertPEM is the pinned Coordinator CA certificate.
	CACertPEM []byte
	// StateDir holds the Agent's key, certificate and last confirmed snapshot.
	StateDir string
	// EnrollmentToken is used once, when StateDir holds no certificate.
	EnrollmentToken string
	Applier         Applier
	Version         string
	Log             *slog.Logger
	// Dial, if set, opens connections to the Coordinator.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	ConfirmTimeout time.Duration // default 90 s (ADR 0002)
	HealthInterval time.Duration // default 30 s
	RetryInterval  time.Duration // default 5 s
	RequestTimeout time.Duration // per non-long-poll request; default 10 s
	LongPoll       time.Duration // the Coordinator's long-poll wait; default 60 s

	// MetricsHost is where the monitoring port listens; "" is every
	// address, with nftables letting only the Coordinator VM in.
	MetricsHost string
	// TelegramAPI is where alerts the Coordinator hands over go; default
	// DefaultTelegramAPI.
	TelegramAPI string
	// NodeExporter is node_exporter's URL on localhost, served through
	// /metrics; "" leaves host metrics out.
	NodeExporter string
}

// Files in StateDir.
const (
	keyFile   = "agent.key"
	certFile  = "agent.crt"
	stateFile = "state.json"
)

// persisted is state.json.
type persisted struct {
	// Applied is the last confirmed snapshot.
	Applied *protocol.Snapshot `json:"applied"`
	// Bad lists versions this Agent reverted; it never applies them again.
	Bad []uint64 `json:"bad"`
	// Unreported lists reverted versions the Coordinator has not been told of.
	Unreported []uint64 `json:"unreported"`
}

type agent struct {
	cfg  Config
	log  *slog.Logger
	pool *x509.CertPool

	// healthMu keeps one health report in flight at a time, so reports
	// reach the Coordinator in the order they were made. Otherwise a report
	// stuck behind a cut link could land after a newer one and overwrite it.
	healthMu sync.Mutex

	mu        sync.Mutex
	st        persisted
	current   uint64          // version in effect now, confirmed or not
	state     *protocol.State // the state of that version
	urlIndex  int
	localAddr string
	client    *http.Client
	// lastContact is the last answer from a Coordinator, or the start.
	lastContact   time.Time
	applyFailures uint64 // snapshots reverted since the start

	mon *monitor
}

// Run runs the Agent until ctx ends. It returns early only if the Agent
// cannot start, for example because it has neither a certificate nor an
// enrollment token.
func Run(ctx context.Context, cfg Config) error {
	if len(cfg.Coordinators) == 0 || cfg.Applier == nil || cfg.StateDir == "" {
		return errors.New("agent: Coordinators, Applier and StateDir are required")
	}
	setDefault(&cfg.ConfirmTimeout, 90*time.Second)
	setDefault(&cfg.HealthInterval, 30*time.Second)
	setDefault(&cfg.RetryInterval, 5*time.Second)
	setDefault(&cfg.RequestTimeout, 10*time.Second)
	setDefault(&cfg.LongPoll, 60*time.Second)
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.TelegramAPI == "" {
		cfg.TelegramAPI = DefaultTelegramAPI
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.CACertPEM) {
		return errors.New("agent: no certificate in the pinned CA PEM")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	a := &agent{cfg: cfg, log: cfg.Log, pool: pool, lastContact: time.Now()}
	a.mon = &monitor{a: a}
	defer a.mon.close()

	if err := a.load(); err != nil {
		return err
	}
	if a.st.Applied != nil {
		// Re-apply the last confirmed state before any contact.
		if err := cfg.Applier.Apply(ctx, a.st.Applied.State); err != nil {
			a.log.Error("re-apply persisted snapshot", "version", a.st.Applied.Version, "err", err)
		}
		a.inEffect(a.st.Applied.Version, &a.st.Applied.State)
		a.log.Info("re-applied persisted snapshot", "version", a.current)
	}

	cert, err := a.certificate(ctx)
	if err != nil {
		return err
	}
	a.client = a.httpClient(&cert)
	a.mon.useCert(cert)
	if u, ok := cfg.Applier.(FetchUser); ok {
		u.UseFetch(a.fetch)
	}

	go a.healthLoop(ctx)
	a.pollLoop(ctx)
	return nil
}

func setDefault(d *time.Duration, v time.Duration) {
	if *d == 0 {
		*d = v
	}
}

func (a *agent) load() error {
	b, err := os.ReadFile(filepath.Join(a.cfg.StateDir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &a.st)
}

// save writes state.json atomically. Call with mu held.
func (a *agent) save() error {
	b, err := json.MarshalIndent(a.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(a.cfg.StateDir, stateFile), b, 0o600)
}

// certificate returns the Agent's client certificate, enrolling first if it
// has none. The key is generated here and never leaves the Node.
func (a *agent) certificate(ctx context.Context) (tls.Certificate, error) {
	keyPath, certPath := filepath.Join(a.cfg.StateDir, keyFile), filepath.Join(a.cfg.StateDir, certFile)
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return cert, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	if a.cfg.EnrollmentToken == "" {
		return tls.Certificate{}, errors.New("agent: not enrolled, and no enrollment token given")
	}

	keyPEM, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return tls.Certificate{}, err
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
			return tls.Certificate{}, err
		}
	} else if err != nil {
		return tls.Certificate{}, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return tls.Certificate{}, fmt.Errorf("agent: no PEM block in %s", keyPath)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return tls.Certificate{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "darz-agent"}}, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	req := protocol.EnrollRequest{
		Token: a.cfg.EnrollmentToken,
		CSR:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})),
	}

	client := a.httpClient(nil)
	for {
		var resp protocol.EnrollResponse
		status, err := a.do(ctx, client, http.MethodPost, "/v1/enroll", req, &resp, a.cfg.RequestTimeout)
		if err == nil {
			if err := writeFileAtomic(certPath, []byte(resp.Certificate), 0o644); err != nil {
				return tls.Certificate{}, err
			}
			a.log.Info("enrolled", "node", resp.NodeID)
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
		if status >= 400 && status < 500 {
			return tls.Certificate{}, fmt.Errorf("agent: enrollment refused: %w", err)
		}
		a.log.Warn("enroll", "err", err)
		if !sleep(ctx, a.cfg.RetryInterval) {
			return tls.Certificate{}, ctx.Err()
		}
	}
}

// httpClient returns a client that trusts only the pinned CA and presents
// cert, if given.
func (a *agent) httpClient(cert *tls.Certificate) *http.Client {
	tc := &tls.Config{RootCAs: a.pool, MinVersion: tls.VersionTLS13}
	if cert != nil {
		tc.Certificates = []tls.Certificate{*cert}
	}
	dial := a.cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: tc,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dial(ctx, network, addr)
			if err == nil {
				host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
				a.mu.Lock()
				a.localAddr = host
				a.mu.Unlock()
			}
			return conn, err
		},
		ForceAttemptHTTP2: true,
		IdleConnTimeout:   90 * time.Second,
	}}
}

// do sends one JSON request to the current Coordinator URL, moving to the
// next URL if it fails. It returns the HTTP status, or 0 if none arrived.
func (a *agent) do(ctx context.Context, client *http.Client, method, path string, in, out any, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	a.mu.Lock()
	base := a.cfg.Coordinators[a.urlIndex]
	a.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		a.rotate()
		return 0, err
	}
	defer resp.Body.Close()
	a.contact()
	if resp.StatusCode >= 300 {
		var e protocol.Error
		json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		if resp.StatusCode >= 500 {
			a.rotate()
		}
		return resp.StatusCode, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, e.Error)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// fetch GETs a path and returns the raw body.
func (a *agent) fetch(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*a.cfg.RequestTimeout)
	defer cancel()
	a.mu.Lock()
	base := a.cfg.Coordinators[a.urlIndex]
	a.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.rotate()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// contact notes an answer from a Coordinator.
func (a *agent) contact() {
	a.mu.Lock()
	a.lastContact = time.Now()
	a.mu.Unlock()
}

// inEffect records the version and state now in effect, and opens or
// shuts the monitoring port to match. Call with mu held, or before the
// Agent's goroutines start.
func (a *agent) inEffect(version uint64, st *protocol.State) {
	a.current, a.state = version, st
	var mon *protocol.Monitor
	if st != nil {
		mon = st.Monitor
	}
	a.mon.set(mon)
}

func (a *agent) rotate() {
	a.mu.Lock()
	a.urlIndex = (a.urlIndex + 1) % len(a.cfg.Coordinators)
	a.mu.Unlock()
}

// sendHealth reports health once, including any unreported reverts, and
// clears those it delivered.
func (a *agent) sendHealth(ctx context.Context) error {
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	a.mu.Lock()
	h := protocol.Health{
		AppliedVersion: a.current,
		AgentVersion:   a.cfg.Version,
		Time:           time.Now().UTC(),
		Addr:           a.localAddr,
		ApplyFailed:    slices.Clone(a.st.Unreported),
	}
	a.mu.Unlock()
	if r, ok := a.cfg.Applier.(Reporter); ok {
		r.Report(&h)
	}
	if _, err := a.do(ctx, a.client, http.MethodPost, "/v1/health", h, nil, a.cfg.RequestTimeout); err != nil {
		return err
	}
	if k, ok := a.cfg.Applier.(Acker); ok {
		k.Acked(&h)
	}
	if len(h.ApplyFailed) > 0 {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.st.Unreported = slices.DeleteFunc(a.st.Unreported, func(v uint64) bool { return slices.Contains(h.ApplyFailed, v) })
		return a.save()
	}
	return nil
}

func (a *agent) healthLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.HealthInterval)
	defer t.Stop()
	for {
		if err := a.sendHealth(ctx); err != nil && ctx.Err() == nil {
			a.log.Warn("health report", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.mon.retry()
		}
	}
}

func (a *agent) pollLoop(ctx context.Context) {
	for ctx.Err() == nil {
		a.mu.Lock()
		unreported := len(a.st.Unreported) > 0
		after := a.current
		a.mu.Unlock()

		// Tell the Coordinator about a revert before asking for more, or it
		// would hand back the same bad version.
		if unreported {
			if err := a.sendHealth(ctx); err != nil {
				a.log.Warn("report reverted snapshot", "err", err)
				sleep(ctx, a.cfg.RetryInterval)
				continue
			}
		}

		var snap protocol.Snapshot
		status, err := a.do(ctx, a.client, http.MethodGet, "/v1/state?after="+strconv.FormatUint(after, 10), nil, &snap,
			a.cfg.LongPoll+a.cfg.RequestTimeout)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				a.log.Warn("poll state", "err", err)
				sleep(ctx, a.cfg.RetryInterval)
			}
		case status == http.StatusNoContent:
		case a.isBad(snap.Version):
			// The Coordinator has not taken in our report yet.
			sleep(ctx, a.cfg.RetryInterval)
		default:
			a.applyAndConfirm(ctx, snap)
		}
	}
}

func (a *agent) isBad(v uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.st.Bad, v)
}

// applyAndConfirm applies snap and keeps it only if a fresh round trip to the
// Coordinator succeeds within the confirm window.
func (a *agent) applyAndConfirm(ctx context.Context, snap protocol.Snapshot) {
	a.mu.Lock()
	prev := a.st.Applied
	a.current = snap.Version
	a.mu.Unlock()

	log := a.log.With("version", snap.Version)
	log.Info("applying snapshot")
	confirmed := false
	err := a.cfg.Applier.Apply(ctx, snap.State)
	a.mu.Lock()
	a.inEffect(snap.Version, &snap.State) // after Apply: nftables guards the port first
	a.mu.Unlock()
	if err != nil {
		log.Error("apply snapshot", "err", err)
	} else {
		deadline := time.Now().Add(a.cfg.ConfirmTimeout)
		for {
			err := a.sendHealth(ctx)
			if err == nil {
				confirmed = true
				break
			}
			log.Warn("confirm snapshot", "err", err)
			if ctx.Err() != nil {
				return // shutting down: on boot the last confirmed snapshot is re-applied
			}
			if time.Until(deadline) <= 0 {
				break
			}
			sleep(ctx, min(a.cfg.RetryInterval, time.Until(deadline)))
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if confirmed {
		a.st.Applied = &snap
		log.Info("snapshot confirmed")
	} else {
		log.Warn("reverting snapshot")
		a.applyFailures++
		var prevState protocol.State
		var prevVersion uint64
		if prev != nil {
			prevState, prevVersion = prev.State, prev.Version
		}
		if err := a.cfg.Applier.Apply(ctx, prevState); err != nil {
			log.Error("revert to previous snapshot", "err", err)
		}
		a.inEffect(prevVersion, &prevState)
		a.st.Bad = append(a.st.Bad, snap.Version)
		if len(a.st.Bad) > 32 {
			a.st.Bad = a.st.Bad[len(a.st.Bad)-32:]
		}
		a.st.Unreported = append(a.st.Unreported, snap.Version)
	}
	if err := a.save(); err != nil {
		log.Error("persist state", "err", err)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
