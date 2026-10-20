package link

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alakiani/ways/internal/protocol"
)

// The throughput test (ADR 0008): a Bridge asks the far end of each of its
// Links to send it up to about 4 MB for up to 3 s, every 2 hours with
// jitter, over the Link's active instance. That is the direction that
// matters to Users: from the Exit towards the Bridge. Because the Link
// already carries real traffic, the figure is the capacity that is left,
// which is what closes the balancing loop. Being Up never depends on it.
//
// Between tests the figure only goes up: real traffic on the Link that
// runs faster than the figure is a measurement too, and the test is
// skipped while real traffic already exceeds it.

// ThroughputPort is the TCP port every Agent serves the throughput test on,
// on its Links' inner addresses only.
const ThroughputPort = 51801

// A request is "DZT" 0x01 and the number of bytes wanted, as a uint32. The
// answer is that many zero bytes, capped, then the end of the connection.
var throughputMagic = [4]byte{'D', 'Z', 'T', 1}

const (
	// maxServedBytes caps what one request gets, whatever it asks for.
	maxServedBytes = 16 << 20
	// maxServing is how many tests a Node serves at once.
	maxServing = 4
	// peakWindow is how long real traffic must keep up a rate for it to
	// count as a peak: as long as a test runs.
	peakWindow = 3 * time.Second
)

// serveThroughput answers throughput tests on ln until ctx ends, for
// connections that arrive on an address allowed accepts.
func serveThroughput(ctx context.Context, ln net.Listener, allowed func(netip.Addr) bool) {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	slots := make(chan struct{}, maxServing)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		local, _ := netip.ParseAddrPort(c.LocalAddr().String())
		if !allowed(local.Addr().Unmap()) {
			c.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close() // busy: the asker tries again at its next slot
			continue
		}
		go func() {
			defer func() { <-slots }()
			serveOne(c)
		}()
	}
}

func serveOne(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	req := make([]byte, 8)
	if _, err := io.ReadFull(c, req); err != nil || [4]byte(req[:4]) != throughputMagic {
		return
	}
	left := min(int64(binary.BigEndian.Uint32(req[4:])), maxServedBytes)
	// The asker stops reading after its time cap; this bounds a stalled one.
	c.SetDeadline(time.Now().Add(60 * time.Second))
	buf := make([]byte, 32<<10)
	for left > 0 {
		n, err := c.Write(buf[:min(left, int64(len(buf)))])
		left -= int64(n)
		if err != nil {
			return
		}
	}
}

// throughputResult is one test's outcome.
type throughputResult struct {
	mbps  float64
	bytes int64
}

// measureThroughput runs one test against the far end at peer, from this
// end's address local: it reads up to maxBytes for up to maxDur.
//
// The rate leaves out the first tenth of maxBytes, so TCP's slow start on a
// long round trip does not pass for a lack of capacity.
func measureThroughput(ctx context.Context, local, peer netip.Addr, port uint16, maxBytes int64, maxDur time.Duration) (throughputResult, error) {
	d := net.Dialer{LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(local, 0)), Timeout: maxDur}
	c, err := d.DialContext(ctx, "tcp4", netip.AddrPortFrom(peer, port).String())
	if err != nil {
		return throughputResult{}, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()

	req := append(throughputMagic[:0:0], throughputMagic[:]...)
	req = binary.BigEndian.AppendUint32(req, uint32(min(maxBytes, 1<<32-1)))
	start := time.Now()
	c.SetDeadline(start.Add(maxDur))
	if _, err := c.Write(req); err != nil {
		return throughputResult{}, err
	}

	warm := maxBytes / 10
	var total, warmBytes int64
	var warmAt, last time.Time
	buf := make([]byte, 64<<10)
	for total < maxBytes {
		n, err := c.Read(buf[:min(int64(len(buf)), maxBytes-total)])
		if n > 0 {
			total += int64(n)
			last = time.Now()
			if warmAt.IsZero() && total >= warm {
				warmAt, warmBytes = last, total
			}
		}
		if err != nil {
			break // the time cap, or the far end's own cap
		}
	}
	if total == 0 {
		return throughputResult{}, errors.New("throughput test: nothing received")
	}
	r := throughputResult{bytes: total}
	if !warmAt.IsZero() && total > warmBytes && last.Sub(warmAt) > 0 {
		r.mbps = float64(total-warmBytes) * 8 / last.Sub(warmAt).Seconds() / 1e6
	} else if el := last.Sub(start); el > 0 {
		r.mbps = float64(total) * 8 / el.Seconds() / 1e6
	}
	return r, nil
}

// isLinkAddr reports whether addr is this end's inner address of a Link
// instance, so the throughput test is never served on a public address.
func (m *Manager) isLinkAddr(addr netip.Addr) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.insts {
		if in.config().Addr.Addr() == addr {
			return true
		}
	}
	return false
}

// throughputLoop samples every Link's traffic once a second and runs the
// tests that fall due, until ctx ends.
func (m *Manager) throughputLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.measureLinks(ctx)
		}
	}
}

