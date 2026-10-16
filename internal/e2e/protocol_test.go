//go:build linux

// Package e2e holds tests that run the Coordinator and Agents together on
// the network-namespace lab.
package e2e

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/agent"
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
	co := startCoordinator(t, l.Node("coord"))
	coord, s, admin := co.c, co.store, co.admin
	ctx := context.Background()

	// Enroll an Agent on each of n1, n2, n3.
	ids := map[string]string{}
	agentCtx, stopAgents := context.WithCancel(ctx)
	defer stopAgents()
	for _, name := range []string{"n1", "n2", "n3"} {
		node := l.Node(name)
		created := admin.CreateNode(t, protocol.CreateNodeRequest{Name: name, Roles: []protocol.Role{"bridge"}})
		ids[name] = created.Node.ID
		cfg := agent.Config{
			Coordinators:    []string{co.agentURL},
			CACertPEM:       co.caPEM,
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
	// Versions are not predictable: adding a Node gives every other Node's
	// state a Link, and a new version.
	latest := func(name string) store.Snapshot {
		snap, err := s.LatestSnapshot(ctx, ids[name])
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	upToDate := func(name string) bool { return appliedVersion(name) == latest(name).Version }
	for name := range ids {
		testutil.Eventually(t, 10*time.Second, name+" enrolls and confirms its latest version", func() bool {
			return upToDate(name)
		})
		h := admin.GetNode(t, ids[name]).LastHealth
		if want := l.Node(name).Addr().String(); h.Addr != want || h.SeenAddr != want {
			t.Errorf("%s reported addr %q, Coordinator saw %q; want %s", name, h.Addr, h.SeenAddr, want)
		}
	}

	// The next version of n1's state cuts n1 off.
	good := latest("n1").Version
	if err := coord.UpdateNode(ctx, ids["n1"], []protocol.Role{"bridge"}, []string{"cut-contact"}); err != nil {
		t.Fatal(err)
	}
	bad := latest("n1").Version
	if bad == good {
		t.Fatal("the update made no new version")
	}
	testutil.Eventually(t, 15*time.Second, "n1 reverts the cutting version and the Coordinator marks it bad", func() bool {
		snap := latest("n1")
		return snap.Version == bad && snap.Bad
	})
	if v := appliedVersion("n1"); v != good {
		t.Errorf("n1 reports version %d applied, want %d", v, good)
	}

	// n1 stays on the good version instead of taking the bad one again, and
	// keeps reporting.
	before := admin.GetNode(t, ids["n1"]).LastHealth.ReceivedAt
	time.Sleep(4 * time.Second)
	if h := admin.GetNode(t, ids["n1"]).LastHealth; h.AppliedVersion != good || !h.ReceivedAt.After(before) {
		t.Errorf("n1 after the revert: %+v", h)
	}

	// The other Nodes kept up, including with the new Tags their Links show
	// for n1.
	for _, name := range []string{"n2", "n3"} {
		testutil.Eventually(t, 10*time.Second, name+" is on its latest version", func() bool { return upToDate(name) })
	}

	// A further change is a new version and applies.
	if err := coord.UpdateNode(ctx, ids["n1"], []protocol.Role{"bridge"}, []string{"IR"}); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 10*time.Second, "n1 confirms the version after the bad one", func() bool {
		v := appliedVersion("n1")
		return v > bad && v == latest("n1").Version
	})
}
