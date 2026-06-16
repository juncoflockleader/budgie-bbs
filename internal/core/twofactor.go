package core

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/juncoflockleader/budgie-bbs/internal/totp"
)

var (
	// ErrTwoFactorNotEnrolled is returned when a 2FA action needs an enrollment
	// the user does not have.
	ErrTwoFactorNotEnrolled = errors.New("two-factor authentication is not enrolled")
	// ErrTwoFactorInvalidCode is returned for a wrong or expired code.
	ErrTwoFactorInvalidCode = errors.New("invalid or expired verification code")
	// ErrTwoFactorNoEmail is returned when email 2FA is requested but no address
	// is on file.
	ErrTwoFactorNoEmail = errors.New("no email address on file for email two-factor")
)

const (
	outboxEmail2FACode = "email.2fa"
	email2FACodeTTL    = 10 * time.Minute
	totpIssuer         = "BudgieBBS"
)

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

// SecuritySettings returns the site security settings (zero value if unset).
func (c *Core) SecuritySettings() (*SecuritySettings, error) {
	out := &SecuritySettings{}
	var req int
	err := qQueryRow(c.DB, `SELECT staff_2fa_required, updated_at FROM security_settings WHERE id='default'`).Scan(&req, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Staff2FARequired = req != 0
	return out, nil
}

// SetSecuritySettings toggles whether staff (admin/moderator) must complete 2FA.
func (c *Core) SetSecuritySettings(staff2FARequired bool) (*SecuritySettings, error) {
	if _, err := qExec(c.DB,
		`INSERT INTO security_settings (id, staff_2fa_required, updated_at) VALUES ('default', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET staff_2fa_required=excluded.staff_2fa_required, updated_at=excluded.updated_at`,
		boolToInt(staff2FARequired), nowMS()); err != nil {
		return nil, err
	}
	return c.SecuritySettings()
}

// TwoFactorStatus returns a user's enrollment state.
func (c *Core) TwoFactorStatus(userID string) (TwoFactorStatus, error) {
	var st TwoFactorStatus
	var totpEnrolled, emailEnrolled int
	err := qQueryRow(c.DB, `SELECT totp_enrolled, email_enrolled FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&totpEnrolled, &emailEnrolled)
	if err == sql.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.TOTPEnrolled = totpEnrolled != 0
	st.EmailEnrolled = emailEnrolled != 0
	st.BackupCodesRemaining = c.BackupCodesRemaining(userID)
	return st, nil
}

// BeginTOTPEnrollment generates a pending TOTP secret (not yet active) and the
// otpauth URI the authenticator app consumes. Call ConfirmTOTPEnrollment with a
// code to activate it.
func (c *Core) BeginTOTPEnrollment(userID, accountName string) (secret, uri string, err error) {
	secret, err = totp.NewSecret()
	if err != nil {
		return "", "", err
	}
	if _, err = qExec(c.DB,
		`INSERT INTO user_2fa_settings (user_id, totp_pending, updated_at) VALUES (?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET totp_pending=excluded.totp_pending, updated_at=excluded.updated_at`,
		userID, secret, nowMS()); err != nil {
		return "", "", err
	}
	return secret, totp.OTPAuthURI(totpIssuer, accountName, secret), nil
}

// ConfirmTOTPEnrollment activates a pending TOTP secret once the user proves
// possession with a valid code.
func (c *Core) ConfirmTOTPEnrollment(userID, code string) error {
	var pending string
	err := qQueryRow(c.DB, `SELECT totp_pending FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&pending)
	if err == sql.ErrNoRows || strings.TrimSpace(pending) == "" {
		return ErrTwoFactorNotEnrolled
	}
	if err != nil {
		return err
	}
	if !totp.Validate(pending, code, time.Now().Unix(), 1) {
		return ErrTwoFactorInvalidCode
	}
	_, err = qExec(c.DB,
		`UPDATE user_2fa_settings SET totp_secret=?, totp_pending='', totp_enrolled=1, updated_at=? WHERE user_id=?`,
		pending, nowMS(), userID)
	return err
}

// DisableTOTP removes a user's authenticator enrollment.
func (c *Core) DisableTOTP(userID string) error {
	if _, err := qExec(c.DB, `UPDATE user_2fa_settings SET totp_secret='', totp_pending='', totp_enrolled=0, updated_at=? WHERE user_id=?`, nowMS(), userID); err != nil {
		return err
	}
	return c.clearBackupCodesIfUnenrolled(userID)
}

// EnableEmail2FA turns on email-code 2FA; requires an email on file.
func (c *Core) EnableEmail2FA(userID string) error {
	if c.userRegistrationEmail(userID) == "" {
		return ErrTwoFactorNoEmail
	}
	_, err := qExec(c.DB,
		`INSERT INTO user_2fa_settings (user_id, email_enrolled, updated_at) VALUES (?,1,?)
		 ON CONFLICT(user_id) DO UPDATE SET email_enrolled=1, updated_at=excluded.updated_at`,
		userID, nowMS())
	return err
}

// DisableEmail2FA turns off email-code 2FA.
func (c *Core) DisableEmail2FA(userID string) error {
	if _, err := qExec(c.DB, `UPDATE user_2fa_settings SET email_enrolled=0, updated_at=? WHERE user_id=?`, nowMS(), userID); err != nil {
		return err
	}
	return c.clearBackupCodesIfUnenrolled(userID)
}

const backupCodeCount = 10

// backupCodeAlphabet excludes visually ambiguous characters.
const backupCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// GenerateBackupCodes issues a fresh set of single-use recovery codes (replacing
// any existing ones) and returns the plaintext codes to show once. Requires an
// enrolled second factor.
func (c *Core) GenerateBackupCodes(userID string) ([]string, error) {
	st, err := c.TwoFactorStatus(userID)
	if err != nil {
		return nil, err
	}
	if !st.Enrolled() {
		return nil, ErrTwoFactorNotEnrolled
	}
	now := nowMS()
	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint
	if _, err := qExec(tx, `DELETE FROM two_factor_backup_codes WHERE user_id=?`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		code, err := randomBackupCode()
		if err != nil {
			return nil, err
		}
		if _, err := qExec(tx,
			`INSERT INTO two_factor_backup_codes (id, user_id, code_hash, used, created_at) VALUES (?,?,?,0,?)`,
			newID("bkp_"), userID, hashBackupCode(code), now); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// VerifyBackupCode consumes a single-use recovery code, returning nil on success.
func (c *Core) VerifyBackupCode(userID, code string) error {
	if normalizeBackupCode(code) == "" {
		return ErrTwoFactorInvalidCode
	}
	res, err := qExec(c.DB,
		`UPDATE two_factor_backup_codes SET used=1 WHERE user_id=? AND code_hash=? AND used=0`,
		userID, hashBackupCode(code))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTwoFactorInvalidCode
	}
	return nil
}

// BackupCodesRemaining counts a user's unused recovery codes.
func (c *Core) BackupCodesRemaining(userID string) int {
	var n int
	_ = qQueryRow(c.DB, `SELECT COUNT(*) FROM two_factor_backup_codes WHERE user_id=? AND used=0`, userID).Scan(&n)
	return n
}

func (c *Core) clearBackupCodesIfUnenrolled(userID string) error {
	var totpEnrolled, emailEnrolled int
	err := qQueryRow(c.DB, `SELECT totp_enrolled, email_enrolled FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&totpEnrolled, &emailEnrolled)
	if err == nil && (totpEnrolled != 0 || emailEnrolled != 0) {
		return nil
	}
	_, err = qExec(c.DB, `DELETE FROM two_factor_backup_codes WHERE user_id=?`, userID)
	return err
}

func randomBackupCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i := range out {
		out[i] = backupCodeAlphabet[int(buf[i])%len(backupCodeAlphabet)]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

func normalizeBackupCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeBackupCode(code)))
	return hex.EncodeToString(sum[:])
}

// TwoFactorRequiredForLogin reports whether the given staff user must pass a 2FA
// challenge at login: enforcement on, role is admin/moderator, and they are
// enrolled. Un-enrolled staff are handled separately (StaffShouldEnroll2FA).
func (c *Core) TwoFactorRequiredForLogin(userID, role string) (bool, error) {
	if role != "admin" && role != "moderator" {
		return false, nil
	}
	ss, err := c.SecuritySettings()
	if err != nil {
		return false, err
	}
	if !ss.Staff2FARequired {
		return false, nil
	}
	st, err := c.TwoFactorStatus(userID)
	if err != nil {
		return false, err
	}
	return st.Enrolled(), nil
}

// StaffShouldEnroll2FA reports whether enforcement is on for this staff user but
// they have no second factor yet (UI nudges them to enroll).
func (c *Core) StaffShouldEnroll2FA(userID, role string) (bool, error) {
	if role != "admin" && role != "moderator" {
		return false, nil
	}
	ss, err := c.SecuritySettings()
	if err != nil || !ss.Staff2FARequired {
		return false, err
	}
	st, err := c.TwoFactorStatus(userID)
	if err != nil {
		return false, err
	}
	return !st.Enrolled(), nil
}

// VerifyTOTP checks an authenticator code against the user's active secret.
func (c *Core) VerifyTOTP(userID, code string) error {
	var secret string
	var enrolled int
	err := qQueryRow(c.DB, `SELECT totp_secret, totp_enrolled FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&secret, &enrolled)
	if err == sql.ErrNoRows || enrolled == 0 || strings.TrimSpace(secret) == "" {
		return ErrTwoFactorNotEnrolled
	}
	if err != nil {
		return err
	}
	if !totp.Validate(secret, code, time.Now().Unix(), 1) {
		return ErrTwoFactorInvalidCode
	}
	return nil
}

// SendEmail2FACode mints a one-time 6-digit code, stores it hashed with a TTL,
// and enqueues the email.
func (c *Core) SendEmail2FACode(userID string) error {
	st, err := c.TwoFactorStatus(userID)
	if err != nil {
		return err
	}
	if !st.EmailEnrolled {
		return ErrTwoFactorNotEnrolled
	}
	email := c.userRegistrationEmail(userID)
	if email == "" {
		return ErrTwoFactorNoEmail
	}
	code, err := randomNumericCode(6)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := nowMS()
	exp := now + email2FACodeTTL.Milliseconds()
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if _, err := qExec(tx,
		`INSERT INTO two_factor_email_codes (user_id, code_hash, created_at, expires_at) VALUES (?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET code_hash=excluded.code_hash, created_at=excluded.created_at, expires_at=excluded.expires_at`,
		userID, string(hash), now, exp); err != nil {
		return err
	}
	subject := "Your BudgieBBS sign-in code"
	body := fmt.Sprintf("Your verification code is %s\n\nIt expires in 10 minutes. If you did not try to sign in, you can ignore this message.", code)
	if err := enqueueOutboxJob(tx, outboxEmail2FACode, emailSendJob{From: mailFrom, To: email, Subject: subject, Body: body, TS: now}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyEmail2FACode checks a single-use email code and consumes it on success.
func (c *Core) VerifyEmail2FACode(userID, code string) error {
	var hash string
	var exp int64
	err := qQueryRow(c.DB, `SELECT code_hash, expires_at FROM two_factor_email_codes WHERE user_id=?`, userID).Scan(&hash, &exp)
	if err == sql.ErrNoRows {
		return ErrTwoFactorInvalidCode
	}
	if err != nil {
		return err
	}
	if nowMS() > exp {
		_, _ = qExec(c.DB, `DELETE FROM two_factor_email_codes WHERE user_id=?`, userID)
		return ErrTwoFactorInvalidCode
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(strings.TrimSpace(code))) != nil {
		return ErrTwoFactorInvalidCode
	}
	// Atomically claim the code so it cannot be redeemed twice by concurrent
	// requests. Guarding on the exact stored hash means a wrong guess (rejected
	// above) never consumes the code, while the correct code stays single-use
	// under a race: exactly one deleter wins, the rest get 0 rows and fail.
	res, err := qExec(c.DB, `DELETE FROM two_factor_email_codes WHERE user_id=? AND code_hash=?`, userID, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTwoFactorInvalidCode
	}
	return nil
}

func (c *Core) userRegistrationEmail(userID string) string {
	var email string
	_ = qQueryRow(c.DB, `SELECT COALESCE(registration_email,'') FROM user_private_profiles WHERE user_id=?`, userID).Scan(&email)
	return strings.TrimSpace(email)
}

func randomNumericCode(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const digits = "0123456789"
	for i := range buf {
		buf[i] = digits[int(buf[i])%10]
	}
	return string(buf), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