// measureLinks is one round of throughputLoop. Tests run one at a time, so
// two never share this Bridge's own capacity.
func (m *Manager) measureLinks(ctx context.Context) {
	type job struct {
		id          uint64
		peer        string
		tp          *throughput
		iface       string
		transport   protocol.Transport
		local, addr netip.Addr
	}
	m.mu.Lock()
	if !m.bridge {
		m.mu.Unlock()
		return
	}
	tm := throughputTimingFrom(m.scoring)
	var jobs []job
	for _, id := range slices.Sorted(maps.Keys(m.links)) {
		ls := m.links[id]
		in := m.insts[ls.active]
		if ls.active == 0 || !ls.routed || in == nil {
			continue
		}
		tp := m.tputs[id]
		if tp == nil {
			tp = &throughput{}
			m.tputs[id] = tp
		}
		jobs = append(jobs, job{id, ls.peer.Name, tp, in.iface(), in.spec.Transport, in.config().Addr.Addr(), in.peerAddr})
	}
	m.mu.Unlock()

	var due []job
	for _, j := range jobs {
		rx, err := rxBytes(j.iface)
		now := time.Now()
		m.mu.Lock()
		rate, ok := 0.0, false
		if err == nil {
			rate, ok = j.tp.sample(now, j.iface, rx)
		}
		before, wasDue := j.tp.mbps, !j.tp.next.IsZero() && !now.Before(j.tp.next)
		run := j.tp.due(now, rate, ok, tm, rand.Float64)
		after := j.tp.mbps
		if after > before {
			j.tp.observed(now, j.transport)
		}
		m.mu.Unlock()
		switch {
		case run:
			due = append(due, j)
		case wasDue:
			m.log.Info("throughput test skipped: real traffic is above the figure", "link", j.id, "peer", j.peer, "mbps", after)
		case after > before:
			m.log.Debug("throughput figure raised by real traffic", "link", j.id, "peer", j.peer, "mbps", after)
		}
	}
	for _, j := range due {
		r, err := measureThroughput(ctx, j.local, j.addr, ThroughputPort, tm.maxBytes, tm.maxDur)
		if err != nil {
			// The figure stays; the test comes again at the next slot.
			m.log.Warn("throughput test", "link", j.id, "peer", j.peer, "err", err)
			continue
		}
		m.mu.Lock()
		j.tp.measured(r.mbps, time.Now(), j.transport)
		m.mu.Unlock()
		m.log.Info("throughput measured", "link", j.id, "peer", j.peer, "mbps", fmt.Sprintf("%.1f", r.mbps), "bytes", r.bytes)
	}
}

// rxBytes is how many bytes an interface has received.
func rxBytes(iface string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", "rx_bytes"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// throughputTiming is the test's schedule and caps, from the pushed
// scoring parameters.
type throughputTiming struct {
	every, jitter, maxDur time.Duration
	maxBytes              int64
}

func throughputTimingFrom(sc protocol.Scoring) throughputTiming {
	def := protocol.DefaultScoring
	pick := func(v, d int) time.Duration {
		if v <= 0 {
			v = d
		}
		return time.Duration(v) * time.Second
	}
	tm := throughputTiming{
		every:    pick(sc.ThroughputEverySec, def.ThroughputEverySec),
		jitter:   pick(sc.ThroughputJitterSec, def.ThroughputJitterSec),
		maxDur:   pick(sc.ThroughputMaxSec, def.ThroughputMaxSec),
		maxBytes: sc.ThroughputMaxBytes,
	}
	if tm.maxBytes <= 0 {
		tm.maxBytes = def.ThroughputMaxBytes
	}
	tm.jitter = min(tm.jitter, tm.every)
	return tm
}

// throughput is one Link's figure and the schedule of its tests.
type throughput struct {
	mbps float64 // 0 until the first test
	// at and transport are when the figure was taken, by a test or a peak
	// of real traffic, and the transport that carried it.
	at        time.Time
	transport protocol.Transport
	next      time.Time // the next test; zero until the Link is first seen Up
	iface     string    // whose counter samples holds
	samples   []rxSample
}

type rxSample struct {
	at time.Time
	rx uint64
}

// sample records the bytes the active instance has received and returns
// the rate of real traffic over the last peakWindow, once it has samples
// that far back.
func (t *throughput) sample(now time.Time, iface string, rx uint64) (float64, bool) {
	if iface != t.iface || len(t.samples) > 0 && rx < t.samples[len(t.samples)-1].rx {
		t.iface, t.samples = iface, nil // another instance, or a new counter
	}
	t.samples = append(t.samples, rxSample{now, rx})
	from := now.Add(-peakWindow)
	for len(t.samples) > 1 && !t.samples[1].at.After(from) {
		t.samples = t.samples[1:]
	}
	first := t.samples[0]
	if first.at.After(from) || !now.After(first.at) {
		return 0, false
	}
	return float64(rx-first.rx) * 8 / now.Sub(first.at).Seconds() / 1e6, true
}

// due raises the figure to a peak of real traffic, and reports whether the
// test should run now. A test that falls due while real traffic runs at or
// above the figure is skipped: that traffic is the measurement. rnd draws
// the jitter, from 0 to 1.
func (t *throughput) due(now time.Time, rate float64, haveRate bool, tm throughputTiming, rnd func() float64) bool {
	if t.next.IsZero() {
		// The first test comes at a random point within the jitter, so
		// Bridges that start together do not test an Exit together.
		t.next = now.Add(time.Duration(rnd() * float64(tm.jitter)))
	}
	peak := haveRate && t.mbps > 0 && rate >= t.mbps
	if peak {
		t.mbps = rate
	}
	if now.Before(t.next) {
		return false
	}
	t.next = now.Add(tm.every + time.Duration((2*rnd()-1)*float64(tm.jitter)))
	return !peak
}

// measured takes a test's figure, which replaces the last one even when it
// is lower. The test's own bytes are not real traffic, so the samples
// start again.
func (t *throughput) measured(mbps float64, at time.Time, tr protocol.Transport) {
	t.mbps = mbps
	t.samples = nil
	t.observed(at, tr)
}

// observed records when the figure was taken and over which transport.
func (t *throughput) observed(at time.Time, tr protocol.Transport) {
	t.at, t.transport = at, tr
}
