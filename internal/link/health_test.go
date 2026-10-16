package link

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// probeEverySecond records n probes, one a second from start, answered
// unless lost(i).
func probeEverySecond(m *meter, start time.Time, n int, lost func(i int) bool) time.Time {
	at := start
	for i := 0; i < n; i++ {
		m.record(at, !lost(i), 20*time.Millisecond)
		at = at.Add(time.Second)
	}
	return at
}

func never(int) bool { return false }

func TestUpNeedsHandshakeFreshSuccessAndLowLoss(t *testing.T) {
	m := &meter{}
	now := probeEverySecond(m, t0, 30, never)
	if m.up(now, time.Time{}) {
		t.Error("up without a handshake")
	}
	if !m.up(now, now.Add(-time.Minute)) {
		t.Error("not up with a recent handshake and every probe answered")
	}
	if m.up(now, now.Add(-4*time.Minute)) {
		t.Error("up with a handshake older than a session lasts")
	}

	// 3 of the last 30 lost is 10%, which is not under 10%.
	m = &meter{}
	now = probeEverySecond(m, t0, 30, func(i int) bool { return i%10 == 9 })
	if m.up(now, now) {
		t.Errorf("up at %.0f%% loss", 100*m.loss(now))
	}
	// 2 of 30 is fine.
	m = &meter{}
	now = probeEverySecond(m, t0, 30, func(i int) bool { return i == 3 || i == 17 })
	if !m.up(now, now) {
		t.Errorf("not up at %.1f%% loss", 100*m.loss(now))
	}

	// The last success must be under 10 s old.
	m = &meter{}
	now = probeEverySecond(m, t0, 30, never)
	if m.up(now.Add(10*time.Second), now) {
		t.Error("up 10 s after the last success")
	}
}

func TestLossWindowIsThirtySeconds(t *testing.T) {
	m := &meter{}
	now := probeEverySecond(m, t0, 20, func(int) bool { return true }) // 20 lost
	now = probeEverySecond(m, now, 30, never)                          // then 30 answered
	if loss := m.loss(now); loss != 0 {
		t.Errorf("loss = %v, want old losses out of the window", loss)
	}
	if rtt := m.rtt(now); rtt != 20*time.Millisecond {
		t.Errorf("rtt = %v", rtt)
	}
}

func TestChooseFirstUpInPreferenceOrder(t *testing.T) {
	const switchBack = 5 * time.Minute
	wg, awg := uint64(1), uint64(2)
	up := func(id uint64, since time.Duration) candidate {
		return candidate{id: id, up: true, upSince: t0.Add(-since)}
	}
	down := func(id uint64) candidate { return candidate{id: id} }

	cases := []struct {
		name   string
		active uint64
		cands  []candidate
		want   uint64
	}{
		{"new link takes the first up", 0, []candidate{down(wg), up(awg, 0)}, awg},
		{"new link prefers the first", 0, []candidate{up(wg, 0), up(awg, 0)}, wg},
		{"nothing up", awg, []candidate{down(wg), down(awg)}, 0},
		{"active fails: switch at once", wg, []candidate{down(wg), up(awg, time.Second)}, awg},
		{"preferred back but not for long", awg, []candidate{up(wg, 4*time.Minute), up(awg, time.Hour)}, awg},
		{"preferred up for 5 minutes: switch back", awg, []candidate{up(wg, switchBack), up(awg, time.Hour)}, wg},
		{"active vanished from the snapshot", 9, []candidate{down(wg), up(awg, 0)}, awg},
	}
	for _, c := range cases {
		if got := choose(t0, c.active, c.cands, switchBack); got != c.want {
			t.Errorf("%s: choose = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBackoffDoublesWithJitterAndCaps(t *testing.T) {
	b := backoff{min: 30 * time.Second, max: 15 * time.Minute}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, w := range want {
		got := b.next()
		if got < w*8/10 || got > w*12/10 {
			t.Errorf("attempt %d: wait %v, want %v ±20%%", i, got, w)
		}
	}
	b.reset()
	if got := b.next(); got > 36*time.Second {
		t.Errorf("after reset: wait %v, want about 30 s", got)
	}
}
