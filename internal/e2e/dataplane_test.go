//go:build linux

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/testutil"
)

// whoami answers on UDP and TCP with the source address it saw, so a test
// can tell which Node the traffic left from.
func whoami(t *testing.T, n *lab.Node, port int) {
	t.Helper()
	addr := net.JoinHostPort(n.Addr().String(), fmt.Sprint(port))
	var udp net.PacketConn
	var tcp net.Listener
	err := n.Do(func() (err error) {
		if udp, err = net.ListenPacket("udp4", addr); err != nil {
			return err
		}
		tcp, err = net.Listen("tcp4", addr)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			_, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			udp.WriteTo([]byte(from.(*net.UDPAddr).IP.String()), from)
		}
	}()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
			c.Write([]byte(host))
			c.Close()
		}
	}()
}

// dnsServer stands in for a public resolver: it answers each UDP query,
// and each length-prefixed query over TCP or over TLS on port 853, with
// the source address it saw. It returns the file of the root its TLS
// certificate verifies against, for the Agents' -upstream-ca.
func dnsServer(t *testing.T, n *lab.Node) (caFile string) {
	t.Helper()
	addr := net.JoinHostPort(n.Addr().String(), "53")
	var udp net.PacketConn
	var tcp net.Listener
	if err := n.Do(func() (err error) {
		if udp, err = net.ListenPacket("udp4", addr); err != nil {
			return err
		}
		tcp, err = net.Listen("tcp4", addr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			_, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			udp.WriteTo([]byte("dns:"+from.(*net.UDPAddr).IP.String()), from)
		}
	}()
	answer := func(_ []byte, from string) []byte { return []byte("dns:" + from) }
	go serveStreamDNS(tcp, answer)
	return serveDoT(t, n, answer)
}

// phone is a lab Node set up as a Device: stock WireGuard to a Bridge, with
// everything routed into the tunnel.
type phone struct {
	t         *testing.T
	node      *lab.Node
	bridgeKey wgtypes.Key
}

func newPhone(t *testing.T, n *lab.Node, key wgtypes.Key, addr netip.Addr, bridge *lab.Node, bridgeKey wgtypes.Key, port int) *phone {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		if out, err := n.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s on %s: %v: %s", strings.Join(args, " "), n.Name(), err, out)
		}
	}
	run("ip", "link", "add", "wg0", "type", "wireguard")
	keepalive := 5 * time.Second
	err := n.Do(func() error {
		c, err := wgctrl.New()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.ConfigureDevice("wg0", wgtypes.Config{PrivateKey: &key, Peers: []wgtypes.PeerConfig{{
			PublicKey:                   bridgeKey,
			Endpoint:                    &net.UDPAddr{IP: bridge.Addr().AsSlice(), Port: port},
			AllowedIPs:                  []net.IPNet{{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}},
			PersistentKeepaliveInterval: &keepalive,
		}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	gw := netip.PrefixFrom(n.Addr(), 24).Masked().Addr().Next()
	run("ip", "addr", "add", addr.String()+"/32", "dev", "wg0")
	run("ip", "link", "set", "wg0", "up")
	run("ip", "route", "add", bridge.Addr().String()+"/32", "via", gw.String(), "dev", "wan0")
	run("ip", "route", "replace", "default", "dev", "wg0")
	return &phone{t: t, node: n, bridgeKey: bridgeKey}
}

// ask sends one UDP datagram and returns the reply.
func (p *phone) ask(target string) (string, error) {
	var c net.Conn
	if err := p.node.Do(func() (err error) { c, err = net.Dial("udp4", target); return err }); err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("who")); err != nil {
		return "", err
	}
	buf := make([]byte, 128)
	n, err := c.Read(buf)
	return string(buf[:n]), err
}

// askTCP opens a TCP connection and returns what the server sends. With
// dns set, it sends one length-prefixed query first.
func (p *phone) askTCP(target string, dns bool) (string, error) {
	var c net.Conn
	if err := p.node.Do(func() (err error) {
		c, err = (&net.Dialer{Timeout: 3 * time.Second}).Dial("tcp4", target)
		return err
	}); err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if dns {
		c.Write([]byte{0, 3, 'w', 'h', 'o'})
		var n [2]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return "", err
		}
		buf := make([]byte, int(n[0])<<8|int(n[1]))
		_, err := io.ReadFull(c, buf)
		return string(buf), err
	}
	b, err := io.ReadAll(c)
	return string(b), err
}

// leavesFrom waits until a request from p is answered and its source is
// want, failing the test otherwise.
func (p *phone) leavesFrom(what string, ask func() (string, error), want string, timeout time.Duration) {
	p.t.Helper()
	p.leavesFromAny(what, ask, []string{want}, timeout)
}

// leavesFromAny is leavesFrom with several acceptable sources.
func (p *phone) leavesFromAny(what string, ask func() (string, error), want []string, timeout time.Duration) {
	p.t.Helper()
	var last string
	var lastErr error
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		last, lastErr = ask()
		if lastErr == nil && slices.Contains(want, last) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	p.t.Fatalf("%s: got %q (err %v), want %q", what, last, lastErr, want)
}

func irFeed(prefixes ...string) string {
	var b strings.Builder
	b.WriteString("#Last update: lab\n/ip firewall address-list\n")
	for _, p := range prefixes {
		fmt.Fprintf(&b, ":do { add address=%s list=NoNAT} on-error={}\n", p)
	}
	return b.String()
}

// TestDataPlane is session 4's "proves in the lab" test: a lab Device
// reaches an abroad address through an abroad Exit and a domestic address
// through an IR Node; DNS goes abroad, and leaves the Bridge only as DNS
// over TLS (ADR 0009); a hostile feed update is rejected and the last good
// set stays.
func TestDataPlane(t *testing.T) {
	binDir(t)
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}
	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"}, // Bridge
		lab.Spec{Name: "ir2"}, // domestic Exit
		lab.Spec{Name: "ir3"}, // Bridge and Exit: exits IR traffic itself
		lab.Spec{Name: "ab1"}, // abroad Exit
		lab.Spec{Name: "phone1"},
		lab.Spec{Name: "phone2"},
		lab.At("web-ir", "2.144.0.10"),    // a site in Iran
		lab.At("web-ab", "93.184.215.14"), // a site abroad, inside 0.0.0.0/1
		lab.At("dns-ab", "9.9.9.9"),       // a public resolver abroad
		lab.At("egress-ab", "1.0.0.10"),   // what abroad Exits' egress checks reach
		lab.At("egress-ir", "2.188.0.10"), // what IR Exits' egress checks reach
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	co.c.Upstreams = []string{"9.9.9.9"}
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))
	dumpNetOnFailure(t, l, "ir1", "ir2", "ir3", "ab1", "phone1", "phone2")

	// The IR feed, served to the Coordinator.
	var feed atomic.Value
	feed.Store(irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8", "10.0.0.0/8"))
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, feed.Load().(string))
	}))
	defer feedSrv.Close()
	co.c.FeedURL, co.c.RIPEstatURL = feedSrv.URL, ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}

	whoami(t, l.Node("web-ir"), 7)
	whoami(t, l.Node("web-ir"), 853)
	whoami(t, l.Node("web-ab"), 7)
	upstreamCA := dnsServer(t, l.Node("dns-ab"))
	// What leaves for the upstream resolver, wherever it leaves from.
	plainDNS := l.Count("ip daddr 9.9.9.9 meta l4proto { tcp, udp } th dport 53")
	dotDNS := l.Count("ip daddr 9.9.9.9 tcp dport 853")

	// Two Devices of one User.
	user, err := co.store.CreateUser(ctx, "maryam")
	if err != nil {
		t.Fatal(err)
	}
	var keys [2]wgtypes.Key
	var addrs [2]netip.Addr
	for i := range keys {
		keys[i], _ = wgtypes.GeneratePrivateKey()
		d, err := co.store.CreateDevice(ctx, user.ID, fmt.Sprintf("phone%d", i+1), keys[i].PublicKey().String())
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = d.Address
	}

	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{},
		agentArgs: []string{"-upstream-ca", upstreamCA}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ir2", []protocol.Role{protocol.RoleExit}, []string{"IR"})
	m.startAgent("ir3", []protocol.Role{protocol.RoleBridge, protocol.RoleExit}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)

	bridgePub, err := co.c.BridgePublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bridgeKey, _ := wgtypes.ParseKey(bridgePub)
	// Both Bridges present the same identity, so the same config shape
	// works on either.
	p1 := newPhone(t, l.Node("phone1"), keys[0], addrs[0], l.Node("ir1"), bridgeKey, coordinator.DefaultDevicePort)
	p2 := newPhone(t, l.Node("phone2"), keys[1], addrs[1], l.Node("ir3"), bridgeKey, coordinator.DefaultDevicePort)

	ab1, ir2, ir3 := l.Node("ab1").Addr().String(), l.Node("ir2").Addr().String(), l.Node("ir3").Addr().String()
	webAb, webIR := "93.184.215.14:7", "2.144.0.10:7"
	udp := func(p *phone, target string) func() (string, error) {
		return func() (string, error) { return p.ask(target) }
	}
	tcp := func(p *phone, target string, dns bool) func() (string, error) {
		return func() (string, error) { return p.askTCP(target, dns) }
	}

	// Abroad through the abroad Exit, domestic through an IR Node.
	p1.leavesFrom("phone1 to a site abroad", udp(p1, webAb), ab1, 120*time.Second)
	// ir1 has two IR Exits, ir2 and the dual-role ir3; its buckets spread
	// over both (ADR 0008), so the phone may leave from either.
	irExits := []string{ir2, ir3}
	p1.leavesFromAny("phone1 to a site in Iran", udp(p1, webIR), irExits, 30*time.Second)
	// A dual-role IR Bridge exits IR traffic from its own NIC.
	p2.leavesFrom("phone2 to a site in Iran", udp(p2, webIR), ir3, 60*time.Second)
	p2.leavesFrom("phone2 to a site abroad", udp(p2, webAb), ab1, 30*time.Second)

	// DNS to any address, UDP or TCP, is answered by the Bridge resolver,
	// whose queries leave abroad.
	p1.leavesFrom("DNS over UDP", udp(p1, "8.8.8.8:53"), "dns:"+ab1, 30*time.Second)
	p1.leavesFrom("DNS over TCP", tcp(p1, "8.8.8.8:53", true), "dns:"+ab1, 30*time.Second)
	p2.leavesFrom("DNS from the other Bridge", udp(p2, "10.77.0.1:53"), "dns:"+ab1, 30*time.Second)
	// The Bridge resolvers reach the upstream over TLS on port 853 only:
	// the Link may be plain GRE, and the filter rewrites port-53 answers.
	if n := plainDNS.Packets(); n != 0 {
		t.Errorf("%d plain port-53 packets left for the upstream resolver", n)
	}
	if dotDNS.Packets() == 0 {
		t.Error("no DNS over TLS reached the upstream resolver")
	}
	// DNS over TLS goes abroad even to an address in the IR set.
	p1.leavesFrom("DoT to an IR address", tcp(p1, "2.144.0.10:853", false), ab1, 30*time.Second)

	// A hostile feed update is rejected and the last good set stays: the
	// abroad site, inside 0.0.0.0/1, still leaves abroad.
	feed.Store(irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8", "0.0.0.0/1"))
	if err := co.c.RefreshIRFeed(ctx); !errors.Is(err, coordinator.ErrFeedRejected) {
		t.Fatalf("hostile feed: err = %v, want rejection", err)
	}
	time.Sleep(3 * time.Second)
	p1.leavesFrom("phone1 abroad after the hostile update", udp(p1, webAb), ab1, 5*time.Second)
	p1.leavesFromAny("phone1 domestic after the hostile update", udp(p1, webIR), irExits, 5*time.Second)

	// Revoking a Device removes it from every Bridge.
	devs, _ := co.store.Devices(ctx)
	for _, d := range devs {
		if d.Address == addrs[0] {
			co.store.RevokeDevice(ctx, d.ID)
		}
	}
	if err := co.c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, 20*time.Second, "the revoked phone is cut off", func() bool {
		_, err := p1.ask(webAb)
		return err != nil
	})
	p2.leavesFrom("the other phone still works", udp(p2, webAb), ab1, 5*time.Second)
}

