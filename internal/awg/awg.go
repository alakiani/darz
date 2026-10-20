// Package awg chooses and checks AmneziaWG instance parameters (ADR 0007).
// The listening end of an instance chooses them; the Coordinator checks
// them before relaying them to the initiating end.
package awg

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alakiani/ways/internal/protocol"
)

type awgParams = protocol.AWGParams

// Limits for AmneziaWG parameters, from amneziawg-go v0.2.19's README and
// packet classifier. Packets stay under a 1280-byte path MTU so the filter
// never sees fragments, which look suspicious.
const (
	maxPacket = 1280
	// maxS4 is the transport padding the instance MTU reserves room for.
	maxS4 = 32
	// Plain WireGuard message types are 1 to 4; headers must never look
	// like them.
	minHeader = 5

	wgInitSize, wgResponseSize, wgCookieSize = 148, 92, 64
)

// ChoosePort returns a random UDP port for a listening instance: between
// 10000 and 60000, which keeps clear of 443 (looks like QUIC, which is
// dropped) and 53 (ADR 0007).
func ChoosePort() uint16 { return uint16(10000 + randInt(50001)) }

// Generate returns random AmneziaWG 2.0 parameters within the ranges
// ValidateAWG accepts.
func Generate() protocol.AWGParams {
	for {
		p := protocol.AWGParams{
			Jc:   4 + randInt(9), // recommended 4 to 12
			Jmin: 40 + randInt(161),
			S1:   15 + randInt(136),
			S2:   15 + randInt(136),
			S3:   15 + randInt(86),
			S4:   1 + randInt(maxS4),
		}
		p.Jmax = p.Jmin + 50 + randInt(1000-p.Jmin-50+1)
		h := disjointRanges()
		p.H1, p.H2, p.H3, p.H4 = h[0], h[1], h[2], h[3]
		p.I1 = decoy()
		for _, i := range []*string{&p.I2, &p.I3, &p.I4, &p.I5} {
			if randInt(2) == 0 {
				*i = decoy()
			}
		}
		if Validate(p) == nil {
			return p
		}
	}
}

// disjointRanges returns four non-overlapping header ranges in random order.
func disjointRanges() [4]string {
	type span struct{ lo, hi uint64 }
	for {
		var spans []span
		for range 4 {
			lo := minHeader + uint64(randInt(1<<32-minHeader-(1<<24)))
			spans = append(spans, span{lo, lo + uint64(randInt(1<<24))})
		}
		sorted := slices.Clone(spans)
		slices.SortFunc(sorted, func(a, b span) int { return int(int64(a.lo) - int64(b.lo)) })
		ok := true
		for i := 1; i < 4; i++ {
			if sorted[i].lo <= sorted[i-1].hi {
				ok = false
			}
		}
		if ok {
			var out [4]string
			for i, s := range spans {
				out[i] = fmt.Sprintf("%d-%d", s.lo, s.hi)
			}
			return out
		}
	}
}

// decoy returns a random custom signature packet: a few tags of static,
// random, random-character, random-digit bytes and timestamps.
func decoy() string {
	var b strings.Builder
	size := 0
	for n := 1 + randInt(4); n > 0; n-- {
		switch randInt(5) {
		case 0:
			k := 2 + randInt(7)
			bytes := make([]byte, k)
			rand.Read(bytes)
			fmt.Fprintf(&b, "<b 0x%s>", hex.EncodeToString(bytes))
			size += k
		case 1:
			b.WriteString("<t>")
			size += 4
		default:
			k := 16 + randInt(185)
			fmt.Fprintf(&b, "<%s %d>", []string{"r", "rc", "rd"}[randInt(3)], k)
			size += k
		}
	}
	return b.String()
}

var tagRE = regexp.MustCompile(`<([a-z]+)(?: ([^>]*))?>`)

// Validate checks parameters against amneziawg-go's rules and WAYS's
// limits.
func Validate(p protocol.AWGParams) error {
	switch {
	case p.Jc < 1 || p.Jc > 128:
		return errors.New("jc must be 1 to 128")
	case p.Jmin < 1 || p.Jmin > p.Jmax:
		return errors.New("need 1 <= jmin <= jmax")
	case p.Jmax >= maxPacket:
		return fmt.Errorf("jmax must stay under %d", maxPacket)
	case p.S1 < 0 || p.S2 < 0 || p.S3 < 0 || p.S4 < 0:
		return errors.New("paddings must be non-negative")
	case wgInitSize+p.S1 > maxPacket || wgResponseSize+p.S2 > maxPacket || wgCookieSize+p.S3 > maxPacket:
		return fmt.Errorf("handshake packets must stay under %d", maxPacket)
	case p.S4 > maxS4:
		return fmt.Errorf("s4 must be at most %d", maxS4)
	case wgInitSize+p.S1 == wgResponseSize+p.S2 || wgInitSize+p.S1 == wgCookieSize+p.S3 || wgResponseSize+p.S2 == wgCookieSize+p.S3:
		// Same-sized handshake messages would be told apart by header alone.
		return errors.New("padded handshake messages must differ in size")
	}

	var spans [][2]uint64
	for i, h := range []string{p.H1, p.H2, p.H3, p.H4} {
		lo, hi, err := parseHRange(h)
		if err != nil {
			return fmt.Errorf("h%d: %w", i+1, err)
		}
		if lo < minHeader {
			return fmt.Errorf("h%d overlaps plain WireGuard's message types", i+1)
		}
		for _, s := range spans {
			if lo <= s[1] && s[0] <= hi {
				return errors.New("header ranges overlap")
			}
		}
		spans = append(spans, [2]uint64{lo, hi})
	}

	for i, spec := range []string{p.I1, p.I2, p.I3, p.I4, p.I5} {
		if spec == "" {
			continue
		}
		size, err := decoySize(spec)
		if err != nil {
			return fmt.Errorf("i%d: %w", i+1, err)
		}
		if size > maxPacket {
			return fmt.Errorf("i%d is %d bytes, over %d", i+1, size, maxPacket)
		}
	}
	return nil
}

func parseHRange(s string) (lo, hi uint64, err error) {
	a, b, isRange := strings.Cut(s, "-")
	if lo, err = strconv.ParseUint(a, 10, 32); err != nil {
		return 0, 0, err
	}
	hi = lo
	if isRange {
		if hi, err = strconv.ParseUint(b, 10, 32); err != nil {
			return 0, 0, err
		}
	}
	if hi < lo {
		return 0, 0, errors.New("range ends before it starts")
	}
	return lo, hi, nil
}

func decoySize(spec string) (int, error) {
	if strings.TrimSpace(tagRE.ReplaceAllString(spec, "")) != "" {
		return 0, errors.New("text outside tags")
	}
	size := 0
	for _, m := range tagRE.FindAllStringSubmatch(spec, -1) {
		switch m[1] {
		case "t":
			size += 4
		case "b":
			h, ok := strings.CutPrefix(m[2], "0x")
			b, err := hex.DecodeString(h)
			if !ok || err != nil {
				return 0, fmt.Errorf("bad bytes tag %q", m[0])
			}
			size += len(b)
		case "r", "rc", "rd":
			n, err := strconv.Atoi(m[2])
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad size in %q", m[0])
			}
			size += n
		default:
			return 0, fmt.Errorf("unknown tag %q", m[0])
		}
	}
	return size, nil
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}
