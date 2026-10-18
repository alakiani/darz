package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// GRE runs plain GRE instances (ADR 0009) as kernel GRE interfaces,
// configured over netlink with ip(8). A tunnel is its pair of outer
// addresses: the local end is the Node's own interface address towards the
// peer, the remote end the peer's seen address. Neither end listens and
// there is no handshake: the first answered in-tunnel probe stands for one.
type GRE struct{}

// greOverhead is the outer IPv4 header and the basic GRE header.
const greOverhead = 24

func (GRE) Up(ctx context.Context, c InstanceConfig) error {
	if !c.Remote.Is4() {
		return errors.New("gre: no IPv4 remote address")
	}
	local, outerMTU, err := routeTo(ctx, c.Remote)
	if err != nil {
		return err
	}
	if linkExists(ctx, c.Iface) {
		cur, err := tunnelEnds(ctx, c.Iface)
		if err == nil && cur.Remote == c.Remote && cur.Local == local {
			return address(ctx, c, outerMTU-greOverhead) // left as it is
		}
		// Moved ends, or not a GRE interface at all: start afresh.
		if err := deleteLink(ctx, c.Iface); err != nil {
			return err
		}
	}
	if _, err := ip(ctx, "link", "add", "dev", c.Iface, "type", "gre",
		"local", local.String(), "remote", c.Remote.String(), "ttl", "64"); err != nil {
		return err
	}
	return address(ctx, c, outerMTU-greOverhead)
}

func (GRE) Down(ctx context.Context, iface string) error { return deleteLink(ctx, iface) }

// LastHandshake reports that GRE has none.
func (GRE) LastHandshake(string) (time.Time, error) { return time.Time{}, ErrNoHandshake }

// routeTo returns the source address and the outer MTU the main table uses
// to reach remote: the Node's own interface address, which works behind a
// 1:1 NAT too.
func routeTo(ctx context.Context, remote netip.Addr) (netip.Addr, int, error) {
	out, err := ip(ctx, "-j", "route", "get", remote.String())
	if err != nil {
		return netip.Addr{}, 0, err
	}
	src, dev, err := parseRouteGet([]byte(out))
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("gre: route to %s: %w", remote, err)
	}
	out, err = ip(ctx, "-j", "link", "show", "dev", dev)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	var links []struct {
		MTU int `json:"mtu"`
	}
	if err := json.Unmarshal([]byte(out), &links); err != nil || len(links) == 0 || links[0].MTU == 0 {
		return netip.Addr{}, 0, fmt.Errorf("gre: no MTU for %s", dev)
	}
	return src, links[0].MTU, nil
}

func parseRouteGet(b []byte) (src netip.Addr, dev string, err error) {
	var routes []struct {
		Dev     string `json:"dev"`
		Prefsrc string `json:"prefsrc"`
	}
	if err := json.Unmarshal(b, &routes); err != nil {
		return netip.Addr{}, "", err
	}
	if len(routes) == 0 || routes[0].Dev == "" {
		return netip.Addr{}, "", errors.New("no route")
	}
	src, err = netip.ParseAddr(routes[0].Prefsrc)
	if err != nil || !src.Is4() {
		return netip.Addr{}, "", fmt.Errorf("no IPv4 source address (%q)", routes[0].Prefsrc)
	}
	return src, routes[0].Dev, nil
}

type greEnds struct{ Local, Remote netip.Addr }

// tunnelEnds reads an existing GRE interface's outer addresses.
func tunnelEnds(ctx context.Context, iface string) (greEnds, error) {
	out, err := ip(ctx, "-j", "-d", "link", "show", "dev", iface)
	if err != nil {
		return greEnds{}, err
	}
	return parseTunnelEnds([]byte(out))
}

func parseTunnelEnds(b []byte) (greEnds, error) {
	var links []struct {
		Info struct {
			Kind string `json:"info_kind"`
			Data struct {
				Local  string `json:"local"`
				Remote string `json:"remote"`
			} `json:"info_data"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal(b, &links); err != nil {
		return greEnds{}, err
	}
	if len(links) == 0 || links[0].Info.Kind != "gre" {
		return greEnds{}, errors.New("not a GRE interface")
	}
	var e greEnds
	e.Local, _ = netip.ParseAddr(links[0].Info.Data.Local)
	e.Remote, _ = netip.ParseAddr(links[0].Info.Data.Remote)
	return e, nil
}
