package link

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// instance is one end of one candidate Transport instance, with the prober
// that measures it.
type instance struct {
	m         *Manager
	linkID    uint64
	spec      protocol.Instance
	transport Transport
	listener  bool
	peerAddr  netip.Addr

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	wakeC    chan struct{}
	interval time.Duration
	bo       backoff

	mu          sync.Mutex
	cfg         InstanceConfig
	dirty       bool // cfg changed since the transport last took it
	meter       meter
	up          bool
	upSince     time.Time
	downSince   time.Time
	flags       Flags // from the peer's latest reply
	lastSuccess time.Time
}

// newInstance returns an instance whose prober runs under ctx. Call with
// the Manager's lock held; start the prober with go run().
func newInstance(ctx context.Context, m *Manager, linkID uint64, spec protocol.Instance, cfg InstanceConfig, tr Transport, listener bool) *instance {
	peer, _ := netip.ParseAddr(spec.PeerAddr)
	in := &instance{m: m, linkID: linkID, spec: spec, transport: tr, listener: listener, peerAddr: peer,
		cfg: cfg, dirty: true, downSince: time.Now(), done: make(chan struct{}), wakeC: make(chan struct{}, 1),
		interval: m.cfg.ProbeInterval,
		bo: backoff{
			min: time.Duration(m.timing.RetryMinSeconds) * time.Second,
			max: time.Duration(m.timing.RetryMaxSeconds) * time.Second,
		}}
	in.ctx, in.cancel = context.WithCancel(ctx)
	return in
}

func (in *instance) iface() string { return in.spec.Iface }

func (in *instance) config() InstanceConfig {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.cfg
}

// reconfigure takes a new configuration, such as a new endpoint for the
// peer; the prober applies it on its next round.
func (in *instance) reconfigure(c InstanceConfig) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !sameConfig(in.cfg, c) {
		in.cfg, in.dirty = c, true
	}
}

func sameConfig(a, b InstanceConfig) bool {
	sameEndpoint := a.Endpoint == nil && b.Endpoint == nil ||
		a.Endpoint != nil && b.Endpoint != nil && a.Endpoint.String() == b.Endpoint.String()
	sameAWG := a.AWG == nil && b.AWG == nil || a.AWG != nil && b.AWG != nil && *a.AWG == *b.AWG
	return a.Iface == b.Iface && a.PrivateKey == b.PrivateKey && a.ListenPort == b.ListenPort &&
		a.PeerKey == b.PeerKey && a.Addr == b.Addr && sameEndpoint && sameAWG
}

func (in *instance) wake() {
	select {
	case in.wakeC <- struct{}{}:
	default:
	}
}

// halt stops the prober and waits for it. The tunnel stays up.
func (in *instance) halt() {
	in.cancel()
	<-in.done
}

func (in *instance) candidate() candidate {
	in.mu.Lock()
	defer in.mu.Unlock()
	return candidate{id: in.spec.ID, up: in.up, upSince: in.upSince}
}

func (in *instance) health(now time.Time) (protocol.InstanceHealth, time.Time, Flags) {
	in.mu.Lock()
	defer in.mu.Unlock()
	h := protocol.InstanceHealth{
		ID:        in.spec.ID,
		Up:        in.up,
		RTTMillis: float64(in.meter.rtt(now).Microseconds()) / 1000,
		Loss:      in.meter.loss(now),
	}
	if !in.up {
		since := in.downSince.UTC()
		h.DownSince = &since
	}
	return h, in.lastSuccess, in.flags
}

// run probes the instance once a second while it is Up or being tried. When
// a try fails to bring it Up within the loss window, it stops probing, so
// no handshake is attempted, and tries again after a backoff (ADR 0007).
func (in *instance) run() {
	defer close(in.done)
	ctx, interval, bo := in.ctx, in.interval, in.bo
	log := in.m.log.With("instance", in.spec.ID, "iface", in.spec.Iface, "transport", in.spec.Transport)

	var conn *net.UDPConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	tryUntil := time.Now().Add(lossWindow)
	var seq uint32
	for ctx.Err() == nil {
		in.mu.Lock()
		cfg, dirty := in.cfg, in.dirty
		in.mu.Unlock()
		if dirty {
			if err := in.transport.Up(ctx, cfg); err != nil {
				log.Warn("bring instance up", "err", err)
				in.backOff(ctx, bo.next())
				continue
			}
			in.mu.Lock()
			in.dirty = !sameConfig(in.cfg, cfg)
			in.mu.Unlock()
			log.Info("instance configured")
		}
		if conn == nil {
			var err error
			conn, err = net.ListenUDP("udp4", &net.UDPAddr{IP: cfg.Addr.Addr().AsSlice()})
			if err != nil {
				log.Warn("probe socket", "err", err)
				sleep(ctx, interval)
				continue
			}
		}

		start := time.Now()
		seq++
		ok, rtt, flags := probe(conn, in.peerAddr, seq, start.Add(interval*9/10))
		handshake, _ := in.transport.LastHandshake(cfg.Iface)
		now := time.Now()

		in.mu.Lock()
		in.meter.record(start, ok, rtt)
		if ok {
			in.flags, in.lastSuccess = flags, now
		}
		up := in.meter.up(now, handshake)
		if up != in.up {
			if up {
				in.upSince, in.downSince = now, time.Time{}
			} else {
				in.downSince, in.upSince = now, time.Time{}
			}
			in.up = up
			log.Info("instance state", "up", up)
		}
		in.mu.Unlock()

		if up {
			bo.reset()
			tryUntil = now.Add(lossWindow)
		} else if now.After(tryUntil) {
			wait := bo.next()
			log.Info("instance not up; backing off", "wait", wait)
			in.backOff(ctx, wait)
			tryUntil = time.Now().Add(lossWindow)
			continue
		}
		// The pace stays one probe per interval whatever arrives: waking
		// here would let two ends' probes trigger each other without end.
		sleep(ctx, time.Until(start.Add(interval)))
	}
}

// backOff waits for d, for ctx to end, or for the peer to probe this
// instance, which shows the peer got through and is worth answering now.
func (in *instance) backOff(ctx context.Context, d time.Duration) {
	select { // forget probes that arrived before the backoff began
	case <-in.wakeC:
	default:
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-in.wakeC:
	}
}

func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// probe sends one probe to the peer and waits for its reply until deadline.
func probe(conn *net.UDPConn, peer netip.Addr, seq uint32, deadline time.Time) (bool, time.Duration, Flags) {
	start := time.Now()
	if _, err := conn.WriteToUDPAddrPort(encodeRequest(seq), netip.AddrPortFrom(peer, ProbePort)); err != nil {
		return false, 0, Flags{}
	}
	conn.SetReadDeadline(deadline)
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return false, 0, Flags{}
		}
		typ, got, f, err := decode(buf[:n])
		if err == nil && typ == probeReply && got == seq {
			return true, time.Since(start), f
		}
	}
}
