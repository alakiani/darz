//go:build linux

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

// loadServer serves TCP throughput tests on a Node: port 5201 counts what
// it receives (upload), and port 5202 sends as fast as the reader takes it
// (download).
type loadServer struct {
	addr string
	got  atomic.Int64
}

func serveLoad(t *testing.T, n *lab.Node) *loadServer {
	t.Helper()
	s := &loadServer{addr: n.Addr().String()}
	var up, down net.Listener
	if err := n.Do(func() (err error) {
		if up, err = net.Listen("tcp4", net.JoinHostPort(s.addr, "5201")); err != nil {
			return err
		}
		down, err = net.Listen("tcp4", net.JoinHostPort(s.addr, "5202"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { up.Close(); down.Close() })
	serve := func(ln net.Listener, handle func(net.Conn)) {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); handle(c) }()
		}
	}
	go serve(up, func(c net.Conn) { io.Copy(counter{&s.got}, c) })
	go serve(down, func(c net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	})
	return s
}

// counter is a Writer that only counts.
type counter struct{ n *atomic.Int64 }

func (c counter) Write(b []byte) (int, error) { c.n.Add(int64(len(b))); return len(b), nil }

// The span of a throughput measurement. The first warm of it is left out:
// a fresh token bucket holds a second of its rate.
const loadWarm, loadSpan = 2 * time.Second, 6 * time.Second

// load runs TCP at full speed between the phone and the load server in
// one direction, counted where it is received. It returns the goodput
// after the warm-up in bytes per second, and every byte moved.
func (p *phone) load(s *loadServer, up bool) (rate float64, moved int64) {
	p.t.Helper()
	port, n := "5202", &atomic.Int64{}
	if up {
		port, n = "5201", &s.got
	}
	var c net.Conn
	if err := p.node.Do(func() (err error) {
		c, err = (&net.Dialer{Timeout: 3 * time.Second}).Dial("tcp4", net.JoinHostPort(s.addr, port))
		return err
	}); err != nil {
		p.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			if up {
				if _, err := c.Write(buf); err != nil {
					return
				}
				continue
			}
			k, err := c.Read(buf)
			n.Add(int64(k))
			if err != nil {
				return
			}
		}
	}()
	start := n.Load()
	time.Sleep(loadWarm)
	from := n.Load()
	time.Sleep(loadSpan - loadWarm)
	to := n.Load()
	c.Close()
	<-done
	return float64(to-from) / (loadSpan - loadWarm).Seconds(), to - start
}

