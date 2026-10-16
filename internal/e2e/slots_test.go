//go:build linux

package e2e

import (
	"context"
	"net"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/arvan"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/testutil"
)

// TestEndpointSlots is session 10's "proves in the lab" test: the
// Coordinator points the three Endpoint Slots at domestic Bridges its probe
// Device finds healthy; killing a Bridge re-points its slots after 60 s, a
// Device that re-resolves reaches the new Bridge, and the slots never move
// back when the Bridge recovers.
func TestEndpointSlots(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ir3"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "phone1"},
		lab.At("web-ab", "93.184.215.14"),
		lab.At("dns-ab", "9.9.9.9"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	serveEgress(t, l.Node("dns-ab")) // a resolver abroad that answers the probe's query
	whoami(t, l.Node("web-ab"), 7)
	dumpNetOnFailure(t, l, "ir1", "ir3")

	// ArvanCloud, faked, with the zone's records still on placeholders.
	dns := arvan.NewFake("lab-key", "4miz.ir")
	for _, s := range []string{"a", "b", "c"} {
		dns.Put(s, "192.0.2.1")
	}
	api := httptest.NewServer(dns)
	t.Cleanup(api.Close)
	co.c.DNS = &arvan.Client{BaseURL: api.URL, APIKey: "lab-key", Zone: "4miz.ir", TTL: 120}
	co.c.Slots.ProbeEvery = 2 * time.Second // ADR 0005's 15 s, shortened; the 60 s stays
	co.c.Slots.ProbeHostname = "check.abroad"
	co.c.Slots.Enter = l.Node("coord").Do // the probe dials from the Coordinator host

	if err := co.c.InitProbe(ctx); err != nil {
		t.Fatal(err)
	}
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ir3", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)

	// Slots never move back, so the first Bridge to pass would keep all
	// three: start the slot loop once both Bridges can carry Devices.
	const defaultRule = 2
	for _, b := range []string{"ir1", "ir3"} {
		testutil.Eventually(t, 120*time.Second, b+" has a direct default-Rule Path", func() bool {
			return m.ruleHealth(b, defaultRule).Mode == "direct"
		})
	}
	go co.c.RunEndpointSlots(ctx)

	addrOf := map[string]string{l.Node("ir1").Addr().String(): "ir1", l.Node("ir3").Addr().String(): "ir3"}
	record := func(slot string) string { return addrOf[dns.A(slot)] }

	// The slots spread over both Bridges once the probe passes on each.
	testutil.Eventually(t, 120*time.Second, "the slots point at ir1, ir3, ir1", func() bool {
		return record("a") == "ir1" && record("b") == "ir3" && record("c") == "ir1"
	})

	// A User's Main config is on slot a.
	user, _ := co.store.CreateUser(ctx, "maryam")
	key, _ := wgtypes.GeneratePrivateKey()
	dev, err := co.store.CreateDevice(ctx, user.ID, "phone", key.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}
	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	resolve := func(slot string) *lab.Node { return l.Node(record(slot)) }
	p := newPhone(t, l.Node("phone1"), key, dev.Address, resolve("a"), bridgeKey, coordinator.DefaultDevicePort)
	abroad := func() (string, error) { return p.ask("93.184.215.14:7") }
	ab1 := l.Node("ab1").Addr().String()
	p.leavesFrom("abroad through slot a", abroad, ab1, 60*time.Second)

	// Kill ir1. Its slots hold for 60 s, then move to ir3.
	kill := l.Filter(lab.DropAll(l.Node("ir1")))
	killed := time.Now()
	testutil.Eventually(t, 120*time.Second, "slot a moves to ir3", func() bool {
		if record("a") == "ir3" {
			return true
		}
		if record("a") != "ir1" {
			t.Fatalf("slot a points at %q", dns.A("a"))
		}
		return false
	})
	took := time.Since(killed)
	t.Logf("slot a re-pointed %v after ir1 died", took.Round(100*time.Millisecond))
	if took < 60*time.Second {
		t.Errorf("slot a moved after %v, before the 60 s", took)
	}
	if took > 60*time.Second+3*co.c.Slots.ProbeEvery+5*time.Second {
		t.Errorf("slot a took %v to move, want about 60 s", took)
	}
	testutil.Eventually(t, 10*time.Second, "slot c moves to ir3 too", func() bool { return record("c") == "ir3" })
	if record("b") != "ir3" {
		t.Errorf("slot b moved to %q", record("b"))
	}
	if ttl := dns.TTL("a"); ttl != 120 {
		t.Errorf("slot a's TTL is %d", ttl)
	}

	// Toggling the tunnel re-resolves slot a and reaches ir3.
	p.setEndpoint(resolve("a"), coordinator.DefaultDevicePort)
	p.leavesFrom("abroad through slot a, re-pointed", abroad, ab1, 30*time.Second)

	// ir1 recovers. Its probe passes again, but the slots stay on ir3.
	kill.Remove()
	testutil.Eventually(t, 60*time.Second, "the probe passes on ir1 again", func() bool {
		st, err := co.c.EndpointSlotStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range st.Bridges {
			if b.Name == "ir1" {
				return b.Probe != nil && b.Probe.OK && b.Eligible
			}
		}
		return false
	})
	time.Sleep(5 * co.c.Slots.ProbeEvery)
	for _, s := range []string{"a", "b", "c"} {
		if record(s) != "ir3" {
			t.Errorf("slot %s moved back to %q", s, record(s))
		}
	}

	// The status board shows the slots.
	st, err := co.c.EndpointSlotStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Slots {
		if s.Bridge != "ir3" || s.Addr != l.Node("ir3").Addr().String() {
			t.Errorf("status shows %+v", s)
		}
	}
}

// setEndpoint points the phone's tunnel at another Bridge, as toggling the
// tunnel does after its slot was re-pointed.
func (p *phone) setEndpoint(bridge *lab.Node, port int) {
	p.t.Helper()
	gw := netip.PrefixFrom(p.node.Addr(), 24).Masked().Addr().Next()
	if out, err := p.node.Command("ip", "route", "replace", bridge.Addr().String()+"/32", "via", gw.String(), "dev", "wan0").CombinedOutput(); err != nil {
		p.t.Fatalf("route to %s: %v: %s", bridge.Name(), err, out)
	}
	err := p.node.Do(func() error {
		c, err := wgctrl.New()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.ConfigureDevice("wg0", wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: p.bridgeKey, UpdateOnly: true,
			Endpoint: &net.UDPAddr{IP: bridge.Addr().AsSlice(), Port: port}}}})
	})
	if err != nil {
		p.t.Fatal(err)
	}
}
