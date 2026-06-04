package core

const ddl = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    applied_at INTEGER NOT NULL
);

-- Append-only event log. seq is the global monotonic cursor.
CREATE TABLE IF NOT EXISTS events (
    seq     INTEGER PRIMARY KEY AUTOINCREMENT,
    id      TEXT    NOT NULL UNIQUE,
    kind    TEXT    NOT NULL,
    scopes  TEXT    NOT NULL,  -- comma-separated scope list
    payload TEXT    NOT NULL,  -- JSON
    ts      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS event_scopes (
    seq   INTEGER NOT NULL REFERENCES events(seq) ON DELETE CASCADE,
    scope TEXT    NOT NULL,
    PRIMARY KEY (seq, scope)
);
CREATE INDEX IF NOT EXISTS idx_event_scopes_scope_seq
    ON event_scopes(scope, seq);

-- Projection tables — derived from the log, rebuildable by full replay.

CREATE TABLE IF NOT EXISTS boards (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS users (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL UNIQUE,
    role     TEXT NOT NULL DEFAULT 'user',
    password TEXT NOT NULL,              -- bcrypt hash
    created  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS threads (
    id         TEXT PRIMARY KEY,
    board      TEXT NOT NULL REFERENCES boards(id),
    author     TEXT NOT NULL,
    author_id  TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0,
    post_count INTEGER NOT NULL DEFAULT 0,
    last_seq   INTEGER NOT NULL DEFAULT 0,
    created_ts INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS posts (
    id           TEXT PRIMARY KEY,
    thread       TEXT NOT NULL REFERENCES threads(id),
    author       TEXT NOT NULL,
    author_id    TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'markup',
    reply_to     TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    redacted     INTEGER NOT NULL DEFAULT 0,
    created_seq  INTEGER NOT NULL,
    updated_seq  INTEGER NOT NULL,
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS cursors (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    seq     INTEGER NOT NULL DEFAULT 0
);

-- Idempotency: deduplicate commands by cid within a 10-minute window.
CREATE TABLE IF NOT EXISTS processed_commands (
    actor_id     TEXT    NOT NULL DEFAULT '',
    cid          TEXT    NOT NULL,
    command_hash TEXT    NOT NULL DEFAULT '',
    result_json  TEXT    NOT NULL,
    processed_at INTEGER NOT NULL,
    PRIMARY KEY (actor_id, cid)
);

CREATE TABLE IF NOT EXISTS outbox_jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT    NOT NULL,
    payload      TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_run_at  INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_outbox_jobs_ready
    ON outbox_jobs(status, next_run_at, created_at);

-- SSH public-key credentials bound to accounts.
CREATE TABLE IF NOT EXISTS auth_pubkeys (
    user_id TEXT NOT NULL REFERENCES users(id),
    pubkey  TEXT NOT NULL,
    PRIMARY KEY (user_id, pubkey)
);

-- Active sanctions (mutes / bans). expires_at=0 means permanent.
CREATE TABLE IF NOT EXISTS user_sanctions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id),
    kind       TEXT NOT NULL,              -- "mute" | "ban"
    scope      TEXT NOT NULL DEFAULT 'global', -- board id or "global"
    expires_at INTEGER NOT NULL DEFAULT 0, -- unix ms; 0 = permanent
    by         TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    seq        INTEGER NOT NULL
);

-- Full-text search index over post bodies (FTS5 projection, rebuildable).
CREATE VIRTUAL TABLE IF NOT EXISTS posts_fts USING fts5(
    body,
    post_id   UNINDEXED,
    thread_id UNINDEXED,
    board_id  UNINDEXED,
    author    UNINDEXED
);

-- Seed the default board.
INSERT OR IGNORE INTO boards (id, name, description)
    VALUES ('general', 'General', 'General discussion');

-- Categories are the modern forum projection. Boards remain as a compatibility
-- surface and as the BBS-facing name for categories.
CREATE TABLE IF NOT EXISTS categories (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    parent_id   TEXT NOT NULL DEFAULT '',
    position    INTEGER NOT NULL DEFAULT 0,
    visibility  TEXT NOT NULL DEFAULT 'public',
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO categories (id, name, description, position)
    VALUES ('general', 'General', 'General discussion', 0);

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id      TEXT PRIMARY KEY REFERENCES users(id),
    display_name TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    avatar       TEXT NOT NULL DEFAULT '',
    updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS moderation_reviews (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'open',
    target_id  TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    reporter   TEXT NOT NULL REFERENCES users(id),
    reason     TEXT NOT NULL DEFAULT '',
    resolution TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_moderation_reviews_status_created
    ON moderation_reviews(status, created_at DESC);

-- ── M10: Reactions ───────────────────────────────────────────────────────────
-- One reaction per user per post (emoji is reserved for future multi-reaction).
CREATE TABLE IF NOT EXISTS post_reactions (
    post_id TEXT    NOT NULL REFERENCES posts(id),
    user_id TEXT    NOT NULL REFERENCES users(id),
    emoji   TEXT    NOT NULL DEFAULT 'heart',
    ts      INTEGER NOT NULL,
    PRIMARY KEY (post_id, user_id)
);

-- ── M11: Polls ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS polls (
    id         TEXT    PRIMARY KEY,
    post_id    TEXT    NOT NULL REFERENCES posts(id),
    question   TEXT    NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL DEFAULT 0,  -- unix ms; 0 = no expiry
    ts         INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS poll_options (
    id       TEXT    PRIMARY KEY,
    poll_id  TEXT    NOT NULL REFERENCES polls(id),
    text     TEXT    NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
-- One vote per user per poll.
CREATE TABLE IF NOT EXISTS poll_votes (
    poll_id   TEXT    NOT NULL REFERENCES polls(id),
    option_id TEXT    NOT NULL REFERENCES poll_options(id),
    user_id   TEXT    NOT NULL REFERENCES users(id),
    ts        INTEGER NOT NULL,
    PRIMARY KEY (poll_id, user_id)
);

-- ── M8: Notifications & thread watch preferences ─────────────────────────────
CREATE TABLE IF NOT EXISTS thread_prefs (
    user_id   TEXT NOT NULL REFERENCES users(id),
    thread_id TEXT NOT NULL REFERENCES threads(id),
    level     TEXT NOT NULL DEFAULT 'normal',  -- 'watch' | 'normal' | 'mute'
    PRIMARY KEY (user_id, thread_id)
);
CREATE TABLE IF NOT EXISTS notifications (
    id        TEXT    PRIMARY KEY,
    user_id   TEXT    NOT NULL REFERENCES users(id),
    kind      TEXT    NOT NULL,  -- 'mention' | 'reply' | 'watched'
    thread_id TEXT    NOT NULL,
    post_id   TEXT    NOT NULL,
    actor     TEXT    NOT NULL,  -- username of who triggered the notification
    read      INTEGER NOT NULL DEFAULT 0,
    ts        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifs_user_unread
    ON notifications(user_id, read, ts DESC);

-- ── M9: Trust levels & activity tracking ────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_activity (
    user_id        TEXT    PRIMARY KEY REFERENCES users(id),
    posts_created  INTEGER NOT NULL DEFAULT 0,
    days_visited   INTEGER NOT NULL DEFAULT 0,
    last_visit_day TEXT    NOT NULL DEFAULT '',  -- 'YYYY-MM-DD'
    reactions_recv INTEGER NOT NULL DEFAULT 0,
    trust_level    INTEGER NOT NULL DEFAULT 0    -- 0–4
);
`
