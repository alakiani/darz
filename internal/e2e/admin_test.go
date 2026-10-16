//go:build linux

package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alakiani/darz/internal/auth"
	"github.com/alakiani/darz/internal/lab"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
)

// TestAdminPanel is session 9's "proves in the lab" test: a browser walks
// every admin screen against a live lab mesh, and every change it makes
// shows in the Coordinator.
func TestAdminPanel(t *testing.T) {
	binDir(t)
	script, _ := filepath.Abs("../../web/e2e/admin.mjs")
	if _, err := os.Stat("../../web/node_modules/@playwright/test"); err != nil {
		t.Skip("web/node_modules is not installed: run npm ci in web/ and npx playwright install chromium")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH")
	}
	if out, err := execCommand("modprobe", "wireguard"); err != nil {
		t.Fatalf("modprobe wireguard: %v: %s", err, out)
	}

	l := lab.StartSpecs(t,
		lab.Spec{Name: "coord"},
		lab.Spec{Name: "ir1"},
		lab.Spec{Name: "ab1"},
		lab.At("egress-ab", "1.0.0.10"),
		lab.At("egress-ir", "2.188.0.10"),
	)
	ctx := context.Background()
	co := startCoordinator(t, l.Node("coord"))
	egressTargets(t, co, l.Node("egress-ab"), l.Node("egress-ir"))

	// The feed starts with three prefixes; the next fetch finds a fourth.
	var feed atomic.Value
	feed.Store(irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, feed.Load().(string))
	}))
	defer srv.Close()
	co.c.FeedURL, co.c.RIPEstatURL = srv.URL, ""
	if err := co.c.RefreshIRFeed(ctx); err != nil {
		t.Fatal(err)
	}
	feed.Store(irFeed("2.144.0.0/14", "2.176.0.0/12", "5.0.0.0/8", "31.0.0.0/24"))

	// An Admin with TOTP, and two Users.
	const adminPassword = "the admin's long password"
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword(adminPassword)
	tok, _, _ := co.store.CreateInvite(ctx, store.Invite{Kind: "new", Admin: true}, time.Hour)
	admin, err := co.store.AcceptNewInvite(ctx, tok, "admin", hash)
	if err != nil {
		t.Fatal(err)
	}
	co.store.SetTOTPPending(ctx, admin.ID, secret)
	co.store.ConfirmTOTP(ctx, admin.ID)
	for _, handle := range []string{"maryam", "reza"} {
		tok, _, _ := co.store.CreateInvite(ctx, store.Invite{Kind: "new"}, time.Hour)
		if _, err := co.store.AcceptNewInvite(ctx, tok, handle, hash); err != nil {
			t.Fatal(err)
		}
	}

	m := &mesh{t: t, l: l, co: co, ids: map[string]string{}, agents: map[string]*agentProc{}}
	m.startAgent("ir1", []protocol.Role{protocol.RoleBridge}, []string{"IR"})
	m.startAgent("ab1", []protocol.Role{protocol.RoleExit}, nil)

	// An alert no Exit could relay waits on the banner (ADR 0006).
	alert, _ := co.store.QueueAlert(ctx, "DARZ\nFIRING critical NodeUnreachable: Node ab9 is unreachable")
	co.store.AlertFailed(ctx, alert, "no healthy Exit")

	cmd := exec.Command(node, script)
	cmd.Dir = filepath.Dir(script)
	cmd.Env = append(os.Environ(), "PANEL_URL="+co.admin.URL, "ADMIN_HANDLE=admin", "ADMIN_PASSWORD="+adminPassword, "TOTP_SECRET="+secret)
	out, err := cmd.CombinedOutput()
	t.Logf("browser:\n%s", strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("browser: %v", err)
	}

	// What the browser did is in the Coordinator.
	maryam, err := co.store.AccountByHandle(ctx, "maryam")
	if err != nil || maryam.Suspended || maryam.DeviceCap != 5 {
		t.Errorf("maryam = %+v, %v; want resumed with a cap of 5", maryam, err)
	}
	if _, err := co.store.AccountByHandle(ctx, "reza"); err == nil {
		t.Error("reza was not deleted")
	}
	invites, _ := co.store.Invites(ctx)
	withdrawn, recovery := 0, 0
	for _, inv := range invites {
		if inv.Withdrawn {
			withdrawn++
		}
		if inv.Kind == "recovery" && inv.Handle == "maryam" {
			recovery++
		}
	}
	if withdrawn != 1 || recovery != 1 {
		t.Errorf("invites: %d withdrawn, %d recovery; want 1 and 1", withdrawn, recovery)
	}
	if _, err := co.store.NodeByName(ctx, "ab9"); err == nil {
		t.Error("ab9 was not deleted")
	}
	if ts, _ := co.store.Transports(ctx); len(ts) != 2 {
		t.Errorf("transports = %v, want both back on", ts)
	}
	rules, _ := co.store.Rules(ctx)
	if rules[0].Fallback != protocol.FallbackNext {
		t.Errorf("IR fallback = %s, want next", rules[0].Fallback)
	}
	sets, _ := co.store.DestinationSets(ctx)
	if adds := prefixStrings(sets[0].Additions); !slices.Contains(adds, "185.0.0.0/24") {
		t.Errorf("IR additions = %v", adds)
	}
	toks, revoked, _ := co.store.APITokens(ctx)
	for i, tk := range toks {
		if tk.Name == "ops-laptop" && !revoked[i] {
			t.Error("the ops-laptop token was not revoked")
		}
	}
}

func prefixStrings[T interface{ String() string }](ps []T) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}
