// Package auth holds the building blocks of sign-in: password hashing,
// one-time codes (TOTP), random tokens and a login rate limiter.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) is defined over HMAC-SHA1; authenticator apps expect it
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommendation: 19 MiB, 2 iterations).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

// MinPasswordLength is enforced on every new password.
const MinPasswordLength = 12

// ErrWeakPassword is returned for passwords that are too short.
var ErrWeakPassword = fmt.Errorf("the password must be at least %d characters", MinPasswordLength)

// HashPassword returns an argon2id hash in the PHC string format.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", ErrWeakPassword
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches hash.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory uint32
	var iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want))) //nolint:gosec // length of a 32-byte hash
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked when a user does not exist, so a wrong email takes
// as long as a wrong password.
var dummyHash, _ = HashPassword("not-a-real-password-for-timing")

// CheckPasswordOrDummy checks against hash, or a dummy hash when it is empty.
func CheckPasswordOrDummy(hash, password string) bool {
	if hash == "" {
		CheckPassword(dummyHash, password)
		return false
	}
	return CheckPassword(hash, password)
}

// Token returns a random token with a prefix, e.g. "pdl_..." for API tokens.
func Token(prefix string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashToken is how session and API tokens are stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TOTP parameters (RFC 6238 defaults, what authenticator apps use).
const (
	totpPeriod = 30
	totpDigits = 6
)

// NewTOTPSecret returns a random base32 secret.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// TOTPURI is the otpauth:// URI that authenticator apps scan.
func TOTPURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "period": {"30"}, "digits": {"6"}, "algorithm": {"SHA1"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCode computes the code for a time step.
func TOTPCode(secret string, step int64, digits int) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", errors.New("invalid TOTP secret")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // time steps are positive
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod), nil
}

// CheckTOTP verifies a code at time now, allowing one step of clock drift.
// It returns the matched step; a step at or below last is a replay and fails.
func CheckTOTP(secret, code string, now time.Time, last int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for _, step := range []int64{current - 1, current, current + 1} {
		if step <= last {
			continue
		}
		want, err := TOTPCode(secret, step, totpDigits)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// Limiter allows a few attempts per key within a window, e.g. logins per
// email and per IP address.
type Limiter struct {
	Max    int
	Window time.Duration
	mu     sync.Mutex
	hits   map[string][]time.Time
}

// Allow records an attempt for key and reports whether it is within limits.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.Window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.Max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	if len(l.hits) > 10000 { // forget old keys so the map cannot grow without bound
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > l.Window {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// Reset forgets the attempts for key, after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
