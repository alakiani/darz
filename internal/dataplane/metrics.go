package dataplane

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alakiani/darz/internal/metrics"
)

// activeWithin is how recent a Device's handshake must be for it to count
// as active (ADR 0006).
const activeWithin = 3 * time.Minute

// countMoves counts one step's bucket moves of a Rule. Call with mu held.
func (p *Plane) countMoves(rule string, moves []move) {
	kinds := map[string]bool{}
	for _, m := range moves {
		p.movedBuckets[[2]string{rule, m.kind}]++
		kinds[m.kind] = true
	}
	for k := range kinds {
		p.moves[[2]string{rule, k}]++
	}
}

// WriteMetrics implements agent.MetricsWriter: bucket moves, and on a
// Bridge its active Devices, as a count only, and the bytes through its
// Device interface beside the bytes metered.
func (p *Plane) WriteMetrics(w *metrics.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range sortedPairs(p.moves) {
		w.Counter("darz_path_moves_total", "Steps that moved buckets of a Rule's Device traffic, by kind; Path flapping counts failures.",
			float64(p.moves[k]), "rule", k[0], "kind", k[1])
	}
	for _, k := range sortedPairs(p.movedBuckets) {
		w.Counter("darz_path_bucket_moves_total", "Buckets of a Rule's Device traffic moved, by kind.",
			float64(p.movedBuckets[k]), "rule", k[0], "kind", k[1])
	}
	if p.st == nil || p.st.Bridge == nil {
		return
	}
	if dev, err := p.wg.Device(DeviceIface); err == nil {
		active := 0
		for _, peer := range dev.Peers {
			if time.Since(peer.LastHandshakeTime) < activeWithin {
				active++
			}
		}
		w.Gauge("darz_bridge_active_devices", "Devices with a handshake in the last 3 minutes.", float64(active))
	}
	if n, ok := ifaceBytes(DeviceIface); ok {
		w.Counter("darz_bridge_interface_bytes_total", "Bytes through the Device interface, both ways.", float64(n))
	}
	w.Counter("darz_metered_bytes_total", "Bytes metered since the Agent started, both ways.", float64(p.meter.metered()))
}

// ifaceBytes is an interface's received plus sent bytes.
func ifaceBytes(iface string) (uint64, bool) {
	var total uint64
	for _, f := range []string{"rx_bytes", "tx_bytes"} {
		b, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", f))
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			return 0, false
		}
		total += n
	}
	return total, true
}

func sortedPairs(m map[[2]string]uint64) [][2]string {
	return slices.SortedFunc(maps.Keys(m), func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	})
}
