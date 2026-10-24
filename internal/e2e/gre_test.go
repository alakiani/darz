//go:build linux

package e2e

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

// TestGRE is ADR 0009's "proves in the lab" test: a Link that crosses the
// filter carries traffic on plain GRE; blocking GRE but not UDP moves it to
// AmneziaWG within seconds; blocking UDP but not GRE keeps it on GRE; and a
// spoofed GRE packet whose inner source is outside the Device and mesh
// ranges is dropped at the Exit.
func TestGRE(t *testing.T) {
	binDir(t)
	for _, mod := range []string{"wireguard", "ip_gre"} {
		if out, err := execCommand("modprobe", mod); err != nil {
			t.Fatalf("modprobe %s: %v: %s", mod, err, out)
		}
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"}, // Bridge
		lab.Spec{Name: "ab1"}, // abroad Exit
		lab.Spec{Name: "phone1"},
		lab.At("web-ab", "93.184.215.14"),
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	co.c.LinkTiming = protocol.LinkTiming{SwitchBackSeconds: 5, RetryMinSeconds: 2, RetryMaxSeconds: 4, ReplaceAfterSeconds: 3600}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	dumpNetOnFailure(t, l, "ir1", "ab1")
	co.c.FeedURL, co.c.RIPEstatURL = serveFeed(t, irFeed("2.144.0.0/14", "5.0.0.0/8")), ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}
	whoami(t, l.Node("web-ab"), 7)

	user, err := co.store.CreateUser(ctx, "maryam")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := wgtypes.GeneratePrivateKey()
	dev, err := co.store.CreateDevice(ctx, user.ID, "phone1", key.PublicKey().String())
	if err != nil {
		t.Fatal(err)
	}

	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)
	bridgePub, err := co.c.BridgePublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	p := newPhone(t, l.Node("phone1"), key, dev.Address, l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)

	ir1, ab1 := l.Node("ir1"), l.Node("ab1")
	link := m.linkID("ir1", "ab1")
	byTransport := map[protocol.Transport]int{}
	for _, in := range m.instances(link) {
		byTransport[in.Transport]++
	}
	if byTransport[protocol.TransportGRE] != 1 || byTransport[protocol.TransportAmneziaWG] != 2 || byTransport[protocol.TransportWireGuard] != 1 {
		t.Errorf("crossing link instances by transport = %v, want 1 gre, 2 amneziawg, 1 wireguard", byTransport)
	}
	transport := func() protocol.Transport {
		if lh := m.link("ir1", link); lh.Up {
			return lh.Transport
		}
		return ""
	}
	// upAt counts ir1's Up instances of the Link by Transport.
	upAt := func() map[protocol.Transport]int {
		ups := map[protocol.Transport]int{}
		lh := m.link("ir1", link)
		for _, ih := range lh.Instances {
			if ih.Up {
				ups[m.instance(ih.ID).Transport]++
			}
		}
		return ups
	}

	// GRE carries the Link: it comes first, and if AmneziaWG came up a
	// moment sooner, the Link switches back to GRE after 5 s.
	testutil.Eventually(t, 90*time.Second, "crossing link carried by gre", func() bool { return transport() == protocol.TransportGRE })
	webAb := "93.184.215.14:7"
	ask := func() (string, error) { return p.ask(webAb) }
	p.leavesFrom("phone1 abroad over GRE", ask, ab1.Addr().String(), 30*time.Second)
	gre := l.Count(fmt.Sprintf("ip saddr { %s, %s } ip daddr { %s, %s } meta l4proto gre", ir1.Addr(), ab1.Addr(), ir1.Addr(), ab1.Addr()))
	for range 50 {
		if _, err := ask(); err != nil {
			t.Fatalf("phone1 over GRE: %v", err)
		}
	}
	// 50 requests and 50 replies, each one GRE packet, on top of about 4
	// probe packets a second.
	if n := gre.Packets(); n < 100 {
		t.Errorf("%d GRE packets crossed for 50 round trips, want the traffic on GRE", n)
	}
	for _, in := range m.instances(link) {
		if in.Transport == protocol.TransportGRE {
			if mtu := ifaceMTU(t, ir1, fmt.Sprintf("ways%di", in.ID)); mtu != 1500-24 {
				t.Errorf("GRE interface MTU = %d, want the outer 1500 less 24", mtu)
			}
		}
	}

	// A spoofed GRE packet claiming the Bridge's address but carrying an
	// inner source outside the Device and mesh ranges is dropped at the
	// Exit. The same packet from a Device's address is forwarded, which
	// shows the packet itself is good.
	spoofed := l.Count(fmt.Sprintf("ip saddr %s ip daddr 93.184.215.14 udp dport 9", ab1.Addr()))
	control := l.Count(fmt.Sprintf("ip saddr %s ip daddr 93.184.215.14 udp dport 10", ab1.Addr()))
	for range 5 {
		sendGRE(t, ir1, ab1.Addr(), netip.MustParseAddr("192.0.2.66"), netip.MustParseAddr("93.184.215.14"), 9)
		sendGRE(t, ir1, ab1.Addr(), netip.MustParseAddr("10.77.0.250"), netip.MustParseAddr("93.184.215.14"), 10)
	}
	testutil.Eventually(t, 5*time.Second, "the Device-sourced GRE packet forwarded", func() bool { return control.Packets() > 0 })
	if n := spoofed.Packets(); n != 0 {
		t.Errorf("the Exit forwarded %d spoofed GRE packets with an outside source", n)
	}

	// GRE blocked, UDP not: the Link moves to AmneziaWG within seconds,
	// once both AmneziaWG instances are Up as warm standby.
	testutil.Eventually(t, 60*time.Second, "both amneziawg instances up at ir1", func() bool {
		return upAt()[protocol.TransportAmneziaWG] == 2
	})
	block := l.Filter(lab.DropGREBetween(ir1, ab1))
	start := time.Now()
	testutil.Eventually(t, 15*time.Second, "crossing link moves to amneziawg", func() bool {
		return transport() == protocol.TransportAmneziaWG
	})
	moved := time.Since(start)
	t.Logf("GRE blocked: the link moved to amneziawg in %v", moved.Round(100*time.Millisecond))
	// About a second to switch once GRE stops being Up, which takes a
	// few lost probes; health reports every second add their lag.
	if moved > 8*time.Second {
		t.Errorf("moving off blocked GRE took %v, want a few seconds", moved)
	}
	p.leavesFrom("phone1 abroad over AmneziaWG", ask, ab1.Addr().String(), 10*time.Second)

	// GRE back: the Link returns to it once it has been Up for 5 s.
	block.Remove()
	testutil.Eventually(t, 90*time.Second, "crossing link back on gre", func() bool { return transport() == protocol.TransportGRE })

	// UDP blocked, GRE not: the Link stays on GRE while every UDP instance
	// goes down.
	udp := l.Filter(lab.DropUDPBetween(ir1, ab1))
	defer udp.Remove()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if got := transport(); got != protocol.TransportGRE {
			t.Fatalf("with UDP blocked the link is on %q, want gre", got)
		}
		time.Sleep(500 * time.Millisecond)
	}
	testutil.Eventually(t, 30*time.Second, "every UDP instance down at ir1", func() bool {
		ups := upAt()
		return ups[protocol.TransportAmneziaWG] == 0 && ups[protocol.TransportWireGuard] == 0 && ups[protocol.TransportGRE] == 1
	})
	p.leavesFrom("phone1 abroad over GRE with UDP blocked", ask, ab1.Addr().String(), 10*time.Second)
}

