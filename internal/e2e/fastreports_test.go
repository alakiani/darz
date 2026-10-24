//go:build linux

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

// healthTap sees every health POST that reaches the Coordinator, and can
// answer some with 503, as a failing Coordinator would.
type healthTap struct {
	mu      sync.Mutex
	fail    map[string]int // source address -> POSTs still to fail
	reports []tappedReport
}

// tappedReport is one health POST as it reached the Coordinator.
type tappedReport struct {
	at     time.Time
	from   string // the Agent's source address
	failed bool   // answered 503 by the tap
	status int    // the Coordinator's answer
	health protocol.Health
}

// statusWriter keeps the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (tp *healthTap) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		rep := tappedReport{at: time.Now()}
		rep.from, _, _ = net.SplitHostPort(r.RemoteAddr)
		json.Unmarshal(body, &rep.health)
		tp.mu.Lock()
		if tp.fail[rep.from] > 0 {
			tp.fail[rep.from]--
			rep.failed = true
		}
		i := len(tp.reports)
		tp.reports = append(tp.reports, rep)
		tp.mu.Unlock()
		if rep.failed {
			http.Error(w, "failing on purpose", http.StatusServiceUnavailable)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		tp.mu.Lock()
		tp.reports[i].status = sw.status
		tp.mu.Unlock()
	})
}

// failNext answers the next n health POSTs from a source address with 503.
func (tp *healthTap) failNext(from string, n int) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.fail == nil {
		tp.fail = map[string]int{}
	}
	tp.fail[from] += n
}

// from returns the health POSTs seen from a source address, oldest first.
func (tp *healthTap) from(addr string) []tappedReport {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var out []tappedReport
	for _, r := range tp.reports {
		if r.from == addr {
			out = append(out, r)
		}
	}
	return out
}

// stateDir is where a lab Agent keeps its state.
func (a *agentProc) stateDir() string {
	i := slices.Index(a.args, "-state-dir")
	if i < 0 {
		a.t.Fatal("agent has no -state-dir")
	}
	return a.args[i+1]
}

