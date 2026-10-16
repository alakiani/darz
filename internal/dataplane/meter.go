package dataplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// Metering on a Bridge (ADR 0002, ADR 0003): nftables counts bytes per
// Device address per candidate mark; the meter turns counter readings into
// bytes per Device per Exit per hour, keeps those buckets on disk, and
// hands them to health reports until the Coordinator has them. Only this
// Bridge's own Devices are counted, by the candidate it chose, so traffic
// in transit is counted once, at the Bridge the Device is on.

const (
	// connection marks of Device traffic carry this bit, so they never
	// look like a Link's return mark.
	deviceConnBit = 0x10000
	// maxBucketAge is how long an unacknowledged bucket is kept.
	maxBucketAge = 30 * 24 * time.Hour
)

// counterElem is one element of a counting set.
type counterElem struct {
	addr  string
	mark  uint32
	bytes uint64
}

type cell struct {
	Up   uint64 `json:"up"`
	Down uint64 `json:"down"`
}

// meter keeps hourly buckets: hour -> "address|exit" -> bytes.
type meter struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	state meterState
	total uint64 // bytes counted since the Agent started
}

// meterState is what the meter keeps on disk. The last readings are kept
// too: the counters outlive an Agent restart, and a restarted Agent must
// not count them again.
type meterState struct {
	Buckets map[int64]map[string]*cell `json:"buckets"`
	Last    map[string]uint64          `json:"last"` // counter bytes at the last reading, by "set|address|mark"
}

func openMeter(path string, now func() time.Time) (*meter, error) {
	m := &meter{path: path, now: now}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &m.state); err != nil {
			return nil, err
		}
	}
	if m.state.Buckets == nil {
		m.state.Buckets = map[int64]map[string]*cell{}
	}
	if m.state.Last == nil {
		m.state.Last = map[string]uint64{}
	}
	return m, nil
}

// collect reads the counting sets and adds what moved since the last
// reading to the current hour's buckets. exitOf names the Exit a candidate
// mark leads to, or "" for marks that are not usage.
func (m *meter) collect(read func(set string) ([]counterElem, error), exitOf func(mark uint32) string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hour := m.now().UTC().Truncate(time.Hour).Unix()
	for _, set := range []string{"usage_up", "usage_down"} {
		elems, err := read(set)
		if err != nil {
			return err
		}
		for _, e := range elems {
			k := fmt.Sprintf("%s|%s|%d", set, e.addr, e.mark)
			delta := e.bytes
			if prev, ok := m.state.Last[k]; ok && e.bytes >= prev {
				delta = e.bytes - prev
			}
			m.state.Last[k] = e.bytes
			mark := e.mark
			if set == "usage_down" {
				mark &^= deviceConnBit
			}
			exit := exitOf(mark)
			if delta == 0 || exit == "" {
				continue
			}
			m.total += delta
			if m.state.Buckets[hour] == nil {
				m.state.Buckets[hour] = map[string]*cell{}
			}
			key := e.addr + "|" + exit
			c := m.state.Buckets[hour][key]
			if c == nil {
				c = &cell{}
				m.state.Buckets[hour][key] = c
			}
			if set == "usage_up" {
				c.Up += delta
			} else {
				c.Down += delta
			}
		}
	}
	m.forget()
	return m.save()
}

// metered is every byte counted since the Agent started.
func (m *meter) metered() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}

// reset forgets the last readings: the counters restart from zero after a
// ruleset reload. Call collect just before the reload.
func (m *meter) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.state.Last)
	m.save()
}

// forget drops buckets older than maxBucketAge. Call with mu held.
func (m *meter) forget() {
	oldest := m.now().Add(-maxBucketAge).Unix()
	for h := range m.state.Buckets {
		if h < oldest {
			delete(m.state.Buckets, h)
		}
	}
}

func (m *meter) save() error {
	b, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// report returns up to max buckets, oldest first, the open hour included.
func (m *meter) report(max int) []protocol.UsageBucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	hours := make([]int64, 0, len(m.state.Buckets))
	for h := range m.state.Buckets {
		hours = append(hours, h)
	}
	slices.Sort(hours)
	var out []protocol.UsageBucket
	for _, h := range hours {
		keys := make([]string, 0, len(m.state.Buckets[h]))
		for k := range m.state.Buckets[h] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(out) == max {
				return out
			}
			addr, exit, _ := strings.Cut(k, "|")
			c := m.state.Buckets[h][k]
			out = append(out, protocol.UsageBucket{Hour: h, Device: addr, Exit: exit, Up: c.Up, Down: c.Down})
		}
	}
	return out
}

// acked drops the closed-hour buckets the Coordinator now has. The open
// hour stays: it is sent again, larger, until it closes.
func (m *meter) acked(sent []protocol.UsageBucket) {
	m.mu.Lock()
	defer m.mu.Unlock()
	open := m.now().UTC().Truncate(time.Hour).Unix()
	for _, b := range sent {
		if b.Hour >= open {
			continue
		}
		delete(m.state.Buckets[b.Hour], b.Device+"|"+b.Exit)
		if len(m.state.Buckets[b.Hour]) == 0 {
			delete(m.state.Buckets, b.Hour)
		}
	}
	m.save()
}
