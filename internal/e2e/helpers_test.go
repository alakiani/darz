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
