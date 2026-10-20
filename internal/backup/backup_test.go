package backup

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/alakiani/ways/internal/store"
)

var ctx = context.Background()

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ways.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.CreateNode(ctx, "ir1-secret-name", nil, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestBackupRestores is session 11's "a backup restores": the nightly
// backup is encrypted to the operator's key, unreadable without it, and
// restores to a working database with it.
func TestBackupRestores(t *testing.T) {
	s := newStore(t)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	path, size, err := Write(ctx, s, dir, []age.Recipient{id.Recipient()}, at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "ways-20261016T030000Z.db.age" {
		t.Errorf("backup named %s", path)
	}
	b, _ := os.ReadFile(path)
	if int64(len(b)) != size || !bytes.HasPrefix(b, []byte("age-encryption.org/v1")) {
		t.Errorf("backup is %d bytes, said %d, starting %q", len(b), size, b[:min(len(b), 22)])
	}
	if bytes.Contains(b, []byte("ir1-secret-name")) || bytes.Contains(b, []byte("SQLite format")) {
		t.Error("the backup holds plaintext")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the backup directory holds %d files; the plaintext snapshot must not stay", len(entries))
	}

	// Without the key, nothing.
	other, _ := age.GenerateX25519Identity()
	if err := Restore(bytes.NewReader(b), []age.Identity{other}, filepath.Join(t.TempDir(), "x.db")); err == nil {
		t.Error("restored with the wrong key")
	}

	// With it, the database as it was.
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := Restore(bytes.NewReader(b), []age.Identity{id}, restored); err != nil {
		t.Fatal(err)
	}
	r, err := store.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.NodeByName(ctx, "ir1-secret-name"); err != nil {
		t.Errorf("the restored database lacks the Node: %v", err)
	}
	if err := Restore(bytes.NewReader(b), []age.Identity{id}, restored); err == nil {
		t.Error("Restore overwrote an existing database")
	}
}

func TestPrune(t *testing.T) {
	s := newStore(t)
	id, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, _, err := Write(ctx, s, dir, []age.Recipient{id.Recipient()}, start.Add(time.Duration(i)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "unrelated.txt"), nil, 0o600)
	if err := Prune(dir, 3); err != nil {
		t.Fatal(err)
	}
	var names []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := "unrelated.txt ways-20261003T030000Z.db.age ways-20261004T030000Z.db.age ways-20261005T030000Z.db.age"
	if strings.Join(names, " ") != want {
		t.Errorf("left %v", names)
	}
}

func TestS3Put(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	s3 := &S3{Endpoint: srv.URL, Region: "eu-central-1", Bucket: "ways-backups", AccessKey: "AKID", SecretKey: "secret",
		now: func() time.Time { return time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC) }}
	if err := s3.Put(ctx, "ways-20261016T030000Z.db.age", []byte("ciphertext")); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPut || got.URL.Path != "/ways-backups/ways-20261016T030000Z.db.age" || string(body) != "ciphertext" {
		t.Errorf("%s %s with %q", got.Method, got.URL.Path, body)
	}
	auth := got.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/20261016/eu-central-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Errorf("Authorization: %s", auth)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
	}))
	defer failing.Close()
	s3.Endpoint = failing.URL
	if err := s3.Put(ctx, "x", nil); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err = %v", err)
	}
}

// AWS's documented Signature Version 4 example: GET Object, with a Range
// header (docs.aws.amazon.com, "Examples of signature calculations").
func TestSignV4MatchesAWSExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	signV4(req, emptySHA256, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1",
		time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
