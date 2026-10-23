// Package deploytest runs the deployment playbooks (coordinator.yml and
// enroll.yml) against throwaway Ubuntu 26.04 containers, through the same
// scripts/ways-ansible the operator runs, with release bundles built by
// scripts/build-release.sh.
//
// It needs Docker, ansible-playbook, the wireguard module loaded on the host,
// wayfinder.ir resolving to coordIP, and two releases under WAYS_DEPLOY_RELEASES
// named ci-1 and ci-2. CI's deploy job sets all of that up; elsewhere the
// test skips unless WAYS_DEPLOY_TEST=1.
package deploytest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/alakiani/ways/internal/auth"
	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/testutil"
)

const (
	network = "ways-deploytest"
	subnet  = "172.30.0.0/24"
	coordIP = "172.30.0.10"
	image   = "ways-deploytest"
)

type host struct{ name, group, ip string }

var (
	coordinator = host{"coordinator", "coordinators", coordIP}
	nodes       = []host{
		{"ir-1", "domestic", "172.30.0.11"}, // Bridge and Exit, tagged IR
		{"ab-1", "abroad", "172.30.0.12"},   // Exit
	}
	roles = map[string][]protocol.Role{
		"domestic": {protocol.RoleBridge, protocol.RoleExit},
		"abroad":   {protocol.RoleExit},
	}
	tags = map[string][]string{"domestic": {"IR"}, "abroad": {}}
)

