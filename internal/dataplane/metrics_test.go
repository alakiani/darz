package dataplane

import (
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/metrics"
	"github.com/alakiani/darz/internal/protocol"
)

// Path flapping counts failure moves, not buckets: one Link dying moves
// many buckets in one step (ADR 0006, ADR 0008).
func TestMovesAreCountedByStepAndKind(t *testing.T) {
	p := &Plane{moves: map[[2]string]uint64{}, movedBuckets: map[[2]string]uint64{}}
	p.countMoves("Default", []move{{kind: moveFailure}, {kind: moveFailure}, {kind: moveFailure}})
	p.countMoves("Default", []move{{kind: moveRebalance}})
	p.countMoves("Default", nil)
	p.countMoves("IR", []move{{kind: moveFailure}})
	var w metrics.Writer
	p.WriteMetrics(&w)
	got := string(w.Bytes())
	for _, want := range []string{
		`darz_path_moves_total{rule="Default",kind="failure"} 1`,
		`darz_path_moves_total{rule="Default",kind="rebalance"} 1`,
		`darz_path_moves_total{rule="IR",kind="failure"} 1`,
		`darz_path_bucket_moves_total{rule="Default",kind="failure"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metrics lack %q:\n%s", want, got)
		}
	}
}

// fakeLinks is the Link layer as the data plane sees it.
type fakeLinks struct{ links []link.Status }

func (f *fakeLinks) LinkStatus() []link.Status { return f.links }
func (f *fakeLinks) EgressOK() bool            { return false }
func (f *fakeLinks) SetTransitRules([]uint32)  {}

// samples parses a scrape into each series' value.
func samples(b []byte) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		v, _ := strconv.ParseFloat(line[i+1:], 64)
		out[line[:i]] = v
	}
	return out
}

// Each candidate's weight, target share, cutoff and availability come
// from the Path selection itself (issue #51). The case is production's of
// 2026-10-18: AWS's 5.4 Mbit/s is under 25% of LightNode's 141.9.
func TestPathMetricsFollowPathSelection(t *testing.T) {
	exit := func(name string) protocol.Peer {
		return protocol.Peer{Name: name, Roles: []protocol.Role{protocol.RoleExit}}
	}
	bridge := protocol.Peer{Name: "ir3", Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}}
	links := &fakeLinks{links: []link.Status{
		{ID: 1, Up: true, Peer: exit("aws"), PeerEgressOK: true, RTT: 50 * time.Millisecond, ThroughputMbps: 5.4},
		{ID: 2, Up: true, Peer: exit("lightnode"), PeerEgressOK: true, RTT: 300 * time.Millisecond, ThroughputMbps: 141.9},
		{ID: 3, Up: true, Peer: bridge, PeerTransitRules: []uint32{2}},
	}}
	st := &protocol.State{
		Node:   protocol.NodeInfo{Name: "ir1", Roles: []protocol.Role{protocol.RoleBridge}, Tags: []string{"IR"}},
		Bridge: &protocol.BridgeState{Rules: rules, Scoring: sc},
	}
	for _, l := range links.links {
		st.Links = append(st.Links, protocol.Link{ID: l.ID, Peer: l.Peer})
	}
	p := &Plane{cfg: Config{Links: links}, log: slog.New(slog.DiscardHandler), st: st,
		dev: map[uint64]*bucketState{}, tr: map[uint64]*bucketState{}, avail: map[string]*availability{},
		moves: map[[2]string]uint64{}, movedBuckets: map[[2]string]uint64{}}
	scrape := func() map[string]float64 {
		p.computePaths()
		var w metrics.Writer
		p.writePaths(&w)
		return samples(w.Bytes())
	}
	def := func(cand string) string {
		return `{rule_id="2",rule="Default",default="true",candidate="` + cand + `"}`
	}

	got := scrape()
	lightnode := 141.9 * 150 / 300 // over the RTT floor
	for series, want := range map[string]float64{
		"darz_path_weight" + def("aws"):             5.4,
		"darz_path_weight" + def("lightnode"):       lightnode,
		"darz_path_target_share" + def("aws"):       5.4 / (5.4 + lightnode),
		"darz_path_target_share" + def("lightnode"): lightnode / (5.4 + lightnode),
		"darz_path_cut_off" + def("aws"):            1,
		"darz_path_cut_off" + def("lightnode"):      0,
		"darz_path_availability" + def("aws"):       1,
		"darz_path_availability" + def("lightnode"): 1,
		// The transit candidate is weighed, at 1 until measured, but takes
		// nothing while a direct one is eligible.
		"darz_path_weight" + def("ir3"):                                                 1,
		"darz_path_target_share" + def("ir3"):                                           0,
		"darz_path_cut_off" + def("ir3"):                                                0,
		`darz_path_target_share{rule_id="1",rule="IR",default="false",candidate="ir3"}`: 0,
	} {
		if v, ok := got[series]; !ok || v != want {
			t.Errorf("%s = %v (present %v), want %v", series, v, ok, want)
		}
	}
	// The buckets agree: the candidate cut off has none.
	for _, rh := range p.paths {
		if rh.RuleID == 2 && (rh.Shares["link:1"] != 0 || rh.Shares["link:2"] != 64) {
			t.Errorf("shares = %v, want every bucket on lightnode", rh.Shares)
		}
	}

	// LightNode's Link goes down: its availability takes the flap penalty
	// and its weight goes with it, and AWS takes every bucket.
	links.links[1].Up = false
	got = scrape()
	for series, want := range map[string]float64{
		"darz_path_availability" + def("lightnode"): 0,
		"darz_path_weight" + def("lightnode"):       0,
		"darz_path_target_share" + def("lightnode"): 0,
		"darz_path_cut_off" + def("lightnode"):      0,
		"darz_path_target_share" + def("aws"):       1,
		"darz_path_cut_off" + def("aws"):            0,
	} {
		if v, ok := got[series]; !ok || v != want {
			t.Errorf("after lightnode went down: %s = %v (present %v), want %v", series, v, ok, want)
		}
	}
	for _, rh := range p.paths {
		if rh.RuleID == 2 && rh.Shares["link:1"] != 64 {
			t.Errorf("shares = %v, want every bucket on aws", rh.Shares)
		}
	}

	// Not a Bridge: nothing.
	p.st = &protocol.State{}
	var w metrics.Writer
	p.writePaths(&w)
	if len(w.Bytes()) != 0 {
		t.Errorf("Path metrics off a Bridge:\n%s", w.Bytes())
	}
}
