//go:build linux

package e2e

import (
	"context"
	"maps"
	"net"
	"slices"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

// discard drops every UDP datagram sent to a port.
func discard(t *testing.T, n *lab.Node, port string) {
	t.Helper()
	var conn net.PacketConn
	if err := n.Do(func() (err error) {
		conn, err = net.ListenPacket("udp4", net.JoinHostPort(n.Addr().String(), port))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := conn.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
}

// send sends count datagrams of size bytes from the phone to target.
func (p *phone) send(target string, count, size int) {
	p.t.Helper()
	var c net.Conn
	if err := p.node.Do(func() (err error) { c, err = net.Dial("udp4", target); return err }); err != nil {
		p.t.Fatal(err)
	}
	defer c.Close()
	payload := make([]byte, size)
	for i := 0; i < count; i++ {
		if _, err := c.Write(payload); err != nil {
			p.t.Fatal(err)
		}
		if i%50 == 49 {
			time.Sleep(5 * time.Millisecond) // no bursts beyond what the tunnel queues
		}
	}
}

// TestMetering is session 8's "proves in the lab" test: lab traffic appears
// as usage per Device per Exit, and a Coordinator outage during traffic
// loses no bytes, even across an Agent restart.
func TestMetering(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ab1"},
		lab.Spec{Name: "ab2"},
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
	m.startAgent("ab2", []protocol.Role{protocol.RoleExit}, nil)
	bridgePub, _ := co.c.BridgePublicKey(ctx)
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p := newPhone(t, l.Node("phone1"), key, dev.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)

	var exit string
	testutil.Eventually(t, 120*time.Second, "the phone reaches the site abroad", func() bool {
		got, err := p.ask("93.184.215.14:7")
		exit = got
		return err == nil
	})
	exitID := map[string]string{l.Node("ab1").Addr().String(): m.ids["ab1"], l.Node("ab2").Addr().String(): m.ids["ab2"]}[exit]

	// usage is what the Coordinator holds for the phone: bytes up per Exit.
	usage := func() (map[string]uint64, uint64) {
		u, err := co.store.Usage(ctx, dev.ID)
		if err != nil {
			t.Fatal(err)
		}
		per, total := map[string]uint64{}, uint64(0)
		for _, r := range u.Hourly {
			per[r.Exit] += r.Up
			total += r.Up
		}
		return per, total
	}
	const count, size = 1000, 1000
	const wire = count * (size + 28) // IPv4 and UDP headers: what the counters see
	const slack = 20_000             // the whoami probes, and a little more

	// Traffic appears as usage per Device per Exit.
	_, before := usage()
	p.send("93.184.215.14:9", count, size)
	testutil.Eventually(t, 60*time.Second, "the bytes reach the Coordinator", func() bool {
		_, total := usage()
		return total-before >= wire
	})
	per, first := usage()
	if first-before > wire+slack {
		t.Errorf("usage grew by %d, want about %d", first-before, wire)
	}
	if exits := slices.Collect(maps.Keys(per)); len(exits) != 1 || exits[0] != exitID {
		t.Errorf("usage by Exit = %v, want all through %s (%s)", per, exitID, exit)
	}

	// A Coordinator outage during traffic, with an Agent restart in it,
	// loses nothing and counts nothing twice.
	cut := l.Filter(lab.DropAll(l.Node("coord")))
	p.send("93.184.215.14:9", count, size)
	time.Sleep(12 * time.Second) // past a counter reading
	m.agents["ir1"].restart()
	time.Sleep(5 * time.Second)
	if _, during := usage(); during != first {
		t.Errorf("usage changed during the outage: %d -> %d", first, during)
	}
	cut.Remove()
	testutil.Eventually(t, 60*time.Second, "the outage's bytes arrive afterwards", func() bool {
		_, total := usage()
		return total-first >= wire
	})
	time.Sleep(3 * time.Second)
	if _, after := usage(); after-first > wire+slack {
		t.Errorf("after the outage usage grew by %d, want about %d: counted twice?", after-first, wire)
	}
}
