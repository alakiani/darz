package dataplane

import (
	"testing"
	"time"

	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

var (
	irRule  = protocol.Rule{ID: 1, Name: "IR", RequiredTags: []string{"IR"}, Fallback: protocol.FallbackNext}
	defRule = protocol.Rule{ID: 2, Name: "Default", RequiredTags: []string{"!IR"}, Fallback: protocol.FallbackDrop}
	rules   = []protocol.Rule{irRule, defRule}

	t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	sc = protocol.DefaultScoring
)

func direct(key string, weight float64, eligible bool) candidate {
	return candidate{key: key, kind: candDirect, weight: weight, eligible: eligible}
}

func counts(b *bucketState) map[string]int {
	out := map[string]int{}
	for _, k := range b.assign {
		out[k]++
	}
	return out
}

func TestEqualCandidatesSplitTheBuckets(t *testing.T) {
	b := &bucketState{}
	b.step(t0, []candidate{direct("link:1", 1, true), direct("link:2", 1, true)}, sc)
	if c := counts(b); c["link:1"] != 32 || c["link:2"] != 32 || b.mode != modeDirect {
		t.Errorf("shares = %v, mode %s; want 32/32 direct", c, b.mode)
	}
	// Unequal weights give proportional shares.
	b = &bucketState{}
	b.step(t0, []candidate{direct("link:1", 1, true), direct("link:2", 3, true)}, sc)
	if c := counts(b); c["link:1"] != 16 || c["link:2"] != 48 {
		t.Errorf("1:3 shares = %v", c)
	}
}

func TestFailureMovesAreImmediateAndTouchOnlyTheDeadCandidate(t *testing.T) {
	b := &bucketState{}
	three := []candidate{direct("link:1", 1, true), direct("link:2", 1, true), direct("link:3", 1, true)}
	b.step(t0, three, sc)
	before := b.assign

	three[0].eligible = false
	moves := b.step(t0.Add(time.Second), three, sc)
	if c := counts(b); c["link:1"] != 0 || c["link:2"]+c["link:3"] != 64 {
		t.Fatalf("after link:1 died: %v", c)
	}
	for i, k := range before {
		if k != "link:1" && b.assign[i] != k {
			t.Errorf("bucket %d moved from live %s to %s", i, k, b.assign[i])
		}
	}
	for _, m := range moves {
		if m.kind != moveFailure || m.from != "link:1" {
			t.Errorf("move %+v, want failure moves off link:1", m)
		}
	}
	if c := counts(b); c["link:2"]-c["link:3"] > 1 || c["link:3"]-c["link:2"] > 1 {
		t.Errorf("orphans spread unevenly: %v", c)
	}
}

func TestRecoveredCandidateFillsOnlyByRebalancing(t *testing.T) {
	b := &bucketState{}
	two := []candidate{direct("link:1", 1, true), direct("link:2", 1, false)}
	b.step(t0, two, sc)
	if c := counts(b); c["link:1"] != 64 {
		t.Fatalf("one eligible: %v", c)
	}

	two[1].eligible = true
	now := t0.Add(time.Second)
	if moves := b.step(now, two, sc); len(moves) != 0 {
		t.Fatalf("recovery moved %d buckets at once, want none", len(moves))
	}
	// Off target by 50 points, but not for 15 minutes yet.
	now = now.Add(14 * time.Minute)
	if moves := b.step(now, two, sc); len(moves) != 0 {
		t.Fatalf("rebalanced after 14 minutes")
	}
	now = now.Add(time.Minute)
	moves := b.step(now, two, sc)
	if len(moves) != 1 || moves[0].kind != moveRebalance || moves[0].from != "link:1" || moves[0].to != "link:2" {
		t.Fatalf("at 15 minutes: moves %+v, want one rebalancing move to link:2", moves)
	}
	// Then one bucket every 5 minutes, not sooner.
	if moves := b.step(now.Add(4*time.Minute), two, sc); len(moves) != 0 {
		t.Errorf("moved again after 4 minutes")
	}
	if moves := b.step(now.Add(5*time.Minute), two, sc); len(moves) != 1 {
		t.Errorf("no move after 5 minutes")
	}
	// Within 10 points of target, it stops.
	for i := 0; i < 40; i++ {
		now = now.Add(5 * time.Minute)
		b.step(now, two, sc)
	}
	if c := counts(b); c["link:2"] < 26 || c["link:2"] > 32 {
		t.Errorf("after rebalancing: %v, want link:2 within 10 points of half", c)
	}
}

func TestCutoffStarvesAPoorCandidate(t *testing.T) {
	b := &bucketState{}
	b.step(t0, []candidate{direct("link:1", 1, true), direct("link:2", 0.2, true)}, sc)
	if c := counts(b); c["link:2"] != 0 || c["link:1"] != 64 {
		t.Errorf("shares = %v, want nothing for a candidate under 25%% of the best", c)
	}
}

func TestTransitOnlyWhenNoDirectIsLeft(t *testing.T) {
	transit := func(key string, w float64, ok bool) candidate {
		return candidate{key: key, kind: candTransit, weight: w, eligible: ok}
	}
	cands := []candidate{direct("link:1", 1, true), transit("link:5", 1, true), transit("link:6", 2, true)}
	b := &bucketState{}
	b.step(t0, cands, sc)
	if c := counts(b); c["link:1"] != 64 {
		t.Fatalf("with a direct candidate: %v, want no transit", c)
	}
	cands[0].eligible = false
	b.step(t0.Add(time.Second), cands, sc)
	if c := counts(b); c["link:6"] != 64 || b.mode != modeTransit {
		t.Fatalf("no direct: %v (%s), want all on the best transit", c, b.mode)
	}
	// A direct candidate coming back takes everything back at once.
	cands[0].eligible = true
	b.step(t0.Add(2*time.Second), cands, sc)
	if c := counts(b); c["link:1"] != 64 || b.mode != modeDirect {
		t.Errorf("direct back: %v (%s)", c, b.mode)
	}
}

func TestLocalTakesEveryBucketAndNothingMeansFallback(t *testing.T) {
	b := &bucketState{}
	b.step(t0, []candidate{{key: keyLocal, kind: candLocal, weight: 1, eligible: true}, direct("link:1", 1, true)}, sc)
	if c := counts(b); c[keyLocal] != 64 || b.mode != modeLocal {
		t.Errorf("local: %v (%s)", c, b.mode)
	}
	b.step(t0, []candidate{direct("link:1", 1, false)}, sc)
	if b.mode != modeFallback {
		t.Errorf("nothing eligible: mode %s, want fallback", b.mode)
	}
}

func TestDeriveCandidates(t *testing.T) {
	exit := protocol.Peer{Roles: []protocol.Role{protocol.RoleExit}}
	irExit := protocol.Peer{Roles: []protocol.Role{protocol.RoleExit}, Tags: []string{"IR"}}
	bridge := protocol.Peer{Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}}
	links := []link.Status{
		{ID: 1, Up: true, Peer: exit, PeerEgressOK: true, RTT: 50 * time.Millisecond},
		{ID: 2, Up: true, Peer: exit, PeerEgressOK: false},
		{ID: 3, Up: false, Peer: irExit, PeerEgressOK: true},
		{ID: 4, Up: true, Peer: bridge, PeerTransitRules: []uint32{2}},
	}
	self := protocol.NodeInfo{Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}}

	byKey := func(cs []candidate) map[string]candidate {
		m := map[string]candidate{}
		for _, c := range cs {
			m[c.key] = c
		}
		return m
	}
	d := byKey(deriveCandidates(defRule, self, false, links, false))
	if !d["link:1"].eligible || d["link:2"].eligible || d["link:1"].kind != candDirect {
		t.Errorf("direct eligibility needs Up and egress OK: %+v", d)
	}
	if _, ok := d["link:3"]; ok {
		t.Error("an IR Exit is a candidate for the !IR default Rule")
	}
	if c := d["link:4"]; c.kind != candTransit || !c.eligible {
		t.Errorf("transit candidate = %+v, want eligible by the neighbour's flag", c)
	}
	ir := byKey(deriveCandidates(irRule, self, false, links, false))
	if ir["link:4"].eligible {
		t.Error("transit eligible for a Rule the neighbour does not carry")
	}
	if ir["link:3"].eligible {
		t.Error("a down Link is eligible")
	}
	// Traffic that arrived as transit never goes on to a third Bridge.
	if _, ok := byKey(deriveCandidates(defRule, self, false, links, true))["link:4"]; ok {
		t.Error("the transit view offers a transit candidate")
	}

	dual := protocol.NodeInfo{Roles: []protocol.Role{protocol.RoleBridge, protocol.RoleExit}, Tags: []string{"IR"}}
	if c := byKey(deriveCandidates(irRule, dual, true, links, false))[keyLocal]; c.kind != candLocal || !c.eligible {
		t.Errorf("dual-role IR Node: local = %+v", c)
	}
	if c := byKey(deriveCandidates(irRule, dual, false, links, false))[keyLocal]; c.eligible {
		t.Error("local is eligible while the Node's own egress check fails")
	}
}

