package ca_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/ca"
)

func csrPEM(t *testing.T) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "spoofed"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestLoadOrCreateKeepsTheSameCA(t *testing.T) {
	dir := t.TempDir()
	a, err := ca.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ca.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.CertPEM()) != string(b.CertPEM()) {
		t.Error("reloading the CA produced a different certificate")
	}
	info, err := os.Stat(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("ca.key mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSignClientNamesTheNodeAndVerifies(t *testing.T) {
	c, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certPEM, serial, err := c.SignClient(csrPEM(t), "nd_abc", now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "nd_abc" {
		t.Errorf("CN = %q, want the Node id, not the CSR's subject", cert.Subject.CommonName)
	}
	if ca.Serial(cert) != serial {
		t.Errorf("serial = %q, want %q", ca.Serial(cert), serial)
	}
	if life := cert.NotAfter.Sub(now); life < 364*24*time.Hour || life > 366*24*time.Hour {
		t.Errorf("certificate lasts %v, want about a year", life)
	}
	_, err = cert.Verify(x509.VerifyOptions{Roots: c.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		t.Errorf("client cert does not verify against the CA: %v", err)
	}

	if _, _, err := c.SignClient([]byte("not a csr"), "nd_abc", now); err == nil {
		t.Error("signing garbage succeeded")
	}
}

func TestServerCertCoversItsHosts(t *testing.T) {
	c, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc, err := c.ServerCert([]string{"4miz.ir", "10.200.1.2"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(tc.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"4miz.ir", "10.200.1.2"} {
		if _, err := cert.Verify(x509.VerifyOptions{DNSName: host, Roots: c.Pool()}); err != nil {
			t.Errorf("server cert for %s: %v", host, err)
		}
	}
}

// A Node's certificate also serves its metrics port (ADR 0006): Prometheus
// checks it by the shared name, the Coordinator by the Node's id.
func TestNodeCertificateServesTheMetricsPort(t *testing.T) {
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certPEM, _, err := authority.SignClient(csrPEM(t), "nd_1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	for _, name := range []string{"nd_1", ca.AgentServerName} {
		if _, err := cert.Verify(x509.VerifyOptions{DNSName: name, Roots: authority.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Errorf("as a server for %s: %v", name, err)
		}
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: authority.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("as a client: %v", err)
	}
}

func TestIssueClient(t *testing.T) {
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := authority.IssueClient(ca.PrometheusName, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert := pair.Leaf
	if cert.Subject.CommonName != ca.PrometheusName {
		t.Errorf("CN = %q", cert.Subject.CommonName)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: authority.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Error(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: authority.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Error("a monitoring client certificate can serve")
	}
}
