package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("correct horse battery", h) {
		t.Error("right password rejected")
	}
	if CheckPassword("correct horse batter", h) || CheckPassword("", h) {
		t.Error("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password must differ (random salt)")
	}
}

func TestShortPasswordRefused(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrWeakPassword {
		t.Errorf("got %v", err)
	}
}

func TestMalformedHashesAreJustNo(t *testing.T) {
	for _, h := range []string{
		"", "plain", "$argon2id$", "$argon2i$v=19$m=8,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=999999999,t=1,p=1$AAAA$AAAA", // would allocate gigabytes
		"$argon2id$v=19$m=8,t=1,p=1$!!!$!!!",
		"$argon2id$v=18$m=8,t=1,p=1$AAAA$AAAA",
	} {
		if CheckPassword("whatever", h) {
			t.Errorf("accepted %q", h)
		}
	}
}

func TestTokens(t *testing.T) {
	a, ha := NewToken()
	b, hb := NewToken()
	if a == b || ha == hb {
		t.Fatal("tokens must be unique")
	}
	if HashToken(a) != ha || ha == a {
		t.Error("stored form must be the hash, not the token")
	}
	if len(a) < 40 {
		t.Errorf("token too short: %d", len(a))
	}
}

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890"; the RFC lists
// 8-digit codes, these are their last six digits.
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, c := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		step, ok := VerifyTOTP(secret, c.code, time.Unix(c.unix, 0))
		if !ok || step != c.unix/30 {
			t.Errorf("t=%d code %s: ok=%v step=%d", c.unix, c.code, ok, step)
		}
	}
}

func TestTOTPWindowAndRejects(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := totpCode(secret, now.Unix()/30)
	if _, ok := VerifyTOTP(secret, code, now.Add(30*time.Second)); !ok {
		t.Error("one step of drift must be tolerated")
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(5*time.Minute)); ok {
		t.Error("a stale code must be refused")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok := VerifyTOTP(secret, bad, now); ok && bad != "000000" {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, ok := VerifyTOTP("not base32!!", "123456", now); ok {
		t.Error("garbage secret must not verify")
	}
}

func TestTOTPURI(t *testing.T) {
	u := TOTPURI("ABC", "me@example.com", "Stokehold")
	if !strings.HasPrefix(u, "otpauth://totp/Stokehold:me@example.com?") || !strings.Contains(u, "secret=ABC") {
		t.Errorf("unexpected uri %q", u)
	}
}
