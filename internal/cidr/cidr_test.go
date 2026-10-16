package cidr

import (
	"net/netip"
	"slices"
	"testing"
)

func pfx(ss ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func strs(ps []netip.Prefix) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func TestMergeCoalescesAndSorts(t *testing.T) {
	got := Merge(pfx("10.0.1.0/24", "10.0.0.0/24", "10.0.0.128/25", "192.0.2.0/24"))
	want := []string{"10.0.0.0/23", "192.0.2.0/24"}
	if !slices.Equal(strs(got), want) {
		t.Errorf("Merge = %v, want %v", strs(got), want)
	}
}

func TestSubtractSplits(t *testing.T) {
	got := Subtract(pfx("0.0.0.0/1"), pfx("10.0.0.0/8", "127.0.0.0/8"))
	want := []string{"0.0.0.0/5", "8.0.0.0/7", "11.0.0.0/8", "12.0.0.0/6", "16.0.0.0/4", "32.0.0.0/3", "64.0.0.0/3", "96.0.0.0/4", "112.0.0.0/5", "120.0.0.0/6", "124.0.0.0/7", "126.0.0.0/8"}
	if !slices.Equal(strs(got), want) {
		t.Errorf("Subtract = %v, want %v", strs(got), want)
	}
	if got := Size(got); got != 1<<31-2<<24 {
		t.Errorf("Size = %d", got)
	}
}

func TestSizeCountsAddressesOnce(t *testing.T) {
	if got := Size(pfx("10.0.0.0/24", "10.0.0.0/25", "0.0.0.0/0")); got != 1<<32 {
		t.Errorf("Size = %d, want 2^32", got)
	}
}

func TestStripReserved(t *testing.T) {
	got := StripReserved(pfx("2.144.0.0/14", "10.0.0.0/8", "192.168.1.0/24", "100.64.0.0/16", "5.160.0.0/16"))
	want := []string{"2.144.0.0/14", "5.160.0.0/16"}
	if !slices.Equal(strs(got), want) {
		t.Errorf("StripReserved = %v, want %v", strs(got), want)
	}
}
