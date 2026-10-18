package link

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestRouteGetGivesTheInterfaceAddress(t *testing.T) {
	// What ip -j route get prints on a Bridge behind a 1:1 NAT.
	out := `[{"dst":"18.130.1.2","gateway":"10.10.10.1","dev":"ens3","prefsrc":"10.10.10.215","flags":[],"uid":0,"cache":[]}]`
	src, dev, err := parseRouteGet([]byte(out))
	if err != nil || src != netip.MustParseAddr("10.10.10.215") || dev != "ens3" {
		t.Errorf("route = %v %q %v", src, dev, err)
	}
	for _, bad := range []string{`[]`, `[{"dev":"ens3"}]`, `[{"dev":"ens3","prefsrc":"2001:db8::1"}]`, `nonsense`} {
		if _, _, err := parseRouteGet([]byte(bad)); err == nil {
			t.Errorf("parsed %s", bad)
		}
	}
}

func TestTunnelEndsOfAGREInterface(t *testing.T) {
	out := `[{"ifindex":9,"ifname":"dz5i","flags":["POINTOPOINT","NOARP","UP","LOWER_UP"],"mtu":1476,` +
		`"linkinfo":{"info_kind":"gre","info_data":{"remote":"18.130.1.2","local":"10.10.10.215","ttl":64,"pmtudisc":true}}}]`
	e, err := parseTunnelEnds([]byte(out))
	if err != nil || e.Local != netip.MustParseAddr("10.10.10.215") || e.Remote != netip.MustParseAddr("18.130.1.2") {
		t.Errorf("ends = %+v, %v", e, err)
	}
	wg := `[{"ifindex":7,"ifname":"dz5i","linkinfo":{"info_kind":"wireguard"}}]`
	if _, err := parseTunnelEnds([]byte(wg)); err == nil {
		t.Error("a WireGuard interface read as GRE")
	}
}

// GRE has no handshake: the first answered probe stands for one, so the
// instance is Up on the same probe evidence as any other (ADR 0009).
func TestHandshakelessTransportIsUpOnProbesAlone(t *testing.T) {
	if _, err := (GRE{}).LastHandshake("dz1i"); !errors.Is(err, ErrNoHandshake) {
		t.Fatalf("GRE LastHandshake err = %v", err)
	}
	m := &meter{}
	if m.up(t0, handshakeOf(time.Time{}, ErrNoHandshake, time.Time{})) {
		t.Error("up before any probe was answered")
	}
	now := probeEverySecond(m, t0, 30, never)
	last := now.Add(-time.Second)
	if !m.up(now, handshakeOf(time.Time{}, ErrNoHandshake, last)) {
		t.Error("not up with every probe answered")
	}
	// A WireGuard-family instance still needs its own handshake.
	if m.up(now, handshakeOf(time.Time{}, nil, last)) {
		t.Error("a transport with a handshake is up without one")
	}
}
