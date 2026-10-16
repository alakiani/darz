//go:build linux

// Package lab runs a mesh of Nodes on one Linux host, each Node in its own
// network namespace, for tests that need real packets.
//
// Every Node gets one public IPv4 on an uplink to a shared "internet"
// namespace, which routes between them. Impairments (tc netem) sit on a
// Node's uplink; the filter (nftables) sits in the internet namespace, in the
// path of every packet between two Nodes.
//
// The lab needs root, iproute2, tc with the sch_netem module, and nft. Tests
// that use it skip themselves when not run as root.
package lab

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Lab is a running set of Nodes. Start one with Start; it is torn down when
// the test ends, or earlier by Close. Its methods fail the test that started
// it, so call them from that test's goroutine, not from a subtest.
type Lab struct {
	t      testing.TB
	prefix string // unique per Lab, so labs can run side by side
	wan    string // the internet namespace
	nodes  []*Node

	closeOnce sync.Once
}

// Node is one Node of the lab: a network namespace with one public IPv4.
type Node struct {
	lab    *Lab
	name   string
	ns     string
	addr   netip.Addr
	uplink string // the Node's end of its veth pair, inside ns
	wanEnd string // the internet's end of the same pair, inside the internet namespace
}

// Spec describes one Node. A zero Addr gets the next 10.200.<n>.2.
type Spec struct {
	Name string
	Addr netip.Addr
}

// At is a Spec for a Node with a chosen address, such as a host standing
// for a site in Iran. Each Node gets the /24 around its address, with the
// internet namespace on its .1.
func At(name, addr string) Spec { return Spec{Name: name, Addr: netip.MustParseAddr(addr)} }

// Start brings up one Node per name. It skips the test unless it runs as
// root on Linux, and fails it if the lab cannot be built.
func Start(t testing.TB, names ...string) *Lab {
	t.Helper()
	var specs []Spec
	for _, n := range names {
		specs = append(specs, Spec{Name: n})
	}
	return StartSpecs(t, specs...)
}

// StartSpecs brings up one Node per Spec, like Start.
func StartSpecs(t testing.TB, specs ...Spec) *Lab {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("the lab needs root: run with go test -exec sudo")
	}
	if len(specs) == 0 || len(specs) > 250 {
		t.Fatalf("lab: want 1 to 250 Nodes, got %d", len(specs))
	}

	suffix := make([]byte, 3)
	rand.Read(suffix)
	l := &Lab{t: t, prefix: "darz-" + hex.EncodeToString(suffix)}
	l.wan = l.prefix + "-wan"
	t.Cleanup(l.Close)

	l.ip("netns", "add", l.wan)
	l.run(l.wan, "ip", "link", "set", "lo", "up")
	l.run(l.wan, "sysctl", "-qw", "net.ipv4.ip_forward=1")
	l.nft(`table inet lab {
		chain forward { type filter hook forward priority 0; policy accept; }
	}`)

	seen, subnets := map[string]bool{}, map[netip.Prefix]bool{}
	for i, spec := range specs {
		if seen[spec.Name] {
			t.Fatalf("lab: duplicate Node name %q", spec.Name)
		}
		seen[spec.Name] = true
		addr := spec.Addr
		if !addr.IsValid() {
			addr = netip.AddrFrom4([4]byte{10, 200, byte(i + 1), 2})
		}
		subnet := netip.PrefixFrom(addr, 24).Masked()
		gw := subnet.Addr().Next()
		if subnets[subnet] || !addr.Is4() || addr == gw || addr == subnet.Addr() {
			t.Fatalf("lab: Node %s needs an IPv4 address alone in its /24 and not on .0 or .1, got %s", spec.Name, addr)
		}
		subnets[subnet] = true

		n := &Node{
			lab:    l,
			name:   spec.Name,
			ns:     fmt.Sprintf("%s-n%d", l.prefix, i+1),
			addr:   addr,
			uplink: "wan0",
			wanEnd: fmt.Sprintf("v%d", i+1),
		}
		gateway := gw.String()
		l.ip("netns", "add", n.ns)
		l.run(n.ns, "ip", "link", "set", "lo", "up")
		l.run(l.wan, "ip", "link", "add", n.wanEnd, "type", "veth", "peer", "name", n.uplink, "netns", n.ns)
		l.run(l.wan, "ip", "addr", "add", gateway+"/24", "dev", n.wanEnd)
		l.run(l.wan, "ip", "link", "set", n.wanEnd, "up")
		l.run(n.ns, "ip", "addr", "add", netip.PrefixFrom(n.addr, 24).String(), "dev", n.uplink)
		l.run(n.ns, "ip", "link", "set", n.uplink, "up")
		l.run(n.ns, "ip", "route", "add", "default", "via", gateway)
		l.nodes = append(l.nodes, n)
	}
	return l
}

