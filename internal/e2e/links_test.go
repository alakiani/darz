//go:build linux

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
	"github.com/alakiani/darz/internal/testutil"
)

// binDir holds the agent and amneziawg-go binaries the lab runs. CI builds
// them before the tests; see .github/workflows/ci.yml.
func binDir(t *testing.T) string {
	dir := os.Getenv("DARZ_BIN")
	if dir == "" {
		t.Skip("DARZ_BIN is not set: build ./cmd/agent and amneziawg-go into a directory and point DARZ_BIN at it")
	}
	return dir
}

// mesh is the lab mesh of this test: Nodes running the real Agent binary.
type mesh struct {
	t   *testing.T
	l   *lab.Lab
	co  *coord
	ids map[string]string // Node name -> id
}

func (m *mesh) startAgent(name string, roles []protocol.Role, tags []string) {
	t := m.t
	bin := binDir(t)
	created := m.co.admin.CreateNode(t, protocol.CreateNodeRequest{Name: name, Roles: roles, Tags: tags})
	m.ids[name] = created.Node.ID

	dir := t.TempDir()
	caFile, tokenFile, logFile := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "token"), filepath.Join(dir, "agent.log")
	os.WriteFile(caFile, m.co.caPEM, 0o644)
	os.WriteFile(tokenFile, []byte(created.EnrollmentToken), 0o600)
	log, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.l.Node(name).Command(filepath.Join(bin, "agent"),
		"-coordinators", m.co.agentURL, "-ca", caFile, "-state-dir", filepath.Join(dir, "state"),
		"-enroll-token-file", tokenFile, "-awg-runner", "exec", "-awg-binary", filepath.Join(bin, "amneziawg-go"),
		"-health-interval", "1s")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dumpOnFailure(t, "agent "+name, logFile)
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
}

// linkID is the id of the Link between two Nodes.
func (m *mesh) linkID(a, b string) uint64 {
	m.t.Helper()
	links, err := m.co.store.Links(context.Background())
	if err != nil {
		m.t.Fatal(err)
	}
	for _, l := range links {
		if (l.NodeA == m.ids[a] && l.NodeB == m.ids[b]) || (l.NodeA == m.ids[b] && l.NodeB == m.ids[a]) {
			return l.ID
		}
	}
	m.t.Fatalf("no link between %s and %s", a, b)
	return 0
}

// link is what Node from reports about a Link.
func (m *mesh) link(from string, linkID uint64) protocol.LinkHealth {
	m.t.Helper()
	if h := m.co.health(m.t, m.ids[from]); h != nil {
		for _, lh := range h.Links {
			if lh.LinkID == linkID {
				return lh
			}
		}
	}
	return protocol.LinkHealth{}
}

// instances are a Link's live instances, as the Coordinator stores them.
func (m *mesh) instances(linkID uint64) []store.Instance {
	m.t.Helper()
	all, err := m.co.store.Instances(context.Background())
	if err != nil {
		m.t.Fatal(err)
	}
	var out []store.Instance
	for _, in := range all {
		if in.LinkID == linkID {
			out = append(out, in)
		}
	}
	return out
}

func (m *mesh) instance(id uint64) store.Instance {
	m.t.Helper()
	all, _ := m.co.store.Instances(context.Background())
	for _, in := range all {
		if in.ID == id {
			return in
		}
	}
	m.t.Fatalf("no live instance %d", id)
	return store.Instance{}
}

func (m *mesh) name(nodeID string) string {
	for name, id := range m.ids {
		if id == nodeID {
			return name
		}
	}
	return ""
}