func TestDeploy(t *testing.T) {
	if os.Getenv("WAYS_DEPLOY_TEST") != "1" {
		t.Skip("set WAYS_DEPLOY_TEST=1 (CI's deploy job does)")
	}
	releases := os.Getenv("WAYS_DEPLOY_RELEASES")
	for _, v := range []string{"ci-1", "ci-2"} {
		if !testutil.Exists(filepath.Join(releases, v, "SHA256SUMS")) {
			t.Fatalf("no release %s under WAYS_DEPLOY_RELEASES=%q", v, releases)
		}
	}
	if addrs, _ := net.LookupHost("wayfinder.ir"); len(addrs) != 1 || addrs[0] != coordIP {
		t.Fatalf("wayfinder.ir resolves to %v; point it at %s in /etc/hosts", addrs, coordIP)
	}

	f := newFleet(t, releases)

	// A fresh Coordinator VM.
	out := f.ansible(t, "ci-1", "coordinator.yml")
	invite := regexp.MustCompile(`https://wayfinder\.ir/#/invite/([A-Za-z0-9_-]+)`).FindStringSubmatch(out)
	if invite == nil {
		t.Fatalf("coordinator.yml printed no first Admin Invite")
	}
	for _, file := range []string{"ca.crt", "enroll-token"} {
		if !testutil.Exists(filepath.Join(f.home, file)) {
			t.Fatalf("coordinator.yml brought back no %s", file)
		}
	}
	for _, unit := range []string{"ways-coordinator", "ways-prometheus", "ways-alertmanager", "ways-grafana", "prometheus-node-exporter"} {
		f.eventually(t, coordinator, "systemctl is-active --quiet "+unit, unit+" active")
	}
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:9090/-/ready", "Prometheus ready")
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:3000/grafana/api/health", "Grafana healthy")
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:9090/api/v1/targets | grep -q coordinator", "Prometheus scraping the Coordinator")
	f.grafanaThroughThePanel(t, invite[1])
	if out, err := f.exec(coordinator, "ss -Hltn sport = :9100"); err != nil || !strings.Contains(out, "127.0.0.1:9100") {
		t.Errorf("node_exporter isn't on loopback only: %q", out)
	}

	// Enroll both Nodes, then their Link comes up over the pushed binaries.
	f.ansible(t, "ci-1", "enroll.yml")
	ids := map[string]string{}
	for _, n := range nodes {
		st := f.node(t, n)
		if !st.Enrolled || st.LastHealth == nil || st.LastHealth.AgentVersion != "ci-1" {
			t.Fatalf("%s after enroll.yml: %+v", n.name, st)
		}
		ids[n.name] = st.ID
	}
	f.linksUp(t, ids)
	f.rulesSurviveNetworkd(t, nodes[0])
	if out, err := f.exec(nodes[0], "ss -Hltn sport = :9100"); err != nil || !strings.Contains(out, "127.0.0.1:9100") {
		t.Errorf("node_exporter on %s isn't on loopback only: %q", nodes[0].name, out)
	}

	// Reruns change nothing (ADR 0002: reruns are idempotent).
	for _, pb := range []string{"coordinator.yml", "enroll.yml"} {
		out := f.ansible(t, "ci-1", pb)
		for h, changed := range recap(out) {
			if changed != "0" {
				t.Errorf("rerunning %s changed %s tasks on %s", pb, changed, h)
			}
		}
		// Every Link is up by now, so enroll.yml must not warn about one.
		if pb == "enroll.yml" && strings.Contains(out, "Links on") {
			t.Errorf("enroll.yml warned about Links that are up")
		}
	}
	for _, n := range nodes {
		f.mustExec(t, n, "command -v wg") // wireguard-tools, for debugging Links
	}

	// A release with files that fail SHA256SUMS, like a download cut off
	// half-way, is refused before any host is touched.
	f.brokenRelease(t, "ci-2", "ci-2-broken", "ways-coordinator", "ways-agent")
	for _, pb := range []string{"coordinator.yml", "enroll.yml"} {
		out, err := f.runAnsible("ci-2-broken", pb)
		if err == nil {
			t.Errorf("%s ran with a broken release:\n%s", pb, out)
		}
		for _, file := range []string{"ways-coordinator", "ways-agent"} {
			if !strings.Contains(out, file) {
				t.Errorf("%s didn't name the broken %s:\n%s", pb, file, out)
			}
		}
	}
	if v := strings.TrimSpace(f.mustExec(t, coordinator, "/usr/local/bin/ways-coordinator -version")); v != "ci-1" {
		t.Errorf("Coordinator runs %q after a refused upgrade, want ci-1", v)
	}
	for _, n := range nodes {
		if st := f.node(t, n); st.LastHealth == nil || st.LastHealth.AgentVersion != "ci-1" {
			t.Errorf("%s after a refused upgrade: %+v", n.name, st.LastHealth)
		}
	}

	// Upgrading keeps every Node's identity.
	certs := map[string]string{}
	for _, n := range nodes {
		certs[n.name] = f.mustExec(t, n, "sha256sum /var/lib/ways-agent/agent.crt")
	}
	f.ansible(t, "ci-2", "coordinator.yml")
	if v := strings.TrimSpace(f.mustExec(t, coordinator, "/usr/local/bin/ways-coordinator -version")); v != "ci-2" {
		t.Errorf("Coordinator runs %q after the upgrade, want ci-2", v)
	}
	f.ansible(t, "ci-2", "enroll.yml")
	for _, n := range nodes {
		st := f.node(t, n)
		if st.ID != ids[n.name] || st.LastHealth.AgentVersion != "ci-2" {
			t.Errorf("%s after the upgrade: id %s (was %s), Agent %s", n.name, st.ID, ids[n.name], st.LastHealth.AgentVersion)
		}
		if got := f.mustExec(t, n, "sha256sum /var/lib/ways-agent/agent.crt"); got != certs[n.name] {
			t.Errorf("%s was re-keyed by an upgrade", n.name)
		}
	}
	f.linksUp(t, ids)
}

// fleet is the containers plus the laptop's ~/.ways for them.
type fleet struct {
	home, key, apiCA string
	admin            *testutil.AdminClient
}

