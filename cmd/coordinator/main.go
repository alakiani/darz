// Command coordinator runs the DARZ Coordinator: identity, topology, policy and metering for the mesh.
// It never touches a packet on the data path.
//
// Usage:
//
//	coordinator [serve flags]               run the Coordinator
//	coordinator token create [flags]        mint a scoped API token and print its secret once
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alakiani/darz/internal/ca"
	"github.com/alakiani/darz/internal/coordinator"
	"github.com/alakiani/darz/internal/store"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var err error
	if len(os.Args) > 2 && os.Args[1] == "token" && os.Args[2] == "create" {
		err = tokenCreate(os.Args[3:])
	} else {
		err = serve(os.Args[1:], log)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "coordinator:", err)
		os.Exit(1)
	}
}

func serve(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("coordinator", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print the version and exit")
	dbPath := fs.String("db", "/var/lib/darz/darz.db", "SQLite database")
	caDir := fs.String("ca-dir", "/etc/darz/ca", "directory holding ca.crt and ca.key, created if empty")
	adminListen := fs.String("admin-listen", ":443", "Admin API listen address")
	adminCert := fs.String("admin-tls-cert", "", "public TLS certificate for the Admin API; plain HTTP if unset")
	adminKey := fs.String("admin-tls-key", "", "key for -admin-tls-cert")
	agentListen := fs.String("agent-listen", ":8443", "Agent API listen address")
	agentHosts := fs.String("agent-hosts", "4miz.ir", "comma-separated hostnames and IPs Agents dial")
	fs.Parse(args)
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	authority, err := ca.LoadOrCreate(*caDir)
	if err != nil {
		return err
	}
	c := coordinator.New(s, authority, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Reconcile(ctx); err != nil {
		return err
	}
	go c.RunIRFeed(ctx, 24*time.Hour)

	agentTLS, err := c.AgentTLSConfig(strings.Split(*agentHosts, ","))
	if err != nil {
		return err
	}
	agentLn, err := net.Listen("tcp", *agentListen)
	if err != nil {
		return err
	}
	agentSrv := &http.Server{Handler: c.AgentHandler(), ReadHeaderTimeout: 10 * time.Second}
	adminSrv := &http.Server{Addr: *adminListen, Handler: c.AdminHandler(), ReadHeaderTimeout: 10 * time.Second}

	errs := make(chan error, 2)
	go func() { errs <- agentSrv.Serve(tls.NewListener(agentLn, agentTLS)) }()
	go func() {
		if *adminCert != "" {
			errs <- adminSrv.ListenAndServeTLS(*adminCert, *adminKey)
		} else {
			log.Warn("Admin API is plain HTTP; set -admin-tls-cert outside development")
			errs <- adminSrv.ListenAndServe()
		}
	}()
	log.Info("coordinator started", "version", version, "admin", *adminListen, "agent", *agentListen)

	select {
	case err = <-errs:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agentSrv.Shutdown(shutdown)
	adminSrv.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func tokenCreate(args []string) error {
	fs := flag.NewFlagSet("coordinator token create", flag.ExitOnError)
	dbPath := fs.String("db", "/var/lib/darz/darz.db", "SQLite database")
	scope := fs.String("scope", store.ScopeNodesEnroll, "token scope")
	name := fs.String("name", "", "what the token is for, such as laptop (required)")
	ttl := fs.Duration("ttl", 90*24*time.Hour, "how long the token lasts")
	fs.Parse(args)
	if *name == "" {
		return errors.New("token create: -name is required")
	}
	if *scope != store.ScopeNodesEnroll {
		return fmt.Errorf("token create: unknown scope %q", *scope)
	}
	s, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	secret, tok, err := s.CreateAPIToken(context.Background(), *scope, *name, *ttl)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created %s (%s), expires %s\n", tok.ID, tok.Scope, tok.ExpiresAt.Format(time.RFC3339))
	fmt.Println(secret)
	return nil
}
