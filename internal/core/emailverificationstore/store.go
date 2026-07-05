package emailverificationstore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

type Token struct {
	UserID    string
	ExpiresAt int64
}

type Status struct {
	Verified bool
	Email    string
}

func Start(tx *sql.Tx, userID, email, token string, createdAt, expiresAt int64) error {
	if _, err := sqlstore.Exec(tx, `UPDATE users SET email_verified=0 WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err := sqlstore.Exec(tx,
		`INSERT INTO user_private_profiles (user_id, registration_email, updated_at)
		 VALUES (?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET registration_email=excluded.registration_email, updated_at=excluded.updated_at`,
		userID, email, createdAt,
	); err != nil {
		return err
	}
	if _, err := sqlstore.Exec(tx, `DELETE FROM email_verification_tokens WHERE user_id=?`, userID); err != nil {
		return err
	}
	_, err := sqlstore.Exec(tx,
		`INSERT INTO email_verification_tokens (token, user_id, email, created_at, expires_at) VALUES (?,?,?,?,?)`,
		token, userID, email, createdAt, expiresAt,
	)
	return err
}

func ClaimToken(db *sql.DB, token string) (Token, bool, error) {
	var out Token
	err := sqlstore.QueryRow(db,
		`SELECT user_id, expires_at FROM email_verification_tokens WHERE token=?`,
		token,
	).Scan(&out.UserID, &out.ExpiresAt)
	if err == sql.ErrNoRows {
		return Token{}, false, nil
	}
	if err != nil {
		return Token{}, false, err
	}
	res, err := sqlstore.Exec(db, `DELETE FROM email_verification_tokens WHERE token=?`, token)
	if err != nil {
		return Token{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Token{}, false, err
	}
	return out, n > 0, nil
}

func MarkVerified(db *sql.DB, userID string, verifiedAt int64) error {
	_, err := sqlstore.Exec(db, `UPDATE users SET email_verified=1, email_verified_at=? WHERE id=?`, verifiedAt, userID)
	return err
}

func UserStatus(db *sql.DB, userID string) (Status, error) {
	var (
		verified int
		out      Status
	)
	err := sqlstore.QueryRow(db,
		`SELECT u.email_verified, COALESCE(p.registration_email,'')
		   FROM users u
		   LEFT JOIN user_private_profiles p ON p.user_id=u.id
		  WHERE u.id=?`,
		userID,
	).Scan(&verified, &out.Email)
	if err == sql.ErrNoRows {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	out.Verified = verified != 0
	return out, nil
}

func EmailVerified(db *sql.DB, userID string) (bool, error) {
	status, err := UserStatus(db, userID)
	return status.Verified, err
}
