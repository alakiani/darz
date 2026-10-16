package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/alakiani/darz/internal/backup"
	"github.com/alakiani/darz/internal/protocol"
)

// BackupSettings configure the nightly backup (ADR 0001).
type BackupSettings struct {
	// Recipients are the operator's age keys; none turns backups off.
	Recipients []age.Recipient
	// Dir holds the encrypted backups; the newest Keep stay.
	Dir  string
	Keep int
	// Upload pushes each backup abroad; nil keeps them on the VM only.
	Upload backup.Uploader
	// Every is how often a backup is due.
	Every time.Duration
}

func (b BackupSettings) configured() bool { return len(b.Recipients) > 0 }

// DefaultBackupSettings back up nightly, keeping a week on the VM.
var DefaultBackupSettings = BackupSettings{Dir: "/var/lib/darz/backups", Keep: 7, Every: 24 * time.Hour}

// RunBackups backs up whenever the last good backup is older than Every,
// checking hourly, until ctx ends.
func (c *Coordinator) RunBackups(ctx context.Context) {
	if !c.Backup.configured() {
		c.log.Warn("no age recipient: nightly backups are off")
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := c.backupIfDue(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("backup", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// backupIfDue makes a backup unless a good one is younger than Every. A
// failed attempt is retried at the next check.
func (c *Coordinator) backupIfDue(ctx context.Context) error {
	good, _, err := c.store.Backups(ctx)
	if err != nil {
		return err
	}
	now := c.store.Now()
	if good != nil && now.Sub(good.At) < c.Backup.Every {
		return nil
	}
	size, err := c.backupNow(ctx, now)
	msg := ""
	if err != nil {
		msg, size = err.Error(), 0
	}
	if rerr := c.store.RecordBackup(ctx, size, msg); err == nil {
		err = rerr
	}
	return err
}

func (c *Coordinator) backupNow(ctx context.Context, now time.Time) (int64, error) {
	path, size, err := backup.Write(ctx, c.store, c.Backup.Dir, c.Backup.Recipients, now)
	if err != nil {
		return 0, err
	}
	if c.Backup.Upload != nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		if err := c.Backup.Upload.Put(ctx, filepath.Base(path), data); err != nil {
			return 0, err
		}
	}
	if err := backup.Prune(c.Backup.Dir, c.Backup.Keep); err != nil {
		c.log.Warn("prune old backups", "err", err)
	}
	c.log.Info("backup made", "file", filepath.Base(path), "bytes", size, "uploaded", c.Backup.Upload != nil)
	return size, nil
}

// BackupStatus is the nightly backup for the status board.
func (c *Coordinator) BackupStatus(ctx context.Context) (protocol.BackupStatus, error) {
	st := protocol.BackupStatus{Configured: c.Backup.configured()}
	good, last, err := c.store.Backups(ctx)
	if err != nil {
		return st, err
	}
	if good != nil {
		st.LastSuccess, st.SizeBytes = &good.At, good.Size
	}
	if last != nil {
		st.LastAttempt, st.LastError = &last.At, last.Error
	}
	return st, nil
}
