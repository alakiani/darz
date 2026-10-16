package store_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/alakiani/darz/internal/protocol"
)

func TestDevicesGetUnusedAddressesThatAreNeverReused(t *testing.T) {
	s := open(t, newClock())
	u, err := s.CreateUser(ctx, "maryam")
	if err != nil {
		t.Fatal(err)
	}
	d1, err := s.CreateDevice(ctx, u.ID, "phone", "key1")
	if err != nil {
		t.Fatal(err)
	}
	if d1.Address != netip.MustParseAddr("10.77.0.2") {
		t.Errorf("first device address = %s, want 10.77.0.2 (10.77.0.1 is the resolver)", d1.Address)
	}
	if err := s.RevokeDevice(ctx, d1.ID); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.CreateDevice(ctx, u.ID, "laptop", "key2")
	if d2.Address == d1.Address {
		t.Error("a revoked device's address was reused")
	}
	devs, _ := s.Devices(ctx)
	if len(devs) != 2 || !devs[0].Revoked || devs[0].PublicKey != "" || devs[1].Revoked {
		t.Errorf("devices = %+v, want the revoked one without its key", devs)
	}

	s.SetUserSuspended(ctx, u.ID, true)
	if devs, _ := s.Devices(ctx); !devs[1].UserSuspended {
		t.Error("suspension not visible on the device")
	}
	probe, err := s.CreateDevice(ctx, "", "probe", "key3")
	if err != nil || probe.UserID != "" {
		t.Errorf("probe device = %+v, %v", probe, err)
	}
}

func TestBuiltinRulesAndSets(t *testing.T) {
	s := open(t, newClock())
	rules, err := s.Rules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].Name != "IR" || rules[1].Name != "Default" {
		t.Fatalf("rules = %+v", rules)
	}
	ir, def := rules[0], rules[1]
	if ir.Fallback != protocol.FallbackNext || def.Fallback != protocol.FallbackDrop || !def.Builtin {
		t.Errorf("IR = %+v, default = %+v", ir, def)
	}
	if def.RequiredTags[0] != "!IR" || ir.RequiredTags[0] != "IR" {
		t.Errorf("tags: IR %v, default %v", ir.RequiredTags, def.RequiredTags)
	}
}

func TestSetContentIsFeedPlusAdditionsMinusRemovals(t *testing.T) {
	s := open(t, newClock())
	p := func(ss ...string) []netip.Prefix {
		var out []netip.Prefix
		for _, x := range ss {
			out = append(out, netip.MustParsePrefix(x))
		}
		return out
	}
	if err := s.SetFeedContent(ctx, 1, p("2.144.0.0/14", "5.160.0.0/16")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverrides(ctx, 1, p("185.0.0.0/24"), p("5.160.0.0/17")); err != nil {
		t.Fatal(err)
	}
	sets, _ := s.DestinationSets(ctx)
	ir := sets[0]
	body, err := s.SetBlob(ctx, ir.ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2.144.0.0/14\n5.160.128.0/17\n185.0.0.0/24\n"; body != want {
		t.Errorf("content = %q, want %q", body, want)
	}

	if err := s.RefreshSetContent(ctx, 2); err != nil {
		t.Fatal(err)
	}
	sets, _ = s.DestinationSets(ctx)
	if body, _ := s.SetBlob(ctx, sets[1].ContentHash); strings.TrimSpace(body) != "0.0.0.0/0" {
		t.Errorf("Everything = %q", body)
	}
}

func TestSettingIfUnsetKeepsTheFirstValue(t *testing.T) {
	s := open(t, newClock())
	if v, _ := s.SetSettingIfUnset(ctx, "k", "a"); v != "a" {
		t.Errorf("first = %q", v)
	}
	if v, _ := s.SetSettingIfUnset(ctx, "k", "b"); v != "a" {
		t.Errorf("second = %q, want the first value kept", v)
	}
}
