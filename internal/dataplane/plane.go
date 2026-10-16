// Package dataplane is the Agent's Bridge and Exit data plane (ADR 0004,
// ADR 0005): the Device-facing WireGuard interface with the shared Bridge
// identity, Rule marks over Destination Sets in nftables, policy routing
// per Rule, the DNS relay and its redirects, the IPv6 drop, and NAT on
// Exits with replies sent back over the Link they came in on.
package dataplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/agent"
	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

// LinkSource reports this Node's Links as the Link layer sees them.
type LinkSource interface {
	LinkStatus() []link.Status
}

// Config configures a Plane.
type Config struct {
	// StateDir caches Destination Sets by hash.
	StateDir string
	Links    LinkSource
	Log      *slog.Logger
}

const syncInterval = 500 * time.Millisecond

// Plane is the Agent's Applier for the data plane.
type Plane struct {
	cfg Config
	log *slog.Logger
	wg  *wgctrl.Client

	mu       sync.Mutex
	fetch    agent.Fetch
	st       *protocol.State
	uplink   uplink
	resolver *resolver
	resKey   string         // what the running resolver was started with
	routes   map[int]string // table -> route last written
}

type uplink struct{ dev, gateway string }

// New returns a Plane.
func New(cfg Config) (*Plane, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	c, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "sets"), 0o700); err != nil {
		return nil, err
	}
	return &Plane{cfg: cfg, log: cfg.Log, wg: c, routes: map[int]string{}}, nil
}

// UseFetch implements agent.FetchUser.
func (p *Plane) UseFetch(f agent.Fetch) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetch = f
}

// Apply puts the snapshot's data plane into effect. Run it after the Link
// layer's Apply, so Link instances exist.
func (p *Plane) Apply(ctx context.Context, st protocol.State) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := setSysctls(); err != nil {
		return err
	}
	up, err := findUplink(ctx)
	if err != nil {
		return err
	}
	p.uplink = up

	rs := ruleset{Uplink: up.dev, ExitIfaces: map[string]uint64{}}
	if slices.Contains(st.Node.Roles, protocol.RoleExit) {
		for _, l := range st.Links {
			for _, in := range l.Instances {
				rs.ExitIfaces[in.Iface] = l.ID
			}
		}
	}

	b := st.Bridge
	if b != nil && len(b.Rules) > 0 {
		if err := p.applyDeviceIface(ctx, b); err != nil {
			return fmt.Errorf("device interface: %w", err)
		}
		rs.Bridge = true
		rs.DefaultRule = b.Rules[len(b.Rules)-1].ID
		addr, err := netip.ParsePrefix(b.Address)
		if err != nil {
			return err
		}
		rs.ResolverAddr = addr.Addr().String()
		for _, r := range b.Rules {
			prefixes, err := p.set(ctx, r.SetHash)
			if err != nil {
				return fmt.Errorf("rule %s: %w", r.Name, err)
			}
			rs.Rules = append(rs.Rules, nftRule{ID: r.ID, Elements: prefixes})
			if slices.Contains(st.Node.Roles, protocol.RoleExit) && r.Matches(st.Node.Tags) {
				rs.LocalExit = true
			}
		}
	} else {
		deleteLink(ctx, DeviceIface)
	}

	if err := nft(ctx, render(rs)); err != nil {
		return err
	}
	p.st = &st
	if err := p.syncRules(ctx); err != nil {
		return err
	}
	p.syncRoutes(ctx)
	return p.syncResolver(rs.DefaultRule)
}

// Run keeps each Rule's route in step with the Links until ctx ends.
func (p *Plane) Run(ctx context.Context) {
	t := time.NewTicker(syncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			if p.resolver != nil {
				p.resolver.stop()
			}
			p.mu.Unlock()
			return
		case <-t.C:
			p.mu.Lock()
			p.syncRoutes(ctx)
			p.mu.Unlock()
		}
	}
}

