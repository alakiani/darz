package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashVerifies(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Errorf("hash = %q, want an argon2id encoding", h)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Error("the right password does not verify")
	}
	if VerifyPassword(h, "correct horse batterY") {
		t.Error("a wrong password verifies")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password are equal: no salt")
	}
	for _, junk := range []string{"", "$argon2id$v=19$m=1,t=1,p=1$x$y", "plain"} {
		if VerifyPassword(junk, "x") {
			t.Errorf("junk hash %q verifies", junk)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, bad := range []string{"", "short", strings.Repeat("x", 9), strings.Repeat("x", 1025)} {
		if CheckPassword(bad) == nil {
			t.Errorf("accepted %d-char password", len(bad))
		}
	}
	if err := CheckPassword("ten chars!"); err != nil {
		t.Errorf("rejected a 10-char password: %v", err)
	}
}

// RFC 6238 appendix B, SHA-1, with the 20-byte ASCII secret
// "12345678901234567890", truncated to 6 digits.
func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		if got := totpAt(secret, time.Unix(unix, 0)); got != want {
			t.Errorf("TOTP at %d = %s, want %s", unix, got, want)
		}
	}
}

func TestTOTPAcceptsOneStepOfSkewOnly(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		if !VerifyTOTP(secret, TOTP(secret, now.Add(d)), now) {
			t.Errorf("code from %v away rejected", d)
		}
	}
	for _, d := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		if VerifyTOTP(secret, TOTP(secret, now.Add(d)), now) {
			t.Errorf("code from %v away accepted", d)
		}
	}
	if VerifyTOTP(secret, "12345", now) || VerifyTOTP(secret, "abcdef", now) {
		t.Error("a malformed code verifies")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("JBSWY3DPEHPK3PXP", "maryam")
	if uri != "otpauth://totp/DARZ:maryam?algorithm=SHA1&digits=6&issuer=DARZ&period=30&secret=JBSWY3DPEHPK3PXP" {
		t.Errorf("uri = %s", uri)
	}
}
