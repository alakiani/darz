package coordinator

import (
	"slices"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/protocol"
)

// usage is the signed-in User's Usage and Quota.
func (cl *client) usage() protocol.MyUsage {
	cl.a.t.Helper()
	var u protocol.MyUsage
	cl.must(200, "GET", "/v1/me/usage", nil, &u)
	return u
}

func (cl *client) userInfo(id string) protocol.UserInfo {
	cl.a.t.Helper()
	var users []protocol.UserInfo
	cl.must(200, "GET", "/v1/admin/users", nil, &users)
	i := slices.IndexFunc(users, func(u protocol.UserInfo) bool { return u.ID == id })
	if i < 0 {
		cl.a.t.Fatalf("no User %s in %+v", id, users)
	}
	return users[i]
}

func left(u protocol.MyUsage) int64 {
	if u.Left == nil {
		return -1
	}
	return int64(*u.Left)
}

func TestATopUpAndAResetUnblockWithoutChangingUsage(t *testing.T) {
	a, admin, me, dev, bridge := quotaFixture(t)
	onBridge := func() bool { return len(a.bridgeDevices(bridge)) == 1 }
	user := "/v1/admin/users/" + me.ID
	maryam := a.client()
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil)

	admin.must(200, "POST", user+"/quota", protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutBlock}, nil)
	a.use(dev, 1000, 0) // this hour's total: 1000 of 1000
	if onBridge() {
		t.Fatal("a User who ran out is still on the Bridge")
	}

	// A Top-up brings them back at once and leaves Usage as it was.
	var u protocol.UserInfo
	admin.must(200, "POST", user+"/top-up", protocol.TopUp{Bytes: 500}, &u)
	if !onBridge() || u.PeriodBytes != 1000 || u.PeriodRemedies != 500 {
		t.Errorf("after a Top-up: on the Bridge %v, period bytes %d, remedies %d", onBridge(), u.PeriodBytes, u.PeriodRemedies)
	}
	if got := maryam.usage(); left(got) != 500 || got.Current.Bytes != 1000 || len(got.Current.Remedies) != 1 ||
		got.Current.Remedies[0].Kind != "top-up" || got.Current.Remedies[0].Bytes != 500 {
		t.Errorf("after a Top-up: %+v", got)
	}

	a.use(dev, 1500, 0) // 1500 of 1000 + 500
	if onBridge() {
		t.Fatal("using the Top-up up did not block")
	}

	// Reset now brings them back to a full Quota, again without changing Usage.
	admin.must(200, "POST", user+"/reset", nil, &u)
	if !onBridge() || u.PeriodBytes != 1500 || u.PeriodRemedies != 1500 {
		t.Errorf("after a Reset: on the Bridge %v, period bytes %d, remedies %d", onBridge(), u.PeriodBytes, u.PeriodRemedies)
	}
	if got := admin.userInfo(me.ID); got.PeriodRemedies != 1500 {
		t.Errorf("the Users list shows remedies %d, want 1500", got.PeriodRemedies)
	}
	got := maryam.usage()
	if left(got) != 1000 || got.Current.Bytes != 1500 || len(got.Current.Remedies) != 2 || got.Current.Remedies[1].Kind != "reset" || got.Current.Remedies[1].Bytes != 1000 {
		t.Errorf("after a Reset: %+v", got)
	}
	if d := admin.device(me.ID); d.State != "active" {
		t.Errorf("after a Reset the Device is %s (%s)", d.State, d.Reason)
	}

	// Unlimited: a Top-up is allowed, Reset now is not.
	admin.must(200, "POST", user+"/quota", protocol.SetQuota{RunningOut: protocol.RunningOutBlock}, nil)
	admin.must(200, "POST", user+"/top-up", protocol.TopUp{Bytes: 1}, nil)
	admin.must(409, "POST", user+"/reset", nil, nil)

	admin.must(400, "POST", user+"/top-up", protocol.TopUp{}, nil)
	admin.must(400, "POST", user+"/top-up", protocol.TopUp{Bytes: 1 << 63}, nil)
	admin.must(404, "POST", "/v1/admin/users/us_nope/top-up", protocol.TopUp{Bytes: 1}, nil)
	admin.must(404, "POST", "/v1/admin/users/us_nope/reset", nil, nil)
	maryam.must(403, "POST", user+"/top-up", protocol.TopUp{Bytes: 1}, nil)
	maryam.must(403, "POST", user+"/reset", nil, nil)
}

