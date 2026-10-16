package link

import (
	"context"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeRoundTrip(t *testing.T) {
	typ, seq, _, err := decode(encodeRequest(42))
	if err != nil || typ != probeRequest || seq != 42 {
		t.Fatalf("request = %d, %d, %v", typ, seq, err)
	}
	want := Flags{EgressOK: true, TransitRules: []uint32{1, 7}}
	typ, seq, got, err := decode(encodeReply(43, want))
	if err != nil || typ != probeReply || seq != 43 || got.EgressOK != want.EgressOK || !slices.Equal(got.TransitRules, want.TransitRules) {
		t.Fatalf("reply = %d, %d, %+v, %v", typ, seq, got, err)
	}
	for _, junk := range [][]byte{nil, []byte("DZP"), []byte("XXXX\x01\x00\x00\x00\x01"), encodeReply(1, want)[:12]} {
		if _, _, _, err := decode(junk); err == nil {
			t.Errorf("decoded junk %q", junk)
		}
	}
}

func TestServeProbesAnswersWithCurrentFlags(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var egress atomic.Bool
	go serveProbes(ctx, server, func() Flags { return Flags{EgressOK: egress.Load()} })

	client, err := net.Dial("udp4", server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i, wantEgress := range []bool{false, true} {
		egress.Store(wantEgress)
		client.SetDeadline(time.Now().Add(time.Second))
		client.Write(encodeRequest(uint32(i)))
		buf := make([]byte, 64)
		n, err := client.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		_, seq, f, err := decode(buf[:n])
		if err != nil || seq != uint32(i) || f.EgressOK != wantEgress {
			t.Errorf("reply %d = seq %d, %+v, %v", i, seq, f, err)
		}
	}
}