// Node returns the Node with the given name, failing the test if there is none.
func (l *Lab) Node(name string) *Node {
	l.t.Helper()
	for _, n := range l.nodes {
		if n.name == name {
			return n
		}
	}
	l.t.Fatalf("lab: no Node named %q", name)
	return nil
}

// Nodes returns every Node, in the order they were started.
func (l *Lab) Nodes() []*Node { return append([]*Node(nil), l.nodes...) }

// Namespaces returns the names of every network namespace the lab created.
func (l *Lab) Namespaces() []string {
	ns := []string{l.wan}
	for _, n := range l.nodes {
		ns = append(ns, n.ns)
	}
	return ns
}

// Close tears the lab down. Deleting a namespace deletes its interfaces,
// qdiscs and nftables tables with it. Close is safe to call more than once.
func (l *Lab) Close() {
	l.closeOnce.Do(func() {
		// Kill what runs in the namespaces first: a daemon that outlives
		// its namespace would hold on to sockets the next lab reuses.
		for _, ns := range l.Namespaces() {
			out, _ := exec.Command("ip", "netns", "pids", ns).Output()
			for _, pid := range strings.Fields(string(out)) {
				exec.Command("kill", "-9", pid).Run()
			}
		}
		for _, ns := range l.Namespaces() {
			if out, err := exec.Command("ip", "netns", "del", ns).CombinedOutput(); err != nil &&
				!bytes.Contains(out, []byte("No such file")) {
				l.t.Errorf("lab: delete namespace %s: %v: %s", ns, err, out)
			}
		}
	})
}

// Impairment is what tc netem does to packets on a Node's uplink. It applies
// in each direction, so a Delay of 50ms adds 100ms to every round trip.
type Impairment struct {
	Delay  time.Duration
	Jitter time.Duration
	Loss   float64 // percent, 0 to 100
	// Rate limits the uplink to this many bits per second, with a queue
	// of about 50 ms beyond the delay, as on a real bottleneck: a deep
	// queue would delay probes by seconds while a throughput test runs.
	Rate uint64
}

// Netem impairs n's uplink, replacing any earlier impairment.
func (l *Lab) Netem(n *Node, imp Impairment) {
	l.t.Helper()
	args := []string{"netem"}
	if imp.Delay > 0 || imp.Jitter > 0 {
		args = append(args, "delay", tcTime(imp.Delay))
		if imp.Jitter > 0 {
			args = append(args, tcTime(imp.Jitter))
		}
	}
	if imp.Loss > 0 {
		args = append(args, "loss", fmt.Sprintf("%g%%", imp.Loss))
	}
	if imp.Rate > 0 {
		args = append(args, "rate", fmt.Sprintf("%dbit", imp.Rate))
		// netem's limit counts packets, delayed ones included.
		queue := (2*imp.Delay + imp.Jitter + 50*time.Millisecond).Seconds()
		args = append(args, "limit", fmt.Sprint(max(16, int(float64(imp.Rate)/8*queue/1500))))
	}
	l.run(n.ns, append([]string{"tc", "qdisc", "replace", "dev", n.uplink, "root"}, args...)...)
	l.run(l.wan, append([]string{"tc", "qdisc", "replace", "dev", n.wanEnd, "root"}, args...)...)
}

// ClearNetem removes any impairment from n's uplink.
func (l *Lab) ClearNetem(n *Node) {
	l.t.Helper()
	for _, end := range []struct{ ns, dev string }{{n.ns, n.uplink}, {l.wan, n.wanEnd}} {
		out, err := exec.Command("ip", "netns", "exec", end.ns, "tc", "qdisc", "del", "dev", end.dev, "root").CombinedOutput()
		// Deleting the default qdisc fails harmlessly when nothing was set.
		if err != nil && !bytes.Contains(out, []byte("Cannot delete qdisc with handle of zero")) &&
			!bytes.Contains(out, []byte("No such file")) {
			l.t.Fatalf("lab: clear netem on %s: %v: %s", end.dev, err, out)
		}
	}
}

func tcTime(d time.Duration) string { return fmt.Sprintf("%dus", d.Microseconds()) }

// FilterMatch describes packets the filter drops. Build one with DropUDP or DropAll.
type FilterMatch struct{ expr string }

// DropUDP matches UDP packets to n on the given port.
func DropUDP(n *Node, port uint16) FilterMatch {
	return FilterMatch{fmt.Sprintf("ip daddr %s udp dport %d", n.addr, port)}
}

// DropAll matches every packet to n's IP.
func DropAll(n *Node) FilterMatch {
	return FilterMatch{fmt.Sprintf("ip daddr %s", n.addr)}
}

