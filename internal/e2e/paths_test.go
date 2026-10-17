//go:build linux

package e2e

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/testutil"
)

// ruleHealth is how a Bridge reports carrying a Rule.
func (m *mesh) ruleHealth(bridge string, ruleID uint64) protocol.RuleHealth {
	m.t.Helper()
	if h := m.co.health(m.t, m.ids[bridge]); h != nil {
		for _, p := range h.Paths {
			if p.RuleID == ruleID {
				return p
			}
		}
	}
	return protocol.RuleHealth{}
}

// TestPathSelection is session 5's "proves in the lab" test: killing an
// Exit moves its buckets within seconds; killing a Bridge's direct Links
// hands its traffic to a neighbouring Bridge; the Rule fallback applies
// when nothing is eligible.
func TestPathSelection(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"}, // the phone's Bridge
		lab.Spec{Name: "ir2"}, // domestic Exit
		lab.Spec{Name: "ir3"}, // a neighbouring Bridge
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "ab2"},
		lab.Spec{Name: "phone1"},
		lab.At("web-ir", "2.144.0.10"),
		lab.At("web-ab", "93.184.215.14"),
		lab.At("dns-ab", "9.9.9.9"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	sc := protocol.DefaultScoring
	sc.RebalanceAfterSec, sc.RebalanceEverySec = 3, 1 // converge in seconds, not hours
	co.c.Scoring = sc
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	dumpNetOnFailure(t, l, "ir1", "ir3", "ab1", "ab2")
	// Equal capacities, so throughput tests that land during the test
	// keep the shares equal; unlimited, they would measure CPU noise.
	for _, ab := range []string{"ab1", "ab2"} {
		l.Netem(l.Node(ab), lab.Impairment{Rate: 50_000_000})
	}

	co.c.FeedURL, co.c.RIPEstatURL = serveFeed(t, irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8")), ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}
	whoami(t, l.Node("web-ir"), 7)
	whoami(t, l.Node("web-ab"), 7)

	// Nothing to the site abroad may ever leave from a domestic Node's own
	// address.
	leak := l.Count(fmt.Sprintf("ip saddr { %s, %s, %s } ip daddr 93.184.215.14",
		l.Node("ir1").Addr(), l.Node("ir2").Addr(), l.Node("ir3").Addr()))

	user, _ := co.store.CreateUser(ctx, "maryam")
	key, _ := wgtypes.GeneratePrivateKey()
	dev, err := co.store.CreateDevice(ctx, user.ID, "phone", key.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}

	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ir2", []protocol.Role{protocol.RoleExit}, []string{"IR"})
	m.startAgent("ir3", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	m.startAgent("ab2", []protocol.Role{protocol.RoleExit}, nil)

	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p := newPhone(t, l.Node("phone1"), key, dev.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	abroad := func() (string, error) { return p.ask("93.184.215.14:7") }
	domestic := func() (string, error) { return p.ask("2.144.0.10:7") }
	ab1, ab2 := l.Node("ab1").Addr().String(), l.Node("ab2").Addr().String()
	ir2 := l.Node("ir2").Addr().String()
	const irRule, defaultRule = 1, 2

	// Both abroad Exits share the default Rule's buckets.
	testutil.Eventually(t, 120*time.Second, "ir1 spreads the default Rule over both abroad Exits", func() bool {
		rh := m.ruleHealth("ir1", defaultRule)
		if rh.Mode != "direct" || len(rh.Shares) != 2 {
			return false
		}
		for _, n := range rh.Shares {
			if n < 22 { // within ten points of half
				return false
			}
		}
		return true
	})
	p.leavesFrom("domestic through the domestic Exit", domestic, ir2, 30*time.Second)
	var carrier string
	testutil.Eventually(t, 30*time.Second, "the phone reaches the site abroad", func() bool {
		got, err := abroad()
		carrier = got
		return err == nil && (got == ab1 || got == ab2)
	})

	// Killing the Exit that carries the phone moves its bucket within seconds.
	other := map[string]string{ab1: ab2, ab2: ab1}[carrier]
	name := map[string]string{ab1: "ab1", ab2: "ab2"}
	kill := l.Filter(lab.DropAll(l.Node(name[carrier])))
	start := time.Now()
	p.leavesFrom("abroad after its Exit died", abroad, other, 20*time.Second)
	moved := time.Since(start)
	t.Logf("the phone moved from %s to %s in %v", name[carrier], name[other], moved.Round(100*time.Millisecond))
	if moved > 10*time.Second {
		t.Errorf("moving off a dead Exit took %v, want seconds", moved)
	}
	// The shares come in ir1's next health report.
	testutil.Eventually(t, 5*time.Second, "every default-Rule bucket on the surviving Exit", func() bool {
		return len(m.ruleHealth("ir1", defaultRule).Shares) == 1
	})
	kill.Remove()

	// Cutting ir1 off from both abroad Exits hands its traffic to ir3.
	cut1 := l.Filter(lab.DropBetween(l.Node("ir1"), l.Node("ab1")))
	cut2 := l.Filter(lab.DropBetween(l.Node("ir1"), l.Node("ab2")))
	testutil.Eventually(t, 30*time.Second, "ir1 carries the default Rule through ir3", func() bool {
		return m.ruleHealth("ir1", defaultRule).Mode == "transit"
	})
	testutil.Eventually(t, 20*time.Second, "the phone reaches the site abroad through ir3", func() bool {
		got, err := abroad()
		return err == nil && (got == ab1 || got == ab2)
	})

	// With the domestic Exit gone too, the IR Rule falls through to the
	// default Rule: the site in Iran sees an abroad Exit.
	killIR := l.Filter(lab.DropAll(l.Node("ir2")))
	testutil.Eventually(t, 30*time.Second, "the IR Rule falls back", func() bool {
		return m.ruleHealth("ir1", irRule).Mode == "fallback"
	})
	testutil.Eventually(t, 20*time.Second, "domestic traffic leaves abroad", func() bool {
		got, err := domestic()
		return err == nil && slices.Contains([]string{ab1, ab2}, got)
	})

	// With nothing eligible anywhere, the default Rule drops: no answer,
	// and nothing leaks out of a domestic uplink.
	killAb1 := l.Filter(lab.DropAll(l.Node("ab1")))
	killAb2 := l.Filter(lab.DropAll(l.Node("ab2")))
	testutil.Eventually(t, 30*time.Second, "the default Rule has nothing eligible", func() bool {
		return m.ruleHealth("ir1", defaultRule).Mode == "fallback"
	})
	time.Sleep(2 * time.Second)
	for _, ask := range []func() (string, error){abroad, domestic} {
		if got, err := ask(); err == nil {
			t.Errorf("got an answer %q with every Exit gone", got)
		}
	}
	if n := leak.Packets(); n != 0 {
		t.Errorf("%d packets to the site abroad left from a domestic Node", n)
	}

	// Everything comes back once the Exits do.
	for _, f := range []*lab.FilterRule{killAb1, killAb2, killIR, cut1, cut2} {
		f.Remove()
	}
	testutil.Eventually(t, 60*time.Second, "the phone reaches the site abroad after recovery", func() bool {
		got, err := abroad()
		return err == nil && (got == ab1 || got == ab2)
	})
}
