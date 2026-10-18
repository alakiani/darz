//go:build linux

package e2e

import (
	"context"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/arvan"
	"github.com/alakiani/darz/internal/auth"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
	"github.com/alakiani/darz/internal/testutil"
)

// TestEndpointSlots is session 10's "proves in the lab" test: the
// Coordinator points the three Endpoint Slots at domestic Bridges its probe
// Device finds healthy; killing a Bridge re-points its slots after 60 s, a
// Device that re-resolves reaches the new Bridge, and the slots never move
// back when the Bridge recovers.
func TestEndpointSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sl := startSlotLab(t, ctx, "ir1", "ir3")
	l, co, dns, record := sl.l, sl.co, sl.dns, sl.record
	go co.c.RunEndpointSlots(ctx)

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
	if err := co.c.Reconcile(ctx); err != nil { // the Bridges are already up: tell them
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
	p.toggle(resolve("a"), coordinator.DefaultDevicePort)
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

// TestRebalanceSlots is issue #49's lab test: with every slot forced onto
// one of three eligible Bridges, as after the Agent restarts in production,
// an Admin's rebalance spreads them over all three at ArvanCloud and in the
// Coordinator, and the slot rounds after it leave them there. Then the
// panel does the same, previewing first.
func TestRebalanceSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sl := startSlotLab(t, ctx, "ir1", "ir2", "ir3")
	co, record := sl.co, sl.record
	go co.c.RunEndpointSlots(ctx)

	// Wait for every Bridge to be eligible and every slot to have a record.
	testutil.Eventually(t, 120*time.Second, "every Bridge is eligible and every slot assigned", func() bool {
		st, err := co.c.EndpointSlotStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range st.Bridges {
			if !b.Eligible {
				return false
			}
		}
		return len(st.Bridges) == 3 && record("a") != "" && record("b") != "" && record("c") != ""
	})
	forceOnto := func(b string) {
		t.Helper()
		for _, slot := range co.c.EndpointSlots {
			if err := co.store.AssignSlot(ctx, slot, sl.m.ids[b]); err != nil {
				t.Fatal(err)
			}
		}
		testutil.Eventually(t, 30*time.Second, "every record points at "+b, func() bool {
			return record("a") == b && record("b") == b && record("c") == b
		})
	}
	// spread checks that the records point at three different Bridges and
	// the Coordinator agrees, and returns where each slot points.
	spread := func() map[string]string {
		t.Helper()
		got := map[string]string{"a": record("a"), "b": record("b"), "c": record("c")}
		if vals := slices.Sorted(maps.Values(got)); !slices.Equal(vals, []string{"ir1", "ir2", "ir3"}) {
			t.Fatalf("the records point at %v", got)
		}
		st, err := co.c.EndpointSlotStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range st.Slots {
			if want := got[s.Name[:1]]; s.Bridge != want || s.Addr != sl.dns.A(s.Name[:1]) {
				t.Errorf("status shows %+v, the record %s (%s)", s, sl.dns.A(s.Name[:1]), want)
			}
		}
		return got
	}

	secret, admin := slotAdmin(t, co)
	forceOnto("ir1")
	var plan, moved protocol.SlotRebalance
	if got := admin.Do(t, "GET", "/v1/admin/slots/rebalance", nil, &plan); got != 200 {
		t.Fatalf("GET rebalance: %d", got)
	}
	want := []protocol.SlotMove{{Slot: "b.4miz.ir", From: "ir1", To: "ir2"}, {Slot: "c.4miz.ir", From: "ir1", To: "ir3"}}
	if !slices.Equal(plan.Moves, want) {
		t.Fatalf("plan = %+v", plan)
	}
	if got := admin.Do(t, "POST", "/v1/admin/slots/rebalance", nil, &moved); got != 200 {
		t.Fatalf("POST rebalance: %d", got)
	}
	if !slices.Equal(moved.Moves, want) {
		t.Fatalf("moved = %+v", moved)
	}
	after := spread()

	// Later slot rounds change nothing.
	time.Sleep(4 * co.c.Slots.ProbeEvery)
	if again := spread(); !maps.Equal(again, after) {
		t.Errorf("the slot rounds moved %v to %v", after, again)
	}

	// The panel previews, then applies.
	script, _ := filepath.Abs("../../web/e2e/rebalance.mjs")
	if _, err := os.Stat("../../web/node_modules/@playwright/test"); err != nil {
		t.Skip("web/node_modules is not installed: run npm ci in web/ and npx playwright install chromium")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH")
	}
	forceOnto("ir3")
	cmd := exec.Command(node, script)
	cmd.Dir = filepath.Dir(script)
	cmd.Env = append(os.Environ(), "PANEL_URL="+co.admin.URL, "ADMIN_HANDLE=admin", "ADMIN_PASSWORD="+slotAdminPassword, "TOTP_SECRET="+secret)
	out, err := cmd.CombinedOutput()
	t.Logf("browser:\n%s", strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	spread()
}

const slotAdminPassword = "the admin's long password"

// slotAdmin makes an Admin with TOTP, and returns its TOTP secret and an
// Admin API client signed in with a session that proved it.
func slotAdmin(t *testing.T, co *coord) (string, *testutil.AdminClient) {
	ctx := context.Background()
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword(slotAdminPassword)
	tok, _, _ := co.store.CreateInvite(ctx, store.Invite{Kind: "new", Admin: true}, time.Hour)
	admin, err := co.store.AcceptNewInvite(ctx, tok, "admin", hash)
	if err != nil {
		t.Fatal(err)
	}
	co.store.SetTOTPPending(ctx, admin.ID, secret)
	co.store.ConfirmTOTP(ctx, admin.ID)
	session, err := co.store.CreateSession(ctx, admin.ID, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(co.admin.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "darz_session", Value: session}})
	return secret, &testutil.AdminClient{URL: co.admin.URL, Client: &http.Client{Jar: jar}}
}

