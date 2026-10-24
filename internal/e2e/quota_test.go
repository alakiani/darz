//go:build linux

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/ways/internal/auth"
	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
	"github.com/alakiani/ways/internal/testutil"
)

// adminSession is an Admin signed in to the panel's API with TOTP, as the
// admin panel is.
func adminSession(t *testing.T, co *coord) *testutil.AdminClient {
	t.Helper()
	tok, _, err := co.store.CreateInvite(context.Background(), store.Invite{Kind: "new", Admin: true}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	a := &testutil.AdminClient{URL: co.admin.URL, Client: &http.Client{Jar: jar}}
	must := func(method, path string, in, out any) {
		t.Helper()
		if status := a.Do(t, method, path, in, out); status != http.StatusOK {
			t.Fatalf("%s %s: status %d", method, path, status)
		}
	}
	must("POST", "/v1/invites/accept", protocol.AcceptInvite{Token: tok, Handle: "admin", Password: "the admin's long password"}, nil)
	var offer protocol.TOTPOffer
	must("POST", "/v1/me/totp", nil, &offer)
	must("POST", "/v1/me/totp/confirm", protocol.TOTPCode{Code: auth.TOTP(offer.Secret, co.store.Now())}, nil)
	return a
}

// TestQuotaEnforcement is Quota session 2's lab test (ADR 0010): a User who
// runs out of their Quota leaves every Bridge's snapshot within about 30 s,
// raising the Quota brings them back on the next snapshot, and so does
// their next Quota Period, with no traffic. A suspended User who also ran
// out shows the stronger reason.
func TestQuotaEnforcement(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ir2"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "phone1"},
		lab.At("web-ab", "93.184.215.14"),
		lab.At("dns-ab", "9.9.9.9"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	co := startCoordinator(t, l.Node("coord"))
	// The Coordinator's clock runs at an offset the test can move, to reach
	// the User's next Quota Period.
	var offset atomic.Int64
	co.store.Now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	co.c.Upstreams = []string{"9.9.9.9"}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	co.c.FeedURL, co.c.RIPEstatURL = serveFeed(t, irFeed("2.144.0.0/14", "5.0.0.0/8")), ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}
	whoami(t, l.Node("web-ab"), 7)
	discard(t, l.Node("web-ab"), "9")
	go co.c.RunQuotaPeriods(ctx, time.Second)

	admin := adminSession(t, co)
	user, _ := co.store.CreateUser(ctx, "maryam")
	renewalDay := time.Now().UTC().Day() // the day they registered
	key, _ := wgtypes.GeneratePrivateKey()
	pub := key.PublicKey().String()
	dev, err := co.store.CreateDevice(ctx, user.ID, "phone", pub)
	if err != nil {
		t.Fatal(err)
	}
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ir2", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p := newPhone(t, l.Node("phone1"), key, dev.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	abroad := func() bool { _, err := p.ask("93.184.215.14:7"); return err == nil }
	testutil.Eventually(t, 120*time.Second, "the phone reaches the site abroad", abroad)

	// onBridges counts the Bridges whose latest snapshot lets the phone in.
	onBridges := func() int {
		in := 0
		for _, name := range []string{"ir1", "ir2"} {
			snap, err := co.store.LatestSnapshot(ctx, m.ids[name])
			if err != nil {
				t.Fatal(err)
			}
			var st protocol.State
			if err := json.Unmarshal(snap.Body, &st); err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(st.Bridge.Devices, func(d protocol.Device) bool { return d.PublicKey == pub }) {
				in++
			}
		}
		return in
	}
	setQuota := func(quota *uint64) protocol.UserInfo {
		t.Helper()
		var u protocol.UserInfo
		req := protocol.SetQuota{Quota: quota, RunningOut: protocol.RunningOutBlock}
		if status := admin.Do(t, "POST", "/v1/admin/users/"+user.ID+"/quota", req, &u); status != http.StatusOK {
			t.Fatalf("set quota: status %d", status)
		}
		return u
	}
	device := func() protocol.DeviceInfo {
		t.Helper()
		var devs []protocol.DeviceInfo
		if status := admin.Do(t, "GET", "/v1/admin/users/"+user.ID+"/devices", nil, &devs); status != http.StatusOK || len(devs) != 1 {
			t.Fatalf("devices: status %d, %+v", status, devs)
		}
		return devs[0]
	}
	if n := onBridges(); n != 2 {
		t.Fatalf("the phone is on %d Bridges, want 2", n)
	}

	// A small Quota, with room left: nothing changes yet.
	const small = 1_000_000
	u := setQuota(new(uint64(small)))
	if u.Quota == nil || *u.Quota != small || u.RunningOut != protocol.RunningOutBlock || u.RenewalDay != renewalDay {
		t.Errorf("after setting the Quota: %+v", u)
	}
	if n := onBridges(); n != 2 {
		t.Fatalf("with Quota left, the phone is on %d Bridges, want 2", n)
	}

	// Traffic past it: every Bridge leaves the phone out within about 30 s.
	p.send("93.184.215.14:9", 1200, 1000) // about 1.2 MB on the wire
	ranOut := time.Now()
	testutil.Eventually(t, 90*time.Second, "every Bridge's snapshot leaves the phone out", func() bool { return onBridges() == 0 })
	took := time.Since(ranOut)
	t.Logf("the phone left the Bridges %v after running out", took.Round(100*time.Millisecond))
	if took > 35*time.Second {
		t.Errorf("the phone left the Bridges %v after running out, want about 30 s", took)
	}
	testutil.Eventually(t, 30*time.Second, "the Bridge stops carrying the phone", func() bool { return !abroad() })
	if d := device(); d.State != "blocked" || d.Reason != "quota used up" {
		t.Errorf("out of Quota, the Device is %s (%s)", d.State, d.Reason)
	}
	var users []protocol.UserInfo
	admin.Do(t, "GET", "/v1/admin/users", nil, &users)
	for _, u := range users {
		if u.ID == user.ID && u.PeriodBytes < small {
			t.Errorf("the Admin users list shows %d bytes this period, want at least %d", u.PeriodBytes, small)
		}
	}

	// Suspended outranks quota used up; resuming leaves the User blocked.
	if status := admin.Do(t, "POST", "/v1/admin/users/"+user.ID+"/suspend", nil, nil); status != http.StatusOK {
		t.Fatalf("suspend: status %d", status)
	}
	if d := device(); d.State != "blocked" || d.Reason != "user suspended" {
		t.Errorf("suspended and out of Quota, the Device is %s (%s)", d.State, d.Reason)
	}
	if status := admin.Do(t, "POST", "/v1/admin/users/"+user.ID+"/resume", nil, nil); status != http.StatusOK {
		t.Fatalf("resume: status %d", status)
	}
	if d := device(); d.State != "blocked" || d.Reason != "quota used up" {
		t.Errorf("resumed and out of Quota, the Device is %s (%s)", d.State, d.Reason)
	}
	if n := onBridges(); n != 0 {
		t.Errorf("resumed and out of Quota, the phone is on %d Bridges", n)
	}

	// Raising the Quota brings the phone back on the next snapshot.
	setQuota(new(uint64(100 * small)))
	if n := onBridges(); n != 2 {
		t.Errorf("after raising the Quota, the phone is on %d Bridges, want 2", n)
	}
	testutil.Eventually(t, 60*time.Second, "the phone reaches the site again", abroad)
	if d := device(); d.State != "active" {
		t.Errorf("with Quota again, the Device is %s (%s)", d.State, d.Reason)
	}

	// Out again, then the next Quota Period starts: the phone comes back
	// with no traffic.
	setQuota(new(uint64(small)))
	if n := onBridges(); n != 0 {
		t.Fatalf("after lowering the Quota, the phone is on %d Bridges, want 0", n)
	}
	next := store.QuotaPeriodAt(renewalDay, co.store.Now()).End
	offset.Store(int64(next.Add(time.Second).Sub(time.Now())))
	testutil.Eventually(t, 30*time.Second, "the next Quota Period brings the phone back", func() bool { return onBridges() == 2 })
}
