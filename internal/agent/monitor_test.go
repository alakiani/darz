package agent_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/agent"
	"github.com/alakiani/ways/internal/ca"
	"github.com/alakiani/ways/internal/metrics"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

// reporting is an applier that also reports a Link, a Path and usage, and
// writes a data plane metric, like the real Link layer and data plane.
type reporting struct{ applier }

func (r *reporting) Report(_ context.Context, h *protocol.Health) {
	h.Links = append(h.Links, protocol.LinkHealth{LinkID: 7, Up: true, Transport: protocol.TransportAmneziaWG, RTTMillis: 42, Loss: 0.1})
	h.Paths = append(h.Paths, protocol.RuleHealth{RuleID: 2, Mode: "direct", Shares: map[string]int{"link:7": 64}, Eligible: 1})
	hour := time.Now().Add(-5 * time.Hour).Truncate(time.Hour).Unix()
	h.Usage = append(h.Usage, protocol.UsageBucket{Hour: hour, Device: "10.77.0.2", Exit: "nd_x", Up: 1})
}

func (r *reporting) WriteMetrics(w *metrics.Writer) {
	w.Gauge("ways_bridge_active_devices", "Devices.", 3)
}

// fakeTelegram records what reaches Telegram's sendMessage.
type fakeTelegram struct {
	mu   sync.Mutex
	msgs []string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.msgs = append(f.msgs, r.URL.Path+" "+body.ChatID+" "+body.Text)
	f.mu.Unlock()
	io.WriteString(w, `{"ok":true}`)
}

func (f *fakeTelegram) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.msgs, "\n")
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// clientWith returns an HTTPS client presenting a certificate the CA
// issued to name, or none for "".
func clientWith(t *testing.T, rg *rig, name string) *http.Client {
	t.Helper()
	tc := &tls.Config{RootCAs: x509.NewCertPool(), ServerName: ca.AgentServerName}
	tc.RootCAs.AppendCertsFromPEM(rg.caPEM)
	if name != "" {
		certPEM, keyPEM, err := rg.ca.IssueClient(name, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		tc.Certificates = []tls.Certificate{pair}
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tc}}
}