// dumpNetOnFailure prints each Node's ruleset, rules, routes and links if
// the test fails.
func dumpNetOnFailure(t *testing.T, l *lab.Lab, names ...string) {
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, name := range names {
			n := l.Node(name)
			var b strings.Builder
			for _, cmd := range [][]string{
				{"nft", "list", "ruleset"},
				{"ip", "rule"},
				{"ip", "route", "show", "table", "all"},
				{"ip", "-s", "link"},
				{"nstat", "-az", "IpInNoRoutes", "IpExtInNoRoutes", "TcpExtIPReversePathFilter", "IpForwDatagrams", "IpInAddrErrors", "IpOutNoRoutes", "IpInDelivers", "UdpNoPorts"},
				{"sh", "-c", "for f in /proc/sys/net/ipv4/conf/*/rp_filter /proc/sys/net/ipv4/conf/*/src_valid_mark; do echo $f $(cat $f); done"},
				// How the kernel routes a reply to each phone arriving from a site.
				{"ip", "route", "get", "10.77.0.2", "from", "2.144.0.10", "iif", "wan0"},
				{"ip", "route", "get", "10.77.0.3", "from", "2.144.0.10", "iif", "wan0"},
				{"ip", "route", "get", "10.77.0.2", "from", "93.184.215.14", "iif", "wan0"},
			} {
				out, _ := n.Command(cmd[0], cmd[1:]...).CombinedOutput()
				fmt.Fprintf(&b, "$ %s\n%s\n", strings.Join(cmd, " "), out)
			}
			n.Do(func() error {
				c, err := wgctrl.New()
				if err != nil {
					return err
				}
				defer c.Close()
				devs, _ := c.Devices()
				for _, d := range devs {
					for _, p := range d.Peers {
						fmt.Fprintf(&b, "wg %s peer %s endpoint %v handshake %v rx %d tx %d\n",
							d.Name, p.PublicKey.String()[:8], p.Endpoint, p.LastHandshakeTime, p.ReceiveBytes, p.TransmitBytes)
					}
				}
				return nil
			})
			t.Logf("---- network state of %s ----\n%s", name, b.String())
		}
	})
}