func TestARefillExpiresUnused(t *testing.T) {
	a, admin, me, _, _ := quotaFixture(t)
	maryam := a.client()
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/quota", protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutBlock}, nil)

	var u protocol.UserInfo
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/refills", protocol.GiveRefills{Count: 2, Days: 2}, &u)
	if u.Refills != 2 {
		t.Errorf("Refills held = %d, want 2", u.Refills)
	}
	got := maryam.usage()
	expiry := a.clock.Add(48 * time.Hour)
	if len(got.Refills) != 2 || !got.Refills[0].ExpiresAt.Equal(expiry.Truncate(time.Second)) {
		t.Fatalf("held = %+v, want two expiring %s", got.Refills, expiry)
	}

	// Redeemable up to and including its expiry, and gone a second later.
	a.tick(48 * time.Hour)
	maryam.must(200, "POST", "/v1/me/refills/"+got.Refills[0].ID+"/redeem", nil, nil)
	a.tick(time.Second)
	if held := maryam.usage().Refills; len(held) != 0 {
		t.Errorf("held after expiry = %+v", held)
	}
	maryam.must(404, "POST", "/v1/me/refills/"+got.Refills[1].ID+"/redeem", nil, nil)
	if n := admin.userInfo(me.ID).Refills; n != 0 {
		t.Errorf("Admins see %d held", n)
	}

	for _, bad := range []protocol.GiveRefills{{}, {Count: -1}, {Count: 101}, {Count: 1, Days: -1}, {Count: 1, Days: 3651}} {
		admin.must(400, "POST", "/v1/admin/users/"+me.ID+"/refills", bad, nil)
	}
	admin.must(404, "POST", "/v1/admin/users/us_nope/refills", protocol.GiveRefills{Count: 1}, nil)
	maryam.must(403, "POST", "/v1/admin/users/"+me.ID+"/refills", protocol.GiveRefills{Count: 1}, nil)
}

func TestRedeemingTwoRefillsInOnePeriod(t *testing.T) {
	a, admin, me, dev, bridge := quotaFixture(t)
	onBridge := func() bool { return len(a.bridgeDevices(bridge)) == 1 }
	maryam := a.client()
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/quota", protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutBlock}, nil)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/refills", protocol.GiveRefills{Count: 2}, nil)
	held := maryam.usage().Refills
	if len(held) != 2 || !held[1].ExpiresAt.Equal(a.clock.Add(30*24*time.Hour).Truncate(time.Second)) {
		t.Fatalf("held = %+v, want two lasting the default 30 days", held)
	}

	a.use(dev, 1000, 0)
	if onBridge() {
		t.Fatal("a User who ran out is still on the Bridge")
	}
	// The preview: what redeeming adds now.
	if got := maryam.usage(); got.RefillAdds == nil || *got.RefillAdds != 1000 || left(got) != 0 {
		t.Errorf("before redeeming: %+v", got)
	}
	var got protocol.MyUsage
	maryam.must(200, "POST", "/v1/me/refills/"+held[0].ID+"/redeem", nil, &got)
	if !onBridge() || left(got) != 1000 || got.Current.Bytes != 1000 || len(got.Refills) != 1 || *got.RefillAdds != 0 {
		t.Errorf("after one redeem: on the Bridge %v, %+v", onBridge(), got)
	}

	// The second, once they ran out again, adds what they used since the first.
	a.use(dev, 2000, 0)
	if onBridge() {
		t.Fatal("using the first Refill up did not block")
	}
	maryam.must(200, "POST", "/v1/me/refills/"+held[1].ID+"/redeem", nil, &got)
	if !onBridge() || left(got) != 1000 || got.Current.Bytes != 2000 || len(got.Refills) != 0 {
		t.Errorf("after two redeems: on the Bridge %v, %+v", onBridge(), got)
	}
	if r := got.Current.Remedies; len(r) != 2 || r[0].Kind != "reset" || r[0].Bytes != 1000 || r[1].Bytes != 1000 {
		t.Errorf("remedies = %+v, want two Resets of 1000", r)
	}
	maryam.must(404, "POST", "/v1/me/refills/"+held[1].ID+"/redeem", nil, nil)
}

func TestRedeemIsRefusedOnAnUnlimitedQuota(t *testing.T) {
	a, admin, me, _, _ := quotaFixture(t)
	maryam := a.client()
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/refills", protocol.GiveRefills{Count: 1}, nil)
	got := maryam.usage()
	if got.Quota != nil || got.Left != nil || got.RefillAdds != nil || len(got.Refills) != 1 {
		t.Fatalf("unlimited with a Refill: %+v", got)
	}
	maryam.must(409, "POST", "/v1/me/refills/"+got.Refills[0].ID+"/redeem", nil, nil)
	if held := maryam.usage().Refills; len(held) != 1 {
		t.Errorf("a refused redeem used the Refill up: %+v", held)
	}
	// Nobody else can redeem it.
	reza, _ := a.user("reza")
	reza.must(404, "POST", "/v1/me/refills/"+got.Refills[0].ID+"/redeem", nil, nil)

	// Redeeming with nothing used is allowed, once there is a Quota.
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/quota", protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutBlock}, nil)
	var after protocol.MyUsage
	maryam.must(200, "POST", "/v1/me/refills/"+got.Refills[0].ID+"/redeem", nil, &after)
	if left(after) != 1000 || len(after.Refills) != 0 || len(after.Current.Remedies) != 1 || after.Current.Remedies[0].Bytes != 0 {
		t.Errorf("redeeming at zero Usage: %+v", after)
	}
}

