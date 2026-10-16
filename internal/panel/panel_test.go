package panel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeadersAndFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/some/app/route", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "Referrer-Policy", "X-Content-Type-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("no %s header", h)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
	switch rec.Code {
	case http.StatusOK:
		if !strings.Contains(rec.Body.String(), `dir="rtl"`) {
			t.Error("the fallback is not the panel's index.html")
		}
	case http.StatusServiceUnavailable: // not built here; CI builds it first
	default:
		t.Errorf("status %d", rec.Code)
	}
}
