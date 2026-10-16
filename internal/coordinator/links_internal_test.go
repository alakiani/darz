package coordinator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/awg"
	"github.com/alakiani/darz/internal/ca"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
)

type mesh struct {
	t     *testing.T
	c     *Coordinator
	s     *store.Store
	nodes map[string]store.Node
	addrs map[string]string // each Node's public address
}

var bg = context.Background()

// newMesh creates Nodes by name; those named ir* are tagged IR.
func newMesh(t *testing.T, names ...string) *mesh {
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
	m := &mesh{t: t, c: New(s, authority, slog.New(slog.DiscardHandler)), s: s, nodes: map[string]store.Node{}, addrs: map[string]string{}}
	for i, name := range names {
		var tags []string
		if name[:2] == "ir" {
			tags = []string{"IR"}
		}
		n, err := s.CreateNode(bg, name, []protocol.Role{protocol.RoleBridge, protocol.RoleExit}, tags)
		if err != nil {
			t.Fatal(err)
		}
		m.nodes[name] = n
		m.addrs[name] = netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}).String()
		// Every Node has reported once, with its Link key.
		m.health(name, protocol.Health{LinkPublicKey: name + "-key"})
	}
	return m
}

// health delivers a health report from name, as the health handler would.
func (m *mesh) health(name string, h protocol.Health) {
	m.t.Helper()
	n := m.nodes[name]
	if err := m.s.PutHealth(bg, n.ID, h, m.addrs[name]); err != nil {
		m.t.Fatal(err)
	}
	n, _ = m.s.Node(bg, n.ID)
	if _, err := m.c.takeLinkHealth(bg, n, h); err != nil {
		m.t.Fatal(err)
	}
	if err := m.c.Reconcile(bg); err != nil {
		m.t.Fatal(err)
	}
	m.nodes[name] = n
}

func (m *mesh) state(name string) protocol.State {
	m.t.Helper()
	snap, err := m.s.LatestSnapshot(bg, m.nodes[name].ID)
	if err != nil {
		m.t.Fatal(err)
	}
	var st protocol.State
	if err := json.Unmarshal(snap.Body, &st); err != nil {
		m.t.Fatal(err)
	}
	return st
}

func (m *mesh) link(from, to string) protocol.Link {
	m.t.Helper()
	for _, l := range m.state(from).Links {
		if l.Peer.Name == to {
			return l
		}
	}
	m.t.Fatalf("%s has no link to %s", from, to)
	return protocol.Link{}
}

func transports(l protocol.Link) []protocol.Transport {
	var ts []protocol.Transport
	for _, in := range l.Instances {
		ts = append(ts, in.Transport)
	}
	return ts
}

// choose has the listening end of every link of name pick ports and
// parameters, as its Agent would.
func (m *mesh) choose(name string) {
	m.t.Helper()
	var h protocol.Health
	for _, l := range m.state(name).Links {
		for _, in := range l.Instances {
			if l.Initiator || in.ListenPort != 0 {
				continue
			}
			ch := protocol.ListenChoice{InstanceID: in.ID, Port: awg.ChoosePort()}
			if in.Transport == protocol.TransportAmneziaWG {
				p := awg.Generate()
				ch.AWG = &p
			}
			h.Listening = append(h.Listening, ch)
		}
	}
	m.health(name, h)
}

