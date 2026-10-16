package link

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/protocol"
)

// Transport brings instances of one Transport up and down and reports
// their handshakes (ADR 0007). Tunnels it brings up outlive the Agent: Up
// on an instance that already exists reconfigures it in place, without
// resetting its session.
type Transport interface {
	Up(ctx context.Context, c InstanceConfig) error
	Down(ctx context.Context, iface string) error
	// LastHandshake returns the time of the peer's last handshake, zero if
	// none.
	LastHandshake(iface string) (time.Time, error)
}

// InstanceConfig is one end of an instance.
type InstanceConfig struct {
	Iface      string
	PrivateKey wgtypes.Key
	// ListenPort is the listening end's port; 0 on the initiating end,
	// which lets the kernel choose.
	ListenPort uint16
	PeerKey    wgtypes.Key
	// Endpoint is set on the initiating end only; the listening end learns
	// it from the peer's handshake.
	Endpoint *net.UDPAddr
	Addr     netip.Prefix
	AWG      *protocol.AWGParams
}

// MTU is the inner MTU: WireGuard's 1420 for a 1500-byte path, less any
// AmneziaWG transport padding.
func (c InstanceConfig) MTU() int {
	if c.AWG != nil {
		return 1420 - c.AWG.S4
	}
	return 1420
}

// ip runs ip(8) and returns its output.
func ip(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func linkExists(ctx context.Context, iface string) bool {
	_, err := ip(ctx, "link", "show", "dev", iface)
	return err == nil
}

// address gives iface its inner address and MTU, and brings it up.
func address(ctx context.Context, c InstanceConfig) error {
	if _, err := ip(ctx, "addr", "replace", c.Addr.String(), "dev", c.Iface); err != nil {
		return err
	}
	_, err := ip(ctx, "link", "set", "dev", c.Iface, "mtu", fmt.Sprint(c.MTU()), "up")
	return err
}

func deleteLink(ctx context.Context, iface string) error {
	if !linkExists(ctx, iface) {
		return nil
	}
	_, err := ip(ctx, "link", "del", "dev", iface)
	return err
}

// instanceIfaces lists interfaces named like instance interfaces.
func instanceIfaces(ctx context.Context) ([]string, error) {
	out, err := ip(ctx, "-o", "link", "show")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimSuffix(f[1], ":"), "@")
		if isInstanceIface(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// isInstanceIface matches the Coordinator's names: dz<id>l or dz<id>i.
func isInstanceIface(name string) bool {
	if len(name) < 4 || !strings.HasPrefix(name, "dz") {
		return false
	}
	if end := name[len(name)-1]; end != 'l' && end != 'i' {
		return false
	}
	for _, r := range name[2 : len(name)-1] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// linkTable is the routing table that carries a Link: its default route
// goes out the active instance (ADR 0008 routes candidates into it).
func linkTable(linkID uint64) string { return fmt.Sprint(10000 + linkID) }

// routeVia points a Link's table at iface, or makes it unreachable when
// iface is empty.
func routeVia(ctx context.Context, linkID uint64, iface string) error {
	args := []string{"route", "replace", "unreachable", "default", "table", linkTable(linkID)}
	if iface != "" {
		args = []string{"route", "replace", "default", "dev", iface, "table", linkTable(linkID)}
	}
	_, err := ip(ctx, args...)
	return err
}

func flushTable(ctx context.Context, linkID uint64) error {
	_, err := ip(ctx, "route", "flush", "table", linkTable(linkID))
	return err
}

// LinkRoute returns the interface a Link's table currently routes through,
// or "" if none.
func LinkRoute(ctx context.Context, linkID uint64) string {
	out, err := ip(ctx, "route", "show", "table", linkTable(linkID))
	if err != nil {
		return ""
	}
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "dev" {
			return f[i+1]
		}
	}
	return ""
}
