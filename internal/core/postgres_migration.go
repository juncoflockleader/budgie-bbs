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
		{src: "users", dst: "users", cols: []string{"id", "name", "role", "password", "created"}},
		{src: "user_profiles", dst: "user_profiles", cols: []string{"user_id", "display_name", "bio", "avatar", "updated_at"}},
		{src: "auth_pubkeys", dst: "auth_pubkeys", cols: []string{"user_id", "pubkey"}},
		{src: "threads", dst: "threads", cols: []string{"id", "board", "author", "author_id", "title", "locked", "post_count", "last_seq", "created_ts", "created_at", "updated_at"}},
		{src: "posts", dst: "posts", cols: []string{"id", "thread", "author", "author_id", "body", "content_type", "reply_to", "version", "redacted", "created_seq", "updated_seq", "created_at", "updated_at"}},
		{src: "user_sanctions", dst: "user_sanctions", cols: []string{"id", "user_id", "kind", "scope", "expires_at", "by", "reason", "seq"}},
		{src: "thread_prefs", dst: "thread_prefs", cols: []string{"user_id", "thread_id", "level"}},
		{src: "notifications", dst: "notifications", cols: []string{"id", "user_id", "kind", "thread_id", "post_id", "actor", "read", "ts"}},
		{src: "polls", dst: "polls", cols: []string{"id", "post_id", "question", "expires_at", "ts"}},
		{src: "poll_options", dst: "poll_options", cols: []string{"id", "poll_id", "text", "position"}},
		{src: "poll_votes", dst: "poll_votes", cols: []string{"poll_id", "option_id", "user_id", "ts"}},
		{src: "post_reactions", dst: "post_reactions", cols: []string{"post_id", "user_id", "emoji", "ts"}},
		{src: "user_activity", dst: "user_activity", cols: []string{"user_id", "posts_created", "days_visited", "last_visit_day", "reactions_recv", "trust_level"}},
		{src: "moderation_reviews", dst: "moderation_reviews", cols: []string{"id", "kind", "status", "target_id", "target_kind", "reporter", "reason", "resolution", "actor", "created_at", "updated_at"}},
		{src: "outbox_jobs", dst: "outbox_jobs", cols: []string{"id", "kind", "payload", "status", "attempts", "next_run_at", "last_error", "created_at", "updated_at"}},
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
