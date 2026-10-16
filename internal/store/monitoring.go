package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PendingAlert is an alert waiting to be relayed to Telegram.
type PendingAlert struct {
	ID         uint64
	Text       string
	ReceivedAt time.Time
	Attempts   int
	LastError  string
}

// QueueAlert keeps an alert until an Exit relays it.
func (s *Store) QueueAlert(ctx context.Context, text string) (uint64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO alert_outbox (text, received_at) VALUES (?, ?)`, text, s.unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return uint64(id), err
}

// PendingAlerts returns every queued alert, oldest first.
func (s *Store) PendingAlerts(ctx context.Context) ([]PendingAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, text, received_at, attempts, last_error FROM alert_outbox ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingAlert
	for rows.Next() {
		var a PendingAlert
		var at int64
		if err := rows.Scan(&a.ID, &a.Text, &at, &a.Attempts, &a.LastError); err != nil {
			return nil, err
		}
		a.ReceivedAt = time.Unix(at, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// AlertDelivered drops a relayed alert.
func (s *Store) AlertDelivered(ctx context.Context, id uint64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_outbox WHERE id = ?`, id)
	return err
}

// AlertFailed counts a failed attempt to relay an alert.
func (s *Store) AlertFailed(ctx context.Context, id uint64, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alert_outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`, errMsg, id)
	return err
}

// Backup is one attempt at the nightly backup.
type Backup struct {
	At    time.Time
	Size  int64  // encrypted bytes
	Error string // empty if it succeeded
}

// keptBackups is how many backup attempts are remembered.
const keptBackups = 60

// RecordBackup notes a backup attempt: its size if it succeeded, or why
// it failed.
func (s *Store) RecordBackup(ctx context.Context, size int64, errMsg string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO backups (at, size_bytes, error) VALUES (?, ?, ?)`, s.unix(), size, errMsg); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM backups WHERE id <= (SELECT max(id) FROM backups) - ?`, keptBackups)
		return err
	})
}

// Backups returns the last successful backup and the last attempt, nil
// for none.
func (s *Store) Backups(ctx context.Context) (lastGood, lastAttempt *Backup, err error) {
	scan := func(query string) (*Backup, error) {
		var b Backup
		var at int64
		err := s.db.QueryRowContext(ctx, query).Scan(&at, &b.Size, &b.Error)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		b.At = time.Unix(at, 0).UTC()
		return &b, err
	}
	if lastGood, err = scan(`SELECT at, size_bytes, error FROM backups WHERE error = '' ORDER BY id DESC LIMIT 1`); err != nil {
		return nil, nil, err
	}
	lastAttempt, err = scan(`SELECT at, size_bytes, error FROM backups ORDER BY id DESC LIMIT 1`)
	return lastGood, lastAttempt, err
}

// SnapshotTo writes a consistent copy of the database to path, which must
// not exist: SQLite's VACUUM INTO, the equivalent of .backup.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// IntegrityCheck runs SQLite's integrity check.
func (s *Store) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("integrity check: " + result)
	}
	return nil
}

// Size is the database's size in bytes.
func (s *Store) Size(ctx context.Context) (int64, error) {
	var pages, size int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size)
	return pages * size, err
}
