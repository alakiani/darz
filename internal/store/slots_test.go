package store_test

import "testing"

func TestEndpointSlots(t *testing.T) {
	s := open(t, newClock())
	if got, err := s.EndpointSlots(ctx); err != nil || len(got) != 0 {
		t.Fatalf("EndpointSlots = %v, %v; want none", got, err)
	}
	if err := s.AssignSlot(ctx, "a.4miz.ir", "nd_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSlotAddr(ctx, "a.4miz.ir", "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.EndpointSlots(ctx)
	if len(got) != 1 || got[0].NodeID != "nd_1" || got[0].Addr != "203.0.113.1" || got[0].Repoints != 0 {
		t.Fatalf("after assigning: %+v", got)
	}

	// Moving the slot clears the address until DNS is updated, and counts.
	if err := s.AssignSlot(ctx, "a.4miz.ir", "nd_2"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.EndpointSlots(ctx)
	if got[0].NodeID != "nd_2" || got[0].Addr != "" || got[0].Repoints != 1 {
		t.Fatalf("after moving: %+v", got)
	}
	// Assigning it where it already is changes nothing.
	s.SetSlotAddr(ctx, "a.4miz.ir", "203.0.113.2")
	s.AssignSlot(ctx, "a.4miz.ir", "nd_2")
	got, _ = s.EndpointSlots(ctx)
	if got[0].Addr != "203.0.113.2" || got[0].Repoints != 1 {
		t.Fatalf("after a no-op assignment: %+v", got)
	}
}
