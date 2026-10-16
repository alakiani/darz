package dataplane

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

// Path selection (ADR 0008): each Bridge works out the candidates for each
// Rule from its own Links, ranks them on its own measurements, and spreads
// 64 buckets of Devices over them in proportion to their weights.

const buckets = 64

type candKind int

const (
	candLocal   candKind = iota // this Node's own uplink
	candDirect                  // over one Link to an Exit
	candTransit                 // over one Link to a neighbouring Bridge
)

// Candidate keys as they appear in bucket assignments and health.
const (
	keyLocal = "local"
	keyDrop  = "drop"
)

func linkKey(id uint64) string { return fmt.Sprintf("link:%d", id) }

type candidate struct {
	key      string
	kind     candKind
	linkID   uint64
	eligible bool
	rtt      time.Duration
	weight   float64
}

// deriveCandidates lists a Rule's candidates on this Bridge. In the transit
// view, for traffic another Bridge handed over, neighbouring Bridges are
// left out, so a Path never has more than two Links.
func deriveCandidates(r protocol.Rule, self protocol.NodeInfo, selfEgress bool, links []link.Status, transitView bool) []candidate {
	var out []candidate
	if slices.Contains(self.Roles, protocol.RoleExit) && r.Matches(self.Tags) {
		out = append(out, candidate{key: keyLocal, kind: candLocal, eligible: selfEgress})
	}
	for _, l := range links {
		if slices.Contains(l.Peer.Roles, protocol.RoleExit) && r.Matches(l.Peer.Tags) {
			out = append(out, candidate{key: linkKey(l.ID), kind: candDirect, linkID: l.ID, rtt: l.RTT,
				eligible: l.Up && l.PeerEgressOK})
			continue
		}
		if !transitView && slices.Contains(l.Peer.Roles, protocol.RoleBridge) {
			out = append(out, candidate{key: linkKey(l.ID), kind: candTransit, linkID: l.ID, rtt: l.RTT,
				eligible: l.Up && slices.Contains(l.PeerTransitRules, uint32(r.ID))})
		}
	}
	return out
}

// weight is availability² × rtt_factor; throughput joins it in session 12.
func weight(availability float64, rtt time.Duration, sc protocol.Scoring) float64 {
	f := 1.0
	if ms := float64(rtt.Microseconds()) / 1000; ms > sc.RTTFloorMillis {
		f = sc.RTTFloorMillis / ms
	}
	return availability * availability * f
}

// availability is the fraction of the window a candidate was eligible, with
// each recent switch to ineligible counted as extra downtime.
type availability struct {
	first time.Time
	up    bool
	down  []span // ineligible periods; the last may be open
	flips []time.Time
}

type span struct{ start, end time.Time } // zero end: still open

func (a *availability) observe(now time.Time, eligible bool) {
	if a.first.IsZero() {
		a.first, a.up = now, eligible
		if !eligible {
			a.down = append(a.down, span{start: now})
		}
		return
	}
	if eligible == a.up {
		return
	}
	a.up = eligible
	if eligible {
		a.down[len(a.down)-1].end = now
	} else {
		a.down = append(a.down, span{start: now})
		a.flips = append(a.flips, now)
	}
}

