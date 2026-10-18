// Command agent runs on every Node. It applies what the Coordinator decides,
// measures Links, and reports health and usage.
package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alakiani/darz/internal/agent"
	"github.com/alakiani/darz/internal/dataplane"
	"github.com/alakiani/darz/internal/link"
	"github.com/alakiani/darz/internal/protocol"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

type options struct {
	coordinators, caPath, stateDir, tokenFile string
	awgBinary, awgRunner                      string
	healthInterval                            time.Duration
	telegramAPI, nodeExporter                 string
	upstreamCA                                string
}

func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.coordinators, "coordinators", "https://4miz.ir:8443", "comma-separated Agent API URLs, tried in turn")
	flag.StringVar(&o.caPath, "ca", "/etc/darz/ca.crt", "pinned Coordinator CA certificate")
	flag.StringVar(&o.stateDir, "state-dir", "/var/lib/darz-agent", "keys, certificate and last applied state")
	flag.StringVar(&o.tokenFile, "enroll-token-file", "", "file holding a one-time enrollment token, used only if not yet enrolled")
	flag.StringVar(&o.awgRunner, "awg-runner", "systemd", "how to run amneziawg-go daemons: systemd (darz-awg@<iface>.service) or exec")
	flag.StringVar(&o.awgBinary, "awg-binary", "/usr/local/lib/darz/amneziawg-go", "amneziawg-go binary, for -awg-runner=exec")
	flag.DurationVar(&o.healthInterval, "health-interval", 30*time.Second, "how often to report health")
	flag.StringVar(&o.telegramAPI, "telegram-api", agent.DefaultTelegramAPI, "Telegram's Bot API, where an Exit relays the Coordinator's alerts")
	flag.StringVar(&o.nodeExporter, "node-exporter", "http://127.0.0.1:9100/metrics", "node_exporter on localhost, served through /metrics; empty for none")
	flag.StringVar(&o.upstreamCA, "upstream-ca", "", "PEM roots for the Bridge resolver's DNS-over-TLS upstreams; empty for the system's")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, o); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, o options) error {
	caPEM, err := os.ReadFile(o.caPath)
	if err != nil {
		return err
	}
	var token string
	if o.tokenFile != "" {
		b, err := os.ReadFile(o.tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	if err := os.MkdirAll(o.stateDir, 0o700); err != nil {
		return err
	}

	var runner link.Runner = link.SystemdRunner{Unit: "darz-awg@%s.service"}
	switch o.awgRunner {
	case "systemd":
	case "exec":
		runner = &link.ExecRunner{Binary: o.awgBinary, LogDir: o.stateDir}
	default:
		return fmt.Errorf("unknown -awg-runner %q", o.awgRunner)
	}
	wg, err := link.NewKernelWireGuard()
	if err != nil {
		return fmt.Errorf("wireguard netlink: %w", err)
	}
	links, err := link.New(link.Config{
		StateDir: o.stateDir,
		Log:      log,
		Transports: map[protocol.Transport]link.Transport{
			protocol.TransportWireGuard: wg,
			protocol.TransportAmneziaWG: link.NewAmneziaWG(runner),
			protocol.TransportGRE:       link.GRE{},
		},
	})
	if err != nil {
		return err
	}
	var upstreamRoots *x509.CertPool
	if o.upstreamCA != "" {
		pem, err := os.ReadFile(o.upstreamCA)
		if err != nil {
			return err
		}
		upstreamRoots = x509.NewCertPool()
		if !upstreamRoots.AppendCertsFromPEM(pem) {
			return errors.New("-upstream-ca: no certificates")
		}
	}
	plane, err := dataplane.New(dataplane.Config{StateDir: o.stateDir, Links: links, Log: log, UpstreamRoots: upstreamRoots})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	linkErr := make(chan error, 1)
	go func() { linkErr <- links.Run(ctx) }()
	go plane.Run(ctx)

	log.Info("agent starting", "version", version, "link_key", links.PublicKey())
	err = agent.Run(ctx, agent.Config{
		Coordinators:    strings.Split(o.coordinators, ","),
		CACertPEM:       caPEM,
		StateDir:        o.stateDir,
		EnrollmentToken: token,
		Applier:         agent.Appliers{links, plane}, // Links first: the data plane routes over them
		Version:         version,
		Log:             log,
		HealthInterval:  o.healthInterval,
		TelegramAPI:     o.telegramAPI,
		NodeExporter:    o.nodeExporter,
	})
	stop()
	if lerr := <-linkErr; err == nil {
		err = lerr
	}
	return err
}
