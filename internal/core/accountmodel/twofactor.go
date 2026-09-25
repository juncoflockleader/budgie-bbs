package accountmodel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	Email2FACodeTTL = 10 * time.Minute
	TOTPIssuer      = "BudgieBBS"

	BackupCodeCount = 10
)

// backupCodeAlphabet excludes visually ambiguous characters.
const backupCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// SecuritySettings holds site-wide security policy.
type SecuritySettings struct {
	Staff2FARequired bool  `json:"staff2faRequired"`
	UpdatedAt        int64 `json:"updatedAt"`
}

// TwoFactorStatus reports a user's 2FA enrollment.
type TwoFactorStatus struct {
	TOTPEnrolled         bool `json:"totpEnrolled"`
	EmailEnrolled        bool `json:"emailEnrolled"`
	BackupCodesRemaining int  `json:"backupCodesRemaining"`
}

// Enrolled reports whether any second factor is set up. Backup codes are a
// fallback for an enrolled method, not an independent enrollment.
func (s TwoFactorStatus) Enrolled() bool { return s.TOTPEnrolled || s.EmailEnrolled }

// randomIndex returns a uniform random index in [0, n) using rejection sampling,
// avoiding the modulo bias of `byte % n` when n does not divide 256.
func randomIndex(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("randomIndex: range must be positive")
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

func RandomBackupCode() (string, error) {
	out := make([]byte, 8)
	for i := range out {
		idx, err := randomIndex(len(backupCodeAlphabet))
		if err != nil {
			return "", err
		}
		out[i] = backupCodeAlphabet[idx]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

func NormalizeBackupCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func HashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeBackupCode(code)))
	return hex.EncodeToString(sum[:])
}

func RandomNumericCode(n int) (string, error) {
	const digits = "0123456789"
	buf := make([]byte, n)
	for i := range buf {
		idx, err := randomIndex(len(digits))
		if err != nil {
			return "", err
		}
		buf[i] = digits[idx]
	}
	return string(buf), nil
}

func Email2FACodeMessage(code string) (subject, body string) {
	subject = "Your BudgieBBS sign-in code"
	body = fmt.Sprintf("Your verification code is %s\n\nIt expires in 10 minutes. If you did not try to sign in, you can ignore this message.", code)
	return subject, body
}
