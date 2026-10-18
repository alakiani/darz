package coordinator

import (
	"context"
	"maps"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/arvan"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
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

func TestRebalanceSlots(t *testing.T) {
	slots := []string{"a", "b", "c"}
	three := []string{"ir1", "ir2", "ir3"}
	for _, tc := range []struct {
		name     string
		held     map[string]string
		bridges  []string
		eligible []string
		want     []slotMove
	}{
		{name: "three slots on one of three Bridges: two move, one to each other Bridge",
			held:    map[string]string{"a": "ir1", "b": "ir1", "c": "ir1"},
			bridges: three, eligible: three,
			want: []slotMove{{"b", "ir1", "ir2"}, {"c", "ir1", "ir3"}}},
		{name: "already balanced",
			held:    map[string]string{"a": "ir2", "b": "ir3", "c": "ir1"},
			bridges: three, eligible: three},
		{name: "an ineligible Bridge never receives a slot",
			held:    map[string]string{"a": "ir2", "b": "ir2", "c": "ir2"},
			bridges: three, eligible: []string{"ir2", "ir3"},
			want: []slotMove{{"c", "ir2", "ir3"}}},
		{name: "a slot leaves an ineligible Bridge",
			held:    map[string]string{"a": "ir1", "b": "ir2", "c": "ir2"},
			bridges: three, eligible: []string{"ir2", "ir3"},
			want: []slotMove{{"a", "ir1", "ir3"}}},
		{name: "one eligible Bridge: nothing moves",
			held:    map[string]string{"a": "ir1", "b": "ir1", "c": "ir1"},
			bridges: three, eligible: []string{"ir2"}},
		{name: "three slots on two Bridges, two and one: nothing moves",
			held:    map[string]string{"a": "ir2", "b": "ir1", "c": "ir2"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"}},
		{name: "the extra slot stays with the Bridge already holding the most",
			held:    map[string]string{"a": "ir2", "b": "ir2", "c": "ir2"},
			bridges: []string{"ir1", "ir2"}, eligible: []string{"ir1", "ir2"},
			want: []slotMove{{"c", "ir2", "ir1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := map[string]bool{}
			for _, b := range tc.eligible {
				el[b] = true
			}
			held := maps.Clone(tc.held)
			got := rebalanceSlots(slots, held, tc.bridges, el)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			for _, m := range got {
				if !el[m.To] {
					t.Errorf("%s moves to ineligible %s", m.Slot, m.To)
				}
			}
			if !maps.Equal(held, tc.held) {
				t.Errorf("rebalanceSlots changed its input: %v", held)
			}
			// Deterministic: the same input, the same plan.
			if again := rebalanceSlots(slots, held, tc.bridges, el); !slices.Equal(again, got) {
				t.Errorf("a second plan differs: %v", again)
			}
		})
	}
}

// TestAdminSlotRebalance previews and applies a rebalance through the Admin
// API: the slots move in the store and at ArvanCloud, and only Admins may
// ask.
func TestAdminSlotRebalance(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	dns := arvan.NewFake("key", "4miz.ir")
	srv := httptest.NewServer(dns)
	t.Cleanup(srv.Close)
	a.c.DNS = &arvan.Client{BaseURL: srv.URL, APIKey: "key", Zone: "4miz.ir", TTL: 120}

	// Three eligible domestic Bridges, all three slots on ir1.
	addrs := map[string]string{"ir1": "192.0.2.1", "ir2": "192.0.2.2", "ir3": "192.0.2.3"}
	for _, name := range []string{"ir1", "ir2", "ir3"} {
		n, err := a.s.CreateNode(ctx, name, []protocol.Role{protocol.RoleBridge}, []string{"IR"})
		if err != nil {
			t.Fatal(err)
		}
		a.s.PutHealth(ctx, n.ID, protocol.Health{AppliedVersion: 1}, addrs[name])
		a.c.slots.probes[n.ID] = protocol.ProbeResult{OK: true, At: *a.clock}
		if name == "ir1" {
			for _, slot := range a.c.EndpointSlots {
				a.s.AssignSlot(ctx, slot, n.ID)
				a.s.SetSlotAddr(ctx, slot, addrs[name])
				dns.Put(slot[:1], addrs[name])
			}
		}
	}

	user, _ := a.user("maryam")
	user.must(403, "GET", "/v1/admin/slots/rebalance", nil, nil)
	user.must(403, "POST", "/v1/admin/slots/rebalance", nil, nil)
	a.client().must(401, "GET", "/v1/admin/slots/rebalance", nil, nil)

	admin := a.admin()
	want := []protocol.SlotMove{{Slot: "b.4miz.ir", From: "ir1", To: "ir2"}, {Slot: "c.4miz.ir", From: "ir1", To: "ir3"}}
	var plan protocol.SlotRebalance
	admin.must(200, "GET", "/v1/admin/slots/rebalance", nil, &plan)
	if !slices.Equal(plan.Moves, want) || plan.Note == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if dns.A("b") != addrs["ir1"] {
		t.Fatal("the preview moved a slot")
	}

	var moved protocol.SlotRebalance
	admin.must(200, "POST", "/v1/admin/slots/rebalance", nil, &moved)
	if !slices.Equal(moved.Moves, want) {
		t.Fatalf("moved = %+v", moved)
	}
	for slot, name := range map[string]string{"a": "ir1", "b": "ir2", "c": "ir3"} {
		if got := dns.A(slot); got != addrs[name] {
			t.Errorf("record %s = %s, want %s's", slot, got, name)
		}
	}
	st, err := a.c.EndpointSlotStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ir1", "ir2", "ir3"} {
		if s := st.Slots[i]; s.Bridge != name || s.Addr != addrs[name] {
			t.Errorf("status shows %+v, want %s", s, name)
		}
	}

	// Balanced now: the plan is empty and applying it is a no-op.
	admin.must(200, "GET", "/v1/admin/slots/rebalance", nil, &plan)
	if len(plan.Moves) != 0 || plan.Note == "" {
		t.Errorf("plan after = %+v", plan)
	}
	admin.must(200, "POST", "/v1/admin/slots/rebalance", nil, &moved)
	if moved.Moves == nil || len(moved.Moves) != 0 {
		t.Errorf("moved after = %+v", moved)
	}
	slots, _ := a.s.EndpointSlots(ctx)
	if i := slices.IndexFunc(slots, func(e store.EndpointSlot) bool { return e.Repoints != 0 && e.Name == "a.4miz.ir" }); i >= 0 {
		t.Errorf("slot a was re-pointed: %+v", slots[i])
	}
}
