package link

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AmneziaWG runs each AmneziaWG instance as its own amneziawg-go daemon,
// configured over the daemon's UAPI socket (ADR 0007). The daemon, not the
// Agent, carries packets, so restarting the Agent changes no tunnel.
type AmneziaWG struct {
	Runner Runner
	// SocketDir is where amneziawg-go puts its UAPI sockets.
	SocketDir string
}

// NewAmneziaWG returns an AmneziaWG transport using r and the daemon's
// standard socket directory.
func NewAmneziaWG(r Runner) *AmneziaWG {
	return &AmneziaWG{Runner: r, SocketDir: "/var/run/amneziawg"}
}

// Runner starts and stops amneziawg-go daemons, one per interface.
type Runner interface {
	Start(ctx context.Context, iface string) error
	Stop(ctx context.Context, iface string) error
}

// SystemdRunner runs each daemon as an instance of a templated systemd
// unit, such as darz-awg@.service.
type SystemdRunner struct {
	Unit string // e.g. "darz-awg@%s.service"
}

func (r SystemdRunner) Start(ctx context.Context, iface string) error {
	return systemctl(ctx, "start", fmt.Sprintf(r.Unit, iface))
}

func (r SystemdRunner) Stop(ctx context.Context, iface string) error {
	return systemctl(ctx, "stop", fmt.Sprintf(r.Unit, iface))
}

func systemctl(ctx context.Context, verb, unit string) error {
	if out, err := exec.CommandContext(ctx, "systemctl", verb, unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s %s: %w: %s", verb, unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ExecRunner starts daemons as detached processes, for hosts without
// systemd such as the lab. They survive the Agent and exit when their
// interface is deleted.
type ExecRunner struct {
	Binary string
	LogDir string

	mu    sync.Mutex
	procs map[string]*os.Process
}

func (r *ExecRunner) Start(_ context.Context, iface string) error {
	log, err := os.OpenFile(filepath.Join(r.LogDir, "awg-"+iface+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(r.Binary, "-f", iface)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.procs == nil {
		r.procs = map[string]*os.Process{}
	}
	r.procs[iface] = cmd.Process
	r.mu.Unlock()
	go cmd.Wait()
	return nil
}

func (r *ExecRunner) Stop(_ context.Context, iface string) error {
	r.mu.Lock()
	p := r.procs[iface]
	delete(r.procs, iface)
	r.mu.Unlock()
	if p != nil {
		p.Signal(syscall.SIGTERM)
	}
	return nil
}

func (a *AmneziaWG) socket(iface string) string { return filepath.Join(a.SocketDir, iface+".sock") }

func (a *AmneziaWG) Up(ctx context.Context, c InstanceConfig) error {
	if c.AWG == nil {
		return errors.New("amneziawg: no parameters")
	}
	_, err := a.uapi(c.Iface, "get=1\n\n")
	if err == nil && !linkExists(ctx, c.Iface) {
		// A daemon answers but its interface is not here: it is a stale one
		// whose interface is gone. Start afresh on a new socket.
		os.Remove(a.socket(c.Iface))
		err = errors.New("stale daemon")
	}
	if err != nil {
		if err := a.Runner.Start(ctx, c.Iface); err != nil {
			return err
		}
		if err := a.waitSocket(ctx, c.Iface); err != nil {
			return err
		}
	}
	current, err := a.get(c.Iface)
	if err != nil {
		return err
	}

	var b strings.Builder
	set := func(k string, v any) { fmt.Fprintf(&b, "%s=%v\n", k, v) }
	b.WriteString("set=1\n")
	set("private_key", hex.EncodeToString(c.PrivateKey[:]))
	if c.ListenPort != 0 && current.values["listen_port"] != fmt.Sprint(c.ListenPort) {
		set("listen_port", c.ListenPort)
	}
	p := c.AWG
	for _, kv := range []struct {
		k string
		v any
	}{
		{"jc", p.Jc}, {"jmin", p.Jmin}, {"jmax", p.Jmax},
		{"s1", p.S1}, {"s2", p.S2}, {"s3", p.S3}, {"s4", p.S4},
		{"h1", p.H1}, {"h2", p.H2}, {"h3", p.H3}, {"h4", p.H4},
		{"i1", p.I1}, {"i2", p.I2}, {"i3", p.I3}, {"i4", p.I4}, {"i5", p.I5},
	} {
		if s, ok := kv.v.(string); ok && s == "" {
			continue
		}
		set(kv.k, kv.v)
	}
	peer := hex.EncodeToString(c.PeerKey[:])
	for _, other := range current.peers {
		if other != peer {
			set("public_key", other)
			set("remove", "true")
		}
	}
	set("public_key", peer)
	if c.Endpoint != nil {
		set("endpoint", c.Endpoint.String())
	}
	set("persistent_keepalive_interval", 0) // probes keep the path warm
	set("replace_allowed_ips", "true")
	set("allowed_ip", "0.0.0.0/0")
	b.WriteString("\n")
	if _, err := a.uapi(c.Iface, b.String()); err != nil {
		return err
	}
	return address(ctx, c)
}

func (a *AmneziaWG) waitSocket(ctx context.Context, iface string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := a.uapi(iface, "get=1\n\n"); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("amneziawg %s: daemon did not come up: %w", iface, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Down stops the daemon first, so a supervisor does not restart it and
// recreate the interface, then deletes whatever interface is left.
func (a *AmneziaWG) Down(ctx context.Context, iface string) error {
	err := a.Runner.Stop(ctx, iface)
	if delErr := deleteLink(ctx, iface); err == nil {
		err = delErr
	}
	return err
}

func (a *AmneziaWG) LastHandshake(iface string) (time.Time, error) {
	cur, err := a.get(iface)
	if err != nil {
		return time.Time{}, err
	}
	sec, _ := strconv.ParseInt(cur.values["last_handshake_time_sec"], 10, 64)
	if sec <= 0 {
		return time.Time{}, nil
	}
	nsec, _ := strconv.ParseInt(cur.values["last_handshake_time_nsec"], 10, 64)
	return time.Unix(sec, nsec), nil
}

// uapiState is a UAPI get: the device's and its (single) peer's keys, and
// every peer's public key.
type uapiState struct {
	values map[string]string
	peers  []string
}

func (a *AmneziaWG) get(iface string) (*uapiState, error) {
	out, err := a.uapi(iface, "get=1\n\n")
	if err != nil {
		return nil, err
	}
	st := &uapiState{values: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k == "public_key" {
			st.peers = append(st.peers, v)
		}
		st.values[k] = v
	}
	return st, nil
}

// uapi sends one request to iface's UAPI socket and returns the reply,
// failing on a non-zero errno.
func (a *AmneziaWG) uapi(iface, req string) (string, error) {
	conn, err := net.DialTimeout("unix", a.socket(iface), time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		return "", err
	}
	var out strings.Builder
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if errno, ok := strings.CutPrefix(line, "errno="); ok {
			if errno != "0" {
				return out.String(), fmt.Errorf("amneziawg %s: uapi errno %s", iface, errno)
			}
			continue
		}
		out.WriteString(line + "\n")
	}
	return out.String(), sc.Err()
}
