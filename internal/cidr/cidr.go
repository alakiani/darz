// Package cidr does set arithmetic on IPv4 prefixes, as Destination Sets
// need: merging, subtracting, sizing, and stripping reserved ranges.
package cidr

import (
	"math/bits"
	"net/netip"
	"slices"
)

// span is an inclusive range of IPv4 addresses.
type span struct{ lo, hi uint64 }

func toSpans(ps []netip.Prefix) []span {
	var out []span
	for _, p := range ps {
		if !p.Addr().Is4() {
			continue
		}
		p = p.Masked()
		lo := value(p.Addr())
		out = append(out, span{lo, lo + 1<<(32-p.Bits()) - 1})
	}
	slices.SortFunc(out, func(a, b span) int {
		switch {
		case a.lo < b.lo:
			return -1
		case a.lo > b.lo:
			return 1
		}
		return 0
	})
	// Coalesce overlapping and adjacent spans.
	var merged []span
	for _, s := range out {
		if n := len(merged); n > 0 && s.lo <= merged[n-1].hi+1 {
			merged[n-1].hi = max(merged[n-1].hi, s.hi)
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

func fromSpans(spans []span) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range spans {
		for lo := s.lo; lo <= s.hi; {
			// The largest aligned block starting at lo that fits in the span.
			size := uint64(1) << 32
			if lo != 0 {
				size = 1 << bits.TrailingZeros64(lo)
			}
			for size > s.hi-lo+1 {
				size >>= 1
			}
			out = append(out, netip.PrefixFrom(addr(lo), 32-bits.TrailingZeros64(size)))
			lo += size
		}
	}
	return out
}

func addr(v uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// Merge returns the smallest sorted list of prefixes covering ps.
func Merge(ps []netip.Prefix) []netip.Prefix { return fromSpans(toSpans(ps)) }

// Subtract returns the addresses in a and not in b, as merged prefixes.
func Subtract(a, b []netip.Prefix) []netip.Prefix {
	as, bs := toSpans(a), toSpans(b)
	var out []span
	for _, s := range as {
		for _, r := range bs {
			if r.hi < s.lo || r.lo > s.hi {
				continue
			}
			if r.lo > s.lo {
				out = append(out, span{s.lo, r.lo - 1})
			}
			if r.hi >= s.hi {
				s.lo = s.hi + 1 // nothing left
				break
			}
			s.lo = r.hi + 1
		}
		if s.lo <= s.hi {
			out = append(out, s)
		}
	}
	return fromSpans(out)
}

// Range returns the prefixes covering the IPv4 addresses from a to b,
// inclusive.
func Range(a, b netip.Addr) []netip.Prefix {
	if !a.Is4() || !b.Is4() || value(a) > value(b) {
		return nil
	}
	return fromSpans([]span{{value(a), value(b)}})
}

func value(a netip.Addr) uint64 {
	b := a.As4()
	return uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
}

// Size is the number of distinct addresses ps covers.
func Size(ps []netip.Prefix) uint64 {
	var n uint64
	for _, s := range toSpans(ps) {
		n += s.hi - s.lo + 1
	}
	return n
}

// Reserved holds the IPv4 ranges that are never Iranian, or anyone's,
// public address space (RFC 6890 and friends).
var Reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"),
}

// StripReserved removes every reserved range from ps.
func StripReserved(ps []netip.Prefix) []netip.Prefix { return Subtract(ps, Reserved) }