// TestLinks is session 3's "proves in the lab" test: blocking one
// instance's UDP port moves the Link to its other instance within seconds;
// plain WireGuard is never tried on a Link that crosses the filter; a dead
// instance is replaced with new parameters.
func TestLinks(t *testing.T) {
	binDir(t)
	if out, err := exec.Command("modprobe", "wireguard").CombinedOutput(); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.Start(t, "coord", "ir1", "ir2", "ab1", "ab2")
	co := startCoordinator(t, l.Node("coord"))
	co.c.LinkTiming = protocol.LinkTiming{SwitchBackSeconds: 15, RetryMinSeconds: 2, RetryMaxSeconds: 4, ReplaceAfterSeconds: 25}
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}}

	// Plain WireGuard handshake initiations: a 148-byte UDP payload that
	// starts with message type 1. Counted in both directions.
	set := func(names ...string) string {
		var addrs []string
		for _, n := range names {
			addrs = append(addrs, l.Node(n).Addr().String())
		}
		return "{ " + strings.Join(addrs, ", ") + " }"
	}
	wgInit := "udp length 156 @th,64,32 0x01000000"
	ir, ab := set("ir1", "ir2"), set("ab1", "ab2")
	crossOut := l.Count(fmt.Sprintf("ip saddr %s ip daddr %s %s", ir, ab, wgInit))
	crossIn := l.Count(fmt.Sprintf("ip saddr %s ip daddr %s %s", ab, ir, wgInit))
	sameSide := l.Count(fmt.Sprintf("ip saddr %s ip daddr %s %s", ir, ir, wgInit))

	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ir2", []protocol.Role{protocol.RoleBridge, protocol.RoleExit}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	m.startAgent("ab2", []protocol.Role{protocol.RoleExit}, nil)

	// Every Link of every Node comes up.
	names := []string{"ir1", "ir2", "ab1", "ab2"}
	testutil.Eventually(t, 90*time.Second, "every link up at both ends", func() bool {
		for i, a := range names {
			for _, b := range names[i+1:] {
				id := m.linkID(a, b)
				if !m.link(a, id).Up || !m.link(b, id).Up {
					return false
				}
			}
		}
		return true
	})

	// Across the filter there are two AmneziaWG instances and no WireGuard.
	cross := m.linkID("ir1", "ab1")
	for _, in := range m.instances(cross) {
		if in.Transport != protocol.TransportAmneziaWG {
			t.Errorf("crossing link has a %s instance", in.Transport)
		}
	}
	if n := len(m.instances(cross)); n != 2 {
		t.Errorf("crossing link has %d instances, want 2", n)
	}
	same := m.linkID("ir1", "ir2")
	if lh := m.link("ir1", same); lh.Transport != protocol.TransportWireGuard {
		t.Errorf("same-side link carried by %q, want wireguard first", lh.Transport)
	}

	// Blocking the active instance's port moves the Link within seconds to
	// its other instance, once that one is Up as a warm standby.
	testutil.Eventually(t, 60*time.Second, "both crossing instances up at ir1", func() bool {
		ups := 0
		for _, ih := range m.link("ir1", cross).Instances {
			if ih.Up {
				ups++
			}
		}
		return ups == 2
	})
	active := m.link("ir1", cross).ActiveInstance
	blocked := m.instance(active)
	block := l.Filter(lab.DropUDP(l.Node(m.name(blocked.Listener)), blocked.ListenPort))
	start := time.Now()
	testutil.Eventually(t, 15*time.Second, "crossing link moves off the blocked instance", func() bool {
		lh := m.link("ir1", cross)
		return lh.Up && lh.ActiveInstance != active && lh.ActiveInstance != 0
	})
	moved := time.Since(start)
	t.Logf("crossing link moved from instance %d to %d in %v; instances %+v", active, m.link("ir1", cross).ActiveInstance,
		moved.Round(100*time.Millisecond), m.link("ir1", cross).Instances)
	if moved > 8*time.Second {
		t.Errorf("moving off a blocked instance took %v, want a few seconds", moved)
	}

	// Same side: off WireGuard while it is blocked, and back to it only
	// after it has been Up for the switch-back time.
	wg := m.instance(m.link("ir1", same).ActiveInstance)
	wgBlock := l.Filter(lab.DropUDP(l.Node(m.name(wg.Listener)), wg.ListenPort))
	testutil.Eventually(t, 15*time.Second, "same-side link moves to amneziawg", func() bool {
		lh := m.link("ir1", same)
		return lh.Up && lh.Transport == protocol.TransportAmneziaWG
	})
	wgBlock.Remove()
	unblocked := time.Now()
	testutil.Eventually(t, 45*time.Second, "same-side link switches back to wireguard", func() bool {
		return m.link("ir1", same).Transport == protocol.TransportWireGuard
	})
	if back := time.Since(unblocked); back < 15*time.Second {
		t.Errorf("switched back to wireguard after %v, before the 15 s switch-back", back)
	}

	// The blocked crossing instance stays dead, so it is replaced: a new
	// instance with a new port and new parameters, which comes up.
	testutil.Eventually(t, 90*time.Second, "the dead instance is replaced and its successor is up", func() bool {
		var fresh *store.Instance
		for _, in := range m.instances(cross) {
			if in.ID == blocked.ID {
				return false
			}
			if in.ID > blocked.ID && in.ListenPort != 0 {
				fresh = &in
			}
		}
		if fresh == nil {
			return false
		}
		for _, ih := range m.link("ir1", cross).Instances {
			if ih.ID == fresh.ID && ih.Up {
				return true
			}
		}
		return false
	})
	for _, in := range m.instances(cross) {
		if in.ID > blocked.ID && in.ListenPort == blocked.ListenPort && *in.AWG == *blocked.AWG {
			t.Errorf("replacement %d kept the dead instance's port and parameters", in.ID)
		}
	}
	block.Remove()

	if n := crossOut.Packets() + crossIn.Packets(); n != 0 {
		t.Errorf("%d plain WireGuard handshake initiations crossed the filter", n)
	}
	if sameSide.Packets() == 0 {
		t.Error("the WireGuard counter saw no handshake on the same-side link; it is not counting")
	}
}
