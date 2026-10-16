package coordinator

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/alakiani/darz/internal/panel"
	"github.com/alakiani/darz/internal/protocol"
	"github.com/alakiani/darz/internal/store"
)

// authLevel is who may call a route of the Admin API listener.
type authLevel int

const (
	authNone  authLevel = iota // anyone: Invites and sign-in
	authToken                  // a nodes:enroll API token
	authUser                   // a signed-in User
	authAdmin                  // a signed-in Admin whose session proved TOTP (ADR 0001)
)

// route is one operation of the Admin API listener. The mux and the OpenAPI
// spec are both built from the table, so they cannot drift apart.
type route struct {
	method, path string
	auth         authLevel
	summary      string
	request      any // zero value of the body type, or nil
	response     any // zero value of the 2xx body type, or nil for 204
	handler      http.HandlerFunc
}

func (c *Coordinator) routes() []route {
	return []route{
		// Nodes, for the enroll playbook (ADR 0002).
		{"POST", "/v1/nodes", authToken, "Create a Node, or get a fresh enrollment token for one", protocol.CreateNodeRequest{}, protocol.CreateNodeResponse{}, c.createNode},
		{"GET", "/v1/nodes/{id}", authToken, "Read a Node and its latest health", nil, protocol.NodeStatus{}, c.getNode},

		// Invites and sign-in.
		{"POST", "/v1/invites/lookup", authNone, "See what an Invite offers", protocol.InviteToken{}, protocol.InviteInfo{}, c.lookupInvite},
		{"POST", "/v1/invites/accept", authNone, "Redeem an Invite and sign in", protocol.AcceptInvite{}, protocol.Me{}, c.acceptInvite},
		{"POST", "/v1/login", authNone, "Sign in", protocol.Login{}, protocol.Me{}, c.login},
		{"POST", "/v1/logout", authUser, "End this session", nil, nil, c.logout},
		{"POST", "/v1/me/logout-everywhere", authUser, "End every session of mine, this one too", nil, nil, c.logoutEverywhere},

		// The signed-in User.
		{"GET", "/v1/me", authUser, "The signed-in User", nil, protocol.Me{}, c.me},
		{"POST", "/v1/me/password", authUser, "Change password; ends every other session", protocol.ChangePassword{}, nil, c.changePassword},
		{"POST", "/v1/me/totp", authUser, "Offer a new TOTP secret", nil, protocol.TOTPOffer{}, c.offerTOTP},
		{"POST", "/v1/me/totp/confirm", authUser, "Confirm the offered TOTP secret with a code", protocol.TOTPCode{}, protocol.Me{}, c.confirmTOTP},
		{"POST", "/v1/me/totp/disable", authUser, "Turn TOTP off (not allowed for Admins)", protocol.TOTPCode{}, protocol.Me{}, c.disableTOTP},
		{"GET", "/v1/me/devices", authUser, "My Devices", nil, []protocol.DeviceInfo{}, c.myDevices},
		{"POST", "/v1/me/devices", authUser, "Create a Device from a public key made in the browser", protocol.NewDevice{}, protocol.IssuedDevice{}, c.createDevice},
		{"GET", "/v1/me/devices/{id}/usage", authUser, "One of my Devices' usage: hourly, daily and monthly", nil, protocol.DeviceUsage{}, c.deviceUsage},
		{"POST", "/v1/me/devices/{id}/revoke", authUser, "Revoke one of my Devices", nil, protocol.DeviceInfo{}, c.revokeMyDevice},
		{"POST", "/v1/me/devices/{id}/replace", authUser, "Issue a new Device and revoke this one in one step", protocol.NewDevice{}, protocol.IssuedDevice{}, c.replaceDevice},

		// Admins.
		{"POST", "/v1/admin/invites", authAdmin, "Create a new-User Invite", protocol.CreateInvite{}, protocol.IssuedInvite{}, c.adminCreateInvite},
		{"GET", "/v1/admin/users", authAdmin, "Every User", nil, []protocol.UserInfo{}, c.adminUsers},
		{"POST", "/v1/admin/users/{id}/recovery-invite", authAdmin, "Create a recovery Invite: new credentials, same Devices", nil, protocol.IssuedInvite{}, c.adminRecoveryInvite},
		{"POST", "/v1/admin/users/{id}/suspend", authAdmin, "Suspend a User: their Devices leave the Bridges", nil, protocol.UserInfo{}, c.adminSuspend},
		{"POST", "/v1/admin/users/{id}/resume", authAdmin, "Resume a suspended User", nil, protocol.UserInfo{}, c.adminResume},
		{"POST", "/v1/admin/users/{id}/device-cap", authAdmin, "Set a User's Device cap", protocol.SetDeviceCap{}, protocol.UserInfo{}, c.adminSetCap},
		{"DELETE", "/v1/admin/users/{id}", authAdmin, "Delete a User with their Devices and usage", nil, nil, c.adminDelete},
		{"GET", "/v1/admin/users/{id}/devices", authAdmin, "A User's Devices", nil, []protocol.DeviceInfo{}, c.adminUserDevices},
		{"POST", "/v1/admin/devices/{id}/revoke", authAdmin, "Revoke any Device", nil, protocol.DeviceInfo{}, c.adminRevokeDevice},

		{"GET", "/v1/openapi.json", authNone, "This API's OpenAPI description", nil, nil, c.serveSpec},
	}
}

