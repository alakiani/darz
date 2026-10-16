package link

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
)

// ProbePort is the UDP port every Agent answers probes on, on its inner
// addresses (ADR 0007, ADR 0008).
const ProbePort = 51800

// A probe is "DZP" 0x01, a type byte and a sequence number. A reply adds
// the replier's flags: egress OK, and the Rules it carries transit for.
var probeMagic = [4]byte{'D', 'Z', 'P', 1}

const (
	probeRequest = 1
	probeReply   = 2

	flagEgressOK = 1 << 0
	maxRules     = 64
)

// Flags are what a probe reply tells the prober about the replier.
type Flags struct {
	EgressOK     bool
	TransitRules []uint32
}

func encodeRequest(seq uint32) []byte {
	b := make([]byte, 9)
	copy(b, probeMagic[:])
	b[4] = probeRequest
	binary.BigEndian.PutUint32(b[5:], seq)
	return b
}

func encodeReply(seq uint32, f Flags) []byte {
	rules := f.TransitRules[:min(len(f.TransitRules), maxRules)]
	b := make([]byte, 11+4*len(rules))
	copy(b, probeMagic[:])
	b[4] = probeReply
	binary.BigEndian.PutUint32(b[5:], seq)
	if f.EgressOK {
		b[9] |= flagEgressOK
	}
	b[10] = byte(len(rules))
	for i, r := range rules {
		binary.BigEndian.PutUint32(b[11+4*i:], r)
	}
	return b
}

var errNotProbe = errors.New("not a DARZ probe")

func decode(b []byte) (typ byte, seq uint32, f Flags, err error) {
	if len(b) < 9 || [4]byte(b[:4]) != probeMagic {
		return 0, 0, Flags{}, errNotProbe
	}
	typ, seq = b[4], binary.BigEndian.Uint32(b[5:9])
	if typ != probeReply {
		return typ, seq, Flags{}, nil
	}
	if len(b) < 11 || len(b) != 11+4*int(b[10]) {
		return 0, 0, Flags{}, errNotProbe
	}
	f.EgressOK = b[9]&flagEgressOK != 0
	for i := 0; i < int(b[10]); i++ {
		f.TransitRules = append(f.TransitRules, binary.BigEndian.Uint32(b[11+4*i:]))
	}
	return typ, seq, f, nil
}

// serveProbes answers probes on conn until ctx ends, with the flags flags
// returns at the time.
func serveProbes(ctx context.Context, conn net.PacketConn, flags func() Flags) {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 64)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		typ, seq, _, err := decode(buf[:n])
		if err != nil || typ != probeRequest {
			continue
		}
		conn.WriteTo(encodeReply(seq, flags()), from)
	}
}
