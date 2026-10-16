package dataplane

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// resolver is the Bridge's DNS relay (ADR 0004, amended in MVP 04). It
// passes Devices' queries, UDP and TCP, to public resolvers abroad and
// their answers back, without parsing or logging anything. Its upstream
// sockets carry the default Rule's mark, so its queries leave through an
// abroad Exit rather than the domestic uplink.
type resolver struct {
	listen       string // host:port
	upstreams    []string
	upstreamPort string        // "53"; tests point it elsewhere
	mark         func() uint32 // the mark upstream sockets carry, read per query

	udpAddr, tcpAddr string // where it ended up listening

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

const (
	upstreamTimeout = 3 * time.Second
	tcpIdle         = 10 * time.Second
)

func newResolver(listen string, upstreams []string, mark func() uint32) *resolver {
	return &resolver{listen: listen, upstreams: upstreams, upstreamPort: "53", mark: mark}
}

// dialer carries the mark in force now, so the resolver's queries follow
// the default Rule's buckets as they move.
func (r *resolver) dialer() *net.Dialer {
	return &net.Dialer{Timeout: upstreamTimeout, Control: markControl(r.mark())}
}

// start listens on both UDP and TCP.
func (r *resolver) start() error {
	ctx, cancel := context.WithCancel(context.Background())
	udp, err := net.ListenPacket("udp4", r.listen)
	if err != nil {
		cancel()
		return err
	}
	// TCP on the port UDP got, so listening on port 0 works too.
	tcp, err := net.Listen("tcp4", udp.LocalAddr().String())
	if err != nil {
		udp.Close()
		cancel()
		return err
	}
	r.cancel = cancel
	r.udpAddr, r.tcpAddr = udp.LocalAddr().String(), tcp.Addr().String()
	r.wg.Add(3)
	go func() {
		defer r.wg.Done()
		<-ctx.Done()
		udp.Close()
		tcp.Close()
	}()
	go r.serveUDP(ctx, udp)
	go func() {
		defer r.wg.Done()
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go r.serveTCP(ctx, c)
		}
	}()
	return nil
}

func (r *resolver) addrUDP() string { return r.udpAddr }
func (r *resolver) addrTCP() string { return r.tcpAddr }

func (r *resolver) stop() {
	if r.cancel != nil {
		r.cancel()
		r.wg.Wait()
	}
}

func (r *resolver) serveUDP(ctx context.Context, conn net.PacketConn) {
	defer r.wg.Done()
	for {
		buf := make([]byte, 4096)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		go func() {
			if answer, err := r.askUDP(ctx, buf[:n]); err == nil {
				conn.WriteTo(answer, from)
			}
		}()
	}
}

// askUDP tries each upstream in turn.
func (r *resolver) askUDP(ctx context.Context, query []byte) ([]byte, error) {
	err := errors.New("no upstream resolver")
	for _, up := range r.upstreams {
		var c net.Conn
		if c, err = r.dialer().DialContext(ctx, "udp4", net.JoinHostPort(up, r.upstreamPort)); err != nil {
			continue
		}
		c.SetDeadline(time.Now().Add(upstreamTimeout))
		buf := make([]byte, 4096)
		var n int
		if _, err = c.Write(query); err == nil {
			n, err = c.Read(buf)
		}
		c.Close()
		if err == nil {
			return buf[:n], nil
		}
	}
	return nil, err
}

// serveTCP relays length-prefixed queries on one connection until it goes
// idle.
func (r *resolver) serveTCP(ctx context.Context, c net.Conn) {
	defer c.Close()
	for {
		c.SetDeadline(time.Now().Add(tcpIdle))
		query, err := readMsg(c)
		if err != nil {
			return
		}
		answer, err := r.askTCP(ctx, query)
		if err != nil {
			return
		}
		if err := writeMsg(c, answer); err != nil {
			return
		}
	}
}

func (r *resolver) askTCP(ctx context.Context, query []byte) ([]byte, error) {
	err := errors.New("no upstream resolver")
	for _, up := range r.upstreams {
		var c net.Conn
		if c, err = r.dialer().DialContext(ctx, "tcp4", net.JoinHostPort(up, r.upstreamPort)); err != nil {
			continue
		}
		c.SetDeadline(time.Now().Add(upstreamTimeout))
		var answer []byte
		if err = writeMsg(c, query); err == nil {
			answer, err = readMsg(c)
		}
		c.Close()
		if err == nil {
			return answer, nil
		}
	}
	return nil, err
}

func readMsg(r io.Reader) ([]byte, error) {
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	msg := make([]byte, n)
	_, err := io.ReadFull(r, msg)
	return msg, err
}

func writeMsg(w io.Writer, msg []byte) error {
	b := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(b, uint16(len(msg)))
	copy(b[2:], msg)
	_, err := w.Write(b)
	return err
}
