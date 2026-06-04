package core

// PostgresMigrations exposes the production schema target. The SQLite runtime
// remains the default in this repo; deployment tooling can apply these SQL
// migrations when the Postgres EventStore implementation is enabled.
func PostgresMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "postgres-event-store-foundation",
			SQL: `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    BIGINT PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    seq        BIGSERIAL PRIMARY KEY,
    id         TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_scopes (
    seq   BIGINT NOT NULL REFERENCES events(seq) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    PRIMARY KEY (seq, scope)
);
CREATE INDEX IF NOT EXISTS idx_event_scopes_scope_seq
    ON event_scopes(scope, seq);

CREATE TABLE IF NOT EXISTS processed_commands (
    actor_id     TEXT NOT NULL,
    cid          TEXT NOT NULL,
    command_hash TEXT NOT NULL,
    result_json  JSONB NOT NULL,
    processed_at BIGINT NOT NULL,
    PRIMARY KEY (actor_id, cid)
);

CREATE TABLE IF NOT EXISTS outbox_jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_run_at  BIGINT NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_outbox_jobs_ready
    ON outbox_jobs(status, next_run_at, created_at);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (1, 'postgres-event-store-foundation', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 2,
			Name:    "postgres-forum-projections",
			SQL: `
CREATE TABLE IF NOT EXISTS boards (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT ''
);
INSERT INTO boards (id, name, description)
VALUES ('general', 'General', 'General discussion')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS categories (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    parent_id   TEXT NOT NULL DEFAULT '',
    position    INTEGER NOT NULL DEFAULT 0,
    visibility  TEXT NOT NULL DEFAULT 'public',
    created_at  BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS users (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL UNIQUE,
    role     TEXT NOT NULL DEFAULT 'user',
    password TEXT NOT NULL,
    created  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id      TEXT PRIMARY KEY REFERENCES users(id),
    display_name TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    avatar       TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS auth_pubkeys (
    user_id TEXT NOT NULL REFERENCES users(id),
    pubkey  TEXT NOT NULL,
    PRIMARY KEY (user_id, pubkey)
);

CREATE TABLE IF NOT EXISTS threads (
    id         TEXT PRIMARY KEY,
    board      TEXT NOT NULL REFERENCES categories(id),
    author     TEXT NOT NULL,
    author_id  TEXT NOT NULL REFERENCES users(id),
    title      TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    post_count INTEGER NOT NULL DEFAULT 0,
    last_seq   BIGINT NOT NULL DEFAULT 0,
    created_ts BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS posts (
    id           TEXT PRIMARY KEY,
    thread       TEXT NOT NULL REFERENCES threads(id),
    author       TEXT NOT NULL,
    author_id    TEXT NOT NULL REFERENCES users(id),
    body         TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'markup',
    reply_to     TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    redacted     BOOLEAN NOT NULL DEFAULT FALSE,
    created_seq  BIGINT NOT NULL,
    updated_seq  BIGINT NOT NULL,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_sanctions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    kind       TEXT NOT NULL,
    scope      TEXT NOT NULL DEFAULT 'global',
    expires_at BIGINT NOT NULL DEFAULT 0,
    by         TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    seq        BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS thread_prefs (
    user_id TEXT NOT NULL REFERENCES users(id),
    thread_id TEXT NOT NULL REFERENCES threads(id),
    level TEXT NOT NULL DEFAULT 'normal',
    PRIMARY KEY (user_id, thread_id)
);

CREATE TABLE IF NOT EXISTS notifications (
    id        TEXT PRIMARY KEY,
    user_id   TEXT NOT NULL REFERENCES users(id),
    kind      TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    post_id   TEXT NOT NULL,
    actor     TEXT NOT NULL,
    read      INTEGER NOT NULL DEFAULT 0,
    ts        BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifs_user_unread
    ON notifications(user_id, read, ts DESC);

CREATE TABLE IF NOT EXISTS user_activity (
    user_id        TEXT PRIMARY KEY REFERENCES users(id),
    posts_created  INTEGER NOT NULL DEFAULT 0,
    days_visited   INTEGER NOT NULL DEFAULT 0,
    last_visit_day TEXT NOT NULL DEFAULT '',
    reactions_recv INTEGER NOT NULL DEFAULT 0,
    trust_level    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS polls (
    id         TEXT PRIMARY KEY,
    post_id    TEXT NOT NULL REFERENCES posts(id),
    question   TEXT NOT NULL DEFAULT '',
    expires_at BIGINT NOT NULL DEFAULT 0,
    ts         BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS poll_options (
    id       TEXT PRIMARY KEY,
    poll_id  TEXT NOT NULL REFERENCES polls(id),
    text     TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS poll_votes (
    poll_id   TEXT NOT NULL REFERENCES polls(id),
    option_id TEXT NOT NULL REFERENCES poll_options(id),
    user_id   TEXT NOT NULL REFERENCES users(id),
    ts        BIGINT NOT NULL,
    PRIMARY KEY (poll_id, user_id)
);

CREATE TABLE IF NOT EXISTS post_reactions (
    post_id TEXT    NOT NULL REFERENCES posts(id),
    user_id TEXT    NOT NULL REFERENCES users(id),
    emoji   TEXT    NOT NULL DEFAULT 'heart',
    ts      BIGINT  NOT NULL,
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS moderation_reviews (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open',
    target_id   TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    reporter    TEXT NOT NULL REFERENCES users(id),
    reason      TEXT NOT NULL DEFAULT '',
    resolution  TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_moderation_reviews_status_created
    ON moderation_reviews(status, created_at DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (2, 'postgres-forum-projections', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
	}
}