func newFleet(t *testing.T, releases string) *fleet {
	dir := t.TempDir()
	f := &fleet{home: filepath.Join(dir, "ways"), key: filepath.Join(dir, "id_ed25519")}
	must(t, os.MkdirAll(filepath.Join(f.home, "releases"), 0o700))
	for _, v := range []string{"ci-1", "ci-2"} {
		must(t, os.Symlink(filepath.Join(releases, v), filepath.Join(f.home, "releases", v)))
	}
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", f.key)
	pub, err := os.ReadFile(f.key + ".pub")
	must(t, err)

	// The public certificate for wayfinder.ir, from a CA the laptop trusts here.
	f.apiCA = filepath.Join(dir, "api-ca.crt")
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	caPEM := writeTLS(t, f.apiCA, certFile, keyFile)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	f.admin = &testutil.AdminClient{URL: "https://wayfinder.ir", Client: &http.Client{
		Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}

	backupKey, err := age.GenerateX25519Identity()
	must(t, err)
	env := fmt.Sprintf("TLS_CERT_FILE='%s'\nTLS_KEY_FILE='%s'\nBACKUP_AGE_RECIPIENT='%s'\nCOORD_EGRESS_IP='%s'\n",
		certFile, keyFile, backupKey.Recipient(), coordIP)
	must(t, os.WriteFile(filepath.Join(f.home, "fleet.env"), []byte(env), 0o600))
	must(t, os.WriteFile(filepath.Join(f.home, "inventory.yml"), []byte(inventory(f.key)), 0o600))

	run(t, "docker", "build", "-q", "-t", image, "testdata")
	exec.Command("docker", "network", "rm", network).Run() // a previous run's
	run(t, "docker", "network", "create", "--subnet", subnet, network)
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })
	for _, h := range append([]host{coordinator}, nodes...) {
		name := "ways-" + h.name
		exec.Command("docker", "rm", "-f", name).Run()
		run(t, "docker", "run", "-d", "--privileged", "--name", name, "--hostname", h.name,
			"--network", network, "--ip", h.ip, "--add-host", "wayfinder.ir:"+coordIP,
			"--tmpfs", "/run", "--tmpfs", "/run/lock", image)
		t.Cleanup(func() {
			if t.Failed() {
				out, _ := f.exec(h, "journalctl --no-pager -n 200 -u 'ways-*'")
				t.Logf("journal on %s:\n%s", h.name, out)
			}
			exec.Command("docker", "rm", "-f", name).Run()
		})
		cmd := exec.Command("docker", "exec", "-i", name, "sh", "-c",
			"mkdir -p -m 700 /root/.ssh && cat > /root/.ssh/authorized_keys")
		cmd.Stdin = bytes.NewReader(pub)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("authorized_keys on %s: %v\n%s", h.name, err, out)
		}
		testutil.Eventually(t, 60*time.Second, "SSH on "+h.name, func() bool {
			return exec.Command("ssh", "-i", f.key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=3", "root@"+h.ip, "true").Run() == nil
		})
	}
	return f
}

// inventory is the file scripts/provision-fleet.sh writes.
func inventory(key string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "all:\n  vars:\n    ansible_ssh_private_key_file: %s\n  children:\n", key)
	for _, group := range []string{"coordinators", "domestic", "abroad"} {
		fmt.Fprintf(&b, "    %s:\n      hosts:\n", group)
		for _, h := range append([]host{coordinator}, nodes...) {
			if h.group == group {
				fmt.Fprintf(&b, "        %s: {ansible_host: %s, ansible_user: root}\n", h.name, h.ip)
			}
		}
	}
	return b.String()
}

// ansible runs a playbook through scripts/ways-ansible, as the operator does.
func (f *fleet) ansible(t *testing.T, release, playbook string) string {
	t.Helper()
	out, err := f.runAnsible(release, playbook)
	t.Logf("%s (%s):\n%s", playbook, release, out)
	if err != nil {
		t.Fatalf("%s (%s): %v", playbook, release, err)
	}
	return out
}

// runAnsible runs a playbook through scripts/ways-ansible, as the operator does.
func (f *fleet) runAnsible(release, playbook string) (string, error) {
	cmd := exec.Command("../../scripts/ways-ansible", playbook,
		"-e", "ways_chrony=false", "-e", "ways_api_ca_path="+f.apiCA)
	cmd.Env = append(os.Environ(), "WAYS_HOME="+f.home, "WAYS_RELEASE="+release,
		"ANSIBLE_HOST_KEY_CHECKING=False", "ANSIBLE_FORCE_COLOR=0", "ANSIBLE_NOCOLOR=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// brokenRelease makes release name, a copy of from whose listed files are
// cut to half their size, as a cut-off download leaves them.
func (f *fleet) brokenRelease(t *testing.T, from, name string, files ...string) {
	t.Helper()
	src, dst := filepath.Join(f.home, "releases", from), filepath.Join(f.home, "releases", name)
	must(t, os.MkdirAll(dst, 0o700))
	entries, err := os.ReadDir(src)
	must(t, err)
	for _, e := range entries {
		must(t, os.Symlink(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())))
	}
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(src, file))
		must(t, err)
		must(t, os.Remove(filepath.Join(dst, file)))
		must(t, os.WriteFile(filepath.Join(dst, file), b[:len(b)/2], 0o755))
	}
}

