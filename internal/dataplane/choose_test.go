package dataplane

import (
	"testing"

	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

var (
	irRule  = protocol.Rule{ID: 1, Name: "IR", RequiredTags: []string{"IR"}, Fallback: protocol.FallbackNext}
	defRule = protocol.Rule{ID: 2, Name: "Default", RequiredTags: []string{"!IR"}, Fallback: protocol.FallbackDrop}
	rules   = []protocol.Rule{irRule, defRule}
)

func exitLink(id uint64, up bool, tags ...string) link.Status {
	return link.Status{ID: id, Up: up, Iface: "dz" + string(rune('0'+id)) + "i",
		Peer: protocol.Peer{Roles: []protocol.Role{protocol.RoleExit}, Tags: tags}}
}

func TestChooseRoutes(t *testing.T) {
	bridge := protocol.NodeInfo{Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}}
	dual := protocol.NodeInfo{Roles: []protocol.Role{protocol.RoleBridge, protocol.RoleExit}, Tags: []string{"IR"}}
	bridgeOnly := link.Status{ID: 5, Up: true, Iface: "dz5i", Peer: protocol.Peer{Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}}}

	cases := []struct {
		name     string
		self     protocol.NodeInfo
		links    []link.Status
		ir, dflt route
	}{
		{"domestic via an IR Exit, abroad via an abroad Exit", bridge,
			[]link.Status{exitLink(1, true), exitLink(2, true, "IR")},
			route{kind: viaLink, iface: "dz2i"}, route{kind: viaLink, iface: "dz1i"}},
		{"a dual-role IR Node exits IR traffic itself", dual,
			[]link.Status{exitLink(1, true), exitLink(2, true, "IR")},
			route{kind: local}, route{kind: viaLink, iface: "dz1i"}},
		{"no IR Exit: IR falls through to the default Rule", bridge,
			[]link.Status{exitLink(1, true), exitLink(2, false, "IR"), bridgeOnly},
			route{kind: viaLink, iface: "dz1i"}, route{kind: viaLink, iface: "dz1i"}},
		{"nothing abroad: the default Rule drops, and IR with it", bridge,
			[]link.Status{exitLink(1, false)},
			route{kind: drop}, route{kind: drop}},
	}
	for _, c := range cases {
		got := chooseRoutes(rules, c.self, c.links)
		if got[1] != c.ir || got[2] != c.dflt {
			t.Errorf("%s: IR %+v, default %+v; want %+v, %+v", c.name, got[1], got[2], c.ir, c.dflt)
		}
	}
}

func TestRuleMatches(t *testing.T) {
	if !defRule.Matches(nil) || defRule.Matches([]string{"IR"}) {
		t.Error("!IR matches wrong")
	}
	both := protocol.Rule{RequiredTags: []string{"IR", "fast"}}
	if both.Matches([]string{"IR"}) || !both.Matches([]string{"fast", "IR"}) {
		t.Error("required tags are not ANDed")
	}
}