func TestWeight(t *testing.T) {
	if w := weight(1, 1, 100*time.Millisecond, sc); w != 1 {
		t.Errorf("under the floor: %v", w)
	}
	if w := weight(1, 1, 300*time.Millisecond, sc); w != 0.5 {
		t.Errorf("300 ms: %v, want 150/300", w)
	}
	if w := weight(1, 0.5, 0, sc); w != 0.25 {
		t.Errorf("availability squared: %v", w)
	}
	if w := weight(40, 0.5, 300*time.Millisecond, sc); w != 5 {
		t.Errorf("40 Mbps × 0.5² × 150/300: %v, want 5", w)
	}
}

// ADR 0008's worked example: one Bridge, the default Rule, three Exits.
func TestWorkedExampleFromADR0008(t *testing.T) {
	frankfurt := weight(8, 0.97, 90*time.Millisecond, sc)
	amsterdam := weight(25, 1, 110*time.Millisecond, sc)
	virginia := weight(60, 1, 230*time.Millisecond, sc)
	for _, c := range []struct {
		name      string
		got, want float64
	}{{"Frankfurt", frankfurt, 7.5}, {"Amsterdam", amsterdam, 25}, {"Virginia", virginia, 39}} {
		if c.got < c.want-0.5 || c.got > c.want+0.5 {
			t.Errorf("%s weight = %.2f, want about %v", c.name, c.got, c.want)
		}
	}
	b := &bucketState{}
	b.step(t0, []candidate{direct("fra", frankfurt, true), direct("ams", amsterdam, true), direct("vir", virginia, true)}, sc)
	// 39% and 61% of 64 buckets.
	if c := counts(b); c["fra"] != 0 || c["ams"] != 25 || c["vir"] != 39 {
		t.Errorf("shares = %v, want Frankfurt cut off, Amsterdam 25, Virginia 39", c)
	}
}

