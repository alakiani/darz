package dataplane

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/protocol"
)

type fakeCounters struct{ up, down []counterElem }

func (f *fakeCounters) read(set string) ([]counterElem, error) {
	if set == "usage_up" {
		return f.up, nil
	}
	return f.down, nil
}

func exitOf(mark uint32) string {
	switch mark {
	case linkMark(4):
		return "nd_ab1"
	case localMark:
		return "nd_self"
	}
	return ""
}

func newMeter(t *testing.T, now *time.Time) *meter {
	m, err := openMeter(filepath.Join(t.TempDir(), "usage.json"), func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func total(bs []protocol.UsageBucket) (up, down uint64) {
	for _, b := range bs {
		up += b.Up
		down += b.Down
	}
	return
}

func TestMeterCountsDeltasPerDeviceExitAndHour(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 10, 0, 0, time.UTC)
	m := newMeter(t, &now)
	c := &fakeCounters{
		up:   []counterElem{{"10.77.0.2", linkMark(4), 100}, {"10.77.0.2", localMark, 7}, {"10.77.0.3", dropMark, 99}},
		down: []counterElem{{"10.77.0.2", linkMark(4) | 0x10000, 1000}},
	}
	if err := m.collect(c.read, exitOf); err != nil {
		t.Fatal(err)
	}
	c.up[0].bytes, c.down[0].bytes = 150, 1600
	m.collect(c.read, exitOf)

	got := m.report(100)
	if len(got) != 2 {
		t.Fatalf("buckets = %+v, want one per (Device, Exit); drops are not usage", got)
	}
	for _, b := range got {
		if b.Hour != now.Truncate(time.Hour).Unix() || b.Device != "10.77.0.2" {
			t.Errorf("bucket %+v", b)
		}
		switch b.Exit {
		case "nd_ab1":
			if b.Up != 150 || b.Down != 1600 {
				t.Errorf("through ab1: %+v", b)
			}
		case "nd_self":
			if b.Up != 7 {
				t.Errorf("local exit: %+v", b)
			}
		}
	}

	// A ruleset reload starts the counters again from zero; no bytes are
	// lost or counted twice.
	m.collect(c.read, exitOf)
	m.reset()
	c.up = []counterElem{{"10.77.0.2", linkMark(4), 30}}
	c.down = nil
	m.collect(c.read, exitOf)
	if up, _ := total(m.report(100)); up != 150+7+30 {
		t.Errorf("up after a reload = %d, want 187", up)
	}
	// The running total, for the metering-mismatch alert, counts the same.
	if got := m.metered(); got != 150+7+30+1600 {
		t.Errorf("metered = %d, want 1787", got)
	}
}

func TestMeterKeepsBucketsOnDiskUntilAcked(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 10, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "usage.json")
	m, _ := openMeter(path, func() time.Time { return now })
	c := &fakeCounters{up: []counterElem{{"10.77.0.2", linkMark(4), 100}}}
	m.collect(c.read, exitOf)

	// The next hour: new bytes go to a new bucket.
	now = now.Add(time.Hour)
	c.up[0].bytes = 160
	m.collect(c.read, exitOf)

	// A restarted Agent still has both.
	m2, err := openMeter(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	sent := m2.report(100)
	if len(sent) != 2 || sent[0].Up != 60 || sent[1].Up != 100 {
		t.Fatalf("after restart: %+v", sent)
	}
	// Acknowledged closed hours go; the open hour stays, to be resent as it grows.
	m2.acked(sent)
	left := m2.report(100)
	if len(left) != 1 || left[0].Hour != now.Truncate(time.Hour).Unix() {
		t.Errorf("after ack: %+v, want only the open hour", left)
	}
}

func TestMeterCapsReportsAndForgetsAfterThirtyDays(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := newMeter(t, &now)
	c := &fakeCounters{}
	var bytes uint64
	for h := 0; h < 40*24; h++ {
		bytes += 10
		c.up = []counterElem{{"10.77.0.2", linkMark(4), bytes}}
		m.collect(c.read, exitOf)
		now = now.Add(time.Hour)
	}
	got := m.report(50)
	if len(got) != 50 {
		t.Fatalf("report of %d buckets, want the cap of 50", len(got))
	}
	oldest := now.Add(-time.Hour).Add(-maxBucketAge).Unix() // as of the last collection
	for _, b := range got {
		if b.Hour < oldest {
			t.Fatalf("a bucket older than 30 days survived: %d", b.Hour)
		}
	}
	if got[0].Hour > got[1].Hour {
		t.Error("oldest buckets should go first")
	}
}

func TestMeterSendsTheOpenHourBeforeTheBacklog(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)
	m := newMeter(t, &now)
	c := &fakeCounters{}
	for h := 0; h < 30; h++ {
		c.up = []counterElem{{"10.77.0.2", linkMark(4), uint64(10 * (h + 1))}, {"10.77.0.3", linkMark(4), uint64(5 * (h + 1))}}
		m.collect(c.read, exitOf)
		now = now.Add(time.Hour)
	}
	open := now.Add(-time.Hour).Truncate(time.Hour).Unix() // the last collection's hour
	now = now.Add(-time.Hour)                              // still in it

	got := m.report(10)
	if len(got) != 10 {
		t.Fatalf("report of %d buckets, want the cap of 10", len(got))
	}
	if got[0].Hour != open || got[1].Hour != open {
		t.Fatalf("report starts with hours %d and %d, want the open hour %d for both Devices", got[0].Hour, got[1].Hour, open)
	}
	if first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(); got[2].Hour != first {
		t.Errorf("the backlog starts at hour %d, want the oldest, %d", got[2].Hour, first)
	}
	for i := 3; i < len(got); i++ {
		if got[i].Hour < got[i-1].Hour {
			t.Fatalf("the backlog is not oldest first: %d after %d", got[i].Hour, got[i-1].Hour)
		}
	}
}

func TestParseCounters(t *testing.T) {
	out := []byte(`{"nftables": [{"metainfo": {"version": "1.0.9"}}, {"set": {"family": "inet", "name": "usage_up", "table": "ways",
		"type": ["ipv4_addr", "mark"], "flags": ["dynamic"], "elem": [
		{"elem": {"val": {"concat": ["10.77.0.2", 131076]}, "counter": {"packets": 3, "bytes": 252}}},
		{"elem": {"val": {"concat": ["10.77.0.3", 262144]}, "counter": {"packets": 1, "bytes": 84}}}]}}]}`)
	got, err := parseCounters(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (counterElem{"10.77.0.2", linkMark(4), 252}) || got[1].mark != localMark {
		t.Errorf("parsed %+v", got)
	}
}

func TestMeterDoesNotCountTwiceAcrossAnAgentRestart(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 10, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "usage.json")
	m, _ := openMeter(path, func() time.Time { return now })
	c := &fakeCounters{up: []counterElem{{"10.77.0.2", linkMark(4), 100}}}
	m.collect(c.read, exitOf)

	// The Agent restarts; the nftables counters do not.
	m2, _ := openMeter(path, func() time.Time { return now })
	c.up[0].bytes = 130
	m2.collect(c.read, exitOf)
	if up, _ := total(m2.report(10)); up != 130 {
		t.Errorf("up after a restart = %d, want 130", up)
	}
}
