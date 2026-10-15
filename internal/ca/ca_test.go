package ca_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/ca"
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
