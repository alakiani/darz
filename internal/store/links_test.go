package store_test

import (
	"testing"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

func TestEnsureLinkIsOnePerPair(t *testing.T) {
	s := open(t, newClock())
	a, b := mustNode(t, s, "a"), mustNode(t, s, "b")
	l1, err := s.EnsureLink(ctx, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := s.EnsureLink(ctx, b.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if l1.ID != l2.ID || l1.NodeA > l1.NodeB {
		t.Errorf("links = %+v, %+v; want one link with sorted ends", l1, l2)
	}
	if links, _ := s.Links(ctx); len(links) != 1 {
		t.Errorf("%d links, want 1", len(links))
	}
}

func TestInstancesTakeTheLowestFreeSlot(t *testing.T) {
	s := open(t, newClock())
	a, b := mustNode(t, s, "a"), mustNode(t, s, "b")
	l, _ := s.EnsureLink(ctx, a.ID, b.ID)
	var ids []uint64
	for i := 0; i < 3; i++ {
		in, err := s.CreateInstance(ctx, l.ID, protocol.TransportAmneziaWG, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if in.Slot != i {
			t.Errorf("instance %d got slot %d, want %d", i, in.Slot, i)
		}
		ids = append(ids, in.ID)
	}
	if err := s.RetireInstance(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	in, err := s.CreateInstance(ctx, l.ID, protocol.TransportAmneziaWG, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.Slot != 1 || in.ID <= ids[2] {
		t.Errorf("replacement = %+v, want slot 1 reused under a new id", in)
	}
	live, _ := s.Instances(ctx)
	if len(live) != 3 {
		t.Errorf("%d live instances, want 3", len(live))
	}
}

func TestListenChoiceIsSetOnceByTheListener(t *testing.T) {
	s := open(t, newClock())
	a, b := mustNode(t, s, "a"), mustNode(t, s, "b")
	l, _ := s.EnsureLink(ctx, a.ID, b.ID)
	in, _ := s.CreateInstance(ctx, l.ID, protocol.TransportAmneziaWG, b.ID)
	params := &protocol.AWGParams{Jc: 5, H1: "10-20"}

	if ok, _ := s.SetListenChoice(ctx, in.ID, a.ID, 20000, params); ok {
		t.Error("the initiating end set the listener's choice")
	}
	if ok, err := s.SetListenChoice(ctx, in.ID, b.ID, 20000, params); !ok || err != nil {
		t.Fatalf("listener's choice: %v, %v", ok, err)
	}
	if ok, _ := s.SetListenChoice(ctx, in.ID, b.ID, 30000, nil); ok {
		t.Error("a second choice overwrote the first")
	}
	live, _ := s.Instances(ctx)
	if live[0].ListenPort != 20000 || live[0].AWG == nil || live[0].AWG.H1 != "10-20" {
		t.Errorf("instance = %+v", live[0])
	}
}

func TestDownSinceAndLinkKey(t *testing.T) {
	c := newClock()
	s := open(t, c)
	a, b := mustNode(t, s, "a"), mustNode(t, s, "b")
	l, _ := s.EnsureLink(ctx, a.ID, b.ID)
	in, _ := s.CreateInstance(ctx, l.ID, protocol.TransportWireGuard, b.ID)
	since := c.t.Add(-time.Hour)
	if err := s.SetInstanceDownSince(ctx, in.ID, &since); err != nil {
		t.Fatal(err)
	}
	live, _ := s.Instances(ctx)
	if live[0].DownSince == nil || !live[0].DownSince.Equal(since) {
		t.Errorf("down since = %v, want %v", live[0].DownSince, since)
	}
	s.SetInstanceDownSince(ctx, in.ID, nil)
	if live, _ := s.Instances(ctx); live[0].DownSince != nil {
		t.Error("down since not cleared")
	}

	if changed, _ := s.SetLinkPublicKey(ctx, a.ID, "key1"); !changed {
		t.Error("first key not stored")
	}
	if changed, _ := s.SetLinkPublicKey(ctx, a.ID, "key1"); changed {
		t.Error("same key reported as a change")
	}
	if n, _ := s.Node(ctx, a.ID); n.LinkPublicKey != "key1" {
		t.Errorf("link key = %q", n.LinkPublicKey)
	}
}

func TestTransportListDefaultsToAll(t *testing.T) {
	s := open(t, newClock())
	ts, err := s.Transports(ctx)
	if err != nil || len(ts) != 3 || ts[0] != protocol.TransportWireGuard || ts[2] != protocol.TransportGRE {
		t.Fatalf("default transports = %v, %v", ts, err)
	}
	s.SetTransports(ctx, []protocol.Transport{protocol.TransportAmneziaWG})
	if ts, _ := s.Transports(ctx); len(ts) != 1 || ts[0] != protocol.TransportAmneziaWG {
		t.Errorf("transports = %v", ts)
	}
}
