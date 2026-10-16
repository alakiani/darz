package dataplane

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// fakeUpstream answers every UDP and TCP query with "answer:" + query.
func fakeUpstream(t *testing.T) string {
	t.Helper()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp4", udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			udp.WriteTo(append([]byte("answer:"), buf[:n]...), from)
		}
	}()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					q, err := readMsg(c)
					if err != nil {
						return
					}
					writeMsg(c, append([]byte("answer:"), q...))
				}
			}()
		}
	}()
	return udp.LocalAddr().String()
}

func TestResolverRelaysUDPAndTCP(t *testing.T) {
	up := fakeUpstream(t)
	host, port, _ := net.SplitHostPort(up)
	r := newResolver("127.0.0.1:0", []string{host}, func() uint32 { return 0 })
	r.upstreamPort = port
	if err := r.start(); err != nil {
		t.Fatal(err)
	}
	defer r.stop()

	c, err := net.Dial("udp4", r.addrUDP())
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte("q1"))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil || !bytes.Equal(buf[:n], []byte("answer:q1")) {
		t.Errorf("UDP answer = %q, %v", buf[:n], err)
	}

	tc, err := net.Dial("tcp4", r.addrTCP())
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	tc.SetDeadline(time.Now().Add(2 * time.Second))
	for _, q := range []string{"q2", "q3"} {
		writeMsg(tc, []byte(q))
		a, err := readMsg(tc)
		if err != nil || string(a) != "answer:"+q {
			t.Errorf("TCP answer = %q, %v", a, err)
		}
	}
}

func TestResolverTriesTheNextUpstream(t *testing.T) {
	up := fakeUpstream(t)
	host, port, _ := net.SplitHostPort(up)
	r := newResolver("127.0.0.1:0", []string{"127.0.0.2", host}, func() uint32 { return 0 })
	r.upstreamPort = port
	if err := r.start(); err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	c, _ := net.Dial("udp4", r.addrUDP())
	c.SetDeadline(time.Now().Add(8 * time.Second))
	c.Write([]byte("q"))
	buf := make([]byte, 64)
	if n, err := c.Read(buf); err != nil || string(buf[:n]) != "answer:q" {
		t.Errorf("answer = %q, %v", buf[:n], err)
	}
}
