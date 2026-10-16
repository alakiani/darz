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

	// A packet's candidate mark picks its routing table. Over a Link it is
	// the Link's mark, which a connection arriving over a Link also keeps
	// as its ct mark, so replies go back the way they came.
	linkMarkBase = 0x20000
	markKind     = 0xffff0000
	localMark    = 0x40000 // out of this Node's own uplink
	dropMark     = 0x40001 // nowhere: fail closed

	dropTable = 29999

	// ip rule priorities, all ahead of the main table (32766). Rules in
	// prioRules..prioMax belong to the data plane.
	prioRules = 2000
	prioLinks = 2100
	prioMax   = 2999
)

func linkMark(id uint64) uint32 { return linkMarkBase | uint32(id) }

// markFor is the packet mark of a candidate key.
func markFor(key string) uint32 {
	switch key {
	case keyLocal:
		return localMark
	case keyDrop, "":
		return dropMark
	}
	var id uint64
	fmt.Sscanf(key, "link:%d", &id)
	return linkMark(id)
}

// ruleset is what the nftables ruleset is made from.
type ruleset struct {
	// Bridge side.
	Bridge       bool
	ResolverAddr string
	Rules        []nftRule // in priority order
	DefaultRule  uint64
	// TransitIfaces are the instances of Links to neighbouring Bridges,
	// whose traffic this Bridge carries on as transit.
	TransitIfaces []string

	// LinkIfaces maps every Link instance's interface to its Link.
	LinkIfaces map[string]uint64
	// Exit is set on Nodes that hold the Exit Role: they masquerade what
	// leaves their uplink for others.
	Exit   bool
	Uplink string
}

type nftRule struct {
	ID       uint64
	Elements []string
	// Device and Transit map each bucket to a candidate key.
	Device, Transit [buckets]string
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
			w("\tmap dev_%d { type ipv4_addr : mark; elements = { %s } }", rule.ID, bucketElements(rule.Device))
			w("\tmap tr_%d { type ipv4_addr : mark; elements = { %s } }", rule.ID, bucketElements(rule.Transit))
		}
		// Metering: bytes per Device address per candidate mark.
		w("\tset usage_up { type ipv4_addr . mark; flags dynamic; size 65536; }")
		w("\tset usage_down { type ipv4_addr . mark; flags dynamic; size 65536; }")
		if len(r.TransitIfaces) > 0 {
			w("\tset transit_ifaces { type ifname; elements = { %s } }", quoteAll(r.TransitIfaces))
		} else {
			w("\tset transit_ifaces { type ifname; }")
		}
	}

	// The bucket of a Device is the low 6 bits of its address: addresses
	// are handed out in order, so Devices spread evenly (ADR 0008).
	bucket := "ip saddr & 0.0.0.63"

	w("\tchain prerouting {")
	w("\t\ttype filter hook prerouting priority mangle; policy accept;")
	if len(r.LinkIfaces) > 0 {
		var entries []string
		for _, iface := range slices.Sorted(maps.Keys(r.LinkIfaces)) {
			entries = append(entries, fmt.Sprintf("%q : %#x", iface, linkMark(r.LinkIfaces[iface])))
		}
		// A connection that arrives over a Link remembers which one.
		w("\t\tct state new counter ct mark set iifname map { %s }", strings.Join(entries, ", "))
	}
	// Its replies go back over that Link.
	w("\t\tct direction reply ct mark & %#x == %#x counter meta mark set ct mark", markKind, linkMarkBase)
	if r.Bridge {
		// IPv6 is black-holed (ADR 0003).
		w("\t\tiifname %q meta nfproto ipv6 drop", DeviceIface)
		// DNS over TLS goes abroad whatever its destination (ADR 0004).
		w("\t\tiifname %q tcp dport 853 counter meta mark set %s map @dev_%d accept", DeviceIface, bucket, r.DefaultRule)
		for _, rule := range r.Rules {
			w("\t\tiifname %q ip daddr @rule_%d counter meta mark set %s map @dev_%d accept", DeviceIface, rule.ID, bucket, rule.ID)
		}
		// Fail closed: Device traffic no Rule claimed goes nowhere.
		w("\t\tiifname %q counter drop", DeviceIface)

		// Transit from a neighbouring Bridge is classified by the same
		// Rules, but only onto this Bridge's own direct and local
		// candidates (ADR 0008).
		w("\t\tiifname @transit_ifaces ct direction original tcp dport 853 counter meta mark set %s map @tr_%d accept", bucket, r.DefaultRule)
		for _, rule := range r.Rules {
			w("\t\tiifname @transit_ifaces ct direction original ip daddr @rule_%d counter meta mark set %s map @tr_%d accept", rule.ID, bucket, rule.ID)
		}
	}
	w("\t}")

	// A nat chain on prerouting must exist even when empty: without one the
	// kernel does not translate replies to masqueraded connections back.
	w("\tchain natpre {")
	w("\t\ttype nat hook prerouting priority dstnat; policy accept;")
	if r.Bridge {
		w("\t\tiifname %q meta l4proto { tcp, udp } th dport 53 counter dnat ip to %s:53", DeviceIface, r.ResolverAddr)
	}
	w("\t}")

	// Links carry less than 1500 bytes; clamp TCP to the path.
	w("\tchain forward {")
	w("\t\ttype filter hook forward priority mangle; policy accept;")
	w("\t\ttcp flags syn / syn,rst tcp option maxseg size set rt mtu")
	if r.Bridge {
		// A Device's connection remembers its candidate, tagged so the
		// return-over-a-Link rule never takes it for a Link's mark; then
		// both directions are counted by that candidate.
		w("\t\tiifname %q ct state new ct mark set meta mark or %#x", DeviceIface, deviceConnBit)
		w("\t\tiifname %q update @usage_up { ip saddr . meta mark counter }", DeviceIface)
		w("\t\toifname %q update @usage_down { ip daddr . ct mark counter }", DeviceIface)
	}
	w("\t}")

	w("\tchain postrouting {")
	w("\t\ttype nat hook postrouting priority srcnat; policy accept;")
	if r.Exit {
		// NAT happens only at the Exit (ADR 0004): traffic that came over a
		// Link, and this Bridge's own Devices on a local exit.
		w("\t\toifname %q ct mark & %#x == %#x counter masquerade", r.Uplink, markKind, linkMarkBase)
		if r.Bridge {
			w("\t\toifname %q ip saddr %s counter masquerade", r.Uplink, devicePool)
		}
	}
	w("\t}")
	w("}")
	return b.String()
}

func bucketElements(assign [buckets]string) string {
	var parts []string
	for i, key := range assign {
		parts = append(parts, fmt.Sprintf("0.0.0.%d : %#x", i, markFor(key)))
	}
	return strings.Join(parts, ", ")
}

// bucketUpdates is the nft transaction that moves changed buckets, each
// one an atomic element update (ADR 0008).
func bucketUpdates(mapName string, old, new [buckets]string) string {
	var b strings.Builder
	for i := range new {
		if markFor(old[i]) == markFor(new[i]) {
			continue
		}
		fmt.Fprintf(&b, "delete element inet darz %s { 0.0.0.%d }\n", mapName, i)
		fmt.Fprintf(&b, "add element inet darz %s { 0.0.0.%d : %#x }\n", mapName, i, markFor(new[i]))
	}
	return b.String()
}

func quoteAll(ss []string) string {
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return strings.Join(out, ", ")
}