// AdminHandler serves the Admin API listener: the panel's API and the
// enroll playbook's.
func (c *Coordinator) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range c.routes() {
		var h http.Handler
		switch r.auth {
		case authNone:
			h = r.handler
		case authToken:
			h = c.requireScope(store.ScopeNodesEnroll, r.handler)
		case authUser:
			h = c.requireSession(false, r.handler)
		case authAdmin:
			h = c.requireSession(true, r.handler)
		}
		if r.request != nil || r.method != "GET" {
			h = requireJSON(h)
		}
		mux.Handle(r.method+" "+r.path, h)
	}
	mux.Handle("GET /", panel.Handler())
	return mux
}

// requireJSON refuses state-changing requests that are not JSON. A
// cross-site form cannot send application/json without a CORS preflight,
// which this API never grants, so this and SameSite=Strict cookies close
// off CSRF.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "requests must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

const sessionCookie = "darz_session"

type sessionKey struct{}

// caller is the signed-in User of a request.
type caller struct {
	account store.Account
	session store.Session
	token   string
}

func callerFrom(r *http.Request) caller { return r.Context().Value(sessionKey{}).(caller) }

// requireSession admits a request with a live session of a User who is not
// suspended; with admin set, only an Admin whose session proved TOTP.
func (c *Coordinator) requireSession(admin bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess, err := c.store.LookupSession(r.Context(), cookie.Value)
		if errors.Is(err, store.ErrInvalidToken) {
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}
		if err != nil {
			c.internalError(w, err)
			return
		}
		acct, err := c.store.Account(r.Context(), sess.UserID)
		if err != nil || acct.Suspended {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		if admin {
			switch {
			case !acct.Admin:
				writeError(w, http.StatusForbidden, "Admins only")
				return
			case acct.TOTPSecret == "":
				writeError(w, http.StatusForbidden, "Admins must turn on TOTP first")
				return
			case !sess.MFA:
				writeError(w, http.StatusForbidden, "sign in again with your TOTP code")
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, caller{account: acct, session: sess, token: cookie.Value})
		next(w, r.WithContext(ctx))
	})
}

// setSession starts a session and hands its cookie to the browser.
func (c *Coordinator) setSession(w http.ResponseWriter, r *http.Request, userID string, mfa bool) (string, error) {
	token, err := c.store.CreateSession(r.Context(), userID, mfa, SessionTTL)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge: int(SessionTTL.Seconds()), HttpOnly: true, Secure: c.SecureCookies, SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func clearSession(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteStrictMode})
}

// clientIP is the address a request came from, for rate limiting.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}
