package coordinator

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

func TestPlanSlots(t *testing.T) {
	slots := []string{"a", "b", "c"}
	now := time.Unix(1_000_000, 0)
	long, short := now.Add(-61*time.Second), now.Add(-59*time.Second)
	for _, tc := range []struct {
		name      string
		held      map[string]string
		bridges   []string
		eligible  []string
		inelSince map[string]time.Time
		want      map[string]string
	}{
		{name: "first assignment spreads over the Bridges",
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"},
			want: map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"}},
		{name: "three Bridges get one slot each",
			bridges: []string{"ir1", "ir2", "ir3"}, eligible: []string{"ir1", "ir2", "ir3"},
			want: map[string]string{"a": "ir1", "b": "ir2", "c": "ir3"}},
		{name: "nothing eligible leaves slots unassigned",
			bridges: []string{"ir1"}, want: map[string]string{}},
		{name: "a slot stays while its Bridge has been ineligible under 60 s",
			held:    map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir2"}, inelSince: map[string]time.Time{"ir1": short},
			want: map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"}},
		{name: "after 60 s its slots move to a surviving Bridge",
			held:    map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir2"}, inelSince: map[string]time.Time{"ir1": long},
			want: map[string]string{"a": "ir2", "b": "ir2", "c": "ir2"}},
		{name: "a moving slot goes to the least loaded eligible Bridge",
			held:    map[string]string{"a": "ir1", "b": "ir2", "c": "ir2"},
			bridges: []string{"ir1", "ir2", "ir3"}, eligible: []string{"ir2", "ir3"}, inelSince: map[string]time.Time{"ir1": long},
			want: map[string]string{"a": "ir3", "b": "ir2", "c": "ir2"}},
		{name: "slots never move back to a recovered Bridge",
			held:    map[string]string{"a": "ir2", "b": "ir2", "c": "ir2"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"},
			want: map[string]string{"a": "ir2", "b": "ir2", "c": "ir2"}},
		{name: "with nowhere to go, a slot stays on its dead Bridge",
			held:    map[string]string{"a": "ir1", "b": "ir1", "c": "ir1"},
			bridges: []string{"ir1"}, inelSince: map[string]time.Time{"ir1": long},
			want: map[string]string{"a": "ir1", "b": "ir1", "c": "ir1"}},
		{name: "a slot on a Node that is no longer a domestic Bridge moves at once",
			held:    map[string]string{"a": "gone", "b": "ir2", "c": "ir1"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"},
			want: map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"}},
		{name: "an unassigned slot is filled as soon as a Bridge is eligible",
			held:    map[string]string{"a": "ir1"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"},
			want: map[string]string{"a": "ir1", "b": "ir2", "c": "ir1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := map[string]bool{}
			for _, b := range tc.eligible {
				el[b] = true
			}
			held := map[string]string{}
			maps.Copy(held, tc.held)
			got := planSlots(slots, held, tc.bridges, el, tc.inelSince, now, 60*time.Second)
			if !maps.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if !maps.Equal(held, tc.held) && !(len(held) == 0 && len(tc.held) == 0) {
				t.Errorf("planSlots changed its input: %v", held)
			}
		})
	}
}

func TestIneligibility(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := func(mode string) *protocol.HealthStatus {
		return &protocol.HealthStatus{ReceivedAt: now.Add(-10 * time.Second),
			Health: protocol.Health{Paths: []protocol.RuleHealth{{RuleID: 1, Mode: "direct"}, {RuleID: 2, Mode: mode}}}}
	}
	ok := protocol.ProbeResult{OK: true}
	for _, tc := range []struct {
		name  string
		probe protocol.ProbeResult
		h     *protocol.HealthStatus
		want  []string
	}{
		{"direct", ok, fresh("direct"), nil},
		{"transit counts", ok, fresh("transit"), nil},
		{"probe failing", protocol.ProbeResult{}, fresh("direct"), []string{"probe failing"}},
		{"never reported", ok, nil, []string{"Agent not reporting"}},
		{"stale report", ok, &protocol.HealthStatus{ReceivedAt: now.Add(-91 * time.Second)}, []string{"Agent not reporting"}},
		{"default Rule in fallback", ok, fresh("fallback"), []string{"no default-Rule Path"}},
		{"default Rule not reported yet", protocol.ProbeResult{}, &protocol.HealthStatus{ReceivedAt: now},
			[]string{"probe failing", "no default-Rule Path"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ineligibility(tc.probe, tc.h, 2, now, 90*time.Second)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
