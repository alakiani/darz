package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
)

var ctx = context.Background()

// clock is a settable time source for the store.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)} }
func open(t *testing.T, c *clock) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ways.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Now = c.now
	return s
}

func mustNode(t *testing.T, s *store.Store, name string) store.Node {
	t.Helper()
	n, err := s.CreateNode(ctx, name, []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReopenKeepsDataAndMigrationsRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ways.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNode(ctx, "b1", []protocol.Role{protocol.RoleBridge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	got, err := s.Node(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "b1" || len(got.Roles) != 1 || got.Roles[0] != protocol.RoleBridge || got.Tags == nil {
		t.Errorf("reopened node = %+v", got)
	}
}

func TestNodeNamesAreUnique(t *testing.T) {
	s := open(t, newClock())
	mustNode(t, s, "b1")
	if _, err := s.CreateNode(ctx, "b1", nil, nil); !errors.Is(err, store.ErrExists) {
		t.Errorf("second b1: err = %v, want ErrExists", err)
	}
	if _, err := s.NodeByName(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown name: err = %v, want ErrNotFound", err)
	}
}

func TestEnrollmentTokenIsOneTimeBoundAndExpires(t *testing.T) {
	c := newClock()
	s := open(t, c)
	n := mustNode(t, s, "b1")
	sign := func(serial string) func(string) (string, error) {
		return func(nodeID string) (string, error) {
			if nodeID != n.ID {
				t.Errorf("sign called for %q, want %q", nodeID, n.ID)
			}
			return serial, nil
		}
	}

	tok, exp, err := s.MintEnrollmentToken(ctx, n.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(c.t.Add(time.Hour)) {
		t.Errorf("expires = %v, want an hour from now", exp)
	}
	id, err := s.Enroll(ctx, tok, sign("serial-1"))
	if err != nil || id != n.ID {
		t.Fatalf("Enroll = %q, %v", id, err)
	}
	if got, _ := s.Node(ctx, n.ID); got.CertSerial != "serial-1" {
		t.Errorf("cert serial = %q, want serial-1", got.CertSerial)
	}
	if _, err := s.Enroll(ctx, tok, sign("serial-2")); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("reused token: err = %v, want ErrInvalidToken", err)
	}

	// A newer token replaces an unused older one.
	old, _, _ := s.MintEnrollmentToken(ctx, n.ID, time.Hour)
	fresh, _, _ := s.MintEnrollmentToken(ctx, n.ID, time.Hour)
	if _, err := s.Enroll(ctx, old, sign("x")); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("superseded token: err = %v, want ErrInvalidToken", err)
	}

	c.advance(time.Hour + time.Second)
	if _, err := s.Enroll(ctx, fresh, sign("x")); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
	if _, err := s.Enroll(ctx, "garbage", sign("x")); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}
}

func TestEnrollFailsCleanlyWhenSigningFails(t *testing.T) {
	s := open(t, newClock())
	n := mustNode(t, s, "b1")
	tok, _, _ := s.MintEnrollmentToken(ctx, n.ID, time.Hour)
	boom := errors.New("bad csr")
	if _, err := s.Enroll(ctx, tok, func(string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the signing error", err)
	}
	// The token was not consumed.
	if _, err := s.Enroll(ctx, tok, func(string) (string, error) { return "s", nil }); err != nil {
		t.Errorf("retry after failed signing: %v", err)
	}
}

func TestAPITokenScopeExpiryAndRevocation(t *testing.T) {
	c := newClock()
	s := open(t, c)
	secret, _, err := s.CreateAPIToken(ctx, store.ScopeNodesEnroll, "laptop", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckAPIToken(ctx, secret, store.ScopeNodesEnroll); err != nil {
		t.Errorf("valid token: %v", err)
	}
	if _, err := s.CheckAPIToken(ctx, secret, "admin"); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("wrong scope: err = %v, want ErrInvalidToken", err)
	}
	if _, err := s.CheckAPIToken(ctx, secret+"x", store.ScopeNodesEnroll); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("wrong secret: err = %v, want ErrInvalidToken", err)
	}

	c.advance(90*24*time.Hour + time.Second)
	if _, err := s.CheckAPIToken(ctx, secret, store.ScopeNodesEnroll); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("expired: err = %v, want ErrInvalidToken", err)
	}

	secret2, tok2, _ := s.CreateAPIToken(ctx, store.ScopeNodesEnroll, "laptop", time.Hour)
	if err := s.RevokeAPIToken(ctx, tok2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckAPIToken(ctx, secret2, store.ScopeNodesEnroll); !errors.Is(err, store.ErrInvalidToken) {
		t.Errorf("revoked: err = %v, want ErrInvalidToken", err)
	}
}

func TestSnapshotsVersionOnlyOnChangeAndRememberBadVersions(t *testing.T) {
	s := open(t, newClock())
	n := mustNode(t, s, "b1")

	if _, err := s.LatestSnapshot(ctx, n.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no snapshot yet: err = %v, want ErrNotFound", err)
	}
	put := func(body string) (uint64, bool) {
		t.Helper()
		v, changed, err := s.PutSnapshot(ctx, n.ID, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return v, changed
	}
	if v, changed := put(`{"a":1}`); v != 1 || !changed {
		t.Errorf("first put = %d, %v; want 1, true", v, changed)
	}
	if v, changed := put(`{"a":1}`); v != 1 || changed {
		t.Errorf("same body = %d, %v; want 1, false", v, changed)
	}
	if v, changed := put(`{"a":2}`); v != 2 || !changed {
		t.Errorf("new body = %d, %v; want 2, true", v, changed)
	}

	if err := s.MarkSnapshotBad(ctx, n.ID, 2); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestSnapshot(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 || !latest.Bad || string(latest.Body) != `{"a":2}` {
		t.Errorf("latest = %+v, want version 2 marked bad", latest)
	}
	// Marking twice is harmless.
	if err := s.MarkSnapshotBad(ctx, n.ID, 2); err != nil {
		t.Errorf("mark twice: %v", err)
	}

	// Changing the state again, even back to an earlier body, is a new version.
	if v, changed := put(`{"a":1}`); v != 3 || !changed {
		t.Errorf("after bad = %d, %v; want 3, true", v, changed)
	}
	if latest, _ := s.LatestSnapshot(ctx, n.ID); latest.Bad {
		t.Errorf("version 3 is marked bad")
	}
}

func TestHealthKeepsTheLatestReport(t *testing.T) {
	c := newClock()
	s := open(t, c)
	n := mustNode(t, s, "b1")
	if h, err := s.Health(ctx, n.ID); err != nil || h != nil {
		t.Fatalf("no report yet = %v, %v", h, err)
	}
	for v := uint64(1); v <= 2; v++ {
		err := s.PutHealth(ctx, n.ID, protocol.Health{AppliedVersion: v, AgentVersion: "dev", Addr: "10.0.0.2"}, "10.0.0.9")
		if err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.Health(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h.AppliedVersion != 2 || h.SeenAddr != "10.0.0.9" || h.Addr != "10.0.0.2" || !h.ReceivedAt.Equal(c.t) {
		t.Errorf("health = %+v", h)
	}
}