// DropBetween matches every packet between two Nodes, in both directions.
func DropBetween(a, b *Node) FilterMatch {
	set := fmt.Sprintf("{ %s, %s }", a.addr, b.addr)
	return FilterMatch{fmt.Sprintf("ip saddr %s ip daddr %s", set, set)}
}

// FilterRule is one rule installed in the filter.
type FilterRule struct {
	lab    *Lab
	handle string
	once   sync.Once
}

var handleRE = regexp.MustCompile(`# handle (\d+)`)

// Filter installs a rule in the filter that drops matching packets between
// any two Nodes, until the rule is removed or the lab closes.
func (l *Lab) Filter(m FilterMatch) *FilterRule {
	l.t.Helper()
	out := l.run(l.wan, "nft", "--echo", "--handle", "add", "rule", "inet", "lab", "forward", m.expr, "drop")
	h := handleRE.FindStringSubmatch(out)
	if h == nil {
		l.t.Fatalf("lab: no rule handle in nft output %q", out)
	}
	return &FilterRule{lab: l, handle: h[1]}
}

// Remove deletes the rule. It is safe to call more than once.
func (r *FilterRule) Remove() {
	r.lab.t.Helper()
	r.once.Do(func() {
		r.lab.run(r.lab.wan, "nft", "delete", "rule", "inet", "lab", "forward", "handle", r.handle)
	})
}

// Counter counts packets between Nodes that match an nftables expression.
type Counter struct {
	lab    *Lab
	handle string
}

var packetsRE = regexp.MustCompile(`counter packets (\d+)`)

// Count starts counting packets that match expr, an nftables match such as
// "ip saddr 10.200.1.2 udp dport 53". Counters sit ahead of filter rules,
// so they also count packets the filter then drops.
func (l *Lab) Count(expr string) *Counter {
	l.t.Helper()
	out := l.run(l.wan, "nft", "--echo", "--handle", "insert", "rule", "inet", "lab", "forward", expr, "counter")
	h := handleRE.FindStringSubmatch(out)
	if h == nil {
		l.t.Fatalf("lab: no rule handle in nft output %q", out)
	}
	return &Counter{lab: l, handle: h[1]}
}

// Packets returns how many packets have matched so far.
func (c *Counter) Packets() int {
	c.lab.t.Helper()
	out := c.lab.run(c.lab.wan, "nft", "--handle", "list", "chain", "inet", "lab", "forward")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "# handle "+c.handle) {
			if m := packetsRE.FindStringSubmatch(line); m != nil {
				n := 0
				fmt.Sscan(m[1], &n)
				return n
			}
		}
	}
	c.lab.t.Fatalf("lab: counter %s not found in\n%s", c.handle, out)
	return 0
}

// Name returns the Node's name as given to Start.
func (n *Node) Name() string { return n.name }

// Addr returns the Node's public IPv4.
func (n *Node) Addr() netip.Addr { return n.addr }

// Namespace returns the name of the Node's network namespace.
func (n *Node) Namespace() string { return n.ns }

// Command returns a command that runs inside the Node's namespace.
func (n *Node) Command(name string, args ...string) *exec.Cmd {
	return exec.Command("ip", append([]string{"netns", "exec", n.ns, name}, args...)...)
}

// Do runs fn on an OS thread switched into the Node's namespace. Sockets fn
// opens stay in the namespace after Do returns; goroutines fn starts do not
// run in it.
func (n *Node) Do(fn func() error) error {
	runtime.LockOSThread()
	orig, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(orig)
	target, err := unix.Open("/run/netns/"+n.ns, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("open netns %s: %w", n.ns, err)
	}
	defer unix.Close(target)
	if err := unix.Setns(target, unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("enter netns %s: %w", n.ns, err)
	}
	defer func() {
		// If the thread cannot go back, leave it locked: the runtime then
		// destroys it when this goroutine exits, instead of reusing it.
		if unix.Setns(orig, unix.CLONE_NEWNET) == nil {
			runtime.UnlockOSThread()
		}
	}()
	return fn()
}

// ip runs an ip(8) command in the root namespace.
func (l *Lab) ip(args ...string) string {
	l.t.Helper()
	return l.exec(exec.Command("ip", args...))
}

// run runs a command inside namespace ns.
func (l *Lab) run(ns string, args ...string) string {
	l.t.Helper()
	return l.exec(exec.Command("ip", append([]string{"netns", "exec", ns}, args...)...))
}

// nft loads a ruleset into the internet namespace.
func (l *Lab) nft(ruleset string) {
	l.t.Helper()
	cmd := exec.Command("ip", "netns", "exec", l.wan, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	l.exec(cmd)
}

func (l *Lab) exec(cmd *exec.Cmd) string {
	l.t.Helper()
	out, err := cmd.CombinedOutput()
	if err != nil {
		l.t.Fatalf("lab: %s: %v\n%s", strings.Join(cmd.Args, " "), err, out)
	}
	return string(out)
}