// TestFastUsageReports is Quota session 5's lab test (ADR 0010, Enforcement
// and its timing): a Bridge reads its counters when it builds a report,
// retries a failed report after 5 s, and sends the open hour before any
// backlog.
func TestFastUsageReports(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "phone1"},
		lab.At("web-ab", "93.184.215.14"),
		lab.At("dns-ab", "9.9.9.9"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	co.c.FeedURL, co.c.RIPEstatURL = serveFeed(t, irFeed("2.144.0.0/14", "5.0.0.0/8")), ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}
	whoami(t, l.Node("web-ab"), 7)
	discard(t, l.Node("web-ab"), "9")
	dumpNetOnFailure(t, l, "ir1", "ab1")

	user, _ := co.store.CreateUser(ctx, "maryam")
	key, _ := wgtypes.GeneratePrivateKey()
	dev, err := co.store.CreateDevice(ctx, user.ID, "phone", key.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p := newPhone(t, l.Node("phone1"), key, dev.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	testutil.Eventually(t, 120*time.Second, "the phone reaches the site abroad", func() bool {
		_, err := p.ask("93.184.215.14:7")
		return err == nil
	})
	bridge := l.Node("ir1").Addr().String()
	exitID := m.ids["ab1"]

	// usage is every byte up the Coordinator holds for the phone.
	usage := func() uint64 {
		u, err := co.store.Usage(ctx, dev.ID)
		if err != nil {
			t.Fatal(err)
		}
		var total uint64
		for _, r := range u.Monthly {
			total += r.Up
		}
		return total
	}
	const count, size = 1000, 1000
	const wire = count * (size + 28) // IPv4 and UDP headers: what the counters see
	const slack = 20_000             // the whoami probes, and a little more

	t.Run("bytes moved just before a report are in it", func(t *testing.T) {
		// The Bridge reports every second (the lab's interval). A first
		// burst shows when the Bridge last read its counters on its
		// 10 s ticker; bytes sent right after must not wait for the next.
		before := usage()
		p.send("93.184.215.14:9", 100, size)
		testutil.Eventually(t, 30*time.Second, "the first burst reaches the Coordinator", func() bool {
			return usage()-before >= 100*(size+28)
		})
		before = usage()
		p.send("93.184.215.14:9", count, size)
		testutil.Eventually(t, 4*time.Second, "the bytes are in the next reports, not 10 s later", func() bool {
			return usage()-before >= wire
		})
	})

	// From here the Bridge reports every 40 s, as slowly as a retry or a
	// backlog would show.
	ag := m.agents["ir1"]
	ag.args = append(ag.args, "-health-interval", "40s")
	ag.restart()
	settled := func() {
		t.Helper()
		n := len(co.tap.from(bridge))
		testutil.Eventually(t, 60*time.Second, "the restarted Bridge reports", func() bool {
			return len(co.tap.from(bridge)) > n
		})
		time.Sleep(5 * time.Second) // past the confirm of its first snapshot
	}
	settled()

	t.Run("a failed report is retried after about 5 s", func(t *testing.T) {
		n := len(co.tap.from(bridge))
		co.tap.failNext(bridge, 1)
		var failed tappedReport
		testutil.Eventually(t, 60*time.Second, "a report fails", func() bool {
			rs := co.tap.from(bridge)
			for _, r := range rs[n:] {
				if r.failed {
					failed = r
					return true
				}
			}
			return false
		})
		var retry tappedReport
		testutil.Eventually(t, 20*time.Second, "the failed report is retried", func() bool {
			for _, r := range co.tap.from(bridge) {
				if r.at.After(failed.at) && !r.failed {
					retry = r
					return true
				}
			}
			return false
		})
		if d := retry.at.Sub(failed.at); d < 4*time.Second || d > 8*time.Second {
			t.Errorf("retried after %v, want about 5 s", d.Round(100*time.Millisecond))
		}
	})

	t.Run("after an outage the open hour comes first and nothing is lost", func(t *testing.T) {
		before := usage()
		cut := l.Filter(lab.DropAll(l.Node("coord")))
		defer cut.Remove()

		// A long backlog: 600 closed hours of the phone's usage, more than
		// one report carries, kept by the Bridge while the Coordinator was
		// out of reach.
		const backlog, perHour = 600, 1000
		ag.stop()
		path := filepath.Join(ag.stateDir(), "usage.json")
		var st struct {
			Buckets map[int64]map[string]*struct {
				Up   uint64 `json:"up"`
				Down uint64 `json:"down"`
			} `json:"buckets"`
			Last map[string]uint64 `json:"last"`
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
		open := time.Now().UTC().Truncate(time.Hour)
		key := dev.Address.String() + "|" + exitID
		for i := range backlog {
			// Two days back and older: hours the Coordinator has never seen.
			h := open.Add(-time.Duration(48+i) * time.Hour).Unix()
			if st.Buckets[h] == nil {
				st.Buckets[h] = map[string]*struct {
					Up   uint64 `json:"up"`
					Down uint64 `json:"down"`
				}{}
			}
			st.Buckets[h][key] = &struct {
				Up   uint64 `json:"up"`
				Down uint64 `json:"down"`
			}{Up: perHour}
		}
		if b, err = json.Marshal(st); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		ag.start()

		p.send("93.184.215.14:9", count, size)
		sentHour := time.Now().UTC().Truncate(time.Hour).Unix()
		time.Sleep(15 * time.Second) // past any report built before the traffic
		n := len(co.tap.from(bridge))
		cut.Remove()

		var first tappedReport
		testutil.Eventually(t, 60*time.Second, "the Bridge reports again", func() bool {
			rs := co.tap.from(bridge)
			if len(rs) > n {
				first = rs[n]
				return true
			}
			return false
		})
		if len(first.health.Usage) == 0 {
			t.Fatal("the first report after the outage carries no usage")
		}
		if first.health.Time.UTC().Truncate(time.Hour).Unix() != sentHour {
			t.Log("the hour turned during the outage; not checking which hour came first")
		} else {
			i := slices.IndexFunc(first.health.Usage, func(u protocol.UsageBucket) bool {
				return u.Hour == sentHour && u.Device == dev.Address.String() && u.Exit == exitID
			})
			if i < 0 {
				t.Errorf("the first report after the outage lacks the open hour; it starts at hour %d, the open hour is %d",
					first.health.Usage[0].Hour, sentHour)
			} else if first.health.Usage[i].Up < wire {
				t.Errorf("the open hour in the first report has %d bytes up, want at least %d", first.health.Usage[i].Up, wire)
			}
		}

		want := before + backlog*perHour + wire
		deadline := time.Now().Add(120 * time.Second)
		for usage() < want && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if got := usage(); got < want {
			for _, r := range co.tap.from(bridge)[n:] {
				var old, oldBytes int
				for _, u := range r.health.Usage {
					if u.Hour < sentHour {
						old++
						oldBytes += int(u.Up)
					}
				}
				t.Logf("report at %s: status %d, failed %v, %d buckets, %d before the open hour (%d bytes up)",
					r.at.Format("15:04:05.000"), r.status, r.failed, len(r.health.Usage), old, oldBytes)
			}
			if b, err := os.ReadFile(path); err == nil {
				t.Logf("the Bridge's usage.json is %d bytes", len(b))
			}
			t.Fatalf("timed out waiting for the whole backlog: usage is %d, want %d (before %d)", got, want, before)
		}
		time.Sleep(3 * time.Second)
		if got := usage(); got > want+slack {
			t.Errorf("usage is %d, want about %d: counted twice?", got, want)
		}
	})
}
