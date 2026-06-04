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
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    role                TEXT NOT NULL DEFAULT 'user',
    password            TEXT NOT NULL,              -- bcrypt hash
    created             INTEGER NOT NULL,
    registration_status TEXT NOT NULL DEFAULT 'approved',
    reviewed_at         INTEGER NOT NULL DEFAULT 0,
    reviewed_by         TEXT NOT NULL DEFAULT '',
    review_reason       TEXT NOT NULL DEFAULT '',
    deactivated_at      INTEGER NOT NULL DEFAULT 0,
    deactivated_by      TEXT NOT NULL DEFAULT '',
    deactivated_reason  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS account_registration_settings (
    id               TEXT PRIMARY KEY DEFAULT 'default',
    require_approval INTEGER NOT NULL DEFAULT 0,
    updated_at       INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO account_registration_settings (id, require_approval, updated_at)
    VALUES ('default', 0, 0);

CREATE TABLE IF NOT EXISTS password_recovery_requests (
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
);
CREATE INDEX IF NOT EXISTS idx_password_recovery_status_updated
    ON password_recovery_requests(status, updated_at DESC, created_at DESC);

-- User-curated board collections. Empty parent_id / folder_id represents the
-- root favorite list; folder rows allow KBS-style nested personal workspaces.
CREATE TABLE IF NOT EXISTS favorite_folders (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id  TEXT    NOT NULL DEFAULT '',
    name       TEXT    NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_favorite_folders_user_parent_position
    ON favorite_folders(user_id, parent_id, position, name);

CREATE TABLE IF NOT EXISTS board_favorites (
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    folder_id  TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_favorites_user_position
    ON board_favorites(user_id, folder_id, position, board_id);
CREATE INDEX IF NOT EXISTS idx_board_favorites_user_folder_position
    ON board_favorites(user_id, folder_id, position, board_id);

-- Board policy metadata mirrors the high-context board flags of classic BBSes.
-- The first enforced flags are read_only, no_reply, and anonymous_allowed;
-- the remaining flags are exposed as board metadata for richer clients.
CREATE TABLE IF NOT EXISTS board_settings (
    board_id            TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    anonymous_allowed   INTEGER NOT NULL DEFAULT 0,
    read_only           INTEGER NOT NULL DEFAULT 0,
    no_reply            INTEGER NOT NULL DEFAULT 0,
    attachments_allowed INTEGER NOT NULL DEFAULT 0,
    mail_in_allowed     INTEGER NOT NULL DEFAULT 0,
    relay_enabled       INTEGER NOT NULL DEFAULT 0,
    member_read_mode    INTEGER NOT NULL DEFAULT 0,
    member_post_mode    INTEGER NOT NULL DEFAULT 0,
    updated_at          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS board_moderators (
    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_moderators_board_position
    ON board_moderators(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_members (
    board_id           TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id            TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title              TEXT    NOT NULL DEFAULT '',
    position           INTEGER NOT NULL DEFAULT 0,
    can_manage_members INTEGER NOT NULL DEFAULT 0,
    can_curate         INTEGER NOT NULL DEFAULT 0,
    can_moderate_posts INTEGER NOT NULL DEFAULT 0,
    can_moderate_threads INTEGER NOT NULL DEFAULT 0,
    can_announce       INTEGER NOT NULL DEFAULT 0,
    can_manage_polls   INTEGER NOT NULL DEFAULT 0,
    can_set_board_settings INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL DEFAULT 0,
    updated_at         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_members_board_user
    ON board_members(board_id, user_id);
CREATE INDEX IF NOT EXISTS idx_board_members_user_board
    ON board_members(user_id, board_id);
CREATE INDEX IF NOT EXISTS idx_board_members_board_position
    ON board_members(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_member_applications (
    id           TEXT    PRIMARY KEY,
    board_id     TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT    NOT NULL DEFAULT 'pending',
    note         TEXT    NOT NULL DEFAULT '',
    title        TEXT    NOT NULL DEFAULT '',
    reviewer_id  TEXT    NOT NULL DEFAULT '',
    review_note  TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    reviewed_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_board_status
    ON board_member_applications(board_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_user_board
    ON board_member_applications(user_id, board_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS board_member_requirements (
    board_id                      TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    min_login_count               INTEGER NOT NULL DEFAULT 0,
    min_post_count                INTEGER NOT NULL DEFAULT 0,
    min_trust_level               INTEGER NOT NULL DEFAULT 0,
    min_score                     INTEGER NOT NULL DEFAULT 0,
    min_board_post_count          INTEGER NOT NULL DEFAULT 0,
    min_board_original_post_count INTEGER NOT NULL DEFAULT 0,
    min_board_digest_count        INTEGER NOT NULL DEFAULT 0,
    min_board_mark_count          INTEGER NOT NULL DEFAULT 0,
    max_members                   INTEGER NOT NULL DEFAULT 0,
    approval_mode                 TEXT    NOT NULL DEFAULT 'manual',
    updated_at                    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS digest_entries (
    id          TEXT    PRIMARY KEY,
    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    target_kind TEXT    NOT NULL DEFAULT 'post',
    target_id   TEXT    NOT NULL,
	kind        TEXT    NOT NULL DEFAULT 'digest',
	title       TEXT    NOT NULL DEFAULT '',
	path        TEXT    NOT NULL DEFAULT '',
	note        TEXT    NOT NULL DEFAULT '',
	body        TEXT    NOT NULL DEFAULT '',
	body_edited INTEGER NOT NULL DEFAULT 0,
	created_by  TEXT    NOT NULL REFERENCES users(id),
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    UNIQUE(board_id, target_kind, target_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_entries_board_kind_path
    ON digest_entries(board_id, kind, path, updated_at DESC);

CREATE TABLE IF NOT EXISTS digest_directories (
    id         TEXT    PRIMARY KEY,
    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL DEFAULT 'archive',
    path       TEXT    NOT NULL DEFAULT '',
    created_by TEXT    NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path
    ON digest_directories(board_id, kind, path);

-- KBS-style private mail is durable and article-like: one message body may
-- appear as an inbox copy for recipients and as a sent/trash/custom copy for
-- the sender.
CREATE TABLE IF NOT EXISTS mail_messages (
    id           TEXT    PRIMARY KEY,
    from_user_id TEXT    NOT NULL REFERENCES users(id),
    subject      TEXT    NOT NULL DEFAULT '',
    body         TEXT    NOT NULL DEFAULT '',
    parent_id    TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0,
    seq          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_messages_created
    ON mail_messages(created_at DESC);

CREATE TABLE IF NOT EXISTS mail_copies (
    message_id TEXT    NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT    NOT NULL DEFAULT 'recipient',
    mailbox    TEXT    NOT NULL DEFAULT 'inbox',
    read       INTEGER NOT NULL DEFAULT 0,
    kept       INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (message_id, user_id, role)
);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_box
    ON mail_copies(user_id, mailbox, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_unread
    ON mail_copies(user_id, read, updated_at DESC);

CREATE TABLE IF NOT EXISTS mail_attachments (
    id           TEXT    PRIMARY KEY,
    message_id   TEXT    NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,
    content_type TEXT    NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    url          TEXT    NOT NULL DEFAULT '',
    created_by   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_attachments_message
    ON mail_attachments(message_id, created_at, id);

CREATE TABLE IF NOT EXISTS mail_attachment_blobs (
    attachment_id TEXT    PRIMARY KEY REFERENCES mail_attachments(id) ON DELETE CASCADE,
    data          BLOB    NOT NULL,
    content_type  TEXT    NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    uploaded_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mail_groups (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mail_groups_user_name
    ON mail_groups(user_id, name);

CREATE TABLE IF NOT EXISTS mail_group_members (
    group_id   TEXT    NOT NULL REFERENCES mail_groups(id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_group_members_group_position
    ON mail_group_members(group_id, position, user_id);

-- Short direct messages are the presence-oriented counterpart to private mail.
CREATE TABLE IF NOT EXISTS direct_messages (
    id              TEXT    PRIMARY KEY,
    conversation_id TEXT    NOT NULL,
    from_user_id    TEXT    NOT NULL REFERENCES users(id),
    to_user_id      TEXT    NOT NULL REFERENCES users(id),
    body            TEXT    NOT NULL DEFAULT '',
    read_at         INTEGER NOT NULL DEFAULT 0,
    sender_deleted  INTEGER NOT NULL DEFAULT 0,
    recipient_deleted INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT 0,
    seq             INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_direct_messages_conversation
    ON direct_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_direct_messages_recipient_unread
    ON direct_messages(to_user_id, read_at, created_at DESC);

CREATE TABLE IF NOT EXISTS direct_message_settings (
    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    policy     TEXT    NOT NULL DEFAULT 'all',
    updated_at INTEGER NOT NULL DEFAULT 0
);

-- User-owned social graph: friends/following and ignore lists. Fans are the
-- reverse lookup of friend rows.
CREATE TABLE IF NOT EXISTS user_relationships (
    user_id        TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           TEXT    NOT NULL,
    note           TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, target_user_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_user_relationships_target_kind
    ON user_relationships(target_user_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_relationships_user_kind
    ON user_relationships(user_id, kind, updated_at DESC);

CREATE TABLE IF NOT EXISTS blessings (
    id           TEXT    PRIMARY KEY,
    from_user_id TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user_id   TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message      TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0,
    seq          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessings_to_created
    ON blessings(to_user_id, created_at DESC, seq DESC);
CREATE INDEX IF NOT EXISTS idx_blessings_from_created
    ON blessings(from_user_id, created_at DESC, seq DESC);

CREATE TABLE IF NOT EXISTS user_presence (
    user_id        TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status         TEXT    NOT NULL DEFAULT 'active',
    mode           TEXT    NOT NULL DEFAULT '',
    board_id       TEXT    NOT NULL DEFAULT '',
    thread_id      TEXT    NOT NULL DEFAULT '',
    location_label TEXT    NOT NULL DEFAULT '',
    from_host      TEXT    NOT NULL DEFAULT '',
    last_seen      INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_presence_last_seen
    ON user_presence(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen
    ON user_presence(board_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS user_presence_sessions (
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
);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen
    ON user_presence_sessions(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen
    ON user_presence_sessions(board_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS community_stat_history (
    day                   TEXT    PRIMARY KEY,
    snapshot_at           INTEGER NOT NULL DEFAULT 0,
    total_users           INTEGER NOT NULL DEFAULT 0,
    total_boards          INTEGER NOT NULL DEFAULT 0,
    total_threads         INTEGER NOT NULL DEFAULT 0,
    total_posts           INTEGER NOT NULL DEFAULT 0,
    total_reactions       INTEGER NOT NULL DEFAULT 0,
    total_mail            INTEGER NOT NULL DEFAULT 0,
    total_direct_messages INTEGER NOT NULL DEFAULT 0,
    online_users          INTEGER NOT NULL DEFAULT 0,
    max_online_users      INTEGER NOT NULL DEFAULT 0,
    max_online_at         INTEGER NOT NULL DEFAULT 0,
    head_seq              INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot
    ON community_stat_history(snapshot_at DESC, day DESC);

-- Per-board read markers power the BBS "unread boards" workflow. previous_seq
-- supports restoring the last marker after an accidental mark-read.
CREATE TABLE IF NOT EXISTS board_read_markers (
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id     TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    last_seq     INTEGER NOT NULL DEFAULT 0,
    previous_seq INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_read_markers_user
    ON board_read_markers(user_id, board_id);

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
    signature    TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'markup',
    reply_to     TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    redacted     INTEGER NOT NULL DEFAULT 0,
    marked       INTEGER NOT NULL DEFAULT 0,
    recommended  INTEGER NOT NULL DEFAULT 0,
    no_reply     INTEGER NOT NULL DEFAULT 0,
    tex          INTEGER NOT NULL DEFAULT 0,
    mail_back    INTEGER NOT NULL DEFAULT 0,
    source_post  TEXT NOT NULL DEFAULT '',
    source_thread TEXT NOT NULL DEFAULT '',
    source_board TEXT NOT NULL DEFAULT '',
    source_author TEXT NOT NULL DEFAULT '',
    source_author_id TEXT NOT NULL DEFAULT '',
    source_title TEXT NOT NULL DEFAULT '',
    created_seq  INTEGER NOT NULL,
    updated_seq  INTEGER NOT NULL,
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS post_attachments (
    id           TEXT    PRIMARY KEY,
    post_id      TEXT    NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,
    content_type TEXT    NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    url          TEXT    NOT NULL DEFAULT '',
    created_by   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_attachments_post
    ON post_attachments(post_id, created_at, id);

CREATE TABLE IF NOT EXISTS attachment_blobs (
    attachment_id TEXT    PRIMARY KEY REFERENCES post_attachments(id) ON DELETE CASCADE,
    data          BLOB    NOT NULL,
    content_type  TEXT    NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    uploaded_at   INTEGER NOT NULL DEFAULT 0
);

-- Per-thread read markers power first-unread navigation inside a board. Board
-- markers remain the baseline, so marking a board read clears its threads too.
CREATE TABLE IF NOT EXISTS thread_read_markers (
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id    TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    last_seq     INTEGER NOT NULL DEFAULT 0,
    previous_seq INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, thread_id)
);
CREATE INDEX IF NOT EXISTS idx_thread_read_markers_user
    ON thread_read_markers(user_id, thread_id);

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

CREATE TABLE IF NOT EXISTS relay_deliveries (
    id          TEXT    PRIMARY KEY,
    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    thread_id   TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    post_id     TEXT    NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id   TEXT    NOT NULL DEFAULT '',
    author_name TEXT    NOT NULL DEFAULT '',
    title       TEXT    NOT NULL DEFAULT '',
    body        TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'pending',
    last_error  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    seq         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_status_created
    ON relay_deliveries(status, created_at, id);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_board_created
    ON relay_deliveries(board_id, created_at, id);

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
    signature    TEXT NOT NULL DEFAULT '',
    plan         TEXT NOT NULL DEFAULT '',
    homepage     TEXT NOT NULL DEFAULT '',
    updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_private_profiles (
    user_id            TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    real_name          TEXT NOT NULL DEFAULT '',
    real_email         TEXT NOT NULL DEFAULT '',
    registration_email TEXT NOT NULL DEFAULT '',
    address            TEXT NOT NULL DEFAULT '',
    phone              TEXT NOT NULL DEFAULT '',
    mobile             TEXT NOT NULL DEFAULT '',
    birthday           TEXT NOT NULL DEFAULT '',
    school             TEXT NOT NULL DEFAULT '',
    contact_note       TEXT NOT NULL DEFAULT '',
    updated_at         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_personal_files (
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL DEFAULT '',
    body       TEXT    NOT NULL DEFAULT '',
    public     INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_user_personal_files_user_public
    ON user_personal_files(user_id, public, name);

CREATE TABLE IF NOT EXISTS user_signatures (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT    NOT NULL DEFAULT '',
    body       TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_signatures_user_position
    ON user_signatures(user_id, position, updated_at, id);

CREATE TABLE IF NOT EXISTS user_signature_settings (
    user_id               TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    selected_signature_id TEXT    NOT NULL DEFAULT '',
    random_enabled        INTEGER NOT NULL DEFAULT 0,
    updated_at            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_login_acl_settings (
    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_login_acl_rules (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pattern    TEXT    NOT NULL DEFAULT '',
    note       TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_login_acl_rules_user_position
    ON user_login_acl_rules(user_id, position, updated_at, id);

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

CREATE TABLE IF NOT EXISTS content_filters (
    id         TEXT PRIMARY KEY,
    pattern    TEXT NOT NULL DEFAULT '',
    scope      TEXT NOT NULL DEFAULT 'global',
    active     INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_content_filters_active_scope
    ON content_filters(active, scope, updated_at DESC);

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
    kind      TEXT    NOT NULL,  -- 'mention' | 'reply' | 'watched' | 'login'
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
    login_count    INTEGER NOT NULL DEFAULT 0,
    posts_created  INTEGER NOT NULL DEFAULT 0,
    days_visited   INTEGER NOT NULL DEFAULT 0,
    last_visit_day TEXT    NOT NULL DEFAULT '',  -- 'YYYY-MM-DD'
    reactions_recv INTEGER NOT NULL DEFAULT 0,
    trust_level    INTEGER NOT NULL DEFAULT 0    -- 0–4
);
`
