//go:build linux

// Package e2e holds tests that run the Coordinator and Agents together on
// the network-namespace lab.
package e2e

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/agent"
	"github.com/alakiani/darz/internal/ca"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
	"github.com/alakiani/darz/internal/testutil"
)

// cutter is the Agent's data plane in this test: a snapshot tagged
// "cut-contact" drops every packet on its Node's uplink, the way a bad
// firewall change would.
type cutter struct {
	l    *lab.Lab
	node *lab.Node
}

func (c cutter) Apply(_ context.Context, s protocol.State) error {
	if slices.Contains(s.Node.Tags, "cut-contact") {
		c.l.Netem(c.node, lab.Impairment{Loss: 100})
	} else {
		c.l.ClearNetem(c.node)
	}
	return nil
}

// TestAgentsEnrollAndBadSnapshotIsReverted is session 2's "proves in the
// lab" test: Agents enroll and receive snapshots; a snapshot that cuts
// contact with the Coordinator is reverted and marked bad for that Node.
func TestAgentsEnrollAndBadSnapshotIsReverted(t *testing.T) {
	l := lab.Start(t, "coord", "n1", "n2", "n3")
	coordNode := l.Node("coord")
	ctx := context.Background()

	s, err := store.Open(filepath.Join(t.TempDir(), "darz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	coord := coordinator.New(s, authority, slog.New(slog.DiscardHandler))
	coord.LongPoll = 2 * time.Second

	// The Agent API listens inside the Coordinator's namespace.
	agentAddr := net.JoinHostPort(coordNode.Addr().String(), "8443")
	var ln net.Listener
	if err := coordNode.Do(func() (err error) { ln, err = net.Listen("tcp", agentAddr); return err }); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := coord.AgentTLSConfig([]string{coordNode.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: coord.AgentHandler()}
	go srv.Serve(tls.NewListener(ln, tlsConfig))
	defer srv.Close()

	// The Admin API is the operator's laptop talking to the Coordinator; it
	// does not need to cross the lab.
	adminSrv := httptest.NewServer(coord.AdminHandler())
	defer adminSrv.Close()
	secret, _, err := s.CreateAPIToken(ctx, store.ScopeNodesEnroll, "laptop", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin := &testutil.AdminClient{URL: adminSrv.URL, Token: secret}

	// Enroll an Agent on each of n1, n2, n3.
	ids := map[string]string{}
	agentCtx, stopAgents := context.WithCancel(ctx)
	defer stopAgents()
	for _, name := range []string{"n1", "n2", "n3"} {
		node := l.Node(name)
		created := admin.CreateNode(t, protocol.CreateNodeRequest{Name: name, Roles: []protocol.Role{"bridge"}})
		ids[name] = created.Node.ID
		cfg := agent.Config{
			Coordinators:    []string{"https://" + agentAddr},
			CACertPEM:       authority.CertPEM(),
			StateDir:        t.TempDir(),
			EnrollmentToken: created.EnrollmentToken,
			Applier:         cutter{l, node},
			Version:         "lab",
			Dial: func(ctx context.Context, network, addr string) (conn net.Conn, err error) {
				err = node.Do(func() error {
					conn, err = (&net.Dialer{}).DialContext(ctx, network, addr)
					return err
				})
				return conn, err
			},
			ConfirmTimeout: 3 * time.Second,
			HealthInterval: time.Second,
			RetryInterval:  250 * time.Millisecond,
			RequestTimeout: time.Second,
			LongPoll:       2 * time.Second,
		}
		go agent.Run(agentCtx, cfg)
	}

	appliedVersion := func(name string) uint64 {
		h := admin.GetNode(t, ids[name]).LastHealth
		if h == nil {
			return 0
		}
		return h.AppliedVersion
	}
	for name := range ids {
		testutil.Eventually(t, 10*time.Second, name+" enrolls and confirms version 1", func() bool {
			return appliedVersion(name) == 1
		})
		h := admin.GetNode(t, ids[name]).LastHealth
		if want := l.Node(name).Addr().String(); h.Addr != want || h.SeenAddr != want {
			t.Errorf("%s reported addr %q, Coordinator saw %q; want %s", name, h.Addr, h.SeenAddr, want)
		}
	}

	// Version 2 of n1's state cuts n1 off.
	if err := coord.UpdateNode(ctx, ids["n1"], []protocol.Role{"bridge"}, []string{"cut-contact"}); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 15*time.Second, "n1 reverts version 2 and the Coordinator marks it bad", func() bool {
		snap, err := s.LatestSnapshot(ctx, ids["n1"])
		return err == nil && snap.Version == 2 && snap.Bad
	})
	if v := appliedVersion("n1"); v != 1 {
		t.Errorf("n1 reports version %d applied, want 1", v)
	}

	// n1 stays on version 1 instead of taking the bad version again, and
	// keeps reporting.
	before := admin.GetNode(t, ids["n1"]).LastHealth.ReceivedAt
	time.Sleep(4 * time.Second)
	if h := admin.GetNode(t, ids["n1"]).LastHealth; h.AppliedVersion != 1 || !h.ReceivedAt.After(before) {
		t.Errorf("n1 after the revert: %+v", h)
	}

	// The other Nodes were never affected.
	for _, name := range []string{"n2", "n3"} {
		if v := appliedVersion(name); v != 1 {
			t.Errorf("%s reports version %d, want 1", name, v)
		}
	}

	// A further change is a new version and applies.
	if err := coord.UpdateNode(ctx, ids["n1"], []protocol.Role{"bridge"}, []string{"IR"}); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 10*time.Second, "n1 confirms version 3", func() bool {
		return appliedVersion("n1") == 3
	})
}
