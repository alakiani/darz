package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alakiani/ways/internal/protocol"
)

// bridgeRates returns the rate each Device in a Bridge's latest snapshot
// carries, by address.
func (a *api) bridgeRates(bridgeID string) map[string]uint64 {
	a.t.Helper()
	snap, err := a.s.LatestSnapshot(context.Background(), bridgeID)
	if err != nil {
		a.t.Fatal(err)
	}
	var st protocol.State
	json.Unmarshal(snap.Body, &st)
	out := map[string]uint64{}
	for _, d := range st.Bridge.Devices {
		out[d.Address] = d.Rate
	}
	return out
}

func TestAThrottledDeviceCarriesItsRateInTheSnapshot(t *testing.T) {
	a, admin, me, dev, bridge := quotaFixture(t)
	path := "/v1/admin/users/" + me.ID + "/quota"
	rate := func() (uint64, bool) {
		r, ok := a.bridgeRates(bridge)[dev.Address+"/32"]
		return r, ok
	}

	admin.must(200, "POST", path, protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutSlow, Rate: 125_000}, nil)
	if r, ok := rate(); !ok || r != 0 {
		t.Errorf("with Quota left: rate %d, in %v; want 0, in", r, ok)
	}
	a.use(dev, 600, 400)
	if r, ok := rate(); !ok || r != 125_000 {
		t.Errorf("out of Quota, slowed: rate %d, in %v; want 125000, in", r, ok)
	}

	// A new rate, then none.
	admin.must(200, "POST", path, protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutSlow, Rate: 250_000}, nil)
	if r, _ := rate(); r != 250_000 {
		t.Errorf("after a new rate: %d, want 250000", r)
	}
	admin.must(200, "POST", path, protocol.SetQuota{RunningOut: protocol.RunningOutBlock}, nil)
	if r, ok := rate(); !ok || r != 0 {
		t.Errorf("unlimited: rate %d, in %v; want 0, in", r, ok)
	}

	// Suspended outranks the throttle: out, with no rate anywhere.
	admin.must(200, "POST", path, protocol.SetQuota{Quota: new(uint64(1000)), RunningOut: protocol.RunningOutSlow, Rate: 125_000}, nil)
	admin.must(200, "POST", "/v1/admin/users/"+me.ID+"/suspend", nil, nil)
	if _, ok := rate(); ok {
		t.Error("a suspended User's throttled Device is still in the snapshot")
	}
}
