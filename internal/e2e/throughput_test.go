//go:build linux

package e2e

import (
	"context"
	"fmt"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
)

// TestThroughputWeights is session 12's "proves in the lab" test: bucket
// shares follow capacity differences injected with netem. One Bridge has
// three abroad Exits whose uplinks are rate-limited; the default Rule's 64
// buckets split in proportion to what each Exit can carry, an Exit under
// 25% of the best gets none, and the shares follow when capacities change.
func TestThroughputWeights(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "ab2"},
		lab.Spec{Name: "ab3"},
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	sc := protocol.DefaultScoring
	// Shortened so the test runs in a minute or two, not hours: the
	// startup, when Links come up one by one, ages out of availability
	// within 30 s; a share more than 2 buckets off its target moves one
	// bucket a second; each Link is tested every 10 s or so.
	sc.AvailabilityWindowSec, sc.FlapWindowSec = 30, 30
	sc.RebalanceThresholdShare, sc.RebalanceAfterSec, sc.RebalanceEverySec = 0.03, 3, 1
	sc.ThroughputEverySec, sc.ThroughputJitterSec = 10, 2
	co.c.Scoring = sc
	// ir1's metrics, read as Prometheus would (issue #51).
	const port = 9443
	co.c.Monitor.Port, co.c.Monitor.AllowFrom = port, []string{l.Node("coord").Addr().String()}
	scrape := newScraper(t, co, l.Node("coord"), port)
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	dumpNetOnFailure(t, l, "ir1", "ab1", "ab2", "ab3")
	co.c.FeedURL, co.c.RIPEstatURL = serveFeed(t, irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8")), ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}

	// Capacities from each Exit towards the Bridge, the direction that
	// matters to Users. ab3 is under 25% of either other Exit.
	rate := func(name string, mbit uint64) {
		l.Netem(l.Node(name), lab.Impairment{Rate: mbit * 1_000_000})
	}
	rate("ab1", 40)
	rate("ab2", 20)
	rate("ab3", 4)

	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{},
		agentArgs: []string{"-node-exporter", ""}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	for _, ab := range []string{"ab1", "ab2", "ab3"} {
		m.startAgent(ab, []protocol.Role{protocol.RoleExit}, nil)
	}
	const defaultRule = 2
	key := map[string]string{}
	for _, ab := range []string{"ab1", "ab2", "ab3"} {
		key[ab] = fmt.Sprintf("link:%d", m.linkID("ir1", ab))
	}

	// shares waits for the default Rule's buckets to settle inside the
	// bands, and logs what ir1 measured.
	shares := func(what string, timeout time.Duration, bands map[string][2]int) {
		t.Helper()
		var last map[string]int
		inBand := func() bool {
			rh := m.ruleHealth("ir1", defaultRule)
			last = rh.Shares
			if rh.Mode != "direct" {
				return false
			}
			for ab, band := range bands {
				if n := rh.Shares[key[ab]]; n < band[0] || n > band[1] {
					return false
				}
			}
			return true
		}
		// Settled means inside the bands, with no bucket moving, for 5 s
		// running: a share passing through a band on its way elsewhere
		// does not count.
		ok := false
		var since time.Time
		var held map[string]int
		for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			if !inBand() {
				since = time.Time{}
				continue
			}
			if since.IsZero() || !maps.Equal(held, last) {
				since, held = time.Now(), last
			}
			if time.Since(since) >= 5*time.Second {
				ok = true
				break
			}
		}
		var figures []string
		for _, ab := range []string{"ab1", "ab2", "ab3"} {
			lh := m.link("ir1", m.linkID("ir1", ab))
			figures = append(figures, fmt.Sprintf("%s %.1f Mbps %d buckets", ab, lh.ThroughputMbps, last[key[ab]]))
		}
		t.Logf("%s: %v", what, figures)
		if !ok {
			t.Fatalf("%s: shares %v never settled within %v of %v", what, last, timeout, bands)
		}
	}

	// weighs waits for ir1's Path metrics to agree with its buckets: a
	// candidate marked cut off has none, the others hold them in
	// proportion to their weights, and each target share is its weight
	// over the eligible candidates' total.
	weighs := func(what string, cut map[string]bool) {
		t.Helper()
		lbl := func(ab string) string { return `{rule_id="2",rule="Default",default="true",candidate="` + ab + `"}` }
		check := func() (string, error) {
			s, err := scrape.samples(l.Node("ir1"))
			if err != nil {
				return "", err
			}
			var total, kept float64
			var got []string
			for _, ab := range []string{"ab1", "ab2", "ab3"} {
				w, okW := s["darz_path_weight"+lbl(ab)]
				c, okC := s["darz_path_cut_off"+lbl(ab)]
				if !okW || !okC {
					return "", fmt.Errorf("no weight or cutoff for %s", ab)
				}
				if (c == 1) != cut[ab] {
					return "", fmt.Errorf("%s cut off = %v, want %v", ab, c, cut[ab])
				}
				total += w
				if c == 0 {
					kept += w
				}
				got = append(got, fmt.Sprintf("%s weight %.1f share %.2f cut %v buckets %v", ab, w,
					s["darz_path_target_share"+lbl(ab)], c, s["darz_path_buckets"+lbl(ab)]))
			}
			for _, ab := range []string{"ab1", "ab2", "ab3"} {
				w, b := s["darz_path_weight"+lbl(ab)], s["darz_path_buckets"+lbl(ab)]
				if share := s["darz_path_target_share"+lbl(ab)]; math.Abs(share-w/total) > 1e-9 {
					return "", fmt.Errorf("%s target share %v, want %v", ab, share, w/total)
				}
				want := 0.0
				if !cut[ab] {
					want = 64 * w / kept
				}
				// Rebalancing stops within 3% of the target, about 2 buckets.
				if math.Abs(b-want) > 3 {
					return "", fmt.Errorf("%s has %v buckets, want %.1f by weight", ab, b, want)
				}
			}
			return strings.Join(got, "; "), nil
		}
		var err error
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
			var got string
			if got, err = check(); err == nil {
				t.Logf("%s: %s", what, got)
				return
			}
		}
		t.Fatalf("%s: the Path metrics disagree with the buckets: %v", what, err)
	}

	// 40 : 20 is 43 : 21 buckets; ab3 is cut off.
	shares("40/20/4 Mbit", 180*time.Second, map[string][2]int{"ab1": {36, 50}, "ab2": {14, 28}, "ab3": {0, 0}})
	weighs("40/20/4 Mbit", map[string]bool{"ab3": true})

	// ab1 loses capacity: 10 : 20 is 21 : 43, and ab3 is still cut off.
	rate("ab1", 10)
	shares("10/20/4 Mbit", 120*time.Second, map[string][2]int{"ab1": {14, 28}, "ab2": {36, 50}, "ab3": {0, 0}})
	weighs("10/20/4 Mbit", map[string]bool{"ab3": true})

	// ab3 gains capacity and is no longer cut off: 10 : 20 : 20 is about
	// 13 : 25.5 : 25.5.
	rate("ab3", 20)
	shares("10/20/20 Mbit", 120*time.Second, map[string][2]int{"ab1": {7, 19}, "ab2": {18, 33}, "ab3": {18, 33}})
	weighs("10/20/20 Mbit", nil)
}
