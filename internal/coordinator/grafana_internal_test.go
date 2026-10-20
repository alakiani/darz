package coordinator

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alakiani/ways/internal/protocol"
	"github.com/alakiani/ways/internal/store"
)

// fakeGrafana records the last request it was sent.
type fakeGrafana struct {
	srv  *httptest.Server
	hits atomic.Int32
	last atomic.Pointer[http.Request]
}

func newFakeGrafana(t *testing.T) *fakeGrafana {
	g := &fakeGrafana{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.hits.Add(1)
		g.last.Store(r)
		w.Write([]byte("grafana"))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// get sends a browser-like GET without following redirects.
func (cl *client) get(path string, header http.Header) *http.Response {
	cl.a.t.Helper()
	req, _ := http.NewRequest("GET", cl.a.srv.URL+path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	hc := *cl.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		cl.a.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestGrafanaIsProxiedForAdmins(t *testing.T) {
	a := newAPI(t)
	g := newFakeGrafana(t)
	a.c.GrafanaURL = g.srv.URL
	admin := a.admin()

	resp := admin.get("/grafana/d/ways-mesh?orgId=1", http.Header{
		"X-Webauth-User": {"someone-else"},
		"Cookie":         {"grafana_session=abc"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	r := g.last.Load()
	if r.URL.Path != "/grafana/d/ways-mesh" || r.URL.RawQuery != "orgId=1" {
		t.Errorf("Grafana got %s", r.URL)
	}
	if got := r.Header.Values("X-Webauth-User"); len(got) != 1 || got[0] != "admin" {
		t.Errorf("X-WEBAUTH-USER = %q, want [admin]", got)
	}
	if _, err := r.Cookie(sessionCookie); err == nil {
		t.Errorf("the panel's session cookie reached Grafana")
	}
	if c, err := r.Cookie("grafana_session"); err != nil || c.Value != "abc" {
		t.Errorf("Grafana's own cookie was not passed on: %v", err)
	}
}

func TestGrafanaIsShutToEveryoneElse(t *testing.T) {
	a := newAPI(t)
	g := newFakeGrafana(t)
	a.c.GrafanaURL = g.srv.URL

	// Nobody signed in is sent to the panel's sign-in.
	resp := a.client().get("/grafana/", nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/#/admin" {
		t.Errorf("signed out: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	user, _ := a.user("maryam")
	if resp := user.get("/grafana/", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("User: status %d", resp.StatusCode)
	}

	// An Admin who has not turned TOTP on yet.
	noTOTP := a.client()
	noTOTP.must(200, "POST", "/v1/invites/accept", protocol.AcceptInvite{Token: a.invite(store.Invite{Kind: "new", Admin: true}), Handle: "newadmin", Password: password}, nil)
	if resp := noTOTP.get("/grafana/", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("Admin without TOTP: status %d", resp.StatusCode)
	}

	if n := g.hits.Load(); n != 0 {
		t.Errorf("Grafana was reached %d times", n)
	}
}

func TestGrafanaUnsetIsNotFound(t *testing.T) {
	a := newAPI(t)
	if resp := a.admin().get("/grafana/", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestGrafanaPathIsNotThePanel(t *testing.T) {
	a := newAPI(t)
	g := newFakeGrafana(t)
	a.c.GrafanaURL = g.srv.URL
	// /grafana without the slash goes to /grafana/, not to the panel's index.
	resp := a.admin().get("/grafana", nil)
	if resp.StatusCode/100 != 3 || !strings.HasSuffix(resp.Header.Get("Location"), "/grafana/") {
		t.Errorf("/grafana: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
