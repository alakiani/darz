package link

import (
	"maps"
	"slices"
	"strconv"

	"github.com/alakiani/darz/internal/metrics"
)

// WriteMetrics implements agent.MetricsWriter: each instance's state, so
// a whole-Link blackout, every transport down together, shows apart from
// one transport failing; and, on a Bridge, each Link's throughput figure
// as the weights use it (ADR 0008), with when and over what it was taken.
// Links are labelled as the health report's darz_link_* are.
func (m *Manager) WriteMetrics(w *metrics.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(m.links)) {
		ls := m.links[id]
		labels := metrics.LinkLabels(id, m.self, ls.peer.Name)
		for _, iid := range ls.order {
			in := m.insts[iid]
			if in == nil {
				continue
			}
			in.mu.Lock()
			up := in.up
			in.mu.Unlock()
			// instance_id, as Prometheus keeps "instance" for the target.
			w.Bool("darz_link_instance_up", "1 while the instance is Up.", up,
				append(slices.Clip(labels), "instance_id", strconv.FormatUint(iid, 10), "transport", string(in.spec.Transport))...)
		}
		if tp := m.tputs[id]; tp != nil && tp.mbps > 0 {
			w.Gauge("darz_link_throughput_mbps", "What the far end can still send this Bridge, in Mbit/s: the last test, raised by peaks of real traffic.",
				tp.mbps, labels...)
			w.Gauge("darz_link_throughput_measured_timestamp_seconds", "When the Link's throughput figure was taken.",
				float64(tp.at.Unix()), labels...)
			w.Gauge("darz_link_throughput_transport", "The transport that carried the Link's throughput figure.", 1,
				append(slices.Clip(labels), "transport", string(tp.transport))...)
		}
	}
}
