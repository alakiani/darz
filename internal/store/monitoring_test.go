package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/store"
)

func TestAlertOutbox(t *testing.T) {
	c := newClock()
	s := open(t, c)
	a, err := s.QueueAlert(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	b, _ := s.QueueAlert(ctx, "second")
	if err := s.AlertFailed(ctx, a, "no healthy Exit"); err != nil {
		t.Fatal(err)
	}
	got, err := s.PendingAlerts(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("PendingAlerts = %+v, %v", got, err)
	}
	if got[0].ID != a || got[0].Text != "first" || got[0].Attempts != 1 || got[0].LastError != "no healthy Exit" ||
		!got[0].ReceivedAt.Equal(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].ID != b || got[1].Attempts != 0 {
		t.Errorf("second = %+v", got[1])
	}
	if err := s.AlertDelivered(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PendingAlerts(ctx); len(got) != 1 || got[0].ID != b {
		t.Errorf("after delivery: %+v", got)
	}
}

func TestBackupRecords(t *testing.T) {
	c := newClock()
	s := open(t, c)
	ok, last, err := s.Backups(ctx)
	if err != nil || ok != nil || last != nil {
		t.Fatalf("no backups yet: %v %v %v", ok, last, err)
	}
	if err := s.RecordBackup(ctx, 1234, ""); err != nil {
		t.Fatal(err)
	}
	c.advance(24 * time.Hour)
	if err := s.RecordBackup(ctx, 0, "upload: timeout"); err != nil {
		t.Fatal(err)
	}
	ok, last, _ = s.Backups(ctx)
	if ok == nil || ok.Size != 1234 || !ok.At.Equal(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("last good = %+v", ok)
	}
	if last == nil || last.Error != "upload: timeout" || !last.At.Equal(c.now()) {
		t.Errorf("last attempt = %+v", last)
	}
}

func TestSnapshotTo(t *testing.T) {
	s := open(t, newClock())
	if _, err := s.CreateNode(ctx, "ir1", nil, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "copy.db")
	if err := s.SnapshotTo(ctx, path); err != nil {
		t.Fatal(err)
	}
	copy, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if _, err := copy.NodeByName(ctx, "ir1"); err != nil {
		t.Errorf("the copy lacks the Node: %v", err)
	}
	if size, err := s.Size(ctx); err != nil || size < 4096 {
		t.Errorf("Size = %d, %v", size, err)
	}
}
