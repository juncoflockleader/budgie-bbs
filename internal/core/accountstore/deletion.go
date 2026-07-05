package accountstore

import (
	"database/sql"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func UserByIDTx(tx *sql.Tx, id string) (*projections.User, error) {
	u := &projections.User{}
	err := sqlstore.QueryRow(tx, `SELECT id, name, role, password, created,
	        COALESCE(NULLIF(registration_status,''), 'approved'), COALESCE(reviewed_at,0), COALESCE(reviewed_by,''), COALESCE(review_reason,''),
	        COALESCE(deactivated_at,0), COALESCE(deactivated_by,''), COALESCE(deactivated_reason,'')
	    FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created,
			&u.RegistrationStatus, &u.ReviewedAt, &u.ReviewedBy, &u.ReviewReason,
			&u.DeactivatedAt, &u.DeactivatedBy, &u.DeactivatedReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func OtherAdminCountTx(tx *sql.Tx, userID string) (int, error) {
	var count int
	err := sqlstore.QueryRow(tx, `SELECT COUNT(*) FROM users WHERE role='admin' AND id<>?`, userID).Scan(&count)
	return count, err
}

func EnsureDeletedUserTx(tx *sql.Tx, actorID string, ts int64) error {
	var exists int
	if err := sqlstore.QueryRow(tx, `SELECT 1 FROM users WHERE id=?`, accountmodel.DeletedUserID).Scan(&exists); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	name := "deleted-user"
	for i := 0; i < 10; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("deleted-user-%d", i)
		}
		var conflictingID string
		err := sqlstore.QueryRow(tx, `SELECT id FROM users WHERE name=?`, candidate).Scan(&conflictingID)
		if err == sql.ErrNoRows {
			name = candidate
			break
		}
		if err != nil {
			return err
		}
		if conflictingID == accountmodel.DeletedUserID {
			name = candidate
			break
		}
	}
	if _, err := sqlstore.Exec(tx,
		`INSERT INTO users (id, name, role, password, created, registration_status, reviewed_at, reviewed_by, review_reason, deactivated_at, deactivated_by, deactivated_reason)
		 VALUES (?, ?, 'user', '', ?, 'rejected', ?, ?, 'system tombstone account', ?, ?, 'system tombstone account')`,
		accountmodel.DeletedUserID, name, ts, ts, actorID, ts, actorID,
	); err != nil {
		return err
	}
	_, err := sqlstore.Exec(tx,
		`INSERT OR IGNORE INTO user_profiles (user_id, display_name, updated_at) VALUES (?, ?, ?)`,
		accountmodel.DeletedUserID, accountmodel.DeletedUserDisplayName, ts,
	)
	return err
}

func PurgeUserTx(tx *sql.Tx, actorID string, target *projections.User, ts int64) error {
	targetID := target.ID
	oldName := target.Name
	cleanup := []struct {
		query string
		args  []any
	}{
		{`UPDATE posts_fts SET author=? WHERE post_id IN (SELECT id FROM posts WHERE author_id=?)`, []any{accountmodel.DeletedUserDisplayName, targetID}},
		{`UPDATE threads SET author=?, author_id=? WHERE author_id=?`, []any{accountmodel.DeletedUserDisplayName, accountmodel.DeletedUserID, targetID}},
		{`UPDATE posts SET author=?, author_id=?, signature='' WHERE author_id=?`, []any{accountmodel.DeletedUserDisplayName, accountmodel.DeletedUserID, targetID}},
		{`UPDATE relay_deliveries SET author_id=?, author_name=? WHERE author_id=?`, []any{accountmodel.DeletedUserID, accountmodel.DeletedUserDisplayName, targetID}},
		{`UPDATE post_attachments SET created_by=? WHERE created_by=?`, []any{accountmodel.DeletedUserID, targetID}},
		{`UPDATE mail_attachments SET created_by=? WHERE created_by=?`, []any{accountmodel.DeletedUserID, targetID}},
		{`UPDATE digest_entries SET created_by=?, updated_at=? WHERE created_by=?`, []any{actorID, ts, targetID}},
		{`UPDATE digest_directories SET created_by=?, updated_at=? WHERE created_by=?`, []any{actorID, ts, targetID}},
		{`UPDATE users SET reviewed_by='' WHERE reviewed_by=?`, []any{targetID}},
		{`UPDATE account_registration_settings SET updated_at=? WHERE id='default' AND updated_at=0`, []any{ts}},
		{`UPDATE password_recovery_requests SET reviewer_id='', review_note='' WHERE reviewer_id=?`, []any{targetID}},
		{`UPDATE board_member_applications SET reviewer_id='', review_note='' WHERE reviewer_id=?`, []any{targetID}},
		{`UPDATE user_sanctions SET by=? WHERE by=?`, []any{accountmodel.DeletedUserID, targetID}},
		{`UPDATE moderation_reviews SET actor=? WHERE actor=? OR actor=?`, []any{accountmodel.DeletedUserDisplayName, targetID, oldName}},
		{`UPDATE notifications SET actor=? WHERE actor=?`, []any{accountmodel.DeletedUserDisplayName, oldName}},
		{`DELETE FROM mail_messages WHERE from_user_id=?`, []any{targetID}},
		{`DELETE FROM direct_messages WHERE from_user_id=? OR to_user_id=?`, []any{targetID, targetID}},
		{`DELETE FROM moderation_reviews WHERE reporter=?`, []any{targetID}},
		{`DELETE FROM post_reactions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM poll_votes WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM thread_prefs WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM notifications WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM cursors WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM auth_pubkeys WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_sanctions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM processed_commands WHERE actor_id=?`, []any{targetID}},
		{`DELETE FROM processed_commands_v2 WHERE actor_id=?`, []any{targetID}},
		{`DELETE FROM command_log_receipts WHERE actor_id=?`, []any{targetID}},
		{`DELETE FROM user_activity WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_copies WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_messages WHERE id NOT IN (SELECT DISTINCT message_id FROM mail_copies)`, nil},
		{`DELETE FROM mail_group_members WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM mail_groups WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM password_recovery_requests WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM favorite_folders WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_favorites WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_moderators WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_members WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_member_applications WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM direct_message_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_relationships WHERE user_id=? OR target_user_id=?`, []any{targetID, targetID}},
		{`DELETE FROM user_presence WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_presence_sessions WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM board_read_markers WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM thread_read_markers WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_private_profiles WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_personal_files WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_signatures WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_signature_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_login_acl_rules WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_login_acl_settings WHERE user_id=?`, []any{targetID}},
		{`DELETE FROM user_profiles WHERE user_id=?`, []any{targetID}},
	}
	for _, step := range cleanup {
		if _, err := sqlstore.Exec(tx, step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}

func DeleteUserTx(tx *sql.Tx, userID string) error {
	_, err := sqlstore.Exec(tx, `DELETE FROM users WHERE id=?`, userID)
	return err
}
