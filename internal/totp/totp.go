// Package totp implements RFC 6238 time-based one-time passwords (the
// authenticator-app second factor) using only the standard library. Codes are
// 6 digits over a 30-second period with HMAC-SHA1, matching Google
// Authenticator, Authy, 1Password, and friends.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
)

const (
	period = 30 // seconds per code
	digits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh base32-encoded secret (160 bits, no padding).
func NewSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// codeAt computes the TOTP code for a secret at a given 30s counter value.
func codeAt(secret string, counter uint64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("totp: bad secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	mod := bin % 1_000_000 // 10^digits
	return fmt.Sprintf("%06d", mod), nil
}

// CodeAtTime returns the code valid at the given unix time (seconds). Used by
// tests and by the email-less verification path.
func CodeAtTime(secret string, unixSeconds int64) (string, error) {
	return codeAt(secret, uint64(unixSeconds/period))
}

// Validate reports whether code matches the secret around unixSeconds, allowing
// +/- skew steps of clock drift (skew=1 → accepts the previous, current, and
// next 30s windows). Comparison is constant-time.
func Validate(secret, code string, unixSeconds int64, skew int) bool {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	if skew < 0 {
		skew = 0
	}
	base := unixSeconds / period
	for d := -skew; d <= skew; d++ {
		want, err := codeAt(secret, uint64(base+int64(d)))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// OTPAuthURI builds the otpauth://totp/ URI that authenticator apps consume
// (via QR or manual entry). issuer and account are shown to the user.
func OTPAuthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", digits))
	q.Set("period", fmt.Sprintf("%d", period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
