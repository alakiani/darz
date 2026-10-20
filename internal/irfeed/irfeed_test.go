package irfeed

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/alakiani/ways/internal/cidr"
)

// sample has the shape of MrAriaNet/Get-IP-Iran list.rsc.
const sample = `#Last update: Fri Oct 16 05:39:57 UTC 2026
/ipv6 firewall address-list remove [/ipv6 firewall address-list find list=IRv6]
/ipv6 firewall address-list
:do { add address=2001:678:b0::/46 list=IRv6} on-error={}
#Last update: Fri Oct 16 05:39:57 UTC 2026
/ip firewall address-list remove [/ip firewall address-list find list=NoNAT]
/ip firewall address-list
:do { add address=5.160.0.0/16 list=NoNAT} on-error={}
:do { add address=10.0.0.0/8 list=NoNAT} on-error={}
:do { add address=2.144.0.0/14 list=NoNAT} on-error={}
:do { add address=2.176.0.0/12 list=NoNAT} on-error={}
`

func TestParseTakesIPv4AndStripsReserved(t *testing.T) {
	got, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := "2.144.0.0/14 2.176.0.0/12 5.160.0.0/16"
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	if strings.Join(s, " ") != want {
		t.Errorf("Parse = %v, want %s", s, want)
	}
}

func TestParseRejectsBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"empty":           "",
		"only IPv6":       ":do { add address=2001:678:b0::/46 list=IRv6} on-error={}\n",
		"malformed entry": sample + ":do { add address=2.999.0.0/16 list=NoNAT} on-error={}\n",
		"only reserved":   ":do { add address=10.0.0.0/8 list=NoNAT} on-error={}\n",
		"html error page": "<html><body>rate limited</body></html>\n",
	} {
		if _, err := Parse(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGateHoldsSizeWithinTwoPercent(t *testing.T) {
	last := cidr.Merge([]netip.Prefix{netip.MustParsePrefix("2.144.0.0/14"), netip.MustParsePrefix("5.0.0.0/8")})
	size := cidr.Size(last)

	if err := Gate(nil, last); err != nil {
		t.Errorf("first set: %v", err)
	}
	if err := Gate(last, last); err != nil {
		t.Errorf("unchanged: %v", err)
	}
	// Adding about 1.5% is fine.
	grow := append(last, netip.MustParsePrefix("31.0.0.0/14")) // 2^18 more on ~2^24
	if err := Gate(last, grow); err != nil {
		t.Errorf("+%.1f%%: %v", 100*float64(cidr.Size(grow)-size)/float64(size), err)
	}
	// A hostile edit is not.
	hostile := append(last, netip.MustParsePrefix("0.0.0.0/1"))
	if err := Gate(last, cidr.StripReserved(hostile)); err == nil {
		t.Error("0.0.0.0/1 accepted")
	}
	// Losing most of the set is not either.
	if err := Gate(last, []netip.Prefix{netip.MustParsePrefix("2.144.0.0/14")}); err == nil {
		t.Error("a shrunken set accepted")
	}
	if err := Gate(last, nil); err == nil {
		t.Error("an empty set accepted")
	}
}

func TestDivergenceIgnoresKnownExtras(t *testing.T) {
	ripe := []netip.Prefix{netip.MustParsePrefix("2.144.0.0/14")}
	extras := []netip.Prefix{netip.MustParsePrefix("5.160.0.0/16")}
	feed := []netip.Prefix{netip.MustParsePrefix("2.144.0.0/14"), netip.MustParsePrefix("5.160.0.0/16")}
	if d := Divergence(feed, ripe, extras); d != 0 {
		t.Errorf("divergence with only known extras = %d", d)
	}
	feed = append(feed, netip.MustParsePrefix("8.8.8.0/24"))
	if d := Divergence(feed, ripe, extras); d != 256 {
		t.Errorf("divergence = %d, want 256", d)
	}
}

func TestParseRIPEstat(t *testing.T) {
	body := `{"data":{"resources":{"ipv4":["2.144.0.0/14","5.22.0.0-5.22.0.255"],"ipv6":["2001:678:b0::/46"]}}}`
	got, err := ParseRIPEstat(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if cidr.Size(got) != 1<<18+256 {
		t.Errorf("RIPEstat parse = %v", got)
	}
}
