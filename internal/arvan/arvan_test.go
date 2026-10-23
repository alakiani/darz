package arvan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func newClient(t *testing.T, f *Fake) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, APIKey: "secret", Zone: "wayfinder.ir", TTL: 120}
}

func TestSetAUpdatesTheExistingRecord(t *testing.T) {
	f := NewFake("secret", "wayfinder.ir")
	f.Put("a", "192.0.2.1")
	f.Put("b", "192.0.2.2")
	c := newClient(t, f)

	if err := c.SetA(context.Background(), "a.wayfinder.ir", netip.MustParseAddr("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	if got := f.A("a"); got != "203.0.113.7" {
		t.Errorf("a = %q, want 203.0.113.7", got)
	}
	if got := f.A("b"); got != "192.0.2.2" {
		t.Errorf("b changed to %q", got)
	}
	if f.Records() != 2 {
		t.Errorf("%d records, want the 2 there were", f.Records())
	}
	if ttl := f.TTL("a"); ttl != 120 {
		t.Errorf("ttl = %d, want 120", ttl)
	}
}

func TestSetACreatesAMissingRecord(t *testing.T) {
	f := NewFake("secret", "wayfinder.ir")
	c := newClient(t, f)
	if err := c.SetA(context.Background(), "c.wayfinder.ir", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatal(err)
	}
	if got := f.A("c"); got != "203.0.113.9" {
		t.Errorf("c = %q, want 203.0.113.9", got)
	}
}

func TestSetAFindsRecordsOnLaterPages(t *testing.T) {
	f := NewFake("secret", "wayfinder.ir")
	f.PerPage = 2
	for _, n := range []string{"w", "x", "y", "z", "a"} {
		f.Put(n, "192.0.2.1")
	}
	c := newClient(t, f)
	if err := c.SetA(context.Background(), "a.wayfinder.ir", netip.MustParseAddr("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	if f.Records() != 5 || f.A("a") != "203.0.113.7" {
		t.Errorf("records = %d, a = %q; want 5 and the update in place", f.Records(), f.A("a"))
	}
}

func TestSetARejectsNamesOutsideTheZone(t *testing.T) {
	c := newClient(t, NewFake("secret", "wayfinder.ir"))
	if err := c.SetA(context.Background(), "a.example.com", netip.MustParseAddr("203.0.113.7")); err == nil {
		t.Error("want an error for a name outside the zone")
	}
}

func TestSetAReportsAPIErrors(t *testing.T) {
	f := NewFake("other-key", "wayfinder.ir")
	c := newClient(t, f)
	err := c.SetA(context.Background(), "a.wayfinder.ir", netip.MustParseAddr("203.0.113.7"))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the 401", err)
	}

	f = NewFake("secret", "wayfinder.ir")
	f.FailWith = http.StatusServiceUnavailable
	c = newClient(t, f)
	if err := c.SetA(context.Background(), "a.wayfinder.ir", netip.MustParseAddr("203.0.113.7")); err == nil {
		t.Error("want an error while the API fails")
	}
}

// The panel shows a key as "apikey <key>"; operators paste it in any form.
func TestNormalizeKey(t *testing.T) {
	const key = "0a1b2c3d-4e5f-6789-abcd-ef0123456789"
	for _, in := range []string{
		key,
		"apikey " + key,
		"Apikey " + key,
		"APIKEY " + key,
		"apikey" + key, // the space lost on the way
		"  apikey " + key + "\n",
	} {
		if got := NormalizeKey(in); got != key {
			t.Errorf("NormalizeKey(%q) = %q, want the bare key", in, got)
		}
	}
}
