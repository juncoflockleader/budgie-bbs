package twofactorstore

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

type BackupCode struct {
	ID        string
	CodeHash  string
	CreatedAt int64
}

type EmailCode struct {
	CodeHash  string
	ExpiresAt int64
}

type TOTPSecret struct {
	Secret   string
	LastStep int64
}

func SecuritySettings(db *sql.DB) (*accountmodel.SecuritySettings, error) {
	out := &accountmodel.SecuritySettings{}
	var req int
	err := sqlstore.QueryRow(db, `SELECT staff_2fa_required, updated_at FROM security_settings WHERE id='default'`).Scan(&req, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Staff2FARequired = req != 0
	return out, nil
}

func SetSecuritySettings(db *sql.DB, staff2FARequired bool, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO security_settings (id, staff_2fa_required, updated_at) VALUES ('default', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET staff_2fa_required=excluded.staff_2fa_required, updated_at=excluded.updated_at`,
		boolInt(staff2FARequired), updatedAt,
	)
	return err
}

func TwoFactorStatus(db *sql.DB, userID string) (accountmodel.TwoFactorStatus, error) {
	var st accountmodel.TwoFactorStatus
	var totpEnrolled, emailEnrolled int
	err := sqlstore.QueryRow(db, `SELECT totp_enrolled, email_enrolled FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&totpEnrolled, &emailEnrolled)
	if err == sql.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.TOTPEnrolled = totpEnrolled != 0
	st.EmailEnrolled = emailEnrolled != 0
	st.BackupCodesRemaining = BackupCodesRemaining(db, userID)
	return st, nil
}

func StorePendingTOTPSecret(db *sql.DB, userID, secret string, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO user_2fa_settings (user_id, totp_pending, updated_at) VALUES (?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET totp_pending=excluded.totp_pending, updated_at=excluded.updated_at`,
		userID, secret, updatedAt,
	)
	return err
}

func PendingTOTPSecret(db *sql.DB, userID string) (string, bool, error) {
	var secret string
	err := sqlstore.QueryRow(db, `SELECT totp_pending FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&secret)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(secret) == "" {
		return "", false, nil
	}
	return secret, true, nil
}

func ActivatePendingTOTP(db *sql.DB, userID, secret string, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`UPDATE user_2fa_settings SET totp_secret=?, totp_pending='', totp_enrolled=1, updated_at=? WHERE user_id=?`,
		secret, updatedAt, userID,
	)
	return err
}

func DisableTOTP(db *sql.DB, userID string, updatedAt int64) error {
	_, err := sqlstore.Exec(db, `UPDATE user_2fa_settings SET totp_secret='', totp_pending='', totp_enrolled=0, updated_at=? WHERE user_id=?`, updatedAt, userID)
	return err
}

func EnableEmail2FA(db *sql.DB, userID string, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO user_2fa_settings (user_id, email_enrolled, updated_at) VALUES (?,1,?)
		 ON CONFLICT(user_id) DO UPDATE SET email_enrolled=1, updated_at=excluded.updated_at`,
		userID, updatedAt,
	)
	return err
}

func DisableEmail2FA(db *sql.DB, userID string, updatedAt int64) error {
	_, err := sqlstore.Exec(db, `UPDATE user_2fa_settings SET email_enrolled=0, updated_at=? WHERE user_id=?`, updatedAt, userID)
	return err
}

func LoadTOTPSecret(db *sql.DB, userID string) (TOTPSecret, bool, error) {
	var secret TOTPSecret
	var enrolled int
	err := sqlstore.QueryRow(db, `SELECT totp_secret, totp_enrolled, totp_last_step FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&secret.Secret, &enrolled, &secret.LastStep)
	if err == sql.ErrNoRows {
		return TOTPSecret{}, false, nil
	}
	if err != nil {
		return TOTPSecret{}, false, err
	}
	if enrolled == 0 || strings.TrimSpace(secret.Secret) == "" {
		return TOTPSecret{}, false, nil
	}
	return secret, true, nil
}

func ClaimTOTPStep(db *sql.DB, userID string, step int64) (bool, error) {
	return execChanged(db, `UPDATE user_2fa_settings SET totp_last_step=? WHERE user_id=? AND totp_last_step < ?`, step, userID, step)
}

func BackupCodesRemaining(db *sql.DB, userID string) int {
	var n int
	_ = sqlstore.QueryRow(db, `SELECT COUNT(*) FROM two_factor_backup_codes WHERE user_id=? AND used=0`, userID).Scan(&n)
	return n
}

func ReplaceBackupCodes(tx *sql.Tx, userID string, codes []BackupCode) error {
	if _, err := sqlstore.Exec(tx, `DELETE FROM two_factor_backup_codes WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := sqlstore.Exec(tx,
			`INSERT INTO two_factor_backup_codes (id, user_id, code_hash, used, created_at) VALUES (?,?,?,0,?)`,
			code.ID, userID, code.CodeHash, code.CreatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func ClaimBackupCode(db *sql.DB, userID, codeHash string) (bool, error) {
	return execChanged(db,
		`UPDATE two_factor_backup_codes SET used=1 WHERE user_id=? AND code_hash=? AND used=0`,
		userID, codeHash,
	)
}

func StoreEmailCode(tx *sql.Tx, userID, codeHash string, createdAt, expiresAt int64) error {
	_, err := sqlstore.Exec(tx,
		`INSERT INTO two_factor_email_codes (user_id, code_hash, created_at, expires_at) VALUES (?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET code_hash=excluded.code_hash, created_at=excluded.created_at, expires_at=excluded.expires_at`,
		userID, codeHash, createdAt, expiresAt,
	)
	return err
}

func LoadEmailCode(db *sql.DB, userID string) (EmailCode, bool, error) {
	var code EmailCode
	err := sqlstore.QueryRow(db, `SELECT code_hash, expires_at FROM two_factor_email_codes WHERE user_id=?`, userID).Scan(&code.CodeHash, &code.ExpiresAt)
	if err == sql.ErrNoRows {
		return EmailCode{}, false, nil
	}
	if err != nil {
		return EmailCode{}, false, err
	}
	return code, true, nil
}

func DeleteEmailCode(db *sql.DB, userID string) error {
	_, err := sqlstore.Exec(db, `DELETE FROM two_factor_email_codes WHERE user_id=?`, userID)
	return err
}

func ClaimEmailCode(db *sql.DB, userID, codeHash string) (bool, error) {
	return execChanged(db, `DELETE FROM two_factor_email_codes WHERE user_id=? AND code_hash=?`, userID, codeHash)
}

func execChanged(execable sqlstore.SQLLike, query string, args ...any) (bool, error) {
	res, err := sqlstore.Exec(execable, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func ClearBackupCodesIfUnenrolled(db *sql.DB, userID string) error {
	var totpEnrolled, emailEnrolled int
	err := sqlstore.QueryRow(db, `SELECT totp_enrolled, email_enrolled FROM user_2fa_settings WHERE user_id=?`, userID).Scan(&totpEnrolled, &emailEnrolled)
	if err == nil && (totpEnrolled != 0 || emailEnrolled != 0) {
		return nil
	}
	_, err = sqlstore.Exec(db, `DELETE FROM two_factor_backup_codes WHERE user_id=?`, userID)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
