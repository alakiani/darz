//go:build linux

package e2e

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/ca"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
	"github.com/alakiani/darz/internal/testutil"
)

// coord is a Coordinator running in-process, its Agent API listening inside
// a lab Node's namespace.
type coord struct {
	c        *coordinator.Coordinator
	store    *store.Store
	admin    *testutil.AdminClient
	caPEM    []byte
	agentURL string // https://<node>:8443
}

func startCoordinator(t *testing.T, node *lab.Node) *coord {
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
	logPath := filepath.Join(t.TempDir(), "coordinator.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	dumpOnFailure(t, "coordinator", logPath)
	c := coordinator.New(s, authority, slog.New(slog.NewTextHandler(logFile, nil)))
	c.LongPoll = 2 * time.Second

	addr := net.JoinHostPort(node.Addr().String(), "8443")
	var ln net.Listener
	if err := node.Do(func() (err error) { ln, err = net.Listen("tcp", addr); return err }); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := c.AgentTLSConfig([]string{node.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: c.AgentHandler()}
	go srv.Serve(tls.NewListener(ln, tlsConfig))
	t.Cleanup(func() { srv.Close() })

	// The Admin API is the operator's laptop talking to the Coordinator; it
	// does not need to cross the lab.
	adminSrv := httptest.NewServer(c.AdminHandler())
	t.Cleanup(adminSrv.Close)
	secret, _, err := s.CreateAPIToken(context.Background(), store.ScopeNodesEnroll, "laptop", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &coord{c: c, store: s, admin: &testutil.AdminClient{URL: adminSrv.URL, Token: secret},
		caPEM: authority.CertPEM(), agentURL: "https://" + addr}
}

// health returns a Node's latest health report, or nil.
func (co *coord) health(t *testing.T, nodeID string) *protocol.HealthStatus {
	t.Helper()
	return co.admin.GetNode(t, nodeID).LastHealth
}

// dumpOnFailure prints a log file if the test fails.
func dumpOnFailure(t *testing.T, name, path string) {
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(path)
			t.Logf("---- %s log ----\n%s", name, b)
		}
	})
}

// execCommand runs a command in the root namespace.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// egressTargets serves what Exits' egress checks reach (ADR 0008): on each
// host, a resolver that answers every A query with the host's own address,
// and an HTTP endpoint that answers 204. It points the Coordinator's
// targets at them: domestic for IR Exits, abroad for the rest.
func egressTargets(t *testing.T, co *coord, abroad, domestic *lab.Node) {
	t.Helper()
	for _, n := range []*lab.Node{abroad, domestic} {
		serveEgress(t, n)
	}
	co.c.Egress = coordinator.EgressTargets{
		Abroad: protocol.EgressCheck{Resolvers: []string{abroad.Addr().String()}, Hostname: "check.abroad",
			URL: "http://" + abroad.Addr().String() + "/generate_204"},
		IR: protocol.EgressCheck{Resolvers: []string{domestic.Addr().String()}, Hostname: "check.ir",
			URL: "http://" + domestic.Addr().String() + "/"},
	}
}

func serveEgress(t *testing.T, n *lab.Node) {
	t.Helper()
	var udp net.PacketConn
	var tcp net.Listener
	if err := n.Do(func() (err error) {
		if udp, err = net.ListenPacket("udp4", net.JoinHostPort(n.Addr().String(), "53")); err != nil {
			return err
		}
		tcp, err = net.Listen("tcp4", net.JoinHostPort(n.Addr().String(), "80"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			k, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if answer := answerA(buf[:k], n.Addr().As4()); answer != nil {
				udp.WriteTo(answer, from)
			}
		}
	}()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(tcp)
}

// answerA answers a DNS query: an A query gets addr, any other type an
// empty NOERROR answer.
func answerA(q []byte, addr [4]byte) []byte {
	if len(q) < 12 {
		return nil
	}
	end := 12
	for end < len(q) && q[end] != 0 {
		end += int(q[end]) + 1
	}
	end += 5 // the root label, then type and class
	if end > len(q) {
		return nil
	}
	isA := q[end-4] == 0 && q[end-3] == 1
	out := append([]byte{}, q[:2]...)   // id
	out = append(out, 0x81, 0x80, 0, 1) // a response, recursion available; one question
	if isA {
		out = append(out, 0, 1, 0, 0, 0, 0) // one answer
	} else {
		out = append(out, 0, 0, 0, 0, 0, 0)
	}
	out = append(out, q[12:end]...)
	if isA {
		out = append(out, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		out = append(out, addr[:]...)
	}
	return out
}

// serveFeed serves a fixed IR feed and returns its URL.
func serveFeed(t *testing.T, body string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
