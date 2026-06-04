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
		{"posts", "created_at", "created_at INTEGER NOT NULL DEFAULT 0"},
		{"posts", "updated_at", "updated_at INTEGER NOT NULL DEFAULT 0"},
		{"processed_commands", "actor_id", "actor_id TEXT NOT NULL DEFAULT ''"},
		{"processed_commands", "command_hash", "command_hash TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.ddl); err != nil {
			return err
		}
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
