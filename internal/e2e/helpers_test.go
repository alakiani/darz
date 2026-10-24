//go:build linux

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/ca"
	"github.com/alakiani/ways/internal/coordinator"
	"github.com/alakiani/ways/internal/lab"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
	"github.com/alakiani/ways/internal/testutil"
)

// coord is a Coordinator running in-process, its Agent API listening inside
// a lab Node's namespace.
type coord struct {
	c        *coordinator.Coordinator
	store    *store.Store
	admin    *testutil.AdminClient
	caPEM    []byte
	agentURL string // https://<node>:8443
	tap      *healthTap
}

func startCoordinator(t *testing.T, node *lab.Node) *coord {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ways.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "coordinator.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	dumpOnFailure(t, "coordinator", logPath)
	c := coordinator.New(s, authority, slog.New(slog.NewTextHandler(logFile, nil)))
	c.LongPoll = 2 * time.Second

	addr := net.JoinHostPort(node.Addr().String(), "8443")
	var ln net.Listener
	if err := node.Do(func() (err error) { ln, err = net.Listen("tcp", addr); return err }); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := c.AgentTLSConfig([]string{node.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	tap := &healthTap{}
	srv := &http.Server{Handler: tap.wrap(c.AgentHandler())}
	go srv.Serve(tls.NewListener(ln, tlsConfig))
	t.Cleanup(func() { srv.Close() })

	// The Admin API is the operator's laptop talking to the Coordinator; it
	// does not need to cross the lab.
	adminSrv := httptest.NewServer(c.AdminHandler())
	t.Cleanup(adminSrv.Close)
	secret, _, err := s.CreateAPIToken(context.Background(), store.ScopeNodesEnroll, "laptop", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &coord{c: c, store: s, admin: &testutil.AdminClient{URL: adminSrv.URL, Token: secret},
		caPEM: authority.CertPEM(), agentURL: "https://" + addr, tap: tap}
}

// health returns a Node's latest health report, or nil.
func (co *coord) health(t *testing.T, nodeID string) *protocol.HealthStatus {
	t.Helper()
	return co.admin.GetNode(t, nodeID).LastHealth
}

// dumpOnFailure prints a log file if the test fails.
func dumpOnFailure(t *testing.T, name, path string) {
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(path)
			t.Logf("---- %s log ----\n%s", name, b)
		}
	})
}

// execCommand runs a command in the root namespace.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// egressTargets serves what Exits' egress checks reach (ADR 0008): on each
// host, a resolver that answers every A query with the host's own address,
// and an HTTP endpoint that answers 204. It points the Coordinator's
// targets at them: domestic for IR Exits, abroad for the rest.
func egressTargets(t *testing.T, co *coord, abroad, domestic *lab.Node) {
	t.Helper()
	for _, n := range []*lab.Node{abroad, domestic} {
		serveEgress(t, n)
	}
	co.c.Egress = coordinator.EgressTargets{
		Abroad: protocol.EgressCheck{Resolvers: []string{abroad.Addr().String()}, Hostname: "check.abroad",
			URL: "http://" + abroad.Addr().String() + "/generate_204"},
		IR: protocol.EgressCheck{Resolvers: []string{domestic.Addr().String()}, Hostname: "check.ir",
			URL: "http://" + domestic.Addr().String() + "/"},
	}
}

// serveEgress also answers DNS over TLS, as the resolvers Bridges forward
// to do; it returns the root to trust for that, for -upstream-ca.
func serveEgress(t *testing.T, n *lab.Node) (caFile string) {
	t.Helper()
	var udp net.PacketConn
	var tcp net.Listener
	if err := n.Do(func() (err error) {
		if udp, err = net.ListenPacket("udp4", net.JoinHostPort(n.Addr().String(), "53")); err != nil {
			return err
		}
		tcp, err = net.Listen("tcp4", net.JoinHostPort(n.Addr().String(), "80"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			k, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if answer := answerA(buf[:k], n.Addr().As4()); answer != nil {
				udp.WriteTo(answer, from)
			}
		}
	}()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(tcp)
	return serveDoT(t, n, func(q []byte, _ string) []byte { return answerA(q, n.Addr().As4()) })
}

// serveDoT answers DNS over TLS on n's port 853 with answer, given each
// query and the address it came from (ADR 0009). Its certificate is for
// n's IP address, as public resolvers' are; it returns the file of the
// root that signs it.
func serveDoT(t *testing.T, n *lab.Node, answer func(q []byte, from string) []byte) (caFile string) {
	t.Helper()
	cert, caPEM := ipCert(t, n.Addr())
	caFile = filepath.Join(t.TempDir(), "upstream-ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	var ln net.Listener
	if err := n.Do(func() (err error) {
		ln, err = tls.Listen("tcp4", net.JoinHostPort(n.Addr().String(), "853"), &tls.Config{Certificates: []tls.Certificate{cert}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go serveStreamDNS(ln, answer)
	return caFile
}

// serveStreamDNS answers length-prefixed DNS queries, as many as each
// connection carries, until ln closes.
func serveStreamDNS(ln net.Listener, answer func(q []byte, from string) []byte) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
			for {
				var n [2]byte
				if _, err := io.ReadFull(c, n[:]); err != nil {
					return
				}
				q := make([]byte, int(n[0])<<8|int(n[1]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				a := answer(q, host)
				if a == nil {
					return
				}
				c.Write(append([]byte{byte(len(a) >> 8), byte(len(a))}, a...))
			}
		}()
	}
}

// ipCert is a certificate for addr, signed by a fresh root, and the root
// in PEM.
func ipCert(t *testing.T, addr netip.Addr) (tls.Certificate, []byte) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab resolver root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: addr.String()},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{addr.AsSlice()}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

// answerA answers a DNS query: an A query gets addr, any other type an
// empty NOERROR answer.
func answerA(q []byte, addr [4]byte) []byte {
	if len(q) < 12 {
		return nil
	}
	end := 12
	for end < len(q) && q[end] != 0 {
		end += int(q[end]) + 1
	}
	end += 5 // the root label, then type and class
	if end > len(q) {
		return nil
	}
	isA := q[end-4] == 0 && q[end-3] == 1
	out := append([]byte{}, q[:2]...)   // id
	out = append(out, 0x81, 0x80, 0, 1) // a response, recursion available; one question
	if isA {
		out = append(out, 0, 1, 0, 0, 0, 0) // one answer
	} else {
		out = append(out, 0, 0, 0, 0, 0, 0)
	}
	out = append(out, q[12:end]...)
	if isA {
		out = append(out, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		out = append(out, addr[:]...)
	}
	return out
}

// serveFeed serves a fixed IR feed and returns its URL.
func serveFeed(t *testing.T, body string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