// TestThrottling is Quota session 4's lab test (ADR 0010): a Device whose
// User ran out with the slow setting is held near its rate in both
// directions by the Bridge, another Device on the same Bridge is not, a
// new rate or none takes effect with the next snapshot, and the throttled
// traffic still counts as Usage.
func TestThrottling(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "phone1"},
		lab.Spec{Name: "phone2"},
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
	srv := serveLoad(t, l.Node("web-ab"))
	dumpNetOnFailure(t, l, "ir1", "ab1")

	admin := adminSession(t, co)
	slow, _ := co.store.CreateUser(ctx, "maryam")
	other, _ := co.store.CreateUser(ctx, "reza")
	key1, _ := wgtypes.GeneratePrivateKey()
	key2, _ := wgtypes.GeneratePrivateKey()
	dev1, err := co.store.CreateDevice(ctx, slow.ID, "phone", key1.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}
	dev2, err := co.store.CreateDevice(ctx, other.ID, "phone", key2.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}
	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p1 := newPhone(t, l.Node("phone1"), key1, dev1.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	p2 := newPhone(t, l.Node("phone2"), key2, dev2.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	for _, p := range []*phone{p1, p2} {
		testutil.Eventually(t, 120*time.Second, "the phone reaches the site abroad", func() bool {
			_, err := p.ask("93.184.215.14:7")
			return err == nil
		})
	}

	// snapshot waits until ir1 has applied its latest snapshot, and
	// returns the phone's entry in it.
	snapshot := func(key wgtypes.Key) protocol.Device {
		t.Helper()
		snap, err := co.store.LatestSnapshot(ctx, m.ids["ir1"])
		if err != nil {
			t.Fatal(err)
		}
		testutil.Eventually(t, 30*time.Second, "ir1 applies its latest snapshot", func() bool {
			h := co.health(t, m.ids["ir1"])
			return h != nil && h.AppliedVersion >= snap.Version
		})
		var st protocol.State
		if err := json.Unmarshal(snap.Body, &st); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(st.Bridge.Devices, func(d protocol.Device) bool { return d.PublicKey == key.PublicKey().String() })
		if i < 0 {
			t.Fatalf("the phone is not in ir1's snapshot %d", snap.Version)
		}
		return st.Bridge.Devices[i]
	}
	setQuota := func(req protocol.SetQuota) {
		t.Helper()
		if status := admin.Do(t, "POST", "/v1/admin/users/"+slow.ID+"/quota", req, nil); status != http.StatusOK {
			t.Fatalf("set quota: status %d", status)
		}
	}
	usage := func() uint64 {
		u, err := co.store.Usage(ctx, dev1.ID)
		if err != nil {
			t.Fatal(err)
		}
		var total uint64
		for _, r := range u.Hourly {
			total += r.Up + r.Down
		}
		return total
	}
	mbit := func(bps float64) float64 { return bps * 8 / 1e6 }
	// near checks a throttled direction: TCP over a policer sits a little
	// under its rate (headers count, goodput does not) and never far over.
	near := func(what string, got float64, rate uint64) {
		t.Helper()
		t.Logf("%s: %.2f Mbit/s, throttled to %.2f", what, mbit(got), mbit(float64(rate)))
		if got < 0.75*float64(rate) || got > 1.1*float64(rate) {
			t.Errorf("%s: %.2f Mbit/s, want near %.2f", what, mbit(got), mbit(float64(rate)))
		}
	}
	// fast checks an unthrottled direction: well past any rate here.
	fast := func(what string, got float64, floor uint64) {
		t.Helper()
		t.Logf("%s: %.2f Mbit/s", what, mbit(got))
		if got < float64(floor) {
			t.Errorf("%s: %.2f Mbit/s, want over %.2f", what, mbit(got), mbit(float64(floor)))
		}
	}
	const rate1, rate2 = 250_000, 500_000 // 2 and 4 Mbit/s
	const floor = 4 * rate2

	// Unthrottled, both phones run at what the lab carries.
	up, _ := p1.load(srv, true)
	fast("phone1 up, unthrottled", up, floor)
	down, _ := p1.load(srv, false)
	fast("phone1 down, unthrottled", down, floor)
	if d := snapshot(key1); d.Rate != 0 {
		t.Errorf("an active Device carries rate %d", d.Rate)
	}

	// maryam has used far more than a 1 MB Quota once it is reported, and
	// is slowed at once.
	testutil.Eventually(t, 90*time.Second, "phone1's traffic reaches the Coordinator", func() bool { return usage() > 1_000_000 })
	setQuota(protocol.SetQuota{Quota: new(uint64(1_000_000)), RunningOut: protocol.RunningOutSlow, Rate: rate1})
	if d := snapshot(key1); d.Rate != rate1 {
		t.Fatalf("throttled, the phone's snapshot entry carries rate %d, want %d", d.Rate, rate1)
	}
	before := usage()
	var throttled int64
	up, n := p1.load(srv, true)
	throttled += n
	near("phone1 up, throttled", up, rate1)
	down, n = p1.load(srv, false)
	throttled += n
	near("phone1 down, throttled", down, rate1)

	// reza's phone on the same Bridge is not slowed.
	if d := snapshot(key2); d.Rate != 0 {
		t.Errorf("the other phone carries rate %d", d.Rate)
	}
	up, _ = p2.load(srv, true)
	fast("phone2 up, beside a throttled phone", up, floor)
	down, _ = p2.load(srv, false)
	fast("phone2 down, beside a throttled phone", down, floor)

	// A new rate takes effect with the next snapshot.
	setQuota(protocol.SetQuota{Quota: new(uint64(1_000_000)), RunningOut: protocol.RunningOutSlow, Rate: rate2})
	if d := snapshot(key1); d.Rate != rate2 {
		t.Fatalf("after changing the rate, the phone carries %d, want %d", d.Rate, rate2)
	}
	up, n = p1.load(srv, true)
	throttled += n
	near("phone1 up, new rate", up, rate2)
	down, n = p1.load(srv, false)
	throttled += n
	near("phone1 down, new rate", down, rate2)

	// Throttled traffic still counts as Usage.
	testutil.Eventually(t, 90*time.Second, "the throttled traffic appears as Usage", func() bool {
		return usage()-before >= uint64(throttled)
	})
	t.Logf("Usage grew by %d bytes while throttled; the phone moved %d", usage()-before, throttled)

	// No rate: an unlimited Quota frees the phone with the next snapshot.
	setQuota(protocol.SetQuota{RunningOut: protocol.RunningOutBlock})
	if d := snapshot(key1); d.Rate != 0 {
		t.Fatalf("unthrottled, the phone still carries rate %d", d.Rate)
	}
	up, _ = p1.load(srv, true)
	fast("phone1 up, rate removed", up, floor)
	down, _ = p1.load(srv, false)
	fast("phone1 down, rate removed", down, floor)
}