// applyDeviceIface brings up the Device-facing interface with the shared
// Bridge identity and exactly the snapshot's Devices as peers.
func (p *Plane) applyDeviceIface(ctx context.Context, b *protocol.BridgeState) error {
	key, err := wgtypes.ParseKey(b.PrivateKey)
	if err != nil {
		return err
	}
	if !linkExists(ctx, DeviceIface) {
		if err := ip(ctx, "link", "add", "dev", DeviceIface, "type", "wireguard"); err != nil {
			return err
		}
	}
	dev, err := p.wg.Device(DeviceIface)
	if err != nil {
		return err
	}
	port := int(b.ListenPort)
	cfg := wgtypes.Config{PrivateKey: &key, ListenPort: &port}
	want := map[wgtypes.Key]bool{}
	for _, d := range b.Devices {
		pk, err := wgtypes.ParseKey(d.PublicKey)
		if err != nil {
			p.log.Warn("skipping device with a bad key", "address", d.Address)
			continue
		}
		_, ipnet, err := net.ParseCIDR(d.Address)
		if err != nil {
			continue
		}
		want[pk] = true
		cfg.Peers = append(cfg.Peers, wgtypes.PeerConfig{PublicKey: pk, ReplaceAllowedIPs: true, AllowedIPs: []net.IPNet{*ipnet}})
	}
	for _, peer := range dev.Peers {
		if !want[peer.PublicKey] {
			cfg.Peers = append(cfg.Peers, wgtypes.PeerConfig{PublicKey: peer.PublicKey, Remove: true})
		}
	}
	if err := p.wg.ConfigureDevice(DeviceIface, cfg); err != nil {
		return err
	}
	if err := ip(ctx, "addr", "replace", b.Address, "dev", DeviceIface); err != nil {
		return err
	}
	return ip(ctx, "link", "set", "dev", DeviceIface, "mtu", "1420", "up")
}

// set returns a Destination Set's prefixes, from the cache or the
// Coordinator. Content is checked against its hash before it is used.
func (p *Plane) set(ctx context.Context, hash string) ([]string, error) {
	path := filepath.Join(p.cfg.StateDir, "sets", hash)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if p.fetch == nil {
			return nil, errors.New("set not cached and the Coordinator is not reachable yet")
		}
		if body, err = p.fetch(ctx, "/v1/sets/"+hash); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != hash {
		os.Remove(path)
		return nil, fmt.Errorf("set %s: content does not match its hash", hash)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if _, err := netip.ParsePrefix(line); err != nil {
				return nil, fmt.Errorf("set %s: %w", hash, err)
			}
			out = append(out, line)
		}
	}
	return out, nil
}

// syncRules keeps exactly this plane's ip rules: one per Rule mark, one
// per Link mark, and a catch-all that drops Device traffic with no mark.
func (p *Plane) syncRules(ctx context.Context) error {
	want := map[string]bool{}
	if b := p.st.Bridge; b != nil {
		for _, r := range b.Rules {
			want[fmt.Sprintf("%d fwmark %#x lookup %d", prioRules, ruleMark(r.ID), ruleTable(r.ID))] = true
		}
		want[fmt.Sprintf("%d iif %s lookup %d", prioDrop, DeviceIface, dropTable)] = true
	}
	for _, l := range p.st.Links {
		want[fmt.Sprintf("%d fwmark %#x lookup %s", prioLinks, linkMark(l.ID), link.LinkTable(l.ID))] = true
	}
	// Tables first, so no rule ever points at an empty table and falls
	// through to the main table.
	if err := ip(ctx, "route", "replace", "blackhole", "default", "table", fmt.Sprint(dropTable)); err != nil {
		return err
	}
	if b := p.st.Bridge; b != nil {
		for _, r := range b.Rules {
			table := ruleTable(r.ID)
			if _, ok := p.routes[table]; ok {
				continue
			}
			if err := ip(ctx, "route", "replace", "blackhole", "default", "table", fmt.Sprint(table)); err != nil {
				return err
			}
			p.routes[table] = "blackhole default"
		}
	}

	have, err := ourRules(ctx)
	if err != nil {
		return err
	}
	for spec := range want {
		if !have[spec] {
			f := strings.Fields(spec)
			if err := ip(ctx, append([]string{"rule", "add", "priority"}, f...)...); err != nil {
				return err
			}
		}
	}
	for spec := range have {
		if !want[spec] {
			f := strings.Fields(spec)
			ip(ctx, append([]string{"rule", "del", "priority"}, f...)...)
		}
	}
	return nil
}

// syncRoutes points each Rule's table at its chosen route. Call with mu
// held.
func (p *Plane) syncRoutes(ctx context.Context) {
	if p.st == nil || p.st.Bridge == nil {
		return
	}
	routes := chooseRoutes(p.st.Bridge.Rules, p.st.Node, p.cfg.Links.LinkStatus())
	for _, r := range p.st.Bridge.Rules {
		table := ruleTable(r.ID)
		args := []string{"route", "replace", "blackhole", "default"}
		switch rt := routes[r.ID]; rt.kind {
		case local:
			args = []string{"route", "replace", "default", "dev", p.uplink.dev}
			if p.uplink.gateway != "" {
				args = append(args[:3], "via", p.uplink.gateway, "dev", p.uplink.dev)
			}
		case viaLink:
			args = []string{"route", "replace", "default", "dev", rt.iface}
		}
		spec := strings.Join(args[2:], " ")
		if p.routes[table] == spec {
			continue
		}
		if err := ip(ctx, append(args, "table", fmt.Sprint(table))...); err != nil {
			p.log.Error("route rule", "rule", r.Name, "err", err)
			continue
		}
		p.log.Info("rule routed", "rule", r.Name, "route", spec)
		p.routes[table] = spec
	}
}

