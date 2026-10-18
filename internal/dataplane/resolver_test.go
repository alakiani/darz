package dataplane

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// testCert is a self-signed certificate for 127.0.0.1, as public
// resolvers have for their IP addresses, and a pool that trusts it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "resolver"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// fakeUpstream is a DNS-over-TLS resolver: it answers every query with
// "answer:" + query, on as many queries per connection as it is sent. It
// counts the connections it accepted.
func fakeUpstream(t *testing.T, cert tls.Certificate) (string, *atomic.Int32) {
	t.Helper()
	ln, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
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
	return ln.Addr().String(), &conns
}

func startResolver(t *testing.T, roots *x509.CertPool, port string, upstreams ...string) *resolver {
	t.Helper()
	r := newResolver("127.0.0.1:0", upstreams, func() uint32 { return 0 })
	r.roots, r.upstreamPort = roots, port
	if err := r.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.stop)
	return r
}

func askUDP(t *testing.T, r *resolver, q string, timeout time.Duration) (string, error) {
	t.Helper()
	c, err := net.Dial("udp4", r.addrUDP())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	c.Write([]byte(q))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	return string(buf[:n]), err
}

// Devices ask over UDP or TCP; the resolver asks its upstreams over TLS
// on port 853, never plain DNS (ADR 0009, amending ADR 0004).
func TestResolverRelaysUDPAndTCPOverTLS(t *testing.T) {
	cert, roots := testCert(t)
	up, conns := fakeUpstream(t, cert)
	host, port, _ := net.SplitHostPort(up)
	if newResolver("", nil, nil).upstreamPort != "853" {
		t.Error("upstreams are not asked on port 853")
	}
	r := startResolver(t, roots, port, host)

	if got, err := askUDP(t, r, "q1", 2*time.Second); err != nil || got != "answer:q1" {
		t.Errorf("UDP answer = %q, %v", got, err)
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
	for _, q := range []string{"q4", "q5"} {
		if got, err := askUDP(t, r, q, 2*time.Second); err != nil || got != "answer:"+q {
			t.Errorf("UDP answer = %q, %v", got, err)
		}
	}
	// Queries one after another reuse a connection rather than paying a
	// TLS handshake across the filter each time.
	if n := conns.Load(); n != 1 {
		t.Errorf("%d upstream connections for 5 queries in turn, want 1", n)
	}
}

// A connection the upstream closed while it sat idle is replaced, and the
// query still answered.
func TestResolverRedialsAClosedConnection(t *testing.T) {
	cert, roots := testCert(t)
	ln, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // one query per connection, then hang up
				defer c.Close()
				if q, err := readMsg(c); err == nil {
					writeMsg(c, append([]byte("answer:"), q...))
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	r := startResolver(t, roots, port, host)
	for _, q := range []string{"q1", "q2", "q3"} {
		if got, err := askUDP(t, r, q, 3*time.Second); err != nil || got != "answer:"+q {
			t.Errorf("answer = %q, %v", got, err)
		}
	}
}

// The filter could answer in a resolver's name; an upstream whose
// certificate does not verify is not used.
func TestResolverRefusesAnUnverifiedUpstream(t *testing.T) {
	cert, _ := testCert(t)
	_, otherRoots := testCert(t)
	up, _ := fakeUpstream(t, cert)
	host, port, _ := net.SplitHostPort(up)
	r := startResolver(t, otherRoots, port, host)
	if got, err := askUDP(t, r, "q", 2*time.Second); err == nil {
		t.Errorf("answered %q through an upstream that failed verification", got)
	}
}

func TestResolverTriesTheNextUpstream(t *testing.T) {
	cert, roots := testCert(t)
	up, _ := fakeUpstream(t, cert)
	host, port, _ := net.SplitHostPort(up)
	r := startResolver(t, roots, port, "127.0.0.2", host)
	if got, err := askUDP(t, r, "q", 8*time.Second); err != nil || !bytes.Equal([]byte(got), []byte("answer:q")) {
		t.Errorf("answer = %q, %v", got, err)
	}
}
