package awg

import (
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedAWGParamsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		p := Generate()
		if err := Validate(p); err != nil {
			t.Fatalf("generated invalid params %+v: %v", p, err)
		}
		seen[p.H1] = true
	}
	if len(seen) < 1990 {
		t.Errorf("only %d distinct H1 ranges in 2000 draws; params are not random enough", len(seen))
	}
}

func TestValidateAWGRejects(t *testing.T) {
	good := Generate()
	cases := map[string]func(p *awgParams){
		"jmin > jmax":                  func(p *awgParams) { p.Jmin, p.Jmax = 500, 400 },
		"jmax not under MTU":           func(p *awgParams) { p.Jmax = 1500 },
		"jc out of range":              func(p *awgParams) { p.Jc = 0 },
		"s1 + 56 == s2":                func(p *awgParams) { p.S1, p.S2 = 20, 76 },
		"h ranges overlap":             func(p *awgParams) { p.H2 = p.H1 },
		"h range covers plain WG":      func(p *awgParams) { p.H1 = "1-100" },
		"h range malformed":            func(p *awgParams) { p.H3 = "9-x" },
		"s4 too big for link MTU":      func(p *awgParams) { p.S4 = 200 },
		"decoy packet bad tag":         func(p *awgParams) { p.I1 = "<q 10>" },
		"decoy packet bigger than mtu": func(p *awgParams) { p.I2 = "<r 1400>" },
	}
	for name, mutate := range cases {
		p := good
		mutate(&p)
		if err := Validate(p); err == nil {
			t.Errorf("%s: accepted %+v", name, p)
		}
	}
}

func TestChoosePortAvoidsReservedPorts(t *testing.T) {
	for i := 0; i < 5000; i++ {
		port := ChoosePort()
		if port < 10000 || port > 60000 || port == 443 || port == 53 {
			t.Fatalf("ChoosePort() = %d", port)
		}
	}
}

func TestHRangeParsing(t *testing.T) {
	lo, hi, err := parseHRange("100-200")
	if err != nil || lo != 100 || hi != 200 {
		t.Errorf("parseHRange = %d, %d, %v", lo, hi, err)
	}
	if lo, hi, err := parseHRange(strconv.Itoa(7)); err != nil || lo != 7 || hi != 7 {
		t.Errorf("single value = %d, %d, %v", lo, hi, err)
	}
	if _, _, err := parseHRange(strings.Repeat("9", 12)); err == nil {
		t.Error("accepted a value beyond uint32")
	}
}
