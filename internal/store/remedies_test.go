package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
)

func TestAResetBringsTheUserBackToAFullQuota(t *testing.T) {
	s, _, user, d1, _ := quotaFixture(t)
	if _, err := s.ResetQuota(ctx, user); !errors.Is(err, store.ErrUnlimited) {
		t.Errorf("a Reset on an unlimited Quota: %v", err)
	}
	s.SetQuota(ctx, user, store.QuotaSetting{Quota: new(uint64(10))})
	s.AddRemedy(ctx, user, store.TopUp, 5)
	use := func(n uint64) {
		s.PutUsage(ctx, "nd_b1", []protocol.UsageBucket{bucket(day(2026, 10, 16), d1.Address.String(), "nd_x1", n, 0)})
	}
	use(12) // 3 of the Top-up left

	// The Reset absorbs the unused Top-up: it adds the Quota minus what is left.
	q, _ := s.QuotaStanding(ctx, user)
	if adds, limited := q.ResetAdds(); !limited || adds != 7 {
		t.Errorf("ResetAdds = %d, %v; want 7", adds, limited)
	}
	n, err := s.ResetQuota(ctx, user)
	if err != nil || n != 7 {
		t.Fatalf("Reset added %d, %v; want 7", n, err)
	}
	q, _ = s.QuotaStanding(ctx, user)
	if left, _ := q.Left(); left != 10 || q.Usage != 12 {
		t.Errorf("after the Reset: left %d, Usage %d; want 10 and 12", left, q.Usage)
	}
	// At a full Quota a Reset adds nothing.
	if n, _ := s.ResetQuota(ctx, user); n != 0 {
		t.Errorf("a second Reset at a full Quota added %d", n)
	}
	// Late usage doesn't change a recorded Reset: it only lowers what is left.
	use(20)
	q, _ = s.QuotaStanding(ctx, user)
	if left, _ := q.Left(); left != 2 || q.Remedies != 12 {
		t.Errorf("after more usage: left %d, remedies %d; want 2 and 12", left, q.Remedies)
	}
	if n, _ := s.ResetQuota(ctx, user); n != 8 {
		t.Errorf("a Reset with 2 left added %d, want 8", n)
	}
	rs, err := s.Remedies(ctx, user)
	if err != nil || len(rs) != 4 || rs[0].Kind != store.TopUp || rs[1].Bytes != 7 || rs[3].Bytes != 8 || !rs[3].PeriodStart.Equal(day(2026, 10, 16)) {
		t.Errorf("remedies = %+v, %v", rs, err)
	}
}

func TestRefillsExpireAndAreRedeemedOnce(t *testing.T) {
	s, c, user, d1, _ := quotaFixture(t)
	if err := s.GiveRefills(ctx, user, 2, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	held, err := s.HeldRefills(ctx, user)
	if err != nil || len(held) != 2 || !held[0].ExpiresAt.Equal(c.t.Add(48*time.Hour)) {
		t.Fatalf("held = %+v, %v", held, err)
	}
	// Unlimited: none can be redeemed, and none is used up.
	if _, err := s.RedeemRefill(ctx, user, held[0].ID); !errors.Is(err, store.ErrUnlimited) {
		t.Errorf("redeeming on an unlimited Quota: %v", err)
	}
	s.SetQuota(ctx, user, store.QuotaSetting{Quota: new(uint64(100))})
	s.PutUsage(ctx, "nd_b1", []protocol.UsageBucket{bucket(day(2026, 10, 16), d1.Address.String(), "nd_x1", 60, 0)})

	// Redeemable up to and including its expiry.
	c.t = c.t.Add(48 * time.Hour)
	if n, err := s.RedeemRefill(ctx, user, held[0].ID); err != nil || n != 60 {
		t.Errorf("redeem at expiry added %d, %v; want 60", n, err)
	}
	if _, err := s.RedeemRefill(ctx, user, held[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("redeeming twice: %v", err)
	}
	if held, _ := s.HeldRefills(ctx, user); len(held) != 1 {
		t.Errorf("held after one redeem = %+v", held)
	}
	c.t = c.t.Add(time.Second)
	if held, _ := s.HeldRefills(ctx, user); len(held) != 0 {
		t.Errorf("held after expiry = %+v", held)
	}
	if _, err := s.RedeemRefill(ctx, user, held[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("redeeming an expired Refill: %v", err)
	}
	if _, err := s.RedeemRefill(ctx, "us_other", held[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("redeeming another User's Refill: %v", err)
	}
	if n, _ := s.HeldRefillCount(ctx, user); n != 0 {
		t.Errorf("held count = %d", n)
	}
}

func TestAnInvitePresetsQuotaAndRefills(t *testing.T) {
	c := newClock()
	s := open(t, c)
	tok, _, err := s.CreateInvite(ctx, store.Invite{Kind: "new", QuotaSetting: store.QuotaSetting{Quota: new(uint64(5e9)), SlowRate: 125_000},
		Refills: 2, RefillTTL: 10 * 24 * time.Hour}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3 * 24 * time.Hour)
	a, err := s.AcceptNewInvite(ctx, tok, "sara", "h")
	if err != nil {
		t.Fatal(err)
	}
	if a.Quota == nil || *a.Quota != 5e9 || a.SlowRate != 125_000 {
		t.Errorf("account = %+v", a.QuotaSetting)
	}
	held, _ := s.HeldRefills(ctx, a.ID)
	if len(held) != 2 || !held[1].ExpiresAt.Equal(c.t.Add(10*24*time.Hour)) {
		t.Errorf("held = %+v, want 2 expiring 10 days after registration", held)
	}
}
