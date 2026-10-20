// Package backup makes the Coordinator's nightly backup (ADR 0001): a
// consistent copy of the SQLite database, encrypted with age to a key held
// only on the operator's laptop, so a seized host cannot read its own
// backups, and pushed to object storage abroad.
package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/alakiani/ways/internal/store"
)

const (
	prefix = "ways-"
	suffix = ".db.age"
	stamp  = "20060102T150405Z"
)

// Name is a backup's file name for its time, such as
// ways-20261016T030000Z.db.age.
func Name(at time.Time) string { return prefix + at.UTC().Format(stamp) + suffix }

// Write copies the database, encrypts the copy to recipients into dir, and
// returns the backup's path and size. The plaintext copy is removed.
func Write(ctx context.Context, s *store.Store, dir string, recipients []age.Recipient, at time.Time) (string, int64, error) {
	if len(recipients) == 0 {
		return "", 0, errors.New("backup: no age recipient")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	plain := filepath.Join(dir, ".snapshot.db")
	os.Remove(plain) // left by a crash; VACUUM INTO needs a new file
	defer os.Remove(plain)
	if err := s.SnapshotTo(ctx, plain); err != nil {
		return "", 0, fmt.Errorf("backup: copy the database: %w", err)
	}
	in, err := os.Open(plain)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()

	path := filepath.Join(dir, Name(at))
	out, err := os.CreateTemp(dir, ".encrypting-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(out.Name())
	enc, err := age.Encrypt(out, recipients...)
	if err == nil {
		_, err = io.Copy(enc, in)
	}
	if err == nil {
		err = enc.Close()
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, fmt.Errorf("backup: encrypt: %w", err)
	}
	if err := os.Rename(out.Name(), path); err != nil {
		return "", 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return path, fi.Size(), nil
}

// Prune keeps the newest keep backups in dir and removes the rest.
func Prune(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // the time stamp sorts by time
	var errs []error
	for _, n := range names[:max(0, len(names)-keep)] {
		errs = append(errs, os.Remove(filepath.Join(dir, n)))
	}
	return errors.Join(errs...)
}

// Restore decrypts a backup with the operator's identities into a new
// database at dbPath, and checks that it opens. It never overwrites.
func Restore(in io.Reader, identities []age.Identity, dbPath string) error {
	dec, err := age.Decrypt(in, identities...)
	if err != nil {
		return fmt.Errorf("backup: decrypt: %w", err)
	}
	out, err := os.OpenFile(dbPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, dec)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		var s *store.Store
		if s, err = store.Open(dbPath); err == nil {
			err = s.IntegrityCheck(context.Background())
			s.Close()
		}
	}
	if err != nil {
		os.Remove(dbPath)
		return fmt.Errorf("backup: restore: %w", err)
	}
	return nil
}

// Uploader puts a backup off the host.
type Uploader interface {
	Put(ctx context.Context, name string, data []byte) error
}

// S3 is a bucket on any S3-compatible object storage, addressed
// path-style: Endpoint/Bucket/name.
type S3 struct {
	Endpoint, Region, Bucket string
	AccessKey, SecretKey     string
	HTTP                     *http.Client // default http.DefaultClient

	now func() time.Time
}

// Put uploads one object, signed with AWS Signature Version 4.
func (s *S3) Put(ctx context.Context, name string, data []byte) error {
	u := strings.TrimSuffix(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	sum := sha256.Sum256(data)
	signV4(req, hex.EncodeToString(sum[:]), s.AccessKey, s.SecretKey, s.Region, now())
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("backup: upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("backup: upload: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// signV4 signs req for S3 with AWS Signature Version 4, over the Host
// header and every header already set.
func signV4(req *http.Request, payloadHash, accessKey, secretKey, region string, now time.Time) {
	now = now.UTC()
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	slices.Sort(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, path, req.URL.Query().Encode(), canonHeaders.String(), signed, payloadHash}, "\n")

	scope := date + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + now.Format("20060102T150405Z") + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := []byte("AWS4" + secretKey)
	for _, part := range []string{date, region, "s3", "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}
