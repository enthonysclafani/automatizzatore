package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") || !CheckPassword(h, "correct horse battery") || CheckPassword(h, "correct horse batterY") {
		t.Fatalf("hash %s does not verify correctly", h)
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if CheckPasswordOrDummy("", "anything at all") {
		t.Fatal("empty hash matched")
	}
}

// RFC 6238 appendix B, SHA1 vectors (8 digits).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{59: "94287082", 1111111109: "07081804", 1234567890: "89005924", 2000000000: "69279037"}
	for unix, want := range cases {
		got, err := TOTPCode(secret, unix/30, 8)
		if err != nil || got != want {
			t.Errorf("T=%d: %s, want %s (%v)", unix, got, want, err)
		}
	}
}

func TestCheckTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := TOTPCode(secret, now.Unix()/30, 6)
	step, ok := CheckTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("current code rejected")
	}
	if _, ok := CheckTOTP(secret, code, now, step); ok {
		t.Fatal("replayed code accepted")
	}
	prev, _ := TOTPCode(secret, now.Unix()/30-1, 6)
	if _, ok := CheckTOTP(secret, prev, now, 0); !ok {
		t.Fatal("code from the previous step (clock drift) rejected")
	}
	old, _ := TOTPCode(secret, now.Unix()/30-3, 6)
	if _, ok := CheckTOTP(secret, old, now, 0); ok {
		t.Fatal("old code accepted")
	}
	if uri := TOTPURI(secret, "me@example.com", "Pendolo"); !strings.HasPrefix(uri, "otpauth://totp/Pendolo:me@example.com?") {
		t.Fatalf("uri = %s", uri)
	}
}

func TestLimiter(t *testing.T) {
	l := &Limiter{Max: 3, Window: time.Minute}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow("k", now) {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if l.Allow("k", now) {
		t.Fatal("fourth attempt allowed")
	}
	if !l.Allow("k", now.Add(2*time.Minute)) {
		t.Fatal("attempt after the window refused")
	}
	l.Reset("k")
	if !l.Allow("k", now) {
		t.Fatal("reset did not clear attempts")
	}
}

func TestTokens(t *testing.T) {
	a, b := Token("pdl_"), Token("pdl_")
	if a == b || !strings.HasPrefix(a, "pdl_") || len(HashToken(a)) != 64 {
		t.Fatal("tokens not random or hash wrong")
	}
}
