package link

import (
	"strings"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/metrics"
	"github.com/alakiani/darz/internal/protocol"
)

// Each instance's state, and the throughput figure the weight uses with
// when and over what it was taken, come from the Link state the Manager
// keeps (issue #51).
func TestLinkMetrics(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	inst := func(id uint64, tr protocol.Transport, up bool) *instance {
		return &instance{spec: protocol.Instance{ID: id, Transport: tr}, up: up}
	}
	m := &Manager{self: "ir1",
		links: map[uint64]*linkState{
			4: {id: 4, peer: protocol.Peer{Name: "aws"}, order: []uint64{10, 11, 12}, active: 11},
			// Every instance down at once: a blackout, not one transport failing.
			5: {id: 5, peer: protocol.Peer{Name: "lightnode"}, order: []uint64{20, 21}},
		},
		insts: map[uint64]*instance{
			10: inst(10, protocol.TransportGRE, false),
			11: inst(11, protocol.TransportAmneziaWG, true),
			12: inst(12, protocol.TransportWireGuard, true),
			20: inst(20, protocol.TransportGRE, false),
			21: inst(21, protocol.TransportAmneziaWG, false),
		},
		tputs: map[uint64]*throughput{4: {}, 5: {}},
	}
	m.tputs[4].measured(5.4, at, protocol.TransportAmneziaWG)

	var w metrics.Writer
	m.WriteMetrics(&w)
	got := string(w.Bytes())
	aws, ln := `link_id="4",link="aws/ir1",peer="aws"`, `link_id="5",link="ir1/lightnode",peer="lightnode"`
	for _, want := range []string{
		`darz_link_instance_up{` + aws + `,instance_id="10",transport="gre"} 0`,
		`darz_link_instance_up{` + aws + `,instance_id="11",transport="amneziawg"} 1`,
		`darz_link_instance_up{` + aws + `,instance_id="12",transport="wireguard"} 1`,
		`darz_link_instance_up{` + ln + `,instance_id="20",transport="gre"} 0`,
		`darz_link_instance_up{` + ln + `,instance_id="21",transport="amneziawg"} 0`,
		`darz_link_throughput_mbps{` + aws + `} 5.4`,
		`darz_link_throughput_measured_timestamp_seconds{` + aws + `} 1.7923158e+09`,
		`darz_link_throughput_transport{` + aws + `,transport="amneziawg"} 1`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("metrics lack %s:\n%s", want, got)
		}
	}
	// No figure yet on the other Link: no throughput series for it.
	if strings.Contains(got, "darz_link_throughput_mbps{"+ln) || strings.Contains(got, "darz_link_throughput_transport{"+ln) {
		t.Errorf("a throughput figure for an unmeasured Link:\n%s", got)
	}
}

// A peak of real traffic raises the figure, and is when and over what it
// was taken; a later test replaces both.
func TestThroughputFigureRecordsWhenAndOverWhat(t *testing.T) {
	tm := throughputTiming{every: time.Hour, jitter: time.Minute}
	var tp throughput
	tp.measured(10, t0, protocol.TransportGRE)
	tp.next = t0.Add(time.Hour)
	if tp.due(t0.Add(time.Minute), 30, true, tm, func() float64 { return 0.5 }) || tp.mbps != 30 {
		t.Fatalf("figure %v after a 30 Mbit/s peak", tp.mbps)
	}
	tp.observed(t0.Add(time.Minute), protocol.TransportAmneziaWG)
	if tp.at != t0.Add(time.Minute) || tp.transport != protocol.TransportAmneziaWG {
		t.Errorf("after the peak: taken %v over %s", tp.at, tp.transport)
	}
	tp.measured(8, t0.Add(2*time.Hour), protocol.TransportWireGuard)
	if tp.mbps != 8 || tp.at != t0.Add(2*time.Hour) || tp.transport != protocol.TransportWireGuard {
		t.Errorf("after a test: %v taken %v over %s", tp.mbps, tp.at, tp.transport)
	}
}