var recapLine = regexp.MustCompile(`(?m)^(\S+)\s+:\s+ok=\d+\s+changed=(\d+)`)

// recap maps each host in a PLAY RECAP to its changed count.
func recap(out string) map[string]string {
	m := map[string]string{}
	for _, s := range recapLine.FindAllStringSubmatch(out, -1) {
		m[s[1]] = s[2]
	}
	return m
}

// node reads a Node through the Admin API with the laptop's token; creating
// an enrolled Node again is how the token finds its id.
func (f *fleet) node(t *testing.T, n host) protocol.NodeStatus {
	t.Helper()
	if f.admin.Token == "" {
		b, err := os.ReadFile(filepath.Join(f.home, "enroll-token"))
		must(t, err)
		f.admin.Token = strings.TrimSpace(string(b))
	}
	created := f.admin.CreateNode(t, protocol.CreateNodeRequest{Name: n.name, Roles: roles[n.group], Tags: tags[n.group]})
	if created.EnrollmentToken != "" {
		t.Fatalf("%s isn't enrolled: the API minted a token", n.name)
	}
	return f.admin.GetNode(t, created.Node.ID)
}

// linksUp waits until each Node reports every one of its Links Up.
func (f *fleet) linksUp(t *testing.T, ids map[string]string) {
	t.Helper()
	for name, id := range ids {
		testutil.Eventually(t, 3*time.Minute, name+"'s Links up", func() bool {
			st := f.admin.GetNode(t, id)
			if st.LastHealth == nil || len(st.LastHealth.Links) == 0 {
				return false
			}
			for _, l := range st.LastHealth.Links {
				if !l.Up {
					return false
				}
			}
			return true
		})
	}
}

func (f *fleet) exec(h host, script string) (string, error) {
	out, err := exec.Command("docker", "exec", "ways-"+h.name, "sh", "-c", script).CombinedOutput()
	return string(out), err
}

func (f *fleet) mustExec(t *testing.T, h host, script string) string {
	t.Helper()
	out, err := f.exec(h, script)
	if err != nil {
		t.Fatalf("%s on %s: %v\n%s", script, h.name, err, out)
	}
	return out
}

func (f *fleet) eventually(t *testing.T, h host, script, what string) {
	t.Helper()
	testutil.Eventually(t, 90*time.Second, what+" on "+h.name, func() bool {
		_, err := f.exec(h, script)
		return err == nil
	})
}

