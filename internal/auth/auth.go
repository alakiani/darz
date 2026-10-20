// Package auth holds the credential primitives for Users (ADR 0001,
// ADR 0003): argon2id passwords and TOTP second factors.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: RFC 9106's second recommended option, about 50 ms
// on a small VM.
const (
	argonTime    = 2
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns an encoded argon2id hash of password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches an encoded hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	// Refuse parameters weaker than ours, or absurdly expensive ones.
	if m < argonMemory || m > 1<<20 || t < argonTime || t > 10 || p < 1 || p > 16 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	key, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(key) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}

// CheckPassword enforces the password policy: at least 10 characters.
// Length is what matters; no character classes are required.
func CheckPassword(password string) error {
	switch n := len([]rune(password)); {
	case n < 10:
		return errors.New("password must be at least 10 characters")
	case len(password) > 1024:
		return errors.New("password is too long")
	}
	return nil
}

// TOTP is RFC 6238 with SHA-1, 6 digits and a 30-second step: what every
// authenticator app speaks.
const (
	totpStep   = 30 * time.Second
	totpDigits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret, base32 as apps expect it.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// TOTP returns the code for a base32 secret at time t.
func TOTP(secret string, t time.Time) string {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	return totpAt(key, t)
}

func totpAt(key []byte, t time.Time) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/int64(totpStep/time.Second)))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

// VerifyTOTP checks a code against the current step and one step either
// side, for clock skew.
func VerifyTOTP(secret, code string, now time.Time) bool {
	if len(code) != totpDigits {
		return false
	}
	ok := 0
	for _, d := range []time.Duration{-totpStep, 0, totpStep} {
		ok |= subtle.ConstantTimeCompare([]byte(TOTP(secret, now.Add(d))), []byte(code))
	}
	return ok == 1
}

// TOTPURI is the otpauth:// URI an authenticator app scans.
func TOTPURI(secret, handle string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "WAYS")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/WAYS:" + url.PathEscape(handle) + "?" + q.Encode()
}