// syncResolver runs the DNS relay on a Bridge, restarting it when what it
// relays changes. Call with mu held.
func (p *Plane) syncResolver(defaultRule uint64) error {
	b := p.st.Bridge
	key := ""
	if b != nil {
		addr, _ := netip.ParsePrefix(b.Address)
		key = fmt.Sprintf("%s|%s|%d", addr.Addr(), strings.Join(b.Upstreams, ","), defaultRule)
	}
	if key == p.resKey {
		return nil
	}
	if p.resolver != nil {
		p.resolver.stop()
		p.resolver = nil
	}
	p.resKey = ""
	if b == nil {
		return nil
	}
	addr, _ := netip.ParsePrefix(b.Address)
	r := newResolver(net.JoinHostPort(addr.Addr().String(), "53"), b.Upstreams, ruleMark(defaultRule))
	if err := r.start(); err != nil {
		return fmt.Errorf("resolver: %w", err)
	}
	p.resolver, p.resKey = r, key
	p.log.Info("resolver up", "listen", r.addrUDP(), "upstreams", b.Upstreams)
	return nil
}

// ourRules lists the ip rules in this plane's priority band.
func ourRules(ctx context.Context) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "ip", "-j", "rule", "show").Output()
	if err != nil {
		return nil, err
	}
	var rules []struct {
		Priority int    `json:"priority"`
		Fwmark   string `json:"fwmark"`
		Iif      string `json:"iif"`
		Table    string `json:"table"`
	}
	if err := json.Unmarshal(out, &rules); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, r := range rules {
		switch {
		case r.Priority < prioRules || r.Priority > prioDrop:
		case r.Fwmark != "":
			have[fmt.Sprintf("%d fwmark %s lookup %s", r.Priority, r.Fwmark, r.Table)] = true
		case r.Iif != "":
			have[fmt.Sprintf("%d iif %s lookup %s", r.Priority, r.Iif, r.Table)] = true
		}
	}
	return have, nil
}

// setSysctls turns on forwarding, and turns reverse-path filtering off on
// the tunnel interfaces. Their far ends are Devices, held to their own /32
// by WireGuard, or this mesh's own Nodes, which legitimately send replies
// and transit traffic from any source; the filter there only drops
// replies. Interfaces made later inherit the default. The uplink keeps
// its own setting, and "all" is 0 so the per-interface value decides.
func setSysctls() error {
	write := func(k, v string) error {
		if err := os.WriteFile("/proc/sys/net/ipv4/"+k, []byte(v), 0o644); err != nil {
			return fmt.Errorf("sysctl %s: %w", k, err)
		}
		return nil
	}
	if err := write("ip_forward", "1"); err != nil {
		return err
	}
	ifaces, err := os.ReadDir("/proc/sys/net/ipv4/conf")
	if err != nil {
		return err
	}
	uplinkValue := "2"
	for _, e := range ifaces {
		name := e.Name()
		if name == "all" || name == "default" || name == "lo" {
			continue
		}
		if name == DeviceIface || strings.HasPrefix(name, "dz") {
			if err := write("conf/"+name+"/rp_filter", "0"); err != nil {
				return err
			}
		} else if b, err := os.ReadFile("/proc/sys/net/ipv4/conf/" + name + "/rp_filter"); err == nil && strings.TrimSpace(string(b)) == "0" {
			// An uplink inheriting 0 from "all" would lose its filter.
			if err := write("conf/"+name+"/rp_filter", uplinkValue); err != nil {
				return err
			}
		}
	}
	if err := write("conf/default/rp_filter", "0"); err != nil {
		return err
	}
	return write("conf/all/rp_filter", "0")
}

// findUplink is the interface and gateway of the main table's default
// route: the Node's public NIC.
func findUplink(ctx context.Context) (uplink, error) {
	out, err := exec.CommandContext(ctx, "ip", "-j", "route", "show", "default", "table", "main").Output()
	if err != nil {
		return uplink{}, err
	}
	var routes []struct {
		Dev     string `json:"dev"`
		Gateway string `json:"gateway"`
	}
	if err := json.Unmarshal(out, &routes); err != nil {
		return uplink{}, err
	}
	if len(routes) == 0 {
		return uplink{}, errors.New("no default route: cannot tell the uplink")
	}
	return uplink{dev: routes[0].Dev, gateway: routes[0].Gateway}, nil
}

func nft(ctx context.Context, ruleset string) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func ip(ctx context.Context, args ...string) error {
	if out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

func linkExists(ctx context.Context, dev string) bool {
	return exec.CommandContext(ctx, "ip", "link", "show", "dev", dev).Run() == nil
}

func deleteLink(ctx context.Context, dev string) {
	if linkExists(ctx, dev) {
		ip(ctx, "link", "del", "dev", dev)
	}
}