// writeTLS issues a certificate for wayfinder.ir from a fresh CA and returns the
// CA's PEM.
func writeTLS(t *testing.T, caFile, certFile, keyFile string) []byte {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "deploytest CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	must(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "wayfinder.ir"},
		DNSNames: []string{"wayfinder.ir"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	must(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	must(t, err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	must(t, os.WriteFile(caFile, caPEM, 0o644))
	must(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644))
	must(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return caPEM
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// grafanaThroughThePanel redeems the first Admin's Invite, turns TOTP on,
// and opens the Mesh dashboard at https://wayfinder.ir/grafana/ as that Admin
// (ADR 0006); signed out, /grafana/ sends the browser to the panel.
func (f *fleet) grafanaThroughThePanel(t *testing.T, invite string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := *f.admin.Client
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	signedOut := &testutil.AdminClient{URL: f.admin.URL, Client: &hc}
	if code := signedOut.Do(t, "GET", "/grafana/", nil, nil); code != http.StatusFound {
		t.Errorf("signed out, /grafana/ answered %d, want 302 to the panel", code)
	}

	admin := &testutil.AdminClient{URL: f.admin.URL, Client: &hc}
	if code := admin.Do(t, "POST", "/v1/invites/accept", protocol.AcceptInvite{Token: invite, Handle: "ops", Password: "a long enough password"}, nil); code != 200 {
		t.Fatalf("accepting the first Admin's Invite: %d", code)
	}
	var offer protocol.TOTPOffer
	if code := admin.Do(t, "POST", "/v1/me/totp", nil, &offer); code != 200 {
		t.Fatalf("offering TOTP: %d", code)
	}
	if code := admin.Do(t, "POST", "/v1/me/totp/confirm", protocol.TOTPCode{Code: auth.TOTP(offer.Secret, time.Now())}, nil); code != 200 {
		t.Fatalf("confirming TOTP: %d", code)
	}

	var user struct{ Login string }
	if code := admin.Do(t, "GET", "/grafana/api/user", nil, &user); code != 200 || user.Login != "ops" {
		t.Errorf("Grafana through the panel: %d, signed in as %q, want ops", code, user.Login)
	}
	var dash struct{ Dashboard struct{ Title string } }
	if code := admin.Do(t, "GET", "/grafana/api/dashboards/uid/ways-mesh", nil, &dash); code != 200 || dash.Dashboard.Title != "Mesh" {
		t.Errorf("the Mesh dashboard through the panel: %d, %q", code, dash.Dashboard.Title)
	}

	// The panels have data: Grafana's Prometheus data source answers a query.
	// Grafana 13 reinstalls its bundled plugins at start unless preinstall is
	// off, and in a root-owned install that leaves Prometheus unregistered on
	// about half the starts, so every panel says No Data.
	testutil.Eventually(t, 90*time.Second, "Grafana's Prometheus data source answering through the panel", func() bool {
		now := time.Now()
		q := map[string]any{
			"from": strconv.FormatInt(now.Add(-5*time.Minute).UnixMilli(), 10), "to": strconv.FormatInt(now.UnixMilli(), 10),
			"queries": []map[string]any{{"refId": "A", "expr": `up{job="coordinator"}`, "range": true, "intervalMs": 15000,
				"datasource": map[string]string{"type": "prometheus", "uid": "ways-prometheus"}}},
		}
		var res struct {
			Results map[string]struct {
				Frames []struct {
					Data struct{ Values [][]any }
				}
			}
		}
		if admin.Do(t, "POST", "/grafana/api/ds/query", q, &res) != 200 {
			return false
		}
		for _, fr := range res.Results["A"].Frames {
			if len(fr.Data.Values) > 0 && len(fr.Data.Values[0]) > 0 {
				return true
			}
		}
		return false
	})
	if out, _ := f.exec(coordinator, "journalctl -u ways-grafana -o cat --no-pager | grep 'logger=plugin.backgroundinstaller' | head -3"); out != "" {
		t.Errorf("Grafana tried to install plugins; it must use the bundled ones as they are:\n%s", out)
	}
}

// rulesSurviveNetworkd restarts systemd-networkd on n, as an unattended
// systemd upgrade does, and checks the Agent's ip rules are still there.
// networkd's default deletes routing-policy rules it did not make when it
// configures an interface, so it is given one to manage, as netplan's eth0
// is on a real VM. The Agent is paused meanwhile, so its own repair can't
// hide a lost rule.
func (f *fleet) rulesSurviveNetworkd(t *testing.T, n host) {
	t.Helper()
	f.mustExec(t, n, `ip link show wayst0 >/dev/null 2>&1 || ip link add wayst0 type dummy
printf '[Match]\nName=wayst0\n\n[Network]\nAddress=192.0.2.1/32\n' > /etc/systemd/network/50-wayst0.network`)
	count := "ip rule show | grep -c fwmark || true"
	before := strings.TrimSpace(f.mustExec(t, n, count))
	if before == "0" {
		t.Fatalf("%s has no fwmark rules to keep", n.name)
	}
	f.mustExec(t, n, "systemctl kill -s STOP ways-agent")
	out, err := f.exec(n, "systemctl restart systemd-networkd && sleep 3; "+count)
	f.mustExec(t, n, "systemctl kill -s CONT ways-agent")
	if err != nil {
		t.Fatalf("restarting systemd-networkd on %s: %v: %s", n.name, err, out)
	}
	if after := strings.TrimSpace(out); after != before {
		t.Errorf("restarting systemd-networkd on %s left %s of its %s fwmark rules", n.name, after, before)
	}
}
