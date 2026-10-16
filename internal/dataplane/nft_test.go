package dataplane

import (
	"strings"
	"testing"
)

func TestBridgeRuleset(t *testing.T) {
	got := render(ruleset{
		Bridge:       true,
		ResolverAddr: "10.77.0.1",
		Rules: []nftRule{
			{ID: 1, Elements: []string{"2.144.0.0/14", "5.160.0.0/16"}},
			{ID: 2, Elements: []string{"0.0.0.0/0"}},
		},
		DefaultRule: 2,
		Uplink:      "eth0",
	})
	for _, want := range []string{
		"table inet darz\ndelete table inet darz\n", // replaced atomically
		"set rule_1 { type ipv4_addr; flags interval; elements = { 2.144.0.0/14, 5.160.0.0/16 } }",
		`iifname "darz0" meta nfproto ipv6 drop`,
		// DoT goes abroad whatever its destination, before any Rule.
		`iifname "darz0" tcp dport 853 counter meta mark set 0x10002 accept`,
		`iifname "darz0" ip daddr @rule_1 counter meta mark set 0x10001 accept`,
		`iifname "darz0" ip daddr @rule_2 counter meta mark set 0x10002 accept`,
		`iifname "darz0" meta l4proto { tcp, udp } th dport 53 counter dnat ip to 10.77.0.1:53`,
		"tcp option maxseg size set rt mtu",
		`iifname "darz0" counter drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "tcp dport 853") > strings.Index(got, "@rule_1") {
		t.Error("DoT is marked after the Rules, so an IR-set resolver would leave domestically")
	}
	if strings.Contains(got, "masquerade") {
		t.Error("a Bridge that is no Exit masquerades")
	}
	if strings.Index(got, `iifname "darz0" counter drop`) < strings.Index(got, "@rule_2") {
		t.Error("the fail-closed drop comes before the Rules")
	}
}

func TestExitRuleset(t *testing.T) {
	got := render(ruleset{
		ExitIfaces: map[string]uint64{"dz3i": 7, "dz4i": 7, "dz9l": 2},
		Uplink:     "eth0",
	})
	for _, want := range []string{
		// New connections from a Link remember it, so replies go back over it.
		`ct state new counter ct mark set iifname map { "dz3i" : 0x20007, "dz4i" : 0x20007, "dz9l" : 0x20002 }`,
		`iifname != "dz*" ct mark & 0xffff0000 == 0x20000 counter meta mark set ct mark`,
		`oifname "eth0" ct mark & 0xffff0000 == 0x20000 counter masquerade`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "darz0") {
		t.Error("an Exit ruleset mentions the Device interface")
	}
	// Replies to masqueraded connections are only translated back while a
	// nat chain hooks prerouting; an Exit has no DNS redirect to put there.
	if !strings.Contains(got, "type nat hook prerouting") {
		t.Errorf("an Exit ruleset has no nat prerouting chain:\n%s", got)
	}
}

func TestLocalExitMasqueradesDevices(t *testing.T) {
	got := render(ruleset{Bridge: true, LocalExit: true, ResolverAddr: "10.77.0.1",
		Rules: []nftRule{{ID: 2, Elements: []string{"0.0.0.0/0"}}}, DefaultRule: 2, Uplink: "eth0"})
	if !strings.Contains(got, `oifname "eth0" ip saddr 10.77.0.0/16 counter masquerade`) {
		t.Errorf("local exit does not masquerade Devices:\n%s", got)
	}
}

func TestEmptySetHasNoElementsClause(t *testing.T) {
	got := render(ruleset{Bridge: true, ResolverAddr: "10.77.0.1", Rules: []nftRule{{ID: 1}}, DefaultRule: 1, Uplink: "eth0"})
	if !strings.Contains(got, "set rule_1 { type ipv4_addr; flags interval; }") {
		t.Errorf("empty set rendered wrong:\n%s", got)
	}
}