func TestLinksAreAFullMeshWithTransportsByFilterSide(t *testing.T) {
	m := newMesh(t, "ir1", "ir2", "ab1")
	for _, name := range []string{"ir1", "ir2", "ab1"} {
		m.choose(name)
	}

	// Across the filter: IR end initiates, two AmneziaWG, never WireGuard.
	cross := m.link("ir1", "ab1")
	if !cross.Initiator || m.link("ab1", "ir1").Initiator {
		t.Error("across the filter, the IR end must initiate")
	}
	if ts := transports(cross); len(ts) != 2 || ts[0] != protocol.TransportAmneziaWG || ts[1] != protocol.TransportAmneziaWG {
		t.Errorf("crossing link transports = %v, want 2 amneziawg", ts)
	}

	// Same side: lower id initiates; WireGuard first, then AmneziaWG.
	same := m.link("ir1", "ir2")
	lowerIsIR1 := m.nodes["ir1"].ID < m.nodes["ir2"].ID
	if same.Initiator != lowerIsIR1 {
		t.Errorf("same-side initiator: ir1 initiates = %v, want %v", same.Initiator, lowerIsIR1)
	}
	if ts := transports(same); len(ts) != 2 || ts[0] != protocol.TransportWireGuard || ts[1] != protocol.TransportAmneziaWG {
		t.Errorf("same-side link transports = %v, want [wireguard amneziawg]", ts)
	}
	if n := len(m.state("ir1").Links); n != 2 {
		t.Errorf("ir1 has %d links, want 2", n)
	}
}

func TestInitiatorGetsTheListenersChoice(t *testing.T) {
	m := newMesh(t, "ir1", "ab1")
	if l := m.link("ab1", "ir1"); len(l.Instances) != 2 || l.Instances[0].ListenPort != 0 || l.Instances[0].AWG != nil {
		t.Fatalf("listener before choosing = %+v, want instances asking it to choose", l.Instances)
	}
	if l := m.link("ir1", "ab1"); len(l.Instances) != 0 {
		t.Fatalf("initiator has %d instances before the listener chose", len(l.Instances))
	}

	m.choose("ab1")
	listen, init := m.link("ab1", "ir1"), m.link("ir1", "ab1")
	if len(init.Instances) != 2 {
		t.Fatalf("initiator has %d instances, want 2", len(init.Instances))
	}
	for i := range init.Instances {
		li, ii := listen.Instances[i], init.Instances[i]
		if li.ID != ii.ID || li.ListenPort == 0 || li.AWG == nil || *li.AWG != *ii.AWG {
			t.Errorf("instance %d: listener %+v, initiator %+v", i, li, ii)
		}
		if want := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.2"), li.ListenPort).String(); ii.Endpoint != want {
			t.Errorf("endpoint = %q, want %q", ii.Endpoint, want)
		}
		lp, ip := netip.MustParsePrefix(li.Addr), netip.MustParsePrefix(ii.Addr)
		if lp.Bits() != 31 || lp.Masked() != ip.Masked() || li.PeerAddr != ip.Addr().String() || ii.PeerAddr != lp.Addr().String() {
			t.Errorf("addresses: listener %s→%s, initiator %s→%s", li.Addr, li.PeerAddr, ii.Addr, ii.PeerAddr)
		}
		if !MeshRange.Contains(lp.Addr()) || li.Iface == ii.Iface {
			t.Errorf("listener %+v, initiator %+v", li, ii)
		}
	}
	if listen.Instances[0].ListenPort == listen.Instances[1].ListenPort && *listen.Instances[0].AWG == *listen.Instances[1].AWG {
		t.Error("two instances share a fingerprint")
	}
}

func TestInvalidChoiceIsIgnored(t *testing.T) {
	m := newMesh(t, "ir1", "ab1")
	in := m.link("ab1", "ir1").Instances[0]
	bad := awg.Generate()
	bad.H2 = bad.H1
	m.health("ab1", protocol.Health{Listening: []protocol.ListenChoice{
		{InstanceID: in.ID, Port: 20000, AWG: &bad},
	}})
	if l := m.link("ab1", "ir1"); l.Instances[0].ListenPort != 0 {
		t.Error("an invalid choice was stored")
	}
}