func TestAnInviteWithPresetsMakesAUserWithThem(t *testing.T) {
	a := newAPI(t)
	admin := a.admin()
	var inv protocol.IssuedInvite
	admin.must(200, "POST", "/v1/admin/invites", protocol.CreateInvite{Quota: new(uint64(5e9)), RunningOut: protocol.RunningOutSlow,
		Rate: 125_000, Refills: 2, RefillDays: 10}, &inv)
	a.tick(3 * 24 * time.Hour) // the Refills' 10 days count from registration
	sara := a.client()
	var me protocol.Me
	sara.must(200, "POST", "/v1/invites/accept", protocol.AcceptInvite{Token: inv.Token, Handle: "sara", Password: password}, &me)

	u := admin.userInfo(me.ID)
	if u.Quota == nil || *u.Quota != 5e9 || u.RunningOut != "slow" || u.Rate != 125_000 || u.Refills != 2 || u.RenewalDay != a.clock.UTC().Day() {
		t.Errorf("the invited User = %+v", u)
	}
	got := sara.usage()
	if len(got.Refills) != 2 || !got.Refills[0].ExpiresAt.Equal(a.clock.Add(10*24*time.Hour).Truncate(time.Second)) {
		t.Errorf("held = %+v, want two expiring 10 days after registration", got.Refills)
	}
	if left(got) != 5e9 || got.RenewsAt.IsZero() || !got.RenewsAt.Equal(got.Current.End) {
		t.Errorf("usage = %+v", got)
	}

	// No presets: unlimited, blocking, no Refills.
	admin.must(200, "POST", "/v1/admin/invites", protocol.CreateInvite{}, &inv)
	reza := a.client()
	reza.must(200, "POST", "/v1/invites/accept", protocol.AcceptInvite{Token: inv.Token, Handle: "reza", Password: password}, &me)
	if u := admin.userInfo(me.ID); u.Quota != nil || u.RunningOut != "block" || u.Refills != 0 {
		t.Errorf("an Invite without presets = %+v", u)
	}

	for _, bad := range []protocol.CreateInvite{
		{RunningOut: protocol.RunningOutSlow},
		{RunningOut: protocol.RunningOutBlock, Rate: 10},
		{RunningOut: "throttle"},
		{Quota: new(uint64(1 << 63))},
		{Refills: -1},
		{Refills: 101},
		{Refills: 1, RefillDays: -1},
	} {
		admin.must(400, "POST", "/v1/admin/invites", bad, nil)
	}
}

func TestMyUsageShowsHistoryAndTheDeviceSplit(t *testing.T) {
	a, admin, me, dev, _ := quotaFixture(t)
	maryam := a.client()
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil)
	var issued protocol.IssuedDevice
	maryam.must(200, "POST", "/v1/me/devices", protocol.NewDevice{Name: "laptop", PublicKey: pubKey(t)}, &issued)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/quota", protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutBlock}, nil)
	a.use(dev, 100, 200)
	a.use(issued.Device, 30, 0)
	maryam.must(200, "POST", "/v1/me/devices/"+issued.Device.ID+"/revoke", nil, nil)

	got := maryam.usage()
	c := got.Current
	if c.Bytes != 330 || !c.Detailed || len(c.Devices) != 1 || c.Devices[0].Name != "phone" || c.Devices[0].Bytes != 300 || c.Revoked != 30 {
		t.Errorf("current = %+v", c)
	}
	if left(got) != 670 || !got.RenewsAt.Equal(c.End) || got.History == nil || got.Quota == nil || *got.Quota != 1000 || got.RunningOut != "block" {
		t.Errorf("usage = %+v", got)
	}
	var du protocol.DeviceUsage
	maryam.must(200, "GET", "/v1/me/devices/"+dev.ID+"/usage", nil, &du)
	if du.Period.Period != c.Start.Format("2006-01-02") || du.Period.Up != 100 || du.Period.Down != 200 || du.Period.Exit != "" {
		t.Errorf("the Device's period = %+v", du.Period)
	}

	// Into the next period: the last one moves into history.
	a.tick(c.End.Sub(*a.clock) + time.Hour)
	if err := a.s.ClosePeriods(t.Context()); err != nil {
		t.Fatal(err)
	}
	maryam.must(200, "POST", "/v1/login", protocol.Login{Handle: "maryam", Password: password}, nil) // a month outlives the session
	got = maryam.usage()
	if got.Current.Bytes != 0 || left(got) != 1000 || len(got.History) != 1 || got.History[0].Bytes != 330 || !got.History[0].Start.Equal(c.Start) {
		t.Errorf("next period: %+v", got)
	}
}
