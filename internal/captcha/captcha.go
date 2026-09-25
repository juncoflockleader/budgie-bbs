// Package captcha provides the pure, dependency-free pieces of signup captcha:
// random challenge codes, a self-hosted distorted-text SVG renderer, answer
// hashing, and a verifier for third-party providers (reCAPTCHA / hCaptcha /
// Turnstile). It holds no state and no database — challenge persistence and
// configuration live in the caller (core/httpapi).
package captcha

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// codeAlphabet excludes visually ambiguous characters (0/O, 1/I/L) so the
// rendered text is unambiguous for humans.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// RandomCode returns a random challenge code of n characters from a
// human-unambiguous alphabet.
func RandomCode(n int) string {
	if n <= 0 {
		n = 5
	}
	b := make([]byte, n)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("captcha: crypto/rand unavailable: " + err.Error())
		}
		b[i] = codeAlphabet[idx.Int64()]
	}
	return string(b)
}

// NormalizeAnswer canonicalizes a submitted answer for case-insensitive,
// whitespace-insensitive comparison.
func NormalizeAnswer(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// HashAnswer returns an HMAC of the normalized answer bound to the challenge id,
// so stored challenge rows never contain the plaintext answer and a hash for one
// challenge cannot be replayed against another.
func HashAnswer(secret, challengeID, answer string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(challengeID))
	mac.Write([]byte{0})
	mac.Write([]byte(NormalizeAnswer(answer)))
	return hex.EncodeToString(mac.Sum(nil))
}

// AnswerMatches reports whether a submitted answer matches a stored hash, in
// constant time.
func AnswerMatches(secret, challengeID, answer, storedHash string) bool {
	want := HashAnswer(secret, challengeID, answer)
	return hmac.Equal([]byte(want), []byte(storedHash))
}

// randInt returns a uniform int in [0,n) using crypto/rand.
func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic("captcha: crypto/rand unavailable: " + err.Error())
	}
	return int(v.Int64())
}

// RenderSVG produces a standalone, self-contained distorted-text SVG for the
// given code: per-character rotation and vertical jitter plus a few noise lines,
// so it resists trivial OCR while staying dependency-free. The output is a
// complete <svg> document suitable for inlining or a data URI.
func RenderSVG(code string) string {
	const (
		w      = 200
		h      = 70
		cellW  = 32
		startX = 18
		midY   = 44
	)
	colors := []string{"#2b3a55", "#3a2b55", "#553a2b", "#2b5540", "#552b3a"}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="captcha challenge">`, w, h, w, h)
	b.WriteString(`<rect width="100%" height="100%" fill="#f3f4f6"/>`)

	// Background noise lines.
	for i := 0; i < 5; i++ {
		x1, y1 := randInt(w), randInt(h)
		x2, y2 := randInt(w), randInt(h)
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.4"/>`,
			x1, y1, x2, y2, colors[randInt(len(colors))])
	}

	// Characters, each rotated and jittered.
	for i, ch := range code {
		x := startX + i*cellW + randInt(6) - 3
		y := midY + randInt(12) - 6
		rot := randInt(40) - 20
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" font-family="monospace" font-size="38" font-weight="700" fill="%s" transform="rotate(%d %d %d)">%s</text>`,
			x, y, colors[randInt(len(colors))], rot, x, y, string(ch))
	}

	// A couple of foreground speckle dots.
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1" fill="%s" opacity="0.5"/>`,
			randInt(w), randInt(h), colors[randInt(len(colors))])
	}
	b.WriteString(`</svg>`)
	return b.String()
}
