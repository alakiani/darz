package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/agent"
	"github.com/alakiani/darz/internal/ca"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
	"github.com/alakiani/darz/internal/testutil"
)

// rig is an in-process Coordinator with both listeners up.
type rig struct {
	store    *store.Store
	coord    *coordinator.Coordinator
	caPEM    []byte
	agentURL string
	admin    *testutil.AdminClient
}

func newRig(t *testing.T) *rig {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "darz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := coordinator.New(s, authority, slog.New(slog.DiscardHandler))
	c.LongPoll = 500 * time.Millisecond

	agentSrv := httptest.NewUnstartedServer(c.AgentHandler())
	if agentSrv.TLS, err = c.AgentTLSConfig([]string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	agentSrv.StartTLS()
	t.Cleanup(agentSrv.Close)
	adminSrv := httptest.NewServer(c.AdminHandler())
	t.Cleanup(adminSrv.Close)

	secret, _, err := s.CreateAPIToken(context.Background(), store.ScopeNodesEnroll, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{store: s, coord: c, caPEM: authority.CertPEM(), agentURL: agentSrv.URL,
		admin: &testutil.AdminClient{URL: adminSrv.URL, Token: secret}}
}

// network is a fake link between an Agent and the Coordinator that a test
// (or an Applier) can cut. Cutting it also kills open connections.
type network struct {
	cut   atomic.Bool
	mu    sync.Mutex
	conns []net.Conn
}

func (n *network) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if n.cut.Load() {
		return nil, errors.New("network is cut")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err == nil {
		n.mu.Lock()
		n.conns = append(n.conns, conn)
		n.mu.Unlock()
	}
	return conn, err
}

func (n *network) setCut(cut bool) {
	n.cut.Store(cut)
	if cut {
		n.mu.Lock()
		for _, c := range n.conns {
			c.Close()
		}
		n.conns = nil
		n.mu.Unlock()
	}
}

// applier records every state it applies. A state tagged "cut-contact"
// cuts the network, like a bad firewall change would.
type applier struct {
	net     *network
	mu      sync.Mutex
	applied []protocol.State
}

func (a *applier) Apply(_ context.Context, s protocol.State) error {
	a.mu.Lock()
	a.applied = append(a.applied, s)
	a.mu.Unlock()
	if a.net != nil {
		a.net.setCut(slices.Contains(s.Node.Tags, "cut-contact"))
	}
	return nil
}

func (a *applier) last() (protocol.State, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.applied) == 0 {
		return protocol.State{}, 0
	}
	return a.applied[len(a.applied)-1], len(a.applied)
}

type running struct {
	cancel context.CancelFunc
	done   chan error
}

func (r *running) stop() error {
	r.cancel()
	return <-r.done
}

func (rg *rig) startAgent(t *testing.T, dir, token string, app *applier, net *network) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := agent.Config{
		Coordinators:    []string{rg.agentURL},
		CACertPEM:       rg.caPEM,
		StateDir:        dir,
		EnrollmentToken: token,
		Applier:         app,
		Version:         "test",
		Dial:            net.Dial,
		ConfirmTimeout:  time.Second,
		HealthInterval:  200 * time.Millisecond,
		RetryInterval:   50 * time.Millisecond,
		RequestTimeout:  300 * time.Millisecond,
		LongPoll:        500 * time.Millisecond,
	}
	r := &running{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- agent.Run(ctx, cfg) }()
	t.Cleanup(func() { r.cancel() })
	return r
}

func TestAgentEnrollsAppliesAndReportsHealth(t *testing.T) {
	rg := newRig(t)
	created := rg.admin.CreateNode(t, protocol.CreateNodeRequest{Name: "b1", Roles: []protocol.Role{"bridge"}, Tags: []string{"IR"}})
	app := &applier{}
	rg.startAgent(t, t.TempDir(), created.EnrollmentToken, app, &network{})

	testutil.Eventually(t, 5*time.Second, "the agent confirms version 1", func() bool {
		h := rg.admin.GetNode(t, created.Node.ID).LastHealth
		return h != nil && h.AppliedVersion == 1
	})
	st, _ := app.last()
	if st.Node.ID != created.Node.ID || st.Node.Name != "b1" || !slices.Equal(st.Node.Tags, []string{"IR"}) {
		t.Errorf("applied state = %+v", st)
	}
	h := rg.admin.GetNode(t, created.Node.ID).LastHealth
	if h.AgentVersion != "test" || h.Addr != "127.0.0.1" || h.SeenAddr != "127.0.0.1" {
		t.Errorf("health = %+v", h)
	}
}

