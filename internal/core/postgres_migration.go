package core

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
)

// OpenPostgres opens a PostgreSQL connection using lib/pq DSN format.
func OpenPostgres(dsn string) (*sql.DB, error) {
	return sql.Open("postgres", dsn)
}

// ApplyPostgresMigrations applies the embedded Postgres migration set.
func ApplyPostgresMigrations(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	for _, m := range PostgresMigrations() {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MigrateSQLiteToPostgres copies core tables plus the event stream from a SQLite
// database into PostgreSQL while preserving original event IDs/sequence numbers.
func MigrateSQLiteToPostgres(ctx context.Context, sqlitePath, pgDSN string) error {
	sqliteDB, err := sql.Open("sqlite", "file:"+sqlitePath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return fmt.Errorf("open sqlite source: %w", err)
	}
	defer sqliteDB.Close()

	pgDB, err := OpenPostgres(pgDSN)
	if err != nil {
		return fmt.Errorf("open postgres destination: %w", err)
	}
	defer pgDB.Close()

	if err := applySQLiteMigrationsForMigration(sqliteDB); err != nil {
		return fmt.Errorf("prepare source db: %w", err)
	}
	if err := ApplyPostgresMigrations(ctx, pgDB); err != nil {
		return fmt.Errorf("apply postgres schema: %w", err)
	}

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres migrate tx: %w", err)
	}
	defer tx.Rollback() //nolint

	tableCopies := []struct {
		src  string
		dst  string
		cols []string
	}{
		{src: "schema_migrations", dst: "schema_migrations", cols: []string{"version", "name", "applied_at"}},
		{src: "boards", dst: "boards", cols: []string{"id", "name", "description"}},
		{src: "categories", dst: "categories", cols: []string{"id", "name", "description", "parent_id", "position", "visibility", "created_at", "updated_at"}},
		{src: "users", dst: "users", cols: []string{"id", "name", "role", "password", "created", "registration_status", "reviewed_at", "reviewed_by", "review_reason", "deactivated_at", "deactivated_by", "deactivated_reason"}},
		{src: "account_registration_settings", dst: "account_registration_settings", cols: []string{"id", "require_approval", "updated_at"}},
		{src: "password_recovery_requests", dst: "password_recovery_requests", cols: []string{"id", "user_id", "status", "submitted_name", "submitted_email", "note", "reviewer_id", "review_note", "created_at", "updated_at"}},
		{src: "user_profiles", dst: "user_profiles", cols: []string{"user_id", "display_name", "title", "bio", "avatar", "signature", "plan", "homepage", "updated_at"}},
		{src: "user_private_profiles", dst: "user_private_profiles", cols: []string{"user_id", "real_name", "real_email", "registration_email", "address", "phone", "mobile", "birthday", "school", "contact_note", "updated_at"}},
		{src: "user_personal_files", dst: "user_personal_files", cols: []string{"user_id", "name", "body", "public", "updated_at"}},
		{src: "user_signatures", dst: "user_signatures", cols: []string{"id", "user_id", "label", "body", "position", "active", "created_at", "updated_at"}},
		{src: "user_signature_settings", dst: "user_signature_settings", cols: []string{"user_id", "selected_signature_id", "random_enabled", "updated_at"}},
		{src: "user_login_acl_settings", dst: "user_login_acl_settings", cols: []string{"user_id", "enabled", "updated_at"}},
		{src: "user_login_acl_rules", dst: "user_login_acl_rules", cols: []string{"id", "user_id", "pattern", "note", "position", "active", "created_at", "updated_at"}},
		{src: "auth_pubkeys", dst: "auth_pubkeys", cols: []string{"user_id", "pubkey"}},
		{src: "mail_messages", dst: "mail_messages", cols: []string{"id", "from_user_id", "subject", "body", "parent_id", "created_at", "seq"}},
		{src: "mail_copies", dst: "mail_copies", cols: []string{"message_id", "user_id", "role", "mailbox", "read", "kept", "updated_at"}},
		{src: "mail_attachments", dst: "mail_attachments", cols: []string{"id", "message_id", "filename", "content_type", "size_bytes", "url", "created_by", "created_at"}},
		{src: "mail_attachment_blobs", dst: "mail_attachment_blobs", cols: []string{"attachment_id", "data", "content_type", "size_bytes", "uploaded_at"}},
		{src: "mail_groups", dst: "mail_groups", cols: []string{"id", "user_id", "name", "created_at", "updated_at"}},
		{src: "mail_group_members", dst: "mail_group_members", cols: []string{"group_id", "user_id", "position", "created_at"}},
		{src: "user_relationships", dst: "user_relationships", cols: []string{"user_id", "target_user_id", "kind", "note", "created_at", "updated_at"}},
		{src: "blessings", dst: "blessings", cols: []string{"id", "from_user_id", "to_user_id", "message", "created_at", "seq"}},
		{src: "recommended_boards", dst: "recommended_boards", cols: []string{"board_id", "note", "position", "curated_by", "created_at", "updated_at"}},
		{src: "user_presence", dst: "user_presence", cols: []string{"user_id", "status", "mode", "board_id", "thread_id", "location_label", "from_host", "last_seen", "updated_at"}},
		{src: "user_presence_sessions", dst: "user_presence_sessions", cols: []string{"user_id", "session_id", "status", "mode", "board_id", "thread_id", "location_label", "from_host", "last_seen", "updated_at"}},
		{src: "guest_presence_sessions", dst: "guest_presence_sessions", cols: []string{"session_id", "status", "location_label", "from_host", "last_seen", "updated_at"}},
		{src: "direct_message_settings", dst: "direct_message_settings", cols: []string{"user_id", "policy", "updated_at"}},
		{src: "direct_messages", dst: "direct_messages", cols: []string{"id", "conversation_id", "from_user_id", "to_user_id", "body", "read_at", "sender_deleted", "recipient_deleted", "created_at", "seq"}},
		{src: "threads", dst: "threads", cols: []string{"id", "board", "author", "author_id", "title", "locked", "post_count", "last_seq", "created_ts", "created_at", "updated_at"}},
		{src: "posts", dst: "posts", cols: []string{"id", "thread", "author", "author_id", "body", "signature", "content_type", "reply_to", "version", "redacted", "marked", "recommended", "no_reply", "tex", "mail_back", "source_post", "source_thread", "source_board", "source_author", "source_author_id", "source_title", "created_seq", "updated_seq", "created_at", "updated_at"}},
		{src: "user_sanctions", dst: "user_sanctions", cols: []string{"id", "user_id", "kind", "scope", "expires_at", "by", "reason", "seq"}},
		{src: "content_filters", dst: "content_filters", cols: []string{"id", "pattern", "scope", "active", "created_by", "created_at", "updated_at"}},
		{src: "thread_prefs", dst: "thread_prefs", cols: []string{"user_id", "thread_id", "level"}},
		{src: "notifications", dst: "notifications", cols: []string{"id", "user_id", "kind", "thread_id", "post_id", "actor", "read", "ts"}},
		{src: "polls", dst: "polls", cols: []string{"id", "post_id", "question", "expires_at", "ts"}},
		{src: "poll_options", dst: "poll_options", cols: []string{"id", "poll_id", "text", "position"}},
		{src: "poll_votes", dst: "poll_votes", cols: []string{"poll_id", "option_id", "user_id", "ts"}},
		{src: "post_reactions", dst: "post_reactions", cols: []string{"post_id", "user_id", "emoji", "ts"}},
		{src: "digest_entries", dst: "digest_entries", cols: []string{"id", "board_id", "target_kind", "target_id", "kind", "title", "path", "note", "body", "body_edited", "created_by", "created_at", "updated_at"}},
		{src: "digest_directories", dst: "digest_directories", cols: []string{"id", "board_id", "kind", "path", "created_by", "created_at", "updated_at"}},
		{src: "user_activity", dst: "user_activity", cols: []string{"user_id", "login_count", "posts_created", "days_visited", "last_visit_day", "reactions_recv", "total_online_seconds", "trust_level"}},
		{src: "community_stat_history", dst: "community_stat_history", cols: []string{"day", "snapshot_at", "total_users", "total_boards", "total_threads", "total_posts", "total_reactions", "total_mail", "total_direct_messages", "total_logins", "total_online_seconds", "online_users", "online_guests", "max_online_users", "max_online_at", "max_online_guests", "max_online_guests_at", "head_seq"}},
		{src: "login_hourly_stats", dst: "login_hourly_stats", cols: []string{"day", "hour", "login_count", "updated_at"}},
		{src: "moderation_reviews", dst: "moderation_reviews", cols: []string{"id", "kind", "status", "target_id", "target_kind", "reporter", "reason", "resolution", "actor", "created_at", "updated_at"}},
		{src: "outbox_jobs", dst: "outbox_jobs", cols: []string{"id", "kind", "payload", "status", "attempts", "next_run_at", "last_error", "created_at", "updated_at"}},
		{src: "relay_deliveries", dst: "relay_deliveries", cols: []string{"id", "board_id", "thread_id", "post_id", "author_id", "author_name", "title", "body", "status", "last_error", "created_at", "updated_at", "seq"}},
		{src: "processed_commands", dst: "processed_commands", cols: []string{"actor_id", "cid", "command_hash", "result_json", "processed_at"}},
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO categories (id, name, description, created_at, updated_at)
		 SELECT b.id, b.name, b.description, ?, ?
		 FROM boards b
		 ON CONFLICT (id) DO NOTHING`,
		nowMS(), nowMS(),
	); err != nil {
		return fmt.Errorf("seed categories from boards: %w", err)
	}

	for _, t := range tableCopies {
		if err := copyTableRows(ctx, tx, sqliteDB, t.src, t.dst, t.cols); err != nil {
			return fmt.Errorf("copy %s: %w", t.src, err)
		}
	}

	maxEventSeq, err := copyEvents(ctx, tx, sqliteDB)
	if err != nil {
		return fmt.Errorf("copy events: %w", err)
	}

	if maxEventSeq > 0 {
		if _, err := tx.ExecContext(ctx, "SELECT setval('events_seq', $1, true)", maxEventSeq); err != nil {
			return fmt.Errorf("adjust event sequence: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}
	return nil
}

func copyTableRows(ctx context.Context, tx *sql.Tx, src *sql.DB, srcTable, dstTable string, columns []string) error {
	colList := strings.Join(columns, ",")
	placeholder := postgresPlaceholders(len(columns))
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING", dstTable, colList, placeholder)
	query := fmt.Sprintf("SELECT %s FROM %s", colList, srcTable)

	rows, err := src.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range pointers {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		values = normalizeSQLiteValues(values)
		if _, err := tx.ExecContext(ctx, insertSQL, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyEvents(ctx context.Context, tx *sql.Tx, src *sql.DB) (int64, error) {
	rows, err := src.QueryContext(ctx, `SELECT seq, id, kind, payload, ts, scopes FROM events ORDER BY seq`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var maxSeq int64
	for rows.Next() {
		var (
			seq     int64
			id      string
			kind    string
			payload any
			ts      int64
			scopes  string
		)
		if err := rows.Scan(&seq, &id, &kind, &payload, &ts, &scopes); err != nil {
			return 0, err
		}
		payloadText, ok := payload.(string)
		if !ok {
			if bs, ok2 := payload.([]byte); ok2 {
				payloadText = string(bs)
			}
		}

		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO events (seq, id, kind, payload, created_at)
			 VALUES ($1, $2, $3, $4::jsonb, $5)
			 ON CONFLICT (seq) DO UPDATE
			   SET id=EXCLUDED.id, kind=EXCLUDED.kind, payload=EXCLUDED.payload, created_at=EXCLUDED.created_at`,
			seq,
			id,
			kind,
			payloadText,
			ts,
		)
		if err != nil {
			return 0, err
		}
		if err := copyEventScopes(ctx, tx, src, seq, scopes); err != nil {
			return 0, err
		}
		maxSeq = seq
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return maxSeq, nil
}

func copyEventScopes(ctx context.Context, tx *sql.Tx, src *sql.DB, seq int64, fallbackScope string) error {
	if fallbackScope != "" {
		for _, raw := range strings.Split(fallbackScope, ",") {
			scope := strings.TrimSpace(raw)
			if scope == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_scopes (seq, scope) VALUES ($1, $2) ON CONFLICT DO NOTHING`, seq, scope); err != nil {
				return err
			}
		}
		return nil
	}

	rows, err := src.QueryContext(ctx, `SELECT scope FROM event_scopes WHERE seq = ?`, seq)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO event_scopes (seq, scope) VALUES ($1, $2) ON CONFLICT DO NOTHING`, seq, scope)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func postgresPlaceholders(n int) string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = "$" + strconv.Itoa(i+1)
	}
	return strings.Join(out, ",")
}

func normalizeSQLiteValues(in []any) []any {
	out := make([]any, len(in))
	for i, v := range in {
		if bs, ok := v.([]byte); ok {
			out[i] = string(bs)
		} else {
			out[i] = v
		}
	}
	return out
}

// applySQLiteMigrationsForMigration ensures source tables used by migration exist.
func applySQLiteMigrationsForMigration(db *sql.DB) error {
	return applySQLiteMigrations(db)
}
