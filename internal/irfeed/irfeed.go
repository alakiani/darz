// Package irfeed parses and checks the IR Destination Set feed (ADR 0004):
// MrAriaNet/Get-IP-Iran's list.rsc, IPv4 lines only. A third-party
// repository is in the trust path, so every update must pass the gates
// before it replaces the last good set.
package irfeed

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strings"

	"github.com/alakiani/darz/internal/cidr"
)

// MaxChange is how far the feed's address space may move from the last
// good set in one update.
const MaxChange = 0.02

// DefaultURL is the feed's raw file.
const DefaultURL = "https://raw.githubusercontent.com/MrAriaNet/Get-IP-Iran/main/list.rsc"

// DefaultRIPEstatURL is RIPEstat's country resource list for Iran, which the
// feed is checked against.
const DefaultRIPEstatURL = "https://stat.ripe.net/data/country-resource-list/data.json?resource=IR"

// KnownExtras are ranges the feed adds to RIPEstat's list on purpose:
// Respina ranges registered abroad but used in Iran (ADR 0004). These are
// the first entries of the feed's IPv4 list as of October 2026; check them
// against the feed's README before relying on the divergence alert.
var KnownExtras = []netip.Prefix{
	netip.MustParsePrefix("5.160.0.0/16"),
	netip.MustParsePrefix("46.209.0.0/16"),
	netip.MustParsePrefix("77.104.64.0/18"),
}

var entryRE = regexp.MustCompile(`\badd address=(\S+) list=`)

// Parse reads the feed and returns its IPv4 prefixes, merged, with private
// and reserved ranges stripped. A malformed entry, or a file with no usable
// IPv4 prefix, is an error.
func Parse(r io.Reader) ([]netip.Prefix, error) {
	var out []netip.Prefix
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		m := entryRE.FindStringSubmatch(sc.Text())
		if m == nil || strings.Contains(m[1], ":") {
			continue // comments, commands, IPv6
		}
		p, err := netip.ParsePrefix(m[1])
		if err != nil || !p.Addr().Is4() {
			return nil, fmt.Errorf("line %d: bad entry %q", n, m[1])
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out = cidr.StripReserved(cidr.Merge(out))
	if len(out) == 0 {
		return nil, errors.New("no IPv4 prefixes in the feed")
	}
	return out, nil
}

// Gate checks an update against the last good set: it must not be empty,
// and its address space must stay within MaxChange of the last good one.
// The first set ever (last is empty) passes if it is not empty.
func Gate(last, next []netip.Prefix) error {
	n := cidr.Size(next)
	if n == 0 {
		return errors.New("empty set")
	}
	l := cidr.Size(last)
	if l == 0 {
		return nil
	}
	change := (float64(n) - float64(l)) / float64(l)
	if change > MaxChange || change < -MaxChange {
		return fmt.Errorf("address space changed by %+.1f%%, more than ±%.0f%%", 100*change, 100*MaxChange)
	}
	return nil
}

// Divergence counts the addresses the feed and RIPEstat disagree on,
// leaving out the feed's known extras.
func Divergence(feed, ripe, extras []netip.Prefix) uint64 {
	feed = cidr.Subtract(feed, extras)
	return cidr.Size(cidr.Subtract(feed, ripe)) + cidr.Size(cidr.Subtract(ripe, feed))
}

// ParseRIPEstat reads RIPEstat's country-resource-list response.
func ParseRIPEstat(r io.Reader) ([]netip.Prefix, error) {
	var body struct {
		Data struct {
			Resources struct {
				IPv4 []string `json:"ipv4"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, s := range body.Data.Resources.IPv4 {
		if lo, hi, ok := strings.Cut(s, "-"); ok {
			a, err1 := netip.ParseAddr(lo)
			b, err2 := netip.ParseAddr(hi)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad range %q", s)
			}
			out = append(out, cidr.Range(a, b)...)
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("bad prefix %q", s)
		}
		out = append(out, p)
	}
	return cidr.StripReserved(cidr.Merge(out)), nil
}