// slotLab is a lab mesh for the Endpoint Slot tests: domestic Bridges, an
// Exit, a phone, a faked ArvanCloud, and the Coordinator's probe Device.
type slotLab struct {
	l      *lab.Lab
	co     *coord
	m      *mesh
	dns    *arvan.Fake
	addrOf map[string]string // a Bridge's address -> its name
}

// startSlotLab starts the Agents and returns once every Bridge can carry
// Devices. Slots never move back, so the first Bridge to pass would keep
// all three: the caller starts the slot loop only then.
func startSlotLab(t *testing.T, ctx context.Context, bridges ...string) *slotLab {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	specs := []lab.Spec{{Name: "coord"}}
	for _, b := range bridges {
		specs = append(specs, lab.Spec{Name: b})
	}
	specs = append(specs,
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "phone1"},
		lab.At("web-ab", "93.184.215.14"),
		lab.At("dns-ab", "9.9.9.9"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	l := lab.StartSpecs(t, specs...)
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	upstreamCA := serveEgress(t, l.Node("dns-ab")) // a resolver abroad that answers the probe's query
	whoami(t, l.Node("web-ab"), 7)
	dumpNetOnFailure(t, l, bridges...)

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
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{},
		agentArgs: []string{"-upstream-ca", upstreamCA}}
	addrOf := map[string]string{}
	for _, b := range bridges {
		m.startAgent(b, []protocol.Role{protocol.RoleBridge}, []string{"IR"})
		addrOf[l.Node(b).Addr().String()] = b
	}
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)

	const defaultRule = 2
	for _, b := range bridges {
		testutil.Eventually(t, 120*time.Second, b+" has a direct default-Rule Path", func() bool {
			return m.ruleHealth(b, defaultRule).Mode == "direct"
		})
	}
	return &slotLab{l: l, co: co, m: m, dns: dns, addrOf: addrOf}
}

// record is the Bridge a slot's record at ArvanCloud points at, such as
// "ir1" for slot "a".
func (sl *slotLab) record(slot string) string { return sl.addrOf[sl.dns.A(slot)] }

// toggle turns the phone's tunnel off and on, resolving its slot afresh to
// bridge: the old session goes, and the next packet starts a handshake.
func (p *phone) toggle(bridge *lab.Node, port int) {
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
		keepalive := 5 * time.Second
		return c.ConfigureDevice("wg0", wgtypes.Config{ReplacePeers: true, Peers: []wgtypes.PeerConfig{{
			PublicKey:                   p.bridgeKey,
			Endpoint:                    &net.UDPAddr{IP: bridge.Addr().AsSlice(), Port: port},
			AllowedIPs:                  []net.IPNet{{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}},
			PersistentKeepaliveInterval: &keepalive,
		}}})
	})
	if err != nil {
		p.t.Fatal(err)
	}
}
