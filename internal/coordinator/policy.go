package coordinator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/alakiani/darz/internal/irfeed"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
)

// DefaultDevicePort is the UDP port Bridges listen on for Devices
// (ADR 0005, amended in MVP 04).
const DefaultDevicePort = 61873

// DefaultUpstreams are the public resolvers abroad that Bridge resolvers
// forward to (ADR 0004).
var DefaultUpstreams = []string{"1.1.1.1", "9.9.9.9"}

// EffectiveState is what a Bridge does with a Device (ADR 0003).
type EffectiveState string

const (
	StateActive    EffectiveState = "active"
	StateBlocked   EffectiveState = "blocked"
	StateThrottled EffectiveState = "throttled" // in the model, not yet enforced
)

// effectiveState is phase one's single policy: a suspended User or a
// revoked Device is blocked, and everything else is active. Later quotas
// read usage totals here too.
func effectiveState(revoked, userSuspended bool) (EffectiveState, string) {
	switch {
	case revoked:
		return StateBlocked, "device revoked"
	case userSuspended:
		return StateBlocked, "user suspended"
	}
	return StateActive, ""
}

// bridgeState is the part of the snapshot every Bridge shares: the Bridge
// identity, the Devices allowed in, and the Rules.
func (c *Coordinator) bridgeState(ctx context.Context) (*protocol.BridgeState, error) {
	key, err := c.bridgeKey(ctx)
	if err != nil {
		return nil, err
	}
	portS, err := c.store.SetSettingIfUnset(ctx, "device_port", strconv.Itoa(DefaultDevicePort))
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portS, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("settings.device_port: %w", err)
	}
	b := &protocol.BridgeState{
		PrivateKey: key,
		ListenPort: uint16(port),
		Address:    netip.PrefixFrom(store.DevicePool.Addr().Next(), store.DevicePool.Bits()).String(),
		Upstreams:  c.Upstreams,
		Devices:    []protocol.Device{},
		Scoring:    c.Scoring,
	}

	devices, err := c.store.Devices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if st, _ := effectiveState(d.Revoked || d.PublicKey == "", d.UserSuspended); st == StateBlocked {
			continue
		}
		b.Devices = append(b.Devices, protocol.Device{PublicKey: d.PublicKey, Address: netip.PrefixFrom(d.Address, 32).String()})
	}

	sets, err := c.store.DestinationSets(ctx)
	if err != nil {
		return nil, err
	}
	hashes := map[uint64]string{}
	for _, s := range sets {
		if s.ContentHash == "" {
			if err := c.store.RefreshSetContent(ctx, s.ID); err != nil {
				return nil, err
			}
			return c.bridgeState(ctx)
		}
		hashes[s.ID] = s.ContentHash
	}
	rules, err := c.store.Rules(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		b.Rules = append(b.Rules, protocol.Rule{ID: r.ID, Name: r.Name, Priority: r.Priority, SetHash: hashes[r.SetID],
			RequiredTags: r.RequiredTags, Fallback: r.Fallback})
	}
	return b, nil
}

// bridgeKey returns the shared Bridge private key, creating it once. It
// lives in the database, so the nightly backup carries it (ADR 0005).
func (c *Coordinator) bridgeKey(ctx context.Context) (string, error) {
	if k, err := c.store.Setting(ctx, "bridge_private_key"); err != nil || k != "" {
		return k, err
	}
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", err
	}
	return c.store.SetSettingIfUnset(ctx, "bridge_private_key", k.String())
}

// BridgePublicKey is what Device configs name as their peer.
func (c *Coordinator) BridgePublicKey(ctx context.Context) (string, error) {
	k, err := c.bridgeKey(ctx)
	if err != nil {
		return "", err
	}
	key, err := wgtypes.ParseKey(k)
	if err != nil {
		return "", err
	}
	return key.PublicKey().String(), nil
}

// isBridge reports whether n holds the Bridge Role.
func isBridge(n store.Node) bool { return slices.Contains(n.Roles, protocol.RoleBridge) }

// setBlob serves a Destination Set's content by hash to Agents.
func (c *Coordinator) setBlob(w http.ResponseWriter, r *http.Request) {
	body, err := c.store.SetBlob(r.Context(), r.PathValue("hash"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such set")
		return
	}
	if err != nil {
		c.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, body)
}

// ErrFeedRejected means an IR feed update failed its gates; the last good
// set stays in force.
var ErrFeedRejected = errors.New("IR feed update rejected")

// RefreshIRFeed fetches the IR feed, checks it against the gates, and makes
// it the IR set's feed content if it passes (ADR 0004).
func (c *Coordinator) RefreshIRFeed(ctx context.Context) error {
	next, err := c.fetchPrefixes(ctx, c.FeedURL, irfeed.Parse)
	if err != nil {
		c.log.Error("IR feed rejected", "err", err)
		return fmt.Errorf("%w: %v", ErrFeedRejected, err)
	}
	sets, err := c.store.DestinationSets(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(sets, func(s store.DestinationSet) bool { return s.Feed == "ir" })
	if i < 0 {
		return errors.New("no Destination Set is fed by the IR feed")
	}
	set := sets[i]
	if err := irfeed.Gate(set.FeedContent, next); err != nil {
		c.log.Error("IR feed rejected; the last good set stays", "err", err)
		return fmt.Errorf("%w: %v", ErrFeedRejected, err)
	}
	if c.RIPEstatURL != "" {
		if ripe, err := c.fetchPrefixes(ctx, c.RIPEstatURL, irfeed.ParseRIPEstat); err != nil {
			c.log.Warn("RIPEstat cross-check failed", "err", err)
		} else if d := irfeed.Divergence(next, ripe, irfeed.KnownExtras); d > 0 {
			c.log.Warn("IR feed diverges from RIPEstat beyond the known extras", "addresses", d)
		}
	}
	if err := c.store.SetFeedContent(ctx, set.ID, next); err != nil {
		return err
	}
	c.log.Info("IR feed accepted", "prefixes", len(next))
	return c.Reconcile(ctx)
}

func (c *Coordinator) fetchPrefixes(ctx context.Context, url string, parse func(io.Reader) ([]netip.Prefix, error)) ([]netip.Prefix, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return parse(io.LimitReader(resp.Body, 32<<20))
}

// RunRollUps folds usage into coarser totals now and then every interval
// until ctx ends (ADR 0003).
func (c *Coordinator) RunRollUps(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := c.store.RollUp(ctx); err != nil {
			c.log.Error("usage roll-up", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunIRFeed refreshes the IR feed now and then every interval until ctx
// ends.
func (c *Coordinator) RunIRFeed(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		c.RefreshIRFeed(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