// ifaceMTU is an interface's MTU inside a Node.
func ifaceMTU(t *testing.T, n *lab.Node, iface string) int {
	t.Helper()
	out, err := n.Command("ip", "-j", "link", "show", "dev", iface).Output()
	if err != nil {
		t.Fatalf("ip link show %s on %s: %v", iface, n.Name(), err)
	}
	var links []struct {
		MTU int `json:"mtu"`
	}
	if err := json.Unmarshal(out, &links); err != nil || len(links) == 0 {
		t.Fatalf("ip link show %s on %s: %s", iface, n.Name(), out)
	}
	return links[0].MTU
}

// sendGRE sends one GRE packet from n to dst whose inner packet is a UDP
// datagram from src to target:port. The kernel puts n's own address on the
// outer header, as a spoofer claiming n's address would.
func sendGRE(t *testing.T, n *lab.Node, dst, src, target netip.Addr, port uint16) {
	t.Helper()
	payload := []byte("ways-spoof")
	inner := make([]byte, 28+len(payload))
	inner[0], inner[8], inner[9] = 0x45, 64, 17 // IPv4, TTL 64, UDP
	binary.BigEndian.PutUint16(inner[2:], uint16(len(inner)))
	copy(inner[12:16], src.AsSlice())
	copy(inner[16:20], target.AsSlice())
	binary.BigEndian.PutUint16(inner[10:], ipChecksum(inner[:20]))
	binary.BigEndian.PutUint16(inner[20:], 40000)
	binary.BigEndian.PutUint16(inner[22:], port)
	binary.BigEndian.PutUint16(inner[24:], uint16(8+len(payload))) // no UDP checksum
	copy(inner[28:], payload)
	pkt := append([]byte{0, 0, 0x08, 0x00}, inner...) // GRE carrying IPv4

	var conn net.PacketConn
	if err := n.Do(func() (err error) { conn, err = net.ListenPacket("ip4:47", "0.0.0.0"); return err }); err != nil {
		t.Fatalf("raw GRE socket on %s: %v", n.Name(), err)
	}
	defer conn.Close()
	if _, err := conn.WriteTo(pkt, &net.IPAddr{IP: dst.AsSlice()}); err != nil {
		t.Fatalf("send GRE from %s: %v", n.Name(), err)
	}
}

func ipChecksum(h []byte) uint16 {
	var sum uint32
	for i := 0; i < len(h); i += 2 {
		sum += uint32(h[i])<<8 | uint32(h[i+1])
	}
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}