func (a *availability) value(now time.Time, sc protocol.Scoring) float64 {
	if a.first.IsZero() {
		return 1
	}
	window := time.Duration(sc.AvailabilityWindowSec) * time.Second
	from := now.Add(-window)
	if a.first.After(from) {
		from = a.first
	}
	observed := now.Sub(from)
	if observed <= 0 {
		if a.up {
			return 1
		}
		return 0
	}
	var down time.Duration
	keep := a.down[:0]
	for _, s := range a.down {
		end := s.end
		if end.IsZero() {
			end = now
		}
		if end.Before(from) {
			continue // out of the window for good
		}
		keep = append(keep, s)
		down += end.Sub(maxTime(s.start, from))
	}
	a.down = keep
	flipFrom := now.Add(-time.Duration(sc.FlapWindowSec) * time.Second)
	keptFlips := a.flips[:0]
	for _, f := range a.flips {
		if !f.Before(flipFrom) {
			keptFlips = append(keptFlips, f)
			down += time.Duration(sc.FlapPenaltySec) * time.Second
		}
	}
	a.flips = keptFlips
	return math.Max(0, 1-float64(down)/float64(observed))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Modes of a Rule's buckets.
const (
	modeLocal    = "local"
	modeDirect   = "direct"
	modeTransit  = "transit"
	modeFallback = "fallback"
)

// Kinds of bucket moves (ADR 0006's Path flapping counts only failures).
const (
	moveFailure   = "failure"
	moveRebalance = "rebalance"
)

type move struct {
	bucket   int
	from, to string
	kind     string
	reason   string
}

// bucketState is one Rule's assignment of buckets to candidates.
type bucketState struct {
	assign   [buckets]string
	mode     string
	offSince time.Time // since when some share is off its target
	lastMove time.Time // last rebalancing move
}

// step brings the assignment in line with the candidates and returns the
// moves it made:
//   - a local candidate takes every bucket;
//   - otherwise direct candidates share the buckets by weight, those under
//     the cutoff getting none. A candidate that becomes ineligible loses its
//     buckets at once; one that becomes eligible gains them only by slow
//     rebalancing;
//   - with no direct candidate, every bucket goes through the best transit
//     candidate;
//   - with nothing eligible, the Rule's fallback applies (see resolve).
func (b *bucketState) step(now time.Time, cands []candidate, sc protocol.Scoring) []move {
	var local *candidate
	var directs, transits []candidate
	for i := range cands {
		c := cands[i]
		switch {
		case !c.eligible:
		case c.kind == candLocal:
			local = &cands[i]
		case c.kind == candDirect:
			directs = append(directs, c)
		default:
			transits = append(transits, c)
		}
	}

	switch {
	case local != nil:
		return b.all(keyLocal, modeLocal, "local exit")
	case len(directs) > 0:
		kept := cutoff(directs, sc.Cutoff)
		if b.mode != modeDirect {
			return b.spread(kept, fmt.Sprintf("direct candidates available (was %s)", orNone(b.mode)))
		}
		return b.rebalance(now, kept, sc)
	case len(transits) > 0:
		best := transits[0]
		for _, c := range transits[1:] {
			if c.weight > best.weight {
				best = c
			}
		}
		return b.all(best.key, modeTransit, "no direct candidate eligible")
	default:
		var moves []move
		for i, k := range b.assign {
			if k != "" {
				moves = append(moves, move{bucket: i, from: k, kind: moveFailure, reason: "nothing eligible"})
			}
			b.assign[i] = ""
		}
		b.mode = modeFallback
		return moves
	}
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

// cutoff keeps the candidates whose weight is at least frac of the best.
func cutoff(cs []candidate, frac float64) []candidate {
	best := 0.0
	for _, c := range cs {
		best = math.Max(best, c.weight)
	}
	var out []candidate
	for _, c := range cs {
		if best == 0 || c.weight >= frac*best {
			out = append(out, c)
		}
	}
	return out
}

// all puts every bucket on one candidate.
func (b *bucketState) all(key, mode, reason string) []move {
	var moves []move
	for i, k := range b.assign {
		if k != key {
			moves = append(moves, move{bucket: i, from: k, to: key, kind: moveFailure, reason: reason})
			b.assign[i] = key
		}
	}
	b.mode, b.offSince = mode, time.Time{}
	return moves
}

// spread assigns every bucket afresh, in proportion to the weights.
func (b *bucketState) spread(cs []candidate, reason string) []move {
	targets := targetCounts(cs)
	keys := sortedKeys(targets)
	var moves []move
	i := 0
	for _, k := range keys {
		for n := 0; n < targets[k]; n++ {
			if b.assign[i] != k {
				moves = append(moves, move{bucket: i, from: b.assign[i], to: k, kind: moveFailure, reason: reason})
				b.assign[i] = k
			}
			i++
		}
	}
	b.mode, b.offSince = modeDirect, time.Time{}
	return moves
}

// rebalance moves orphaned buckets at once, then one bucket at a time
// towards the targets once a share has been off for long enough.
func (b *bucketState) rebalance(now time.Time, cs []candidate, sc protocol.Scoring) []move {
	targets := targetCounts(cs)
	have := map[string]int{}
	for _, k := range b.assign {
		have[k]++
	}
	var moves []move
	for i, k := range b.assign {
		if _, ok := targets[k]; ok {
			continue
		}
		to := mostUnder(targets, have)
		moves = append(moves, move{bucket: i, from: k, to: to, kind: moveFailure, reason: k + " not eligible"})
		have[k]--
		have[to]++
		b.assign[i] = to
	}

	off := 0.0
	for k, t := range targets {
		off = math.Max(off, math.Abs(float64(have[k]-t))/buckets)
	}
	if off <= sc.RebalanceThresholdShare {
		b.offSince = time.Time{}
		return moves
	}
	if b.offSince.IsZero() {
		b.offSince = now
	}
	if now.Sub(b.offSince) < time.Duration(sc.RebalanceAfterSec)*time.Second ||
		!b.lastMove.IsZero() && now.Sub(b.lastMove) < time.Duration(sc.RebalanceEverySec)*time.Second {
		return moves
	}
	from, to := mostOver(targets, have), mostUnder(targets, have)
	for i := buckets - 1; i >= 0; i-- {
		if b.assign[i] == from {
			b.assign[i] = to
			b.lastMove = now
			return append(moves, move{bucket: i, from: from, to: to, kind: moveRebalance,
				reason: fmt.Sprintf("%s off its target share by %.0f points", from, 100*off)})
		}
	}
	return moves
}

// targetCounts shares the buckets by weight, by largest remainder.
func targetCounts(cs []candidate) map[string]int {
	total := 0.0
	for _, c := range cs {
		total += c.weight
	}
	out := map[string]int{}
	type rem struct {
		key  string
		frac float64
	}
	var rems []rem
	given := 0
	for _, c := range cs {
		share := float64(buckets) / float64(len(cs))
		if total > 0 {
			share = buckets * c.weight / total
		}
		out[c.key] = int(share)
		given += int(share)
		rems = append(rems, rem{c.key, share - math.Floor(share)})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		if rems[i].frac != rems[j].frac {
			return rems[i].frac > rems[j].frac
		}
		return rems[i].key < rems[j].key
	})
	for i := 0; given < buckets; i++ {
		out[rems[i%len(rems)].key]++
		given++
	}
	return out
}

func mostUnder(targets, have map[string]int) string {
	best, gap := "", math.MinInt
	for _, k := range sortedKeys(targets) {
		if g := targets[k] - have[k]; g > gap {
			best, gap = k, g
		}
	}
	return best
}

func mostOver(targets, have map[string]int) string {
	best, gap := "", math.MinInt
	for _, k := range sortedKeys(targets) {
		if g := have[k] - targets[k]; g > gap {
			best, gap = k, g
		}
	}
	return best
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolve turns each Rule's buckets into candidate keys, applying
// fallbacks: a Rule with nothing eligible takes the next Rule's buckets if
// its fallback is next, and drops otherwise.
func resolve(rules []protocol.Rule, states map[uint64]*bucketState) map[uint64][buckets]string {
	out := map[uint64][buckets]string{}
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		st := states[r.ID]
		switch {
		case st != nil && st.mode != modeFallback && st.mode != "":
			out[r.ID] = st.assign
		case r.Fallback == protocol.FallbackNext && i+1 < len(rules):
			out[r.ID] = out[rules[i+1].ID]
		default:
			var drop [buckets]string
			for j := range drop {
				drop[j] = keyDrop
			}
			out[r.ID] = drop
		}
	}
	return out
}
