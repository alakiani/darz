package dataplane

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The data plane's names and numbers.
const (
	// DeviceIface is the Bridge's Device-facing WireGuard interface. It
	// must not match "dz*", the Link instances' pattern.
	DeviceIface = "darz0"
	devicePool  = "10.77.0.0/16"

	// A packet's Rule mark on a Bridge, and a connection's Link mark on an
	// Exit. The high half tells them apart.
	ruleMarkBase = 0x10000
	linkMarkBase = 0x20000
	markKind     = 0xffff0000

	// Routing tables: one per Rule, and one that drops.
	ruleTableBase = 20000
	dropTable     = 29999

	// ip rule priorities, all ahead of the main table (32766).
	prioRules = 2000
	prioLinks = 2100
	prioDrop  = 2900 // Device traffic that somehow has no Rule mark
)

func ruleMark(id uint64) uint32 { return ruleMarkBase | uint32(id) }
func linkMark(id uint64) uint32 { return linkMarkBase | uint32(id) }
func ruleTable(id uint64) int   { return ruleTableBase + int(id) }

// ruleset is what the nftables ruleset is made from.
type ruleset struct {
	// Bridge side.
	Bridge       bool
	ResolverAddr string
	Rules        []nftRule // in priority order
	DefaultRule  uint64
	LocalExit    bool // this Bridge is also an Exit and carries some Rule itself

	// Exit side: each Link instance's interface and its Link.
	ExitIfaces map[string]uint64

	Uplink string
}

type nftRule struct {
	ID       uint64
	Elements []string
}

// render writes the whole ruleset as one nft -f transaction that replaces
// the previous one atomically (ADR 0004).
func render(r ruleset) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("table inet darz")
	w("delete table inet darz")
	w("table inet darz {")

	if r.Bridge {
		for _, rule := range r.Rules {
			if len(rule.Elements) == 0 {
				w("\tset rule_%d { type ipv4_addr; flags interval; }", rule.ID)
			} else {
				w("\tset rule_%d { type ipv4_addr; flags interval; elements = { %s } }", rule.ID, strings.Join(rule.Elements, ", "))
			}
		}
	}

	w("\tchain prerouting {")
	w("\t\ttype filter hook prerouting priority mangle; policy accept;")
	if r.Bridge {
		// IPv6 is black-holed (ADR 0003).
		w("\t\tiifname %q meta nfproto ipv6 drop", DeviceIface)
		// DNS over TLS goes abroad whatever its destination (ADR 0004).
		w("\t\tiifname %q tcp dport 853 meta mark set %#x accept", DeviceIface, ruleMark(r.DefaultRule))
		for _, rule := range r.Rules {
			w("\t\tiifname %q ip daddr @rule_%d meta mark set %#x accept", DeviceIface, rule.ID, ruleMark(rule.ID))
		}
	}
	if len(r.ExitIfaces) > 0 {
		var entries []string
		for _, iface := range slices.Sorted(maps.Keys(r.ExitIfaces)) {
			entries = append(entries, fmt.Sprintf("%q : %#x", iface, linkMark(r.ExitIfaces[iface])))
		}
		w("\t\tct state new ct mark set iifname map { %s }", strings.Join(entries, ", "))
	}
	// Replies to connections that arrived over a Link go back over it.
	w("\t\tiifname != \"dz*\" ct mark & %#x == %#x meta mark set ct mark", markKind, linkMarkBase)
	w("\t}")

	if r.Bridge {
		w("\tchain dns {")
		w("\t\ttype nat hook prerouting priority dstnat; policy accept;")
		w("\t\tiifname %q meta l4proto { tcp, udp } th dport 53 dnat ip to %s:53", DeviceIface, r.ResolverAddr)
		w("\t}")
	}

	// Links carry less than 1500 bytes; clamp TCP to the path.
	w("\tchain forward {")
	w("\t\ttype filter hook forward priority mangle; policy accept;")
	w("\t\ttcp flags syn / syn,rst tcp option maxseg size set rt mtu")
	w("\t}")

	w("\tchain postrouting {")
	w("\t\ttype nat hook postrouting priority srcnat; policy accept;")
	if len(r.ExitIfaces) > 0 {
		// NAT happens only at the Exit (ADR 0004).
		w("\t\toifname %q ct mark & %#x == %#x masquerade", r.Uplink, markKind, linkMarkBase)
	}
	if r.Bridge && r.LocalExit {
		w("\t\toifname %q ip saddr %s masquerade", r.Uplink, devicePool)
	}
	w("\t}")
	w("}")
	return b.String()
}