func TestUnmeasuredCandidatesCountAtTheBestFigure(t *testing.T) {
	cs := []candidate{{key: "a", mbps: 30}, {key: "b"}, {key: "c", mbps: 10}}
	fillThroughput(cs)
	if cs[0].mbps != 30 || cs[1].mbps != 30 || cs[2].mbps != 10 {
		t.Errorf("figures = %v %v %v, want 30 30 10", cs[0].mbps, cs[1].mbps, cs[2].mbps)
	}
	// With nothing measured yet, throughput leaves the weights alone.
	none := []candidate{{key: "a"}, {key: "b"}}
	fillThroughput(none)
	if none[0].mbps != 1 || none[1].mbps != 1 {
		t.Errorf("figures = %v %v, want 1 1", none[0].mbps, none[1].mbps)
	}
}

func TestDeriveCandidatesCarriesTheLinkFigure(t *testing.T) {
	exit := protocol.Peer{Roles: []protocol.Role{protocol.RoleExit}}
	links := []link.Status{{ID: 1, Up: true, Peer: exit, PeerEgressOK: true, ThroughputMbps: 42}}
	cs := deriveCandidates(defRule, protocol.NodeInfo{Roles: []protocol.Role{protocol.RoleBridge}}, false, links, false)
	if len(cs) != 1 || cs[0].mbps != 42 {
		t.Errorf("candidates = %+v, want link:1 at 42 Mbps", cs)
	}
}

func TestAvailabilityPenalisesFlapping(t *testing.T) {
	var a availability
	a.observe(t0, true)
	if v := a.value(t0.Add(time.Hour), sc); v != 1 {
		t.Errorf("always up: %v", v)
	}
	// Down for 6 of 60 minutes: 0.9. Plus one switch to down in the last
	// hour, counted as one more minute: 54/60... minus 1 = 53/60.
	var b availability
	b.observe(t0, true)
	b.observe(t0.Add(30*time.Minute), false)
	b.observe(t0.Add(36*time.Minute), true)
	if v, want := b.value(t0.Add(time.Hour), sc), 53.0/60; v < want-0.001 || v > want+0.001 {
		t.Errorf("availability = %v, want %v", v, want)
	}
}

func TestResolveFallsThroughThenDrops(t *testing.T) {
	ir := &bucketState{mode: modeFallback}
	def := &bucketState{}
	def.step(t0, []candidate{direct("link:1", 1, true)}, sc)
	out := resolve(rules, map[uint64]*bucketState{1: ir, 2: def})
	if out[1][0] != "link:1" || out[2][0] != "link:1" {
		t.Errorf("IR falling to the default Rule: %v / %v", out[1][0], out[2][0])
	}
	def.step(t0, nil, sc)
	out = resolve(rules, map[uint64]*bucketState{1: ir, 2: def})
	if out[1][5] != keyDrop || out[2][5] != keyDrop {
		t.Errorf("nothing anywhere: %v / %v, want drop", out[1][5], out[2][5])
	}
}
