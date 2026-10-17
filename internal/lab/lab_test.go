//go:build linux

package lab_test

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/lab"
)

// TestLab is session 1's "proves in the lab" test: the lab brings up 4 empty
// Nodes, applies a netem and a filter rule, and tears everything down.
func TestLab(t *testing.T) {
	l := lab.Start(t, "n1", "n2", "n3", "n4")
	n1, n2, n3, n4 := l.Node("n1"), l.Node("n2"), l.Node("n3"), l.Node("n4")

	// Every Node runs a UDP echo server on two ports.
	for _, n := range l.Nodes() {
		for _, port := range []uint16{5000, 5001} {
			echoServer(t, n, port)
		}
	}

	// Nodes reach each other.
	for _, from := range l.Nodes() {
		for _, to := range l.Nodes() {
			if from != to {
				mustEcho(t, from, to, 5000)
			}
		}
	}

	// Netem delays a node's uplink in both directions.
	{
		l.Netem(n1, lab.Impairment{Delay: 100 * time.Millisecond})
		if rtt := mustEcho(t, n1, n2, 5000); rtt < 200*time.Millisecond {
			t.Errorf("rtt with 100ms netem each way = %v, want >= 200ms", rtt)
		}
		if rtt := mustEcho(t, n3, n4, 5000); rtt >= 50*time.Millisecond {
			t.Errorf("rtt between unimpaired nodes = %v, want < 50ms", rtt)
		}
		l.ClearNetem(n1)
		if rtt := mustEcho(t, n1, n2, 5000); rtt >= 50*time.Millisecond {
			t.Errorf("rtt after clearing netem = %v, want < 50ms", rtt)
		}
	}

	// Netem loss drops packets.
	{
		l.Netem(n1, lab.Impairment{Loss: 100})
		mustNotEcho(t, n1, n2, 5000)
		l.ClearNetem(n1)
		mustEcho(t, n1, n2, 5000)
	}

	// Netem rate limits a node's uplink.
	{
		l.Netem(n1, lab.Impairment{Rate: 8_000_000})
		if mbps := tcpMbps(t, n1, n2, 6000); mbps < 5 || mbps > 9 {
			t.Errorf("TCP from a node limited to 8 Mbit/s ran at %.1f Mbit/s", mbps)
		}
		l.ClearNetem(n1)
		if mbps := tcpMbps(t, n1, n2, 6001); mbps < 50 {
			t.Errorf("TCP after clearing the limit ran at %.1f Mbit/s, want far more than 8", mbps)
		}
	}

	// Counters count matching packets, including ones the filter drops.
	{
		toN4 := l.Count("ip daddr " + n4.Addr().String() + " udp dport 5001")
		rule := l.Filter(lab.DropUDP(n4, 5001))
		mustNotEcho(t, n1, n4, 5001)
		mustEcho(t, n1, n4, 5000)
		rule.Remove()
		if got := toN4.Packets(); got != 1 {
			t.Errorf("counter saw %d packets to n4:5001, want 1", got)
		}
	}

	// Filter drops UDP to one port.
	{
		rule := l.Filter(lab.DropUDP(n2, 5000))
		mustNotEcho(t, n1, n2, 5000)
		mustEcho(t, n1, n2, 5001)
		mustEcho(t, n1, n3, 5000)
		rule.Remove()
		mustEcho(t, n1, n2, 5000)
	}

	// Filter drops everything to one IP.
	{
		rule := l.Filter(lab.DropAll(n3))
		mustNotEcho(t, n1, n3, 5000)
		mustNotEcho(t, n4, n3, 5001)
		mustEcho(t, n1, n4, 5000)
		rule.Remove()
		mustEcho(t, n1, n3, 5000)
	}

	// Teardown removes every namespace.
	{
		namespaces := l.Namespaces()
		l.Close()
		for _, ns := range namespaces {
			if _, err := os.Stat("/run/netns/" + ns); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("namespace %s still exists after Close (stat err: %v)", ns, err)
			}
		}
	}
}

// echoServer runs a UDP echo server inside n's namespace until the test ends.
func echoServer(t *testing.T, n *lab.Node, port uint16) {
	t.Helper()
	var conn net.PacketConn
	err := n.Do(func() (err error) {
		conn, err = net.ListenPacket("udp4", netip.AddrPortFrom(n.Addr(), port).String())
		return err
	})
	if err != nil {
		t.Fatalf("listen on %s:%d: %v", n.Name(), port, err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			k, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			conn.WriteTo(buf[:k], from)
		}
	}()
}

// echo sends one datagram from `from` to `to` and waits for the reply,
// returning the round-trip time.
func echo(from, to *lab.Node, port uint16, timeout time.Duration) (time.Duration, error) {
	var conn net.Conn
	err := from.Do(func() (err error) {
		conn, err = net.Dial("udp4", netip.AddrPortFrom(to.Addr(), port).String())
		return err
	})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	start := time.Now()
	conn.SetDeadline(start.Add(timeout))
	if _, err := conn.Write([]byte("ping")); err != nil {
		return 0, err
	}
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func mustEcho(t *testing.T, from, to *lab.Node, port uint16) time.Duration {
	t.Helper()
	rtt, err := echo(from, to, port, 2*time.Second)
	if err != nil {
		t.Fatalf("echo %s -> %s:%d: %v", from.Name(), to.Name(), port, err)
	}
	return rtt
}

func mustNotEcho(t *testing.T, from, to *lab.Node, port uint16) {
	t.Helper()
	if _, err := echo(from, to, port, 500*time.Millisecond); err == nil {
		t.Fatalf("echo %s -> %s:%d got a reply, want it dropped", from.Name(), to.Name(), port)
	}
}

// tcpMbps sends as much as it can from `from` to `to` over TCP for about 2
// seconds and returns the rate the receiver saw.
func tcpMbps(t *testing.T, from, to *lab.Node, port uint16) float64 {
	t.Helper()
	var ln net.Listener
	if err := to.Do(func() (err error) {
		ln, err = net.Listen("tcp4", netip.AddrPortFrom(to.Addr(), port).String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan float64, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- 0
			return
		}
		defer c.Close()
		start := time.Now()
		n, _ := io.Copy(io.Discard, c)
		got <- float64(n) * 8 / time.Since(start).Seconds() / 1e6
	}()
	var conn net.Conn
	if err := from.Do(func() (err error) {
		conn, err = net.Dial("tcp4", netip.AddrPortFrom(to.Addr(), port).String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := conn.Write(buf); err != nil {
			break
		}
	}
	conn.Close()
	return <-got
}