func TestInstanceDownTooLongIsReplaced(t *testing.T) {
	m := newMesh(t, "ir1", "ab1")
	m.c.LinkTiming.ReplaceAfterSeconds = 3600
	m.choose("ab1")
	l := m.link("ir1", "ab1")
	old := l.Instances[0]

	report := func(down time.Duration) {
		since := time.Now().Add(-down)
		m.health("ir1", protocol.Health{Links: []protocol.LinkHealth{{LinkID: l.ID, Instances: []protocol.InstanceHealth{
			{ID: old.ID, DownSince: &since}, {ID: l.Instances[1].ID, Up: true},
		}}}})
	}
	report(59 * time.Minute)
	if got := m.link("ir1", "ab1").Instances[0].ID; got != old.ID {
		t.Fatalf("replaced after 59 minutes")
	}

	// The listener's view of how long it has been down does not count.
	since := time.Now().Add(-2 * time.Hour)
	m.health("ab1", protocol.Health{Links: []protocol.LinkHealth{{LinkID: l.ID, Instances: []protocol.InstanceHealth{{ID: old.ID, DownSince: &since}}}}})
	if got := m.link("ir1", "ab1").Instances[0].ID; got != old.ID {
		t.Fatalf("replaced on the listener's report")
	}

	report(61 * time.Minute)
	listen := m.link("ab1", "ir1")
	if len(listen.Instances) != 2 {
		t.Fatalf("%d instances after replacement, want 2", len(listen.Instances))
	}
	fresh := listen.Instances[1]
	if listen.Instances[0].ID == old.ID || fresh.ID == old.ID || fresh.ID < old.ID {
		t.Fatalf("instances after replacement = %+v", listen.Instances)
	}
	if fresh.ListenPort != 0 || fresh.AWG != nil {
		t.Errorf("replacement carries old choices: %+v", fresh)
	}
	m.choose("ab1")
	if got := m.link("ab1", "ir1").Instances[1]; got.ListenPort == old.ListenPort && *got.AWG == *old.AWG {
		t.Error("replacement kept the old port and parameters")
	}
}

func TestTagChangeRederivesTransports(t *testing.T) {
	m := newMesh(t, "ir1", "ab1")
	m.choose("ab1")
	// ab1 moves inside the filter: the Link no longer crosses it.
	if err := m.c.UpdateNode(bg, m.nodes["ab1"].ID, []protocol.Role{protocol.RoleBridge}, []string{"IR"}); err != nil {
		t.Fatal(err)
	}
	m.nodes["ab1"], _ = m.s.Node(bg, m.nodes["ab1"].ID)
	var l protocol.Link
	for _, name := range []string{"ir1", "ab1"} {
		if lk := m.link(name, map[string]string{"ir1": "ab1", "ab1": "ir1"}[name]); !lk.Initiator {
			l = lk
		}
	}
	if ts := transports(l); len(ts) != 2 || ts[0] != protocol.TransportWireGuard {
		t.Errorf("after the tag change, listener sees %v, want wireguard first", ts)
	}
}

func TestDisabledTransportIsLeftOut(t *testing.T) {
	m := newMesh(t, "ab1", "ab2")
	if err := m.s.SetTransports(bg, []protocol.Transport{protocol.TransportAmneziaWG}); err != nil {
		t.Fatal(err)
	}
	if err := m.c.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ab1", "ab2"} {
		for _, l := range m.state(name).Links {
			if ts := transports(l); len(ts) > 1 || len(ts) == 1 && ts[0] != protocol.TransportAmneziaWG {
				t.Errorf("%s: transports %v with wireguard disabled", name, ts)
			}
		}
	}
}

func TestEgressTargetsDependOnTags(t *testing.T) {
	m := newMesh(t, "ir1", "ab1")
	if c := m.state("ir1").EgressCheck; c == nil || c.Hostname != DefaultEgressTargets.IR.Hostname {
		t.Errorf("IR Exit egress check = %+v, want the domestic targets", c)
	}
	if c := m.state("ab1").EgressCheck; c == nil || c.Hostname != DefaultEgressTargets.Abroad.Hostname {
		t.Errorf("abroad Exit egress check = %+v, want the abroad targets", c)
	}
}
