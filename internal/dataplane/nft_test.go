package dataplane

import (
	"strings"
	"testing"
)

func allOn(key string) (a [buckets]string) {
	for i := range a {
		a[i] = key
	}
	return a
}

func bridgeRuleset() ruleset {
	ir := nftRule{ID: 1, Elements: []string{"2.144.0.0/14", "5.160.0.0/16"}, Device: allOn("link:3"), Transit: allOn("link:3")}
	def := nftRule{ID: 2, Elements: []string{"0.0.0.0/0"}, Device: allOn("link:4"), Transit: allOn(keyDrop)}
	return ruleset{
		Bridge: true, ResolverAddr: "10.77.0.1", Rules: []nftRule{ir, def}, DefaultRule: 2,
		TransitIfaces: []string{"dz9i"}, LinkIfaces: map[string]uint64{"dz9i": 9, "dz3i": 3, "dz4i": 4}, Uplink: "eth0",
	}
}

func TestBridgeRuleset(t *testing.T) {
	got := render(bridgeRuleset())
	for _, want := range []string{
		"table inet darz\ndelete table inet darz\n", // replaced atomically
		"set rule_1 { type ipv4_addr; flags interval; elements = { 2.144.0.0/14, 5.160.0.0/16 } }",
		"map dev_1 { type ipv4_addr : mark; elements = { 0.0.0.0 : 0x20003, 0.0.0.1 : 0x20003,",
		"0.0.0.63 : 0x20003 } }",
		"map tr_2 { type ipv4_addr : mark; elements = { 0.0.0.0 : 0x40001,",
		`set transit_ifaces { type ifname; elements = { "dz9i" } }`,
		`iifname "darz0" meta nfproto ipv6 drop`,
		`iifname "darz0" tcp dport 853 counter meta mark set ip saddr & 0.0.0.63 map @dev_2 accept`,
		`iifname "darz0" ip daddr @rule_1 counter meta mark set ip saddr & 0.0.0.63 map @dev_1 accept`,
		`iifname "darz0" ip daddr @rule_2 counter meta mark set ip saddr & 0.0.0.63 map @dev_2 accept`,
		`iifname "darz0" counter drop`,
		`iifname @transit_ifaces ct direction original ip daddr @rule_1 counter meta mark set ip saddr & 0.0.0.63 map @tr_1 accept`,
		`iifname "darz0" meta l4proto { tcp, udp } th dport 53 counter dnat ip to 10.77.0.1:53`,
		`ct state new counter ct mark set iifname map { "dz3i" : 0x20003, "dz4i" : 0x20004, "dz9i" : 0x20009 }`,
		`ct direction reply ct mark & 0xffff0000 == 0x20000 counter meta mark set ct mark`,
		"tcp option maxseg size set rt mtu",
		"set usage_up { type ipv4_addr . mark; flags dynamic; size 65536; }",
		`iifname "darz0" ct state new ct mark set meta mark or 0x10000`,
		`iifname "darz0" update @usage_up { ip saddr . meta mark counter }`,
		`oifname "darz0" update @usage_down { ip daddr . ct mark counter }`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	pos := func(s string) int { return strings.Index(got, s) }
	if pos("tcp dport 853") > pos("@rule_1") {
		t.Error("DoT is marked after the Rules, so an IR-set resolver would leave domestically")
	}
	if pos(`iifname "darz0" counter drop`) < pos("map @dev_2 accept\n") {
		t.Error("the fail-closed drop comes before the Rules")
	}
	if strings.Contains(got, "masquerade") {
		t.Error("a Bridge that is no Exit masquerades")
	}
}

func TestExitRuleset(t *testing.T) {
	got := render(ruleset{Exit: true, LinkIfaces: map[string]uint64{"dz3l": 7, "dz4l": 7, "dz9l": 2}, Uplink: "eth0"})
	for _, want := range []string{
		`ct state new counter ct mark set iifname map { "dz3l" : 0x20007, "dz4l" : 0x20007, "dz9l" : 0x20002 }`,
		`oifname "eth0" ct mark & 0xffff0000 == 0x20000 counter masquerade`,
		// Replies to masqueraded connections are only translated back while
		// a nat chain hooks prerouting.
		"type nat hook prerouting",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "darz0") || strings.Contains(got, "ip saddr 10.77.0.0/16") {
		t.Errorf("an Exit ruleset mentions Devices:\n%s", got)
	}
}

func TestDualRoleMasqueradesItsDevices(t *testing.T) {
	r := bridgeRuleset()
	r.Exit = true
	if got := render(r); !strings.Contains(got, `oifname "eth0" ip saddr 10.77.0.0/16 counter masquerade`) {
		t.Errorf("a dual-role Node does not masquerade its Devices:\n%s", got)
	}
}

// The monitoring port answers the Coordinator VM only; everyone else,
// over IPv4 or IPv6, is dropped without a reply (ADR 0006).
func TestMonitoringPortIsAllowlisted(t *testing.T) {
	r := bridgeRuleset()
	if strings.Contains(render(r), "tcp dport 9443") {
		t.Error("monitoring rules with no monitoring port")
	}
	r.MonitorPort, r.MonitorAllow = 9443, []string{"192.0.2.100", "2001:db8::1"}
	got := render(r)
	for _, want := range []string{
		"\tchain input {\n\t\ttype filter hook input priority filter; policy accept;\n",
		"\t\ttcp dport 9443 ip saddr { 192.0.2.100 } accept\n",
		"\t\ttcp dport 9443 ip6 saddr { 2001:db8::1 } accept\n",
		"\t\ttcp dport 9443 drop\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "tcp dport 9443 drop") < strings.Index(got, "accept\n\t\ttcp dport 9443 ip6") {
		t.Error("the drop comes before the allowlist")
	}
}

// GRE has no authentication, so an Exit takes it only from current peers,
// and on a GRE interface forwards only new traffic from Devices or the mesh
// (ADR 0009). Replies to this Node's own connections still come back.
func TestGREIsLimitedToPeersAndOurRanges(t *testing.T) {
	r := ruleset{Exit: true, LinkIfaces: map[string]uint64{"dz3l": 7, "dz4l": 7}, Uplink: "eth0",
		GREIfaces: []string{"dz3l"}, GREPeers: []string{"203.0.113.9", "198.51.100.4"}}
	got := render(r)
	for _, want := range []string{
		"\tchain input {\n\t\ttype filter hook input priority filter; policy accept;\n",
		"\t\tmeta l4proto gre ip saddr { 198.51.100.4, 203.0.113.9 } accept\n",
		"\t\tmeta l4proto gre drop\n",
		`iifname { "dz3l" } ct direction original ip saddr != { 10.77.0.0/16, 10.78.0.0/16 } counter drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "meta l4proto gre drop") < strings.Index(got, "meta l4proto gre ip saddr") {
		t.Error("GRE is dropped before the peers are accepted")
	}
	forward := got[strings.Index(got, "chain forward"):]
	if !strings.Contains(forward[:strings.Index(forward, "\n\t}")], "ct direction original ip saddr !=") {
		t.Error("the source check is not in the forward chain")
	}

	// No GRE peer: no GRE at all.
	got = render(ruleset{Exit: true, Uplink: "eth0"})
	if !strings.Contains(got, "\t\tmeta l4proto gre drop\n") || strings.Contains(got, "gre ip saddr") || strings.Contains(got, "10.78.0.0/16") {
		t.Errorf("ruleset without GRE peers:\n%s", got)
	}
}

func TestBucketUpdatesTouchOnlyMovedBuckets(t *testing.T) {
	old := allOn("link:3")
	moved := old
	moved[5], moved[60] = "link:4", keyDrop
	got := bucketUpdates("dev_2", old, moved)
	want := "delete element inet darz dev_2 { 0.0.0.5 }\nadd element inet darz dev_2 { 0.0.0.5 : 0x20004 }\n" +
		"delete element inet darz dev_2 { 0.0.0.60 }\nadd element inet darz dev_2 { 0.0.0.60 : 0x40001 }\n"
	if got != want {
		t.Errorf("updates =\n%s\nwant\n%s", got, want)
	}
	if bucketUpdates("dev_2", old, old) != "" {
		t.Error("no change produced updates")
	}
}
