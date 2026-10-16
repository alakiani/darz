package link

import (
	"math/rand/v2"
	"time"
)

// ADR 0007's definition of Up.
const (
	lossWindow     = 30 * time.Second
	maxLoss        = 0.10
	maxSuccessAge  = 10 * time.Second
	sessionTimeout = 3 * time.Minute // a WireGuard session without a fresh handshake is dead
)

// meter keeps the probe results of one instance over the loss window.
type meter struct {
	results     []probeResult
	lastSuccess time.Time
}

type probeResult struct {
	at  time.Time
	ok  bool
	rtt time.Duration
}

func (m *meter) record(at time.Time, ok bool, rtt time.Duration) {
	m.results = append(m.results, probeResult{at, ok, rtt})
	if ok {
		m.lastSuccess = at
	}
	m.trim(at)
}

func (m *meter) trim(now time.Time) {
	i := 0
	for i < len(m.results) && now.Sub(m.results[i].at) >= lossWindow {
		i++
	}
	m.results = m.results[i:]
}

// loss is the fraction of probes in the window that went unanswered.
func (m *meter) loss(now time.Time) float64 {
	m.trim(now)
	if len(m.results) == 0 {
		return 1
	}
	lost := 0
	for _, r := range m.results {
		if !r.ok {
			lost++
		}
	}
	return float64(lost) / float64(len(m.results))
}

// rtt is the mean round trip of answered probes in the window.
func (m *meter) rtt(now time.Time) time.Duration {
	m.trim(now)
	var sum time.Duration
	n := 0
	for _, r := range m.results {
		if r.ok {
			sum += r.rtt
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / time.Duration(n)
}

// up reports whether the instance is Up: a live handshake, a probe answered
// in the last 10 s, and under 10% loss over the last 30 s.
func (m *meter) up(now, lastHandshake time.Time) bool {
	return !lastHandshake.IsZero() && now.Sub(lastHandshake) < sessionTimeout &&
		!m.lastSuccess.IsZero() && now.Sub(m.lastSuccess) < maxSuccessAge &&
		m.loss(now) < maxLoss
}

// candidate is one instance as the chooser sees it, in preference order.
type candidate struct {
	id      uint64
	up      bool
	upSince time.Time
}

// choose returns the instance that should carry the Link, or 0 if none is
// Up. A Link leaves its active instance as soon as it stops being Up, and
// returns to a more preferred one only after it has been Up for switchBack.
func choose(now time.Time, active uint64, cands []candidate, switchBack time.Duration) uint64 {
	activeUp := false
	for _, c := range cands {
		if c.id == active && c.up {
			activeUp = true
		}
	}
	if !activeUp {
		for _, c := range cands {
			if c.up {
				return c.id
			}
		}
		return 0
	}
	for _, c := range cands {
		if c.id == active {
			return active
		}
		if c.up && now.Sub(c.upSince) >= switchBack {
			return c.id
		}
	}
	return active
}

// backoff spaces handshake retries of an instance that is not Up:
// exponential from min to max, with ±20% jitter.
type backoff struct {
	min, max time.Duration
	n        int
}

func (b *backoff) next() time.Duration {
	d := b.min
	for i := 0; i < b.n && d < b.max; i++ {
		d *= 2
	}
	d = min(d, b.max)
	b.n++
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func (b *backoff) reset() { b.n = 0 }