func TestSnapshotThatCutsContactIsRevertedAndMarkedBad(t *testing.T) {
	rg := newRig(t)
	ctx := context.Background()
	created := rg.admin.CreateNode(t, protocol.CreateNodeRequest{Name: "b1", Roles: []protocol.Role{"bridge"}})
	id := created.Node.ID
	nw := &network{}
	app := &applier{net: nw}
	rg.startAgent(t, t.TempDir(), created.EnrollmentToken, app, nw)
	testutil.Eventually(t, 5*time.Second, "version 1 confirmed", func() bool {
		h := rg.admin.GetNode(t, id).LastHealth
		return h != nil && h.AppliedVersion == 1
	})

	// Version 2 cuts the Agent off from the Coordinator.
	if err := rg.coord.UpdateNode(ctx, id, []protocol.Role{"bridge"}, []string{"cut-contact"}); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 5*time.Second, "version 2 marked bad", func() bool {
		snap, err := rg.store.LatestSnapshot(ctx, id)
		return err == nil && snap.Version == 2 && snap.Bad
	})
	if st, _ := app.last(); slices.Contains(st.Node.Tags, "cut-contact") {
		t.Fatalf("agent left version 2 in effect: %+v", st)
	}
	if nw.cut.Load() {
		t.Fatal("network is still cut after the revert")
	}
	if h := rg.admin.GetNode(t, id).LastHealth; h.AppliedVersion != 1 {
		t.Errorf("reported applied version = %d, want 1 after the revert", h.AppliedVersion)
	}

	// The bad version is not sent again.
	_, before := app.last()
	time.Sleep(1500 * time.Millisecond)
	if _, after := app.last(); after != before {
		t.Errorf("agent applied %d more states while the only new version was bad", after-before)
	}

	// A further change is a new version, and goes through.
	if err := rg.coord.UpdateNode(ctx, id, []protocol.Role{"bridge"}, []string{"IR"}); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 5*time.Second, "version 3 confirmed", func() bool {
		h := rg.admin.GetNode(t, id).LastHealth
		return h != nil && h.AppliedVersion == 3
	})
}

func TestRestartReappliesPersistedSnapshotWithoutCoordinator(t *testing.T) {
	rg := newRig(t)
	created := rg.admin.CreateNode(t, protocol.CreateNodeRequest{Name: "x1", Roles: []protocol.Role{"exit"}})
	dir := t.TempDir()
	nw := &network{}
	first := rg.startAgent(t, dir, created.EnrollmentToken, &applier{}, nw)
	testutil.Eventually(t, 5*time.Second, "version 1 confirmed", func() bool {
		h := rg.admin.GetNode(t, created.Node.ID).LastHealth
		return h != nil && h.AppliedVersion == 1
	})
	if err := first.stop(); err != nil {
		t.Fatal(err)
	}

	// The Coordinator is unreachable, and the token is spent: the Agent must
	// come up from disk alone.
	nw.setCut(true)
	app := &applier{}
	rg.startAgent(t, dir, "", app, nw)
	testutil.Eventually(t, 2*time.Second, "persisted state re-applied", func() bool {
		st, _ := app.last()
		return st.Node.ID == created.Node.ID
	})
}

func TestEnrollmentTokenWorksOnce(t *testing.T) {
	rg := newRig(t)
	created := rg.admin.CreateNode(t, protocol.CreateNodeRequest{Name: "b1", Roles: []protocol.Role{"bridge"}})
	rg.startAgent(t, t.TempDir(), created.EnrollmentToken, &applier{}, &network{})
	testutil.Eventually(t, 5*time.Second, "first agent enrolled", func() bool {
		return rg.admin.GetNode(t, created.Node.ID).Enrolled
	})

	second := rg.startAgent(t, t.TempDir(), created.EnrollmentToken, &applier{}, &network{})
	select {
	case err := <-second.done:
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("second enrollment: err = %v, want a refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second agent did not give up on a spent token")
	}
}

func TestReenrollmentRetiresTheOldCertificate(t *testing.T) {
	rg := newRig(t)
	req := protocol.CreateNodeRequest{Name: "b1", Roles: []protocol.Role{"bridge"}}
	created := rg.admin.CreateNode(t, req)
	oldDir, newDir := t.TempDir(), t.TempDir()
	old := rg.startAgent(t, oldDir, created.EnrollmentToken, &applier{}, &network{})
	testutil.Eventually(t, 5*time.Second, "old agent enrolled", func() bool {
		return testutil.Exists(filepath.Join(oldDir, "agent.crt"))
	})
	old.stop()

	req.Reenroll = true
	again := rg.admin.CreateNode(t, req)
	if again.EnrollmentToken == "" {
		t.Fatal("reenroll gave no token")
	}
	rg.startAgent(t, newDir, again.EnrollmentToken, &applier{}, &network{})
	testutil.Eventually(t, 5*time.Second, "new agent enrolled", func() bool {
		return testutil.Exists(filepath.Join(newDir, "agent.crt"))
	})

	if got := agentGet(t, rg, oldDir, "/v1/state"); got != http.StatusForbidden {
		t.Errorf("old certificate: status %d, want 403", got)
	}
	if got := agentGet(t, rg, newDir, "/v1/state"); got != http.StatusOK {
		t.Errorf("new certificate: status %d, want 200", got)
	}
}

// agentGet calls the Agent API with the certificate in dir and returns the status.
func agentGet(t *testing.T, rg *rig, dir, path string) int {
	t.Helper()
	resp, err := testutil.AgentClient(t, rg.caPEM, dir).Get(rg.agentURL + path)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
