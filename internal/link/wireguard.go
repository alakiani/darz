package link

import (
	"context"
	"net"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// KernelWireGuard runs plain WireGuard instances in the kernel module,
// configured over netlink.
type KernelWireGuard struct {
	client *wgctrl.Client
}

// NewKernelWireGuard opens a netlink client.
func NewKernelWireGuard() (*KernelWireGuard, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	return &KernelWireGuard{client: c}, nil
}

var allIPv4 = net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}

func (k *KernelWireGuard) Up(ctx context.Context, c InstanceConfig) error {
	if !linkExists(ctx, c.Iface) {
		if _, err := ip(ctx, "link", "add", "dev", c.Iface, "type", "wireguard"); err != nil {
			return err
		}
	}
	dev, err := k.client.Device(c.Iface)
	if err != nil {
		return err
	}
	// Setting the same key, port and peer again leaves the session alone;
	// only other peers are removed.
	noKeepalive := time.Duration(0)
	cfg := wgtypes.Config{PrivateKey: &c.PrivateKey, Peers: []wgtypes.PeerConfig{{
		PublicKey:                   c.PeerKey,
		Endpoint:                    c.Endpoint,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  []net.IPNet{allIPv4},
		PersistentKeepaliveInterval: &noKeepalive, // probes keep the path warm
	}}}
	if c.ListenPort != 0 && dev.ListenPort != int(c.ListenPort) {
		port := int(c.ListenPort)
		cfg.ListenPort = &port
	}
	for _, p := range dev.Peers {
		if p.PublicKey != c.PeerKey {
			cfg.Peers = append(cfg.Peers, wgtypes.PeerConfig{PublicKey: p.PublicKey, Remove: true})
		}
	}
	if err := k.client.ConfigureDevice(c.Iface, cfg); err != nil {
		return err
	}
	return address(ctx, c, c.MTU())
}

func (k *KernelWireGuard) Down(ctx context.Context, iface string) error {
	return deleteLink(ctx, iface)
}

func (k *KernelWireGuard) LastHandshake(iface string) (time.Time, error) {
	dev, err := k.client.Device(iface)
	if err != nil || len(dev.Peers) == 0 {
		return time.Time{}, err
	}
	if t := dev.Peers[0].LastHandshakeTime; t.Unix() > 0 {
		return t, nil
	}
	return time.Time{}, nil // never
}
