package coordinator

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// grafanaUserHeader is the header Grafana's auth proxy takes the signed-in
// user from. Grafana trusts it, so only the proxy may set it.
const grafanaUserHeader = "X-WEBAUTH-USER"

// grafana passes /grafana/ to Grafana for a signed-in Admin whose session
// proved TOTP, as that Admin's handle (ADR 0006). Grafana serves from the
// same sub-path, so paths go through unchanged.
func (c *Coordinator) grafana(w http.ResponseWriter, r *http.Request) {
	if c.GrafanaURL == "" {
		http.NotFound(w, r)
		return
	}
	target, err := url.Parse(c.GrafanaURL)
	if err != nil {
		c.internalError(w, err)
		return
	}
	cl, status, msg, err := c.authenticate(r, true)
	if err != nil {
		c.internalError(w, err)
		return
	}
	if status == http.StatusUnauthorized && r.Method == http.MethodGet {
		// A browser opening Grafana signs in through the panel first.
		http.Redirect(w, r, "/#/admin", http.StatusFound)
		return
	}
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = r.Host
			pr.Out.Header.Set(grafanaUserHeader, cl.account.Handle)
			withoutCookie(pr.Out.Header, sessionCookie)
		},
	}
	proxy.ServeHTTP(w, r)
}

// withoutCookie drops the named cookie from h's Cookie headers, keeping
// the rest.
func withoutCookie(h http.Header, name string) {
	var kept []string
	for _, line := range h.Values("Cookie") {
		var parts []string
		for _, part := range strings.Split(line, ";") {
			if k, _, _ := strings.Cut(strings.TrimSpace(part), "="); k != name {
				parts = append(parts, strings.TrimSpace(part))
			}
		}
		if len(parts) > 0 {
			kept = append(kept, strings.Join(parts, "; "))
		}
	}
	h.Del("Cookie")
	for _, line := range kept {
		h.Add("Cookie", line)
	}
}
