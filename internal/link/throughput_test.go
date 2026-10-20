package link

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/protocol"
)

var loopback = netip.MustParseAddr("127.0.0.1")

func startThroughputServer(t *testing.T, allowed func(netip.Addr) bool) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go serveThroughput(ctx, ln, allowed)
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

func TestThroughputTestStopsAtItsByteCap(t *testing.T) {
	port := startThroughputServer(t, func(netip.Addr) bool { return true })
	start := time.Now()
	r, err := measureThroughput(context.Background(), loopback, loopback, port, 1<<20, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.bytes != 1<<20 {
		t.Errorf("received %d bytes, want exactly the 1 MiB cap", r.bytes)
	}
	if r.mbps <= 0 || time.Since(start) > 2*time.Second {
		t.Errorf("%.1f Mbps in %v: want a positive rate, well before the time cap", r.mbps, time.Since(start))
	}
}

func TestThroughputTestStopsAtItsTimeCap(t *testing.T) {
	// A far end that trickles 10 KB every 100 ms, about 0.8 Mbps.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req := make([]byte, 8)
		if _, err := c.Read(req); err != nil {
			return
		}
		for range 50 {
			if _, err := c.Write(make([]byte, 10_000)); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	start := time.Now()
	r, err := measureThroughput(context.Background(), loopback, loopback, uint16(ln.Addr().(*net.TCPAddr).Port), 4<<20, time.Second)
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("the test ran %v with a 1 s cap", took)
	}
	if err != nil {
		t.Fatal(err)
	}
	if r.mbps < 0.5 || r.mbps > 1.2 {
		t.Errorf("trickle measured at %.2f Mbps, want about 0.8", r.mbps)
	}
}

func TestThroughputServerAnswersOnlyOnLinkAddresses(t *testing.T) {
	port := startThroughputServer(t, func(netip.Addr) bool { return false })
	if _, err := measureThroughput(context.Background(), loopback, loopback, port, 1<<20, time.Second); err == nil {
		t.Error("measured through a server that must refuse this address")
	}
}

func TestThroughputServerCapsWhatItSends(t *testing.T) {
	port := startThroughputServer(t, func(netip.Addr) bool { return true })
	c, err := net.Dial("tcp4", netip.AddrPortFrom(loopback, port).String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := append([]byte{}, throughputMagic[:]...)
	req = binary.BigEndian.AppendUint32(req, 1<<31)
	c.Write(req)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var n int64
	buf := make([]byte, 64<<10)
	for {
		k, err := c.Read(buf)
		n += int64(k)
		if err != nil {
			break
		}
	}
	if n != maxServedBytes {
		t.Errorf("a request for 2 GiB got %d bytes, want the server's cap of %d", n, maxServedBytes)
	}
}

var timing = throughputTimingFrom(protocol.Scoring{
	ThroughputEverySec: 7200, ThroughputJitterSec: 600, ThroughputMaxBytes: 4 << 20, ThroughputMaxSec: 3,
})

func TestThroughputTimingDefaults(t *testing.T) {
	if got := throughputTimingFrom(protocol.Scoring{}); got != throughputTimingFrom(protocol.DefaultScoring) {
		t.Errorf("an old snapshot without throughput timings gives %+v, want the defaults", got)
	}
	if timing.every != 2*time.Hour || timing.maxBytes != 4<<20 || timing.maxDur != 3*time.Second {
		t.Errorf("timing = %+v", timing)
	}
}

func TestFirstTestComesWithinTheJitter(t *testing.T) {
	var tp throughput
	if tp.due(t0, 0, false, timing, func() float64 { return 0.5 }) {
		t.Fatal("a new Link is tested at once, not after a random delay")
	}
	if !tp.next.Equal(t0.Add(5 * time.Minute)) {
		t.Errorf("first test at %v, want halfway into the 10-minute jitter", tp.next.Sub(t0))
	}
	if !tp.due(t0.Add(5*time.Minute), 0, false, timing, func() float64 { return 0.5 }) {
		t.Error("not due at its time")
	}
	// Then every 2 hours, give or take the jitter.
	if d := tp.next.Sub(t0.Add(5 * time.Minute)); d != 2*time.Hour {
		t.Errorf("next test in %v, want 2h (jitter midpoint)", d)
	}
	tp.next = time.Time{}
	if !tp.due(t0, 0, false, timing, func() float64 { return 0 }) {
		t.Error("a first test drawn at no delay is not due")
	}
	if d := tp.next.Sub(t0); d != 2*time.Hour-10*time.Minute {
		t.Errorf("earliest next test in %v, want 2h minus the jitter", d)
	}
}

const mbit = 1_000_000 / 8 // bytes per second at 1 Mbps

func TestPassivePeaksRaiseTheFigure(t *testing.T) {
	var tp throughput
	tp.measured(20, t0, protocol.TransportAmneziaWG)
	rx := uint64(0)
	var rate float64
	var ok bool
	// 30 Mbps of real traffic for 4 s.
	for s := 0; s <= 4; s++ {
		rate, ok = tp.sample(t0.Add(time.Duration(s)*time.Second), "dz1", rx)
		rx += 30 * mbit
	}
	if !ok || rate < 29.9 || rate > 30.1 {
		t.Fatalf("passive rate = %v (%v), want 30 Mbps over the window", rate, ok)
	}
	tp.next = t0.Add(time.Hour)
	tp.due(t0.Add(4*time.Second), rate, ok, timing, func() float64 { return 0.5 })
	if tp.mbps != rate {
		t.Errorf("figure = %v after a 30 Mbps peak, want it raised to the peak", tp.mbps)
	}
	// A lower rate never lowers it; only a test does.
	tp.due(t0.Add(5*time.Second), 5, true, timing, func() float64 { return 0.5 })
	if tp.mbps != rate {
		t.Errorf("figure = %v, lowered by light traffic", tp.mbps)
	}
	tp.measured(12, t0, protocol.TransportAmneziaWG)
	if tp.mbps != 12 {
		t.Errorf("a test gave 12 Mbps but the figure is %v", tp.mbps)
	}
}

func TestTestSkippedWhileRealTrafficExceedsTheFigure(t *testing.T) {
	tp := throughput{mbps: 20, next: t0}
	if tp.due(t0, 25, true, timing, func() float64 { return 0.5 }) {
		t.Error("tested while real traffic already ran above the figure")
	}
	if tp.mbps != 25 || !tp.next.After(t0.Add(time.Hour)) {
		t.Errorf("after skipping: figure %v, next in %v; want 25 and the next slot", tp.mbps, tp.next.Sub(t0))
	}
	tp.next = t0
	if !tp.due(t0, 10, true, timing, func() float64 { return 0.5 }) {
		t.Error("skipped with real traffic under the figure")
	}
}

func TestPassiveRateNeedsAFullWindowAndResetsOnANewInterface(t *testing.T) {
	var tp throughput
	tp.sample(t0, "dz1", 0)
	if _, ok := tp.sample(t0.Add(time.Second), "dz1", mbit); ok {
		t.Error("a rate from 1 s of samples, want a full window")
	}
	tp.sample(t0.Add(2*time.Second), "dz1", 2*mbit)
	if r, ok := tp.sample(t0.Add(3*time.Second), "dz1", 3*mbit); !ok || r < 0.99 || r > 1.01 {
		t.Errorf("rate = %v (%v), want 1 Mbps", r, ok)
	}
	// The active instance changed: its counter is another counter.
	if _, ok := tp.sample(t0.Add(4*time.Second), "dz2", 100); ok {
		t.Error("a rate across two interfaces' counters")
	}
	// A test's own bytes are not a peak.
	tp.sample(t0.Add(5*time.Second), "dz2", 100)
	tp.measured(1, t0, protocol.TransportAmneziaWG)
	if _, ok := tp.sample(t0.Add(9*time.Second), "dz2", 100+100*mbit); ok {
		t.Error("the bytes of the test counted as real traffic")
	}
}
