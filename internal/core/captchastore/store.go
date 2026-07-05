package captchastore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

type Challenge struct {
	AnswerHash string
	ExpiresAt  int64
}

func PruneExpired(db *sql.DB, now int64) error {
	_, err := sqlstore.Exec(db, `DELETE FROM captcha_challenges WHERE expires_at < ?`, now)
	return err
}

func InsertChallenge(db *sql.DB, id, answerHash string, createdAt, expiresAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO captcha_challenges (id, answer_hash, created_at, expires_at) VALUES (?,?,?,?)`,
		id, answerHash, createdAt, expiresAt,
	)
	return err
}

func ClaimChallenge(db *sql.DB, id string) (Challenge, bool, error) {
	var ch Challenge
	err := sqlstore.QueryRow(db,
		`SELECT answer_hash, expires_at FROM captcha_challenges WHERE id=?`,
		id,
	).Scan(&ch.AnswerHash, &ch.ExpiresAt)
	if err == sql.ErrNoRows {
		return Challenge{}, false, nil
	}
	if err != nil {
		return Challenge{}, false, err
	}
	res, err := sqlstore.Exec(db, `DELETE FROM captcha_challenges WHERE id=?`, id)
	if err != nil {
		return Challenge{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Challenge{}, false, err
	}
	return ch, n > 0, nil
}
