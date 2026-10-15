// Package ca is the Coordinator's small certificate authority. It issues each
// Node a client certificate at enrollment and the Agent listener its server
// certificate (ADR 0001, ADR 0002). Agents pin the CA certificate, so no
// public CA is trusted on the Agent channel.
//
// The CA key lives in its own directory, outside the database file, so that
// several Coordinator instances could one day share it.
package ca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ClientCertLifetime is how long a Node's certificate lasts (ADR 0002).
const ClientCertLifetime = 365 * 24 * time.Hour

// CA signs certificates with a key loaded from disk.
type CA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     crypto.Signer
}

// LoadOrCreate loads ca.crt and ca.key from dir, creating both if neither
// exists.
func LoadOrCreate(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		var err error
		if certPEM, keyPEM, err = create(); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
			return nil, err
		}
	case certErr != nil:
		return nil, certErr
	case keyErr != nil:
		return nil, keyErr
	}
	return parse(certPEM, keyPEM)
}

func create() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "DARZ Coordinator CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(20 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func parse(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("ca: no PEM block in ca.crt or ca.key")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca.crt: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca.key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("ca.key: not a signing key")
	}
	return &CA{cert: cert, certPEM: certPEM, key: signer}, nil
}

// CertPEM returns the CA certificate that Agents pin.
func (c *CA) CertPEM() []byte { return c.certPEM }

// Pool returns a pool holding only the CA certificate.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

// SignClient issues a client certificate for the key in a PEM CSR. The
// certificate's identity is nodeID, whatever the CSR asks for.
func (c *CA) SignClient(csrPEM []byte, nodeID string, now time.Time) (certPEM []byte, serial string, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", errors.New("ca: no CERTIFICATE REQUEST PEM block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("ca: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("ca: CSR signature: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: nodeID},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(ClientCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), Serial(cert), nil
}

// ServerCert issues a certificate and key for the Agent listener, valid for
// the given hostnames and IPs. It is made fresh at every start.
func (c *CA) ServerCert(hosts []string, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: "DARZ Coordinator"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(ClientCertLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// Serial is the form a certificate's serial is stored and compared in.
func Serial(cert *x509.Certificate) string { return hex.EncodeToString(cert.SerialNumber.Bytes()) }

func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}
