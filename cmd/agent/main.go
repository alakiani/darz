// Command agent runs on every Node. It applies what the Coordinator decides,
// measures Links, and reports health and usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alakiani/darz/internal/agent"
	"github.com/alakiani/darz/internal/protocol"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

// logApplier stands in for the data plane until later sessions build it.
type logApplier struct{ log *slog.Logger }

func (a logApplier) Apply(_ context.Context, s protocol.State) error {
	a.log.Info("state applied (no data plane yet)", "node", s.Node.Name, "roles", s.Node.Roles, "tags", s.Node.Tags)
	return nil
}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	coordinators := flag.String("coordinators", "https://4miz.ir:8443", "comma-separated Agent API URLs, tried in turn")
	caPath := flag.String("ca", "/etc/darz/ca.crt", "pinned Coordinator CA certificate")
	stateDir := flag.String("state-dir", "/var/lib/darz-agent", "key, certificate and last applied state")
	tokenFile := flag.String("enroll-token-file", "", "file holding a one-time enrollment token, used only if not yet enrolled")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *coordinators, *caPath, *stateDir, *tokenFile); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, coordinators, caPath, stateDir, tokenFile string) error {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	var token string
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("agent starting", "version", version)
	return agent.Run(ctx, agent.Config{
		Coordinators:    strings.Split(coordinators, ","),
		CACertPEM:       caPEM,
		StateDir:        stateDir,
		EnrollmentToken: token,
		Applier:         logApplier{log},
		Version:         version,
		Log:             log,
	})
}
