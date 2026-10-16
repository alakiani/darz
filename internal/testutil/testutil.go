// Package testutil holds helpers shared by DARZ tests.
package testutil

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// Eventually polls cond until it holds, failing the test after timeout.
func Eventually(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for: %s", timeout, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Exists reports whether a file exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// AdminClient calls the Admin API with an API token.
type AdminClient struct {
	URL    string
	Token  string
	Client *http.Client // default http.DefaultClient
}

// Do sends a JSON request and decodes a JSON response into out, returning
// the status.
func (a *AdminClient) Do(t testing.TB, method, path string, in, out any) int {
	t.Helper()
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequest(method, a.URL+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// CreateNode calls POST /v1/nodes and fails the test unless it succeeds.
func (a *AdminClient) CreateNode(t testing.TB, req protocol.CreateNodeRequest) protocol.CreateNodeResponse {
	t.Helper()
	var resp protocol.CreateNodeResponse
	if status := a.Do(t, http.MethodPost, "/v1/nodes", req, &resp); status != http.StatusOK {
		t.Fatalf("create node %s: status %d", req.Name, status)
	}
	return resp
}

// GetNode calls GET /v1/nodes/{id} and fails the test unless it succeeds.
func (a *AdminClient) GetNode(t testing.TB, id string) protocol.NodeStatus {
	t.Helper()
	var resp protocol.NodeStatus
	if status := a.Do(t, http.MethodGet, "/v1/nodes/"+id, nil, &resp); status != http.StatusOK {
		t.Fatalf("get node %s: status %d", id, status)
	}
	return resp
}

// AgentClient returns a client for the Agent API that pins caPEM and
// presents the certificate an Agent stored in dir.
func AgentClient(t testing.TB, caPEM []byte, dir string) *http.Client {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}},
	}}
}