// TestMonitoringPort: the Coordinator opens a Node's monitoring port; only
// Prometheus reads /metrics there and only the Coordinator relays alerts
// through it to Telegram (ADR 0006).
func TestMonitoringPort(t *testing.T) {
	rg := newRig(t)
	ctx := context.Background()
	port := freePort(t)
	tg := &fakeTelegram{}
	tgSrv := httptest.NewServer(tg)
	t.Cleanup(tgSrv.Close)
	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "# TYPE node_load1 gauge\nnode_load1 0.25\n")
	}))
	t.Cleanup(exporter.Close)

	rg.coord.Monitor.Port = port
	rg.coord.Monitor.AllowFrom = []string{"127.0.0.1"}
	rg.coord.Monitor.Telegram.BotToken, rg.coord.Monitor.Telegram.ChatID = "tok", "chat"
	rg.coord.Monitor.RetryEvery = 100 * time.Millisecond
	created := rg.admin.CreateNode(t, protocol.CreateNodeRequest{Name: "x1", Roles: []protocol.Role{"bridge", "exit"}})
	dir := t.TempDir()
	app := &reporting{}
	rg.startAgent(t, dir, created.EnrollmentToken, app, &network{}, func(c *agent.Config) {
		c.MetricsHost = "127.0.0.1"
		c.TelegramAPI = tgSrv.URL
		c.NodeExporter = exporter.URL
	})
	testutil.Eventually(t, 5*time.Second, "the Agent applies the monitoring port", func() bool {
		st, _ := app.last()
		return st.Monitor != nil && st.Monitor.Port == port
	})

	url := fmt.Sprintf("https://127.0.0.1:%d", port)
	prometheus := clientWith(t, rg, ca.PrometheusName)
	var body string
	testutil.Eventually(t, 5*time.Second, "Prometheus scrapes the Agent", func() bool {
		resp, err := prometheus.Get(url + "/metrics")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		return resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Type") == metrics.ContentType
	})
	for _, want := range []string{
		`ways_agent_info{version="test"} 1`,
		"ways_snapshot_applied_version 1",
		"# TYPE ways_coordinator_last_contact_seconds gauge",
		"ways_snapshot_apply_failures_total 0",
		"ways_client_cert_expiry_timestamp_seconds ",
		`ways_link_up{link_id="7",link="link 7/x1",peer="link 7"} 1`,
		`ways_link_rtt_seconds{link_id="7",link="link 7/x1",peer="link 7"} 0.042`,
		`ways_link_loss_ratio{link_id="7",link="link 7/x1",peer="link 7"} 0.1`,
		`ways_link_transport{link_id="7",link="link 7/x1",peer="link 7",transport="amneziawg"} 1`,
		`ways_path_ok{rule_id="2",rule="Default",default="true"} 1`,
		`ways_path_eligible_candidates{rule_id="2",rule="Default",default="true"} 1`,
		`ways_path_buckets{rule_id="2",rule="Default",default="true",candidate="link 7"} 64`,
		"ways_metering_backlog_seconds ",
		"ways_bridge_active_devices 3",
		"ways_node_exporter_up 1",
		"node_load1 0.25",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "10.77.0.2") {
		t.Error("/metrics names a Device")
	}

	// Nobody else reads /metrics, and only the Coordinator relays.
	coordinator := clientWith(t, rg, ca.CoordinatorName)
	node := testutil.AgentClient(t, rg.caPEM, dir)
	node.Transport.(*http.Transport).TLSClientConfig.ServerName = ca.AgentServerName
	for name, c := range map[string]*http.Client{"the Coordinator": coordinator, "a Node": node} {
		if status := get(t, c, url+"/metrics"); status != http.StatusForbidden {
			t.Errorf("%s reading /metrics: %d, want 403", name, status)
		}
	}
	if _, err := clientWith(t, rg, "").Get(url + "/metrics"); err == nil {
		t.Error("a client without a certificate got an answer")
	}
	msg, _ := json.Marshal(protocol.TelegramMessage{BotToken: "tok", ChatID: "chat", Text: "hello"})
	for name, c := range map[string]*http.Client{"Prometheus": prometheus, "a Node": node} {
		if status := post(t, c, url+"/v1/telegram", msg); status != http.StatusForbidden {
			t.Errorf("%s relaying: %d, want 403", name, status)
		}
	}
	if status := post(t, coordinator, url+"/v1/telegram", msg); status != http.StatusNoContent {
		t.Errorf("the Coordinator relaying: %d, want 204", status)
	}
	if got := tg.all(); got != "/bottok/sendMessage chat hello" {
		t.Errorf("Telegram got %q", got)
	}

	// The whole path: Alertmanager's webhook to the Coordinator, which
	// hands the alert to this Exit, which sends it to Telegram.
	internal := httptest.NewServer(rg.coord.InternalHandler())
	t.Cleanup(internal.Close)
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go rg.coord.RunAlertRelay(relayCtx)
	hook := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"LinkDown","severity":"warning"},"annotations":{"summary":"Link a/b has no working transport"}}]}`
	resp, err := http.Post(internal.URL+"/v1/alerts", "application/json", strings.NewReader(hook))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	testutil.Eventually(t, 5*time.Second, "the alert reaches Telegram", func() bool {
		return strings.Contains(tg.all(), "FIRING warning LinkDown: Link a/b has no working transport")
	})

	// Prometheus finds the Agent through the Coordinator.
	resp, err = http.Get(internal.URL + "/v1/sd/agents")
	if err != nil {
		t.Fatal(err)
	}
	var targets []struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}
	json.NewDecoder(resp.Body).Decode(&targets)
	resp.Body.Close()
	if len(targets) != 1 || targets[0].Targets[0] != fmt.Sprintf("127.0.0.1:%d", port) || targets[0].Labels["node"] != "x1" {
		t.Errorf("targets = %+v", targets)
	}
}

func get(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func post(t *testing.T, c *http.Client, url string, body []byte) int {
	t.Helper()
	resp, err := c.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
