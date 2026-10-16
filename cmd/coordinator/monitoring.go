package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/alakiani/darz/internal/backup"
	"github.com/alakiani/darz/internal/coordinator"
)

// configureMonitoring sets up the Coordinator's part in monitoring (ADR
// 0006) from its flags.
func configureMonitoring(c *coordinator.Coordinator, publicCert string, port uint16, from, promTLSDir, tokenFile, chat string) error {
	c.Monitor.PublicCertFile = publicCert
	if from != "" {
		for _, a := range strings.Split(from, ",") {
			addr, err := netip.ParseAddr(strings.TrimSpace(a))
			if err != nil {
				return fmt.Errorf("-monitor-from: %w", err)
			}
			c.Monitor.AllowFrom = append(c.Monitor.AllowFrom, addr.String())
		}
		c.Monitor.Port = port
	}
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return err
		}
		c.Monitor.Telegram = coordinator.TelegramSettings{BotToken: strings.TrimSpace(string(b)), ChatID: chat}
	}
	if promTLSDir != "" {
		return c.WritePrometheusTLS(promTLSDir)
	}
	return nil
}

// configureBackups sets up the nightly backup (ADR 0001) from its flags.
func configureBackups(c *coordinator.Coordinator, recipient, dir string, keep int, endpoint, region, bucket, keyFile string) error {
	if recipient == "" {
		return nil
	}
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return fmt.Errorf("-backup-age-recipient: %w", err)
	}
	c.Backup.Recipients, c.Backup.Dir, c.Backup.Keep = []age.Recipient{r}, dir, keep
	if endpoint == "" {
		return nil
	}
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf("-backup-s3-key-file: %w", err)
	}
	keys := strings.Fields(string(b))
	if len(keys) != 2 || bucket == "" {
		return errors.New("-backup-s3-key-file must hold an access key id and a secret key, and -backup-s3-bucket must be set")
	}
	c.Backup.Upload = &backup.S3{Endpoint: endpoint, Region: region, Bucket: bucket, AccessKey: keys[0], SecretKey: keys[1],
		HTTP: &http.Client{Timeout: 5 * time.Minute}}
	return nil
}

// backupRestore decrypts a backup with the operator's age identity into a
// new database file. It runs on the laptop, where the identity lives.
func backupRestore(args []string) error {
	fs := flag.NewFlagSet("coordinator backup restore", flag.ExitOnError)
	identityFile := fs.String("identity", "", "the operator's age identity file (required)")
	in := fs.String("in", "", "the encrypted backup, darz-<time>.db.age (required)")
	dbPath := fs.String("db", "darz.db", "the database to create; it must not exist")
	fs.Parse(args)
	if *identityFile == "" || *in == "" {
		return errors.New("backup restore: -identity and -in are required")
	}
	idf, err := os.Open(*identityFile)
	if err != nil {
		return err
	}
	defer idf.Close()
	ids, err := age.ParseIdentities(idf)
	if err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := backup.Restore(f, ids, *dbPath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "restored %s into %s\n", *in, *dbPath)
	return nil
}
