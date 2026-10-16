package coordinator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/probe"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
)

// SlotDNS points an Endpoint Slot's A record at an address; the
// ArvanCloud client is one (ADR 0005).
type SlotDNS interface {
	SetA(ctx context.Context, host string, addr netip.Addr) error
}

// SlotSettings are ADR 0005's timings for Endpoint Slots.
type SlotSettings struct {
	ProbeEvery time.Duration // how often the probe Device checks each Bridge
	MoveAfter  time.Duration // how long a Bridge stays ineligible before its slots move
	// ReportingWithin is how recent a Bridge's last health report must be
	// for its Agent to count as reporting.
	ReportingWithin time.Duration
	ProbeTimeout    time.Duration
	ProbeHostname   string // what the probe resolves through each Bridge
	// Enter runs fn in the network the probe dials from; nil runs it
	// directly. The lab dials from the Coordinator's namespace.
	Enter func(fn func() error) error
}

// DefaultSlotSettings are ADR 0005's: a probe every 15 s, slots move after
// 60 s. An Agent reports every 30 s, so three missed reports mean it is not
// reporting.
var DefaultSlotSettings = SlotSettings{
	ProbeEvery:      15 * time.Second,
	MoveAfter:       60 * time.Second,
	ReportingWithin: 90 * time.Second,
	ProbeTimeout:    5 * time.Second,
	ProbeHostname:   "www.gstatic.com",
}

// slotState is what the slot loop remembers between rounds. It is not
// stored: after a restart every Bridge starts a fresh 60 s.
type slotState struct {
	mu        sync.Mutex
	probes    map[string]protocol.ProbeResult // by Node id
	inelSince map[string]time.Time            // by Node id, while ineligible
	reasons   map[string][]string             // by Node id, while ineligible
	prober    *probe.Prober
	// dnsFailures counts ArvanCloud API calls failed in a row.
	dnsFailures int
}

