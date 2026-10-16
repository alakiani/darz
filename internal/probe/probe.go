// Package probe is the probe Device (ADR 0005): a real Device keypair that
// the Coordinator host uses to check each domestic Bridge as a User would,
// by handshaking with the Bridge's public address and resolving a name
// through its in-tunnel resolver.
//
// Each check runs WireGuard in userspace on its own netstack, so the
// Coordinator needs no privileges and no interface, and checks of Bridges
// that share one identity and one resolver address never mix.
package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Prober checks Bridges as the probe Device.
type Prober struct {
	PrivateKey wgtypes.Key
	Address    netip.Addr // the probe Device's tunnel address
	Hostname   string     // what it resolves through the Bridge
	Timeout    time.Duration
	// Enter runs fn in the network the probe dials from; nil runs fn
	// directly. The lab uses it to dial from the Coordinator's namespace.
	Enter func(fn func() error) error
}

// Check passes when the Bridge at endpoint, presenting bridgeKey, answers
// a DNS query to resolver through the tunnel with NOERROR within Timeout.
func (p *Prober) Check(ctx context.Context, endpoint netip.AddrPort, bridgeKey wgtypes.Key, resolver netip.Addr) error {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{p.Address}, nil, 1420)
	if err != nil {
		return err
	}
	dev := device.NewDevice(tun, conn.NewStdNetBind(), device.NewLogger(device.LogLevelSilent, ""))
	defer dev.Close()
	cfg := fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=0.0.0.0/0\n",
		hex.EncodeToString(p.PrivateKey[:]), hex.EncodeToString(bridgeKey[:]), endpoint)
	if err := dev.IpcSet(cfg); err != nil {
		return err
	}
	enter := p.Enter
	if enter == nil {
		enter = func(fn func() error) error { return fn() }
	}
	if err := enter(dev.Up); err != nil { // the UDP socket opens here
		return err
	}

	c, err := tnet.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(resolver, 53))
	if err != nil {
		return err
	}
	defer c.Close()
	go func() { <-ctx.Done(); c.SetDeadline(time.Now()) }()

	query, id := dnsQuery(p.Hostname)
	buf := make([]byte, 1500)
	// The first query waits for the handshake; resend now and then in case
	// one is lost.
	for ctx.Err() == nil {
		if _, err := c.Write(query); err != nil {
			return err
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		for {
			n, err := c.Read(buf)
			if err != nil {
				break
			}
			if n < 12 || binary.BigEndian.Uint16(buf) != id || buf[2]&0x80 == 0 {
				continue
			}
			if rcode := buf[3] & 0x0f; rcode != 0 {
				return fmt.Errorf("probe: resolver answered rcode %d", rcode)
			}
			return nil
		}
	}
	if !handshook(dev) {
		return errors.New("probe: no handshake with the Bridge")
	}
	return errors.New("probe: handshake done, but no DNS answer through the Bridge")
}

// handshook reports whether dev completed a handshake with its peer.
func handshook(dev *device.Device) bool {
	cfg, err := dev.IpcGet()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(cfg, "\n") {
		if v, ok := strings.CutPrefix(line, "last_handshake_time_sec="); ok && v != "0" {
			return true
		}
	}
	return false
}

// dnsQuery is an A query for host with a random id.
func dnsQuery(host string) ([]byte, uint16) {
	var idb [2]byte
	rand.Read(idb[:])
	q := append(idb[:], 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0) // recursion desired; one question
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 1, 0, 1) // root; type A; class IN
	return q, binary.BigEndian.Uint16(idb[:])
}
