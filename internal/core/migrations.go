package core

import (
	"database/sql"
	"fmt"
)

// Migration is a backend storage migration descriptor. SQLite uses executable
// Go helpers today; Postgres migrations are exposed as SQL strings in
// postgres_schema.go so deployment tooling can apply them later.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

func applySQLiteMigrations(db *sql.DB) error {
	if _, err := qExec(db, ddl); err != nil {
		return fmt.Errorf("apply base schema: %w", err)
	}

	columns := []struct {
		table string
		name  string
		ddl   string
	}{
		{"threads", "author_id", "author_id TEXT NOT NULL DEFAULT ''"},
		{"threads", "created_at", "created_at INTEGER NOT NULL DEFAULT 0"},
		{"threads", "updated_at", "updated_at INTEGER NOT NULL DEFAULT 0"},
		{"posts", "author_id", "author_id TEXT NOT NULL DEFAULT ''"},
		{"posts", "signature", "signature TEXT NOT NULL DEFAULT ''"},
		{"posts", "marked", "marked INTEGER NOT NULL DEFAULT 0"},
		{"posts", "recommended", "recommended INTEGER NOT NULL DEFAULT 0"},
		{"posts", "no_reply", "no_reply INTEGER NOT NULL DEFAULT 0"},
		{"posts", "tex", "tex INTEGER NOT NULL DEFAULT 0"},
		{"posts", "mail_back", "mail_back INTEGER NOT NULL DEFAULT 0"},
		{"posts", "source_post", "source_post TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_thread", "source_thread TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_board", "source_board TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_author", "source_author TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_author_id", "source_author_id TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_title", "source_title TEXT NOT NULL DEFAULT ''"},
		{"posts", "created_at", "created_at INTEGER NOT NULL DEFAULT 0"},
		{"posts", "updated_at", "updated_at INTEGER NOT NULL DEFAULT 0"},
		{"user_profiles", "title", "title TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "signature", "signature TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "plan", "plan TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "homepage", "homepage TEXT NOT NULL DEFAULT ''"},
		{"processed_commands", "actor_id", "actor_id TEXT NOT NULL DEFAULT ''"},
		{"processed_commands", "command_hash", "command_hash TEXT NOT NULL DEFAULT ''"},
		{"users", "deactivated_at", "deactivated_at INTEGER NOT NULL DEFAULT 0"},
		{"users", "deactivated_by", "deactivated_by TEXT NOT NULL DEFAULT ''"},
		{"users", "deactivated_reason", "deactivated_reason TEXT NOT NULL DEFAULT ''"},
		{"users", "registration_status", "registration_status TEXT NOT NULL DEFAULT 'approved'"},
		{"users", "reviewed_at", "reviewed_at INTEGER NOT NULL DEFAULT 0"},
		{"users", "reviewed_by", "reviewed_by TEXT NOT NULL DEFAULT ''"},
		{"users", "review_reason", "review_reason TEXT NOT NULL DEFAULT ''"},
		{"board_favorites", "folder_id", "folder_id TEXT NOT NULL DEFAULT ''"},
		{"user_activity", "login_count", "login_count INTEGER NOT NULL DEFAULT 0"},
		{"user_activity", "total_online_seconds", "total_online_seconds INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_logins", "total_logins INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_online_seconds", "total_online_seconds INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "online_guests", "online_guests INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "max_online_guests", "max_online_guests INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "max_online_guests_at", "max_online_guests_at INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_score", "min_score INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_post_count", "min_board_post_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_original_post_count", "min_board_original_post_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_digest_count", "min_board_digest_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_mark_count", "min_board_mark_count INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_manage_members", "can_manage_members INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_curate", "can_curate INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_moderate_posts", "can_moderate_posts INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_moderate_threads", "can_moderate_threads INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_announce", "can_announce INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_manage_polls", "can_manage_polls INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_set_board_settings", "can_set_board_settings INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "position", "position INTEGER NOT NULL DEFAULT 0"},
		{"board_settings", "stats_excluded", "stats_excluded INTEGER NOT NULL DEFAULT 0"},
		{"digest_entries", "body", "body TEXT NOT NULL DEFAULT ''"},
		{"digest_entries", "body_edited", "body_edited INTEGER NOT NULL DEFAULT 0"},
		{"user_presence", "mode", "mode TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "board_id", "board_id TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "thread_id", "thread_id TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "location_label", "location_label TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "from_host", "from_host TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.ddl); err != nil {
			return err
		}
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen ON user_presence(board_id, last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence board index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_members_board_position ON board_members(board_id, position, user_id)`); err != nil {
		return fmt.Errorf("ensure board member position index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_presence_sessions (
		    user_id        TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    session_id     TEXT    NOT NULL DEFAULT 'default',
		    status         TEXT    NOT NULL DEFAULT 'active',
		    mode           TEXT    NOT NULL DEFAULT '',
		    board_id       TEXT    NOT NULL DEFAULT '',
		    thread_id      TEXT    NOT NULL DEFAULT '',
		    location_label TEXT    NOT NULL DEFAULT '',
		    from_host      TEXT    NOT NULL DEFAULT '',
		    last_seen      INTEGER NOT NULL DEFAULT 0,
		    updated_at     INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (user_id, session_id)
		)`); err != nil {
		return fmt.Errorf("ensure user presence sessions table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen ON user_presence_sessions(last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence sessions last_seen index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen ON user_presence_sessions(board_id, last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence sessions board index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS guest_presence_sessions (
		    session_id     TEXT    PRIMARY KEY,
		    status         TEXT    NOT NULL DEFAULT 'active',
		    location_label TEXT    NOT NULL DEFAULT '',
		    from_host      TEXT    NOT NULL DEFAULT '',
		    last_seen      INTEGER NOT NULL DEFAULT 0,
		    updated_at     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure guest presence sessions table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_guest_presence_sessions_last_seen ON guest_presence_sessions(last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure guest presence sessions last_seen index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS community_stat_history (
		    day                   TEXT    PRIMARY KEY,
		    snapshot_at           INTEGER NOT NULL DEFAULT 0,
		    total_users           INTEGER NOT NULL DEFAULT 0,
		    total_boards          INTEGER NOT NULL DEFAULT 0,
		    total_threads         INTEGER NOT NULL DEFAULT 0,
		    total_posts           INTEGER NOT NULL DEFAULT 0,
		    total_reactions       INTEGER NOT NULL DEFAULT 0,
		    total_mail            INTEGER NOT NULL DEFAULT 0,
		    total_direct_messages INTEGER NOT NULL DEFAULT 0,
		    total_logins          INTEGER NOT NULL DEFAULT 0,
		    total_online_seconds  INTEGER NOT NULL DEFAULT 0,
		    online_users          INTEGER NOT NULL DEFAULT 0,
		    online_guests         INTEGER NOT NULL DEFAULT 0,
		    max_online_users      INTEGER NOT NULL DEFAULT 0,
		    max_online_at         INTEGER NOT NULL DEFAULT 0,
		    max_online_guests     INTEGER NOT NULL DEFAULT 0,
		    max_online_guests_at  INTEGER NOT NULL DEFAULT 0,
		    head_seq              INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure community stat history table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot ON community_stat_history(snapshot_at DESC, day DESC)`); err != nil {
		return fmt.Errorf("ensure community stat history index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS login_hourly_stats (
		    day         TEXT    NOT NULL,
		    hour        INTEGER NOT NULL CHECK(hour >= 0 AND hour <= 23),
		    login_count INTEGER NOT NULL DEFAULT 0,
		    updated_at  INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (day, hour)
		)`); err != nil {
		return fmt.Errorf("ensure login hourly stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_login_hourly_stats_updated ON login_hourly_stats(updated_at DESC, day DESC, hour)`); err != nil {
		return fmt.Errorf("ensure login hourly stats index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS content_filters (
		    id         TEXT PRIMARY KEY,
		    pattern    TEXT NOT NULL DEFAULT '',
		    scope      TEXT NOT NULL DEFAULT 'global',
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_by TEXT NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure content filters table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_content_filters_active_scope ON content_filters(active, scope, updated_at DESC)`); err != nil {
		return fmt.Errorf("ensure content filters index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS account_registration_settings (
		    id               TEXT PRIMARY KEY DEFAULT 'default',
		    require_approval INTEGER NOT NULL DEFAULT 0,
		    updated_at       INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure account registration settings table: %w", err)
	}
	if _, err := qExec(db,
		`INSERT OR IGNORE INTO account_registration_settings (id, require_approval, updated_at)
		 VALUES ('default', 0, 0)`,
	); err != nil {
		return fmt.Errorf("ensure account registration settings row: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS password_recovery_requests (
		    id              TEXT PRIMARY KEY,
		    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    status          TEXT NOT NULL DEFAULT 'pending',
		    submitted_name  TEXT NOT NULL DEFAULT '',
		    submitted_email TEXT NOT NULL DEFAULT '',
		    note            TEXT NOT NULL DEFAULT '',
		    reviewer_id     TEXT NOT NULL DEFAULT '',
		    review_note     TEXT NOT NULL DEFAULT '',
		    created_at      INTEGER NOT NULL DEFAULT 0,
		    updated_at      INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure password recovery requests table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_password_recovery_status_updated ON password_recovery_requests(status, updated_at DESC, created_at DESC)`); err != nil {
		return fmt.Errorf("ensure password recovery index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS digest_directories (
		    id         TEXT    PRIMARY KEY,
		    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    kind       TEXT    NOT NULL DEFAULT 'archive',
		    path       TEXT    NOT NULL DEFAULT '',
		    created_by TEXT    NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0,
		    UNIQUE(board_id, kind, path)
		)`); err != nil {
		return fmt.Errorf("ensure digest directories table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path ON digest_directories(board_id, kind, path)`); err != nil {
		return fmt.Errorf("ensure digest directories index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_signatures (
		    id         TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    label      TEXT    NOT NULL DEFAULT '',
		    body       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user signatures table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_signatures_user_position ON user_signatures(user_id, position, updated_at, id)`); err != nil {
		return fmt.Errorf("ensure user signatures index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_signature_settings (
		    user_id               TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    selected_signature_id TEXT    NOT NULL DEFAULT '',
		    random_enabled        INTEGER NOT NULL DEFAULT 0,
		    updated_at            INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user signature settings table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_login_acl_settings (
		    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    enabled    INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user login acl settings table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_login_acl_rules (
		    id         TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    pattern    TEXT    NOT NULL DEFAULT '',
		    note       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user login acl rules table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_login_acl_rules_user_position ON user_login_acl_rules(user_id, position, updated_at, id)`); err != nil {
		return fmt.Errorf("ensure user login acl rules index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS blessings (
		    id           TEXT    PRIMARY KEY,
		    from_user_id TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    to_user_id   TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    message      TEXT    NOT NULL DEFAULT '',
		    created_at   INTEGER NOT NULL DEFAULT 0,
		    seq          INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure blessings table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_blessings_to_created ON blessings(to_user_id, created_at DESC, seq DESC)`); err != nil {
		return fmt.Errorf("ensure blessings target index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_blessings_from_created ON blessings(from_user_id, created_at DESC, seq DESC)`); err != nil {
		return fmt.Errorf("ensure blessings sender index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS recommended_boards (
		    board_id   TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
		    note       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    curated_by TEXT    NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure recommended boards table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_recommended_boards_position ON recommended_boards(position, updated_at DESC, board_id)`); err != nil {
		return fmt.Errorf("ensure recommended boards index: %w", err)
	}

	ts := nowMS()
	updates := []string{
		`UPDATE threads SET author_id = COALESCE((SELECT id FROM users WHERE users.name = threads.author), '') WHERE author_id = ''`,
		`UPDATE threads SET created_at = created_ts WHERE created_at = 0`,
		`UPDATE threads SET updated_at = created_ts WHERE updated_at = 0`,
		`UPDATE posts SET author_id = COALESCE((SELECT id FROM users WHERE users.name = posts.author), '') WHERE author_id = ''`,
		`UPDATE posts SET created_at = COALESCE((SELECT ts FROM events WHERE events.seq = posts.created_seq), ?) WHERE created_at = 0`,
		`UPDATE posts SET updated_at = COALESCE((SELECT ts FROM events WHERE events.seq = posts.updated_seq), created_at) WHERE updated_at = 0`,
		`INSERT OR IGNORE INTO categories (id, name, description, position, created_at, updated_at)
		 SELECT id, name, description, 0, ?, ? FROM boards`,
		`INSERT OR IGNORE INTO user_profiles (user_id, display_name, updated_at)
		 SELECT id, name, ? FROM users`,
		`INSERT OR IGNORE INTO user_signatures (id, user_id, label, body, position, active, created_at, updated_at)
		 SELECT 'sig_profile_' || u.id, u.id, 'Profile signature', up.signature, 0, 1, ?, ?
		   FROM users u
		   JOIN user_profiles up ON up.user_id=u.id
		  WHERE TRIM(COALESCE(up.signature,'')) <> ''`,
		`INSERT OR IGNORE INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 SELECT u.id, 'sig_profile_' || u.id, 0, ?
		   FROM users u
		   JOIN user_profiles up ON up.user_id=u.id
		  WHERE TRIM(COALESCE(up.signature,'')) <> ''`,
		`INSERT OR IGNORE INTO user_presence_sessions (
		    user_id, session_id, status, mode, board_id, thread_id,
		    location_label, from_host, last_seen, updated_at
		 )
		 SELECT user_id, 'default', status, mode, board_id, thread_id,
		        location_label, from_host, last_seen, updated_at
		   FROM user_presence`,
		`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at)
		 VALUES (1, 'sqlite-foundation', ?)`,
	}
	args := [][]any{
		nil,
		nil,
		nil,
		nil,
		{ts},
		nil,
		{ts, ts},
		{ts},
		{ts, ts},
		{ts},
		{},
		{ts},
	}
	for i, stmt := range updates {
		if _, err := qExec(db, stmt, args[i]...); err != nil {
			return fmt.Errorf("apply sqlite data migration %d: %w", i+1, err)
		}
	}
	return nil
}

func ensureColumn(db *sql.DB, table, name, columnDDL string) error {
	rows, err := qQuery(db, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			colName    string
			colType    string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &defaultVal, &pk); err != nil {
			return err
		}
		if colName == name {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := qExec(db, `ALTER TABLE `+table+` ADD COLUMN `+columnDDL); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, name, err)
	}
	return nil
}
