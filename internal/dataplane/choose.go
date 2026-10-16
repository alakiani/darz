package dataplane

import (
	"slices"

	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

type routeKind int

const (
	drop    routeKind = iota // blackhole: fail closed
	local                    // out of this Node's own uplink
	viaLink                  // over a Link to an Exit
)

// route is where one Rule's traffic goes.
type route struct {
	kind  routeKind
	iface string // for viaLink: the Link's active instance
}

// chooseRoutes picks each Rule's route. A zero-Link Path wins outright;
// otherwise the first Up Link to an Exit carrying the Rule's Tags. A Rule
// with nowhere to go falls through to the next Rule, or drops (ADR 0004).
//
// Session 5 replaces this with buckets spread over every eligible
// candidate (ADR 0008).
func chooseRoutes(rules []protocol.Rule, self protocol.NodeInfo, links []link.Status) map[uint64]route {
	out := map[uint64]route{}
	var resolve func(i int) route
	resolve = func(i int) route {
		r := rules[i]
		if slices.Contains(self.Roles, protocol.RoleExit) && r.Matches(self.Tags) {
			return route{kind: local}
		}
		for _, l := range links {
			if l.Up && slices.Contains(l.Peer.Roles, protocol.RoleExit) && r.Matches(l.Peer.Tags) {
				return route{kind: viaLink, iface: l.Iface}
			}
		}
		if r.Fallback == protocol.FallbackNext && i+1 < len(rules) {
			return resolve(i + 1)
		}
		return route{kind: drop}
	}
	for i, r := range rules {
		out[r.ID] = resolve(i)
	}
	return out
}