// RunEndpointSlots probes every domestic Bridge, judges slot eligibility,
// and re-points slots, every ProbeEvery until ctx ends.
func (c *Coordinator) RunEndpointSlots(ctx context.Context) {
	if c.DNS == nil {
		c.log.Warn("no DNS client: Endpoint Slots are assigned but their records are not updated")
	}
	t := time.NewTicker(c.Slots.ProbeEvery)
	defer t.Stop()
	for {
		if err := c.slotRound(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("endpoint slots", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// slotRound is one round: probe, judge, assign, update DNS.
func (c *Coordinator) slotRound(ctx context.Context) error {
	p, err := c.prober(ctx)
	if err != nil {
		return fmt.Errorf("probe Device: %w", err)
	}
	bridges, health, err := c.domesticBridges(ctx)
	if err != nil {
		return err
	}
	defaultRule, err := c.defaultRuleID(ctx)
	if err != nil {
		return err
	}
	bridgePub, err := c.BridgePublicKey(ctx)
	if err != nil {
		return err
	}
	bridgeKey, err := wgtypes.ParseKey(bridgePub)
	if err != nil {
		return err
	}
	port, err := c.devicePort(ctx)
	if err != nil {
		return err
	}
	resolver := store.DevicePool.Addr().Next()

	// Probe every Bridge at once; one dead Bridge must not hold up the rest.
	results := make([]protocol.ProbeResult, len(bridges))
	var wg sync.WaitGroup
	for i, b := range bridges {
		addr, err := netip.ParseAddr(seenAddr(health[b.ID]))
		if err != nil {
			results[i] = protocol.ProbeResult{At: c.store.Now(), Error: "no public address known yet"}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := p.Check(ctx, netip.AddrPortFrom(addr, port), bridgeKey, resolver)
			results[i] = protocol.ProbeResult{OK: err == nil, At: c.store.Now()}
			if err != nil {
				results[i].Error = err.Error()
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	now := c.store.Now()
	ids := make([]string, len(bridges))
	eligible := map[string]bool{}
	s := &c.slots
	s.mu.Lock()
	for i, b := range bridges {
		ids[i] = b.ID
		s.probes[b.ID] = results[i]
		reasons := ineligibility(results[i], health[b.ID], defaultRule, now, c.Slots.ReportingWithin)
		if !results[i].OK {
			c.log.Debug("probe failed", "node", b.Name, "err", results[i].Error)
		}
		_, was := s.inelSince[b.ID]
		switch {
		case len(reasons) == 0:
			eligible[b.ID] = true
			delete(s.inelSince, b.ID)
			delete(s.reasons, b.ID)
			if was {
				c.log.Info("Bridge regained slot eligibility", "node", b.Name)
			}
		default:
			s.reasons[b.ID] = reasons
			if !was {
				s.inelSince[b.ID] = now
				c.log.Warn("Bridge lost slot eligibility", "node", b.Name, "reasons", reasons, "probe", results[i].Error)
			}
		}
	}
	for id := range s.probes {
		if !slices.Contains(ids, id) {
			delete(s.probes, id)
			delete(s.inelSince, id)
			delete(s.reasons, id)
		}
	}
	inelSince := maps.Clone(s.inelSince)
	s.mu.Unlock()

	stored, err := c.store.EndpointSlots(ctx)
	if err != nil {
		return err
	}
	held := map[string]string{}
	for _, e := range stored {
		held[e.Name] = e.NodeID
	}
	plan := planSlots(c.EndpointSlots, held, ids, eligible, inelSince, now, c.Slots.MoveAfter)
	names := map[string]string{}
	for _, b := range bridges {
		names[b.ID] = b.Name
	}
	for _, slot := range c.EndpointSlots {
		to, from := plan[slot], held[slot]
		if to == "" || to == from {
			continue
		}
		if from == "" {
			c.log.Info("Endpoint Slot assigned", "slot", slot, "bridge", names[to])
		} else {
			c.log.Info("Endpoint Slot re-pointed", "slot", slot, "from", names[from], "to", names[to])
		}
		if err := c.store.AssignSlot(ctx, slot, to); err != nil {
			return err
		}
	}
	return c.syncSlotRecords(ctx, health)
}

// syncSlotRecords makes every slot's DNS record hold its Bridge's public
// address: after a move, after a failed update, or when the Bridge's
// address changed.
func (c *Coordinator) syncSlotRecords(ctx context.Context, health map[string]*protocol.HealthStatus) error {
	if c.DNS == nil {
		return nil
	}
	stored, err := c.store.EndpointSlots(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range stored {
		addr, err := netip.ParseAddr(seenAddr(health[e.NodeID]))
		if err != nil || addr.String() == e.Addr {
			continue
		}
		err = c.DNS.SetA(ctx, e.Name, addr)
		c.slots.mu.Lock()
		if err != nil {
			c.slots.dnsFailures++
		} else {
			c.slots.dnsFailures = 0
		}
		c.slots.mu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name, err))
			continue
		}
		c.log.Info("Endpoint Slot record updated", "slot", e.Name, "addr", addr)
		if err := c.store.SetSlotAddr(ctx, e.Name, addr.String()); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// ineligibility lists why a Bridge may not hold a slot; none means it may
// (ADR 0005).
func ineligibility(p protocol.ProbeResult, h *protocol.HealthStatus, defaultRule uint64, now time.Time, reportingWithin time.Duration) []string {
	var out []string
	if !p.OK {
		out = append(out, "probe failing")
	}
	if h == nil || now.Sub(h.ReceivedAt) > reportingWithin {
		out = append(out, "Agent not reporting")
		return out
	}
	i := slices.IndexFunc(h.Paths, func(r protocol.RuleHealth) bool { return r.RuleID == defaultRule })
	if i < 0 || h.Paths[i].Mode == "" || h.Paths[i].Mode == "fallback" {
		out = append(out, "no default-Rule Path")
	}
	return out
}

// planSlots works out where every slot points next. A slot stays on its
// Bridge until that Bridge has been ineligible for moveAfter, and never
// moves back on its own. A slot that must move, or has no Bridge yet, goes
// to the eligible Bridge holding the fewest slots, the first in bridges on
// a tie. With no eligible Bridge, a slot stays where it is. bridges are the
// domestic Bridges in order.
func planSlots(slots []string, held map[string]string, bridges []string, eligible map[string]bool,
	inelSince map[string]time.Time, now time.Time, moveAfter time.Duration) map[string]string {
	out := map[string]string{}
	load := map[string]int{}
	var moving []string
	for _, s := range slots {
		h := held[s]
		since, inel := inelSince[h]
		stays := h != "" && slices.Contains(bridges, h) && (eligible[h] || inel && now.Sub(since) < moveAfter)
		if stays {
			out[s] = h
			load[h]++
		} else {
			moving = append(moving, s)
		}
	}
	for _, s := range moving {
		best := ""
		for _, b := range bridges {
			if eligible[b] && (best == "" || load[b] < load[best]) {
				best = b
			}
		}
		switch {
		case best != "":
			out[s] = best
			load[best]++
		case held[s] != "":
			out[s] = held[s]
		}
	}
	return out
}

// EndpointSlotStatus reports where the slots point and the probe's view of
// every domestic Bridge, for the status board.
func (c *Coordinator) EndpointSlotStatus(ctx context.Context) (protocol.SlotStatus, error) {
	st := protocol.SlotStatus{Slots: []protocol.SlotTile{}, Bridges: []protocol.SlotBridge{}}
	bridges, _, err := c.domesticBridges(ctx)
	if err != nil {
		return st, err
	}
	nodes, err := c.store.Nodes(ctx)
	if err != nil {
		return st, err
	}
	names := map[string]string{}
	for _, n := range nodes {
		names[n.ID] = n.Name
	}
	stored, err := c.store.EndpointSlots(ctx)
	if err != nil {
		return st, err
	}
	for _, name := range c.EndpointSlots {
		tile := protocol.SlotTile{Name: name}
		if i := slices.IndexFunc(stored, func(e store.EndpointSlot) bool { return e.Name == name }); i >= 0 {
			e := stored[i]
			since := time.Unix(e.Changed, 0).UTC()
			tile.Bridge, tile.Addr, tile.Since, tile.Repoints = names[e.NodeID], e.Addr, &since, e.Repoints
		}
		st.Slots = append(st.Slots, tile)
	}
	s := &c.slots
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bridges {
		sb := protocol.SlotBridge{Name: b.Name, Reasons: s.reasons[b.ID]}
		if p, ok := s.probes[b.ID]; ok {
			sb.Probe = &p
			sb.Eligible = len(sb.Reasons) == 0
		}
		if t, ok := s.inelSince[b.ID]; ok {
			sb.IneligibleSince = &t
		}
		st.Bridges = append(st.Bridges, sb)
	}
	return st, nil
}

// domesticBridges are the Bridges tagged IR, by name, with every Node's
// latest health report. Only they face Devices (ADR 0005).
func (c *Coordinator) domesticBridges(ctx context.Context) ([]store.Node, map[string]*protocol.HealthStatus, error) {
	nodes, err := c.store.Nodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []store.Node
	health := map[string]*protocol.HealthStatus{}
	for _, n := range nodes {
		if !isBridge(n) || !isIR(n) {
			continue
		}
		out = append(out, n)
		if health[n.ID], err = c.store.Health(ctx, n.ID); err != nil {
			return nil, nil, err
		}
	}
	return out, health, nil
}

func (c *Coordinator) defaultRuleID(ctx context.Context) (uint64, error) {
	rules, err := c.store.Rules(ctx)
	if err != nil || len(rules) == 0 {
		return 0, err
	}
	return rules[len(rules)-1].ID, nil // in priority order, the default Rule last
}

func (c *Coordinator) devicePort(ctx context.Context) (uint16, error) {
	s, err := c.store.SetSettingIfUnset(ctx, "device_port", strconv.Itoa(DefaultDevicePort))
	if err != nil {
		return 0, err
	}
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("settings.device_port: %w", err)
	}
	return uint16(port), nil
}

// InitProbe creates the probe Device if it does not exist yet, so the
// first snapshots already admit it.
func (c *Coordinator) InitProbe(ctx context.Context) error {
	_, err := c.prober(ctx)
	return err
}

// prober returns the probe Device, creating it once: a Device belonging
// to no User and counted toward no cap, whose key the Coordinator holds.
// Creating it reconciles, so every Bridge admits it.
func (c *Coordinator) prober(ctx context.Context) (*probe.Prober, error) {
	s := &c.slots
	s.mu.Lock()
	p := s.prober
	s.mu.Unlock()
	if p == nil {
		key, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return nil, err
		}
		k, err := c.store.SetSettingIfUnset(ctx, "probe_private_key", key.String())
		if err != nil {
			return nil, err
		}
		if key, err = wgtypes.ParseKey(k); err != nil {
			return nil, fmt.Errorf("settings.probe_private_key: %w", err)
		}
		addr, err := c.probeDevice(ctx, key.PublicKey().String())
		if err != nil {
			return nil, err
		}
		p = &probe.Prober{PrivateKey: key, Address: addr}
		s.mu.Lock()
		s.prober = p
		s.mu.Unlock()
	}
	p.Hostname, p.Timeout, p.Enter = c.Slots.ProbeHostname, c.Slots.ProbeTimeout, c.Slots.Enter
	return p, nil
}

// probeDevice returns the probe Device's address, adding its row first if
// it has none.
func (c *Coordinator) probeDevice(ctx context.Context, pub string) (netip.Addr, error) {
	devices, err := c.store.Devices(ctx)
	if err != nil {
		return netip.Addr{}, err
	}
	if i := slices.IndexFunc(devices, func(d store.Device) bool { return d.PublicKey == pub }); i >= 0 {
		return devices[i].Address, nil
	}
	d, err := c.store.CreateDevice(ctx, "", "probe", pub)
	if err != nil {
		return netip.Addr{}, err
	}
	c.log.Info("probe Device created", "address", d.Address)
	return d.Address, c.Reconcile(ctx)
}

func seenAddr(h *protocol.HealthStatus) string {
	if h == nil {
		return ""
	}
	return h.SeenAddr
}
