// Package deploytest runs the deployment playbooks (coordinator.yml and
// enroll.yml) against throwaway Ubuntu 26.04 containers, through the same
// scripts/darz-ansible the operator runs, with release bundles built by
// scripts/build-release.sh.
//
// It needs Docker, ansible-playbook, the wireguard module loaded on the host,
// 4miz.ir resolving to coordIP, and two releases under DARZ_DEPLOY_RELEASES
// named ci-1 and ci-2. CI's deploy job sets all of that up; elsewhere the
// test skips unless DARZ_DEPLOY_TEST=1.
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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/testutil"
)

const (
	network = "darz-deploytest"
	subnet  = "172.30.0.0/24"
	coordIP = "172.30.0.10"
	image   = "darz-deploytest"
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
	if os.Getenv("DARZ_DEPLOY_TEST") != "1" {
		t.Skip("set DARZ_DEPLOY_TEST=1 (CI's deploy job does)")
	}
	releases := os.Getenv("DARZ_DEPLOY_RELEASES")
	for _, v := range []string{"ci-1", "ci-2"} {
		if !testutil.Exists(filepath.Join(releases, v, "SHA256SUMS")) {
			t.Fatalf("no release %s under DARZ_DEPLOY_RELEASES=%q", v, releases)
		}
	}
	if addrs, _ := net.LookupHost("4miz.ir"); len(addrs) != 1 || addrs[0] != coordIP {
		t.Fatalf("4miz.ir resolves to %v; point it at %s in /etc/hosts", addrs, coordIP)
	}

	f := newFleet(t, releases)

	// A fresh Coordinator VM.
	out := f.ansible(t, "ci-1", "coordinator.yml")
	if !strings.Contains(out, "https://4miz.ir/#/invite/") {
		t.Errorf("coordinator.yml printed no first Admin Invite")
	}
	for _, file := range []string{"ca.crt", "enroll-token"} {
		if !testutil.Exists(filepath.Join(f.home, file)) {
			t.Fatalf("coordinator.yml brought back no %s", file)
		}
	}
	for _, unit := range []string{"darz-coordinator", "darz-prometheus", "darz-alertmanager", "darz-grafana", "prometheus-node-exporter"} {
		f.eventually(t, coordinator, "systemctl is-active --quiet "+unit, unit+" active")
	}
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:9090/-/ready", "Prometheus ready")
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:3000/api/health", "Grafana healthy")
	f.eventually(t, coordinator, "curl -fsS http://127.0.0.1:9090/api/v1/targets | grep -q coordinator", "Prometheus scraping the Coordinator")
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
	}

	// Upgrading keeps every Node's identity.
	certs := map[string]string{}
	for _, n := range nodes {
		certs[n.name] = f.mustExec(t, n, "sha256sum /var/lib/darz-agent/agent.crt")
	}
	f.ansible(t, "ci-2", "coordinator.yml")
	if v := strings.TrimSpace(f.mustExec(t, coordinator, "/usr/local/bin/darz-coordinator -version")); v != "ci-2" {
		t.Errorf("Coordinator runs %q after the upgrade, want ci-2", v)
	}
	f.ansible(t, "ci-2", "enroll.yml")
	for _, n := range nodes {
		st := f.node(t, n)
		if st.ID != ids[n.name] || st.LastHealth.AgentVersion != "ci-2" {
			t.Errorf("%s after the upgrade: id %s (was %s), Agent %s", n.name, st.ID, ids[n.name], st.LastHealth.AgentVersion)
		}
		if got := f.mustExec(t, n, "sha256sum /var/lib/darz-agent/agent.crt"); got != certs[n.name] {
			t.Errorf("%s was re-keyed by an upgrade", n.name)
		}
	}
	f.linksUp(t, ids)
}

// fleet is the containers plus the laptop's ~/.darz for them.
type fleet struct {
	home, key, apiCA string
	admin            *testutil.AdminClient
}

func newFleet(t *testing.T, releases string) *fleet {
	dir := t.TempDir()
	f := &fleet{home: filepath.Join(dir, "darz"), key: filepath.Join(dir, "id_ed25519")}
	must(t, os.MkdirAll(filepath.Join(f.home, "releases"), 0o700))
	for _, v := range []string{"ci-1", "ci-2"} {
		must(t, os.Symlink(filepath.Join(releases, v), filepath.Join(f.home, "releases", v)))
	}
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", f.key)
	pub, err := os.ReadFile(f.key + ".pub")
	must(t, err)

	// The public certificate for 4miz.ir, from a CA the laptop trusts here.
	f.apiCA = filepath.Join(dir, "api-ca.crt")
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	caPEM := writeTLS(t, f.apiCA, certFile, keyFile)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	f.admin = &testutil.AdminClient{URL: "https://4miz.ir", Client: &http.Client{
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
		name := "darz-" + h.name
		exec.Command("docker", "rm", "-f", name).Run()
		run(t, "docker", "run", "-d", "--privileged", "--name", name, "--hostname", h.name,
			"--network", network, "--ip", h.ip, "--add-host", "4miz.ir:"+coordIP,
			"--tmpfs", "/run", "--tmpfs", "/run/lock", image)
		t.Cleanup(func() {
			if t.Failed() {
				out, _ := f.exec(h, "journalctl --no-pager -n 200 -u 'darz-*'")
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

// ansible runs a playbook through scripts/darz-ansible, as the operator does.
func (f *fleet) ansible(t *testing.T, release, playbook string) string {
	t.Helper()
	cmd := exec.Command("../../scripts/darz-ansible", playbook,
		"-e", "darz_chrony=false", "-e", "darz_api_ca_path="+f.apiCA)
	cmd.Env = append(os.Environ(), "DARZ_HOME="+f.home, "DARZ_RELEASE="+release,
		"ANSIBLE_HOST_KEY_CHECKING=False", "ANSIBLE_FORCE_COLOR=0", "ANSIBLE_NOCOLOR=1")
	out, err := cmd.CombinedOutput()
	t.Logf("%s (%s):\n%s", playbook, release, out)
	if err != nil {
		t.Fatalf("%s (%s): %v", playbook, release, err)
	}
	return string(out)
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
	out, err := exec.Command("docker", "exec", "darz-"+h.name, "sh", "-c", script).CombinedOutput()
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

// writeTLS issues a certificate for 4miz.ir from a fresh CA and returns the
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
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "4miz.ir"},
		DNSNames: []string{"4miz.ir"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
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
