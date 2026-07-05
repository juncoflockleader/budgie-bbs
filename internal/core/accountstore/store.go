package accountstore

import (
	"database/sql"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func SaveRegistrationIntake(db *sql.DB, userID string, intake accountmodel.NormalizedRegistrationIntake, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO user_private_profiles (user_id, real_name, school, contact_note, policy_accepted_at, policy_version, updated_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   real_name=excluded.real_name,
		   school=excluded.school,
		   contact_note=excluded.contact_note,
		   policy_accepted_at=excluded.policy_accepted_at,
		   policy_version=excluded.policy_version,
		   updated_at=excluded.updated_at`,
		userID, intake.RealName, intake.Affiliation, intake.Note, intake.PolicyAcceptedAt, intake.PolicyVersion, updatedAt,
	)
	return err
}

func ApproveUser(db *sql.DB, userID string) error {
	_, err := sqlstore.Exec(db, `UPDATE users SET registration_status='approved' WHERE id=?`, userID)
	return err
}

func UpdatePassword(db *sql.DB, userID, passwordHash string, changedAtSeconds int64) error {
	_, err := sqlstore.Exec(db, `UPDATE users SET password=?, password_changed_at=? WHERE id=?`, passwordHash, changedAtSeconds, userID)
	return err
}

func RevokeSessions(db *sql.DB, userID string, validAfterSeconds int64) error {
	_, err := sqlstore.Exec(db, `UPDATE users SET sessions_valid_after=? WHERE id=?`, validAfterSeconds, userID)
	return err
}

func AddPubkey(db *sql.DB, userID, pubkey string) error {
	_, err := sqlstore.Exec(db,
		`INSERT OR IGNORE INTO auth_pubkeys (user_id, pubkey) VALUES (?,?)`,
		userID, pubkey,
	)
	return err
}

func DeactivateTx(tx *sql.Tx, userID, reason string, ts int64) error {
	_, err := sqlstore.Exec(tx,
		`UPDATE users SET deactivated_at=?, deactivated_by=?, deactivated_reason=? WHERE id=? AND deactivated_at=0`,
		ts, userID, reason, userID,
	)
	return err
}

func RegistrationStateTx(tx *sql.Tx) (int, bool, error) {
	var userCount int
	if err := sqlstore.QueryRow(tx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return 0, false, err
	}
	if userCount == 0 {
		return userCount, false, nil
	}
	var requireApproval int
	err := sqlstore.QueryRow(tx, `SELECT COALESCE(require_approval,0) FROM account_registration_settings WHERE id='default'`).Scan(&requireApproval)
	if err == sql.ErrNoRows {
		return userCount, false, nil
	}
	return userCount, requireApproval != 0, err
}

func CreateRegisteredUserTx(tx *sql.Tx, id, name, role, passwordHash, status string, ts int64) error {
	_, err := sqlstore.Exec(tx,
		`INSERT INTO users (id, name, role, password, created, registration_status) VALUES (?,?,?,?,?,?)`,
		id, name, role, passwordHash, ts, status,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	_, err = sqlstore.Exec(tx,
		`INSERT OR IGNORE INTO user_profiles (user_id, display_name, updated_at) VALUES (?,?,?)`,
		id, name, ts,
	)
	if err != nil {
		return fmt.Errorf("create user profile: %w", err)
	}
	if _, err := sqlstore.Exec(tx,
		`INSERT OR IGNORE INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 VALUES (?, '', 0, ?)`,
		id, ts,
	); err != nil {
		return fmt.Errorf("create user signature settings: %w", err)
	}
	if _, err := sqlstore.Exec(tx,
		`INSERT OR IGNORE INTO user_login_acl_settings (user_id, enabled, updated_at)
		 VALUES (?, 0, ?)`,
		id, ts,
	); err != nil {
		return fmt.Errorf("create user login acl settings: %w", err)
	}
	if err := seedDefaultFavorites(tx, id, ts); err != nil {
		return fmt.Errorf("seed default favorites: %w", err)
	}
	return nil
}

func seedDefaultFavorites(tx *sql.Tx, userID string, ts int64) error {
	_, err := sqlstore.Exec(tx,
		`INSERT INTO board_favorites (user_id, board_id, folder_id, position, created_at, updated_at)
		 SELECT ?, id, '', 0, ?, ? FROM boards WHERE id='general'
		 ON CONFLICT(user_id, board_id) DO NOTHING`,
		userID, ts, ts,
	)
	return err
}
