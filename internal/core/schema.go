package core

const ddl = `
-- Append-only event log. seq is the global monotonic cursor.
CREATE TABLE IF NOT EXISTS events (
    seq     INTEGER PRIMARY KEY AUTOINCREMENT,
    id      TEXT    NOT NULL UNIQUE,
    kind    TEXT    NOT NULL,
    scopes  TEXT    NOT NULL,  -- comma-separated scope list
    payload TEXT    NOT NULL,  -- JSON
    ts      INTEGER NOT NULL
);

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
    title      TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0,
    post_count INTEGER NOT NULL DEFAULT 0,
    last_seq   INTEGER NOT NULL DEFAULT 0,
    created_ts INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS posts (
    id           TEXT PRIMARY KEY,
    thread       TEXT NOT NULL REFERENCES threads(id),
    author       TEXT NOT NULL,
    body         TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'markup',
    reply_to     TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    redacted     INTEGER NOT NULL DEFAULT 0,
    created_seq  INTEGER NOT NULL,
    updated_seq  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cursors (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    seq     INTEGER NOT NULL DEFAULT 0
);

-- Idempotency: deduplicate commands by cid within a 10-minute window.
CREATE TABLE IF NOT EXISTS processed_commands (
    cid          TEXT PRIMARY KEY,
    result_json  TEXT    NOT NULL,
    processed_at INTEGER NOT NULL
);

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
`
