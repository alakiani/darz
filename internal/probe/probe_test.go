package probe

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

var (
	resolverAddr = netip.MustParseAddr("10.77.0.1")
	probeAddr    = netip.MustParseAddr("10.77.255.254")
)

// fakeBridge is a userspace WireGuard peer on loopback that admits the
// probe and answers DNS on the resolver address with rcode.
func fakeBridge(t *testing.T, key wgtypes.Key, probePub wgtypes.Key, rcode byte) netip.AddrPort {
	t.Helper()
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{resolverAddr}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewStdNetBind(), device.NewLogger(device.LogLevelSilent, ""))
	t.Cleanup(dev.Close)
	cfg := fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=%s/32\n",
		hex.EncodeToString(key[:]), hex.EncodeToString(probePub[:]), probeAddr)
	if err := dev.IpcSet(cfg); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	got, _ := dev.IpcGet()
	var port int
	for _, line := range strings.Split(got, "\n") {
		if v, ok := strings.CutPrefix(line, "listen_port="); ok {
			port, _ = strconv.Atoi(v)
		}
	}
	pc, err := tnet.ListenUDP(&net.UDPAddr{IP: resolverAddr.AsSlice(), Port: 53})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			reply := append([]byte{}, buf[:n]...)
			reply[2], reply[3] = 0x81, 0x80|rcode
			pc.WriteTo(reply, from)
		}
	}()
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))
}

func keys(t *testing.T) (bridge, probe wgtypes.Key) {
	t.Helper()
	var err error
	if bridge, err = wgtypes.GeneratePrivateKey(); err != nil {
		t.Fatal(err)
	}
	if probe, err = wgtypes.GeneratePrivateKey(); err != nil {
		t.Fatal(err)
	}
	return bridge, probe
}

func TestCheckPassesThroughAWorkingBridge(t *testing.T) {
	bridge, probeKey := keys(t)
	ep := fakeBridge(t, bridge, probeKey.PublicKey(), 0)
	p := &Prober{PrivateKey: probeKey, Address: probeAddr, Hostname: "www.gstatic.com", Timeout: 5 * time.Second}
	if err := p.Check(context.Background(), ep, bridge.PublicKey(), resolverAddr); err != nil {
		t.Fatal(err)
	}
	// Each check is a fresh handshake, so a second one works too, once the
	// handshake timestamp has moved on: WireGuard rounds it to about 16 ms
	// and drops an initiation that is not newer as a replay.
	time.Sleep(50 * time.Millisecond)
	if err := p.Check(context.Background(), ep, bridge.PublicKey(), resolverAddr); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFails(t *testing.T) {
	bridge, probeKey := keys(t)
	other, _ := keys(t)
	ep := fakeBridge(t, bridge, probeKey.PublicKey(), 0)
	servfail := fakeBridge(t, bridge, probeKey.PublicKey(), 2)
	unknown, _ := wgtypes.GeneratePrivateKey() // a Device the Bridge does not know

	for name, tc := range map[string]struct {
		key      wgtypes.Key
		ep       netip.AddrPort
		bridgePK wgtypes.Key
	}{
		"nothing listening":     {probeKey, netip.MustParseAddrPort("127.0.0.1:9"), bridge.PublicKey()},
		"wrong Bridge identity": {probeKey, ep, other.PublicKey()},
		"probe not admitted":    {unknown, ep, bridge.PublicKey()},
		"resolver fails":        {probeKey, servfail, bridge.PublicKey()},
	} {
		t.Run(name, func(t *testing.T) {
			p := &Prober{PrivateKey: tc.key, Address: probeAddr, Hostname: "www.gstatic.com", Timeout: 2 * time.Second}
			start := time.Now()
			if err := p.Check(context.Background(), tc.ep, tc.bridgePK, resolverAddr); err == nil {
				t.Fatal("check passed")
			}
			if d := time.Since(start); d > 4*time.Second {
				t.Errorf("failing took %v, want about the 2 s timeout", d)
			}
		})
	}
}

func TestCheckRunsTheBindInsideEnter(t *testing.T) {
	bridge, probeKey := keys(t)
	ep := fakeBridge(t, bridge, probeKey.PublicKey(), 0)
	entered := 0
	p := &Prober{PrivateKey: probeKey, Address: probeAddr, Hostname: "www.gstatic.com", Timeout: 5 * time.Second,
		Enter: func(fn func() error) error { entered++; return fn() }}
	if err := p.Check(context.Background(), ep, bridge.PublicKey(), resolverAddr); err != nil {
		t.Fatal(err)
	}
	if entered != 1 {
		t.Errorf("Enter ran %d times, want 1", entered)
	}
}
