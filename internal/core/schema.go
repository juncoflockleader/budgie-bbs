package core

const ddl = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    applied_at INTEGER NOT NULL
);

-- Append-only event log. seq is the global monotonic cursor.
CREATE TABLE IF NOT EXISTS events (
    seq              INTEGER PRIMARY KEY AUTOINCREMENT,
    id               TEXT    NOT NULL UNIQUE,
    kind             TEXT    NOT NULL,
    scopes           TEXT    NOT NULL,  -- comma-separated scope list
    payload          TEXT    NOT NULL,  -- JSON
    ts               INTEGER NOT NULL,
    partition_kind   TEXT    NOT NULL DEFAULT 'global',
    partition_key    TEXT    NOT NULL DEFAULT 'global',
    partition_offset INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_events_partition_offset
    ON events(partition_kind, partition_key, partition_offset);

CREATE TABLE IF NOT EXISTS event_partition_offsets (
    partition_kind TEXT    NOT NULL,
    partition_key  TEXT    NOT NULL,
    last_offset    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (partition_kind, partition_key)
);

CREATE TABLE IF NOT EXISTS event_scalar_offsets (
    id       TEXT    PRIMARY KEY,
    last_seq INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO event_scalar_offsets (id, last_seq)
    SELECT 'broker_event_log', COALESCE(MAX(seq), 0) FROM events;

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

-- Site-wide generative-AI kill switch (admin-controlled).
CREATE TABLE IF NOT EXISTS ai_settings (
    id         TEXT PRIMARY KEY DEFAULT 'default',
    enabled    INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO ai_settings (id, enabled, updated_at) VALUES ('default', 0, 0);

-- Per-board generative-AI bot configuration. api_token is bring-your-own and is
-- deliberately never returned by any read API (only DB access can see it).
CREATE TABLE IF NOT EXISTS board_ai_config (
    board_id     TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    enabled      INTEGER NOT NULL DEFAULT 0,
    provider     TEXT NOT NULL DEFAULT 'anthropic',
    model        TEXT NOT NULL DEFAULT 'claude-haiku-4-5',
    api_token    TEXT NOT NULL DEFAULT '',
    trigger_role TEXT NOT NULL DEFAULT 'user',
    mode         TEXT NOT NULL DEFAULT 'reply',
    reply_prompt TEXT NOT NULL DEFAULT '',
    max_total    INTEGER NOT NULL DEFAULT 0,
    max_per_hour INTEGER NOT NULL DEFAULT 0,
    used_total   INTEGER NOT NULL DEFAULT 0,
    window_start INTEGER NOT NULL DEFAULT 0,
    window_count INTEGER NOT NULL DEFAULT 0,
    bot_user_id  TEXT NOT NULL DEFAULT '',
    updated_at   INTEGER NOT NULL DEFAULT 0
);

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

-- KBS ZAP hides a board from a user's unread traversal while leaving the board
-- itself readable and discoverable. Board settings can make a board non-zappable.
CREATE TABLE IF NOT EXISTS board_zaps (
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_zaps_user
    ON board_zaps(user_id, board_id);

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
    stats_excluded      INTEGER NOT NULL DEFAULT 0,
    zap_allowed         INTEGER NOT NULL DEFAULT 1,
    guest_access        TEXT    NOT NULL DEFAULT '',
    updated_at          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS recommended_boards (
    board_id   TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    note       TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    curated_by TEXT    NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recommended_boards_position
    ON recommended_boards(position, updated_at DESC, board_id);

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

CREATE TABLE IF NOT EXISTS board_moderator_terms (
    board_id     TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at   INTEGER NOT NULL DEFAULT 0,
    ended_at     INTEGER NOT NULL DEFAULT 0,
    appointed_by TEXT    NOT NULL DEFAULT '',
    removed_by   TEXT    NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id, started_at)
);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_board_time
    ON board_moderator_terms(board_id, ended_at, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_user_time
    ON board_moderator_terms(user_id, started_at DESC);

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

CREATE TABLE IF NOT EXISTS digest_entry_removals (
    id         TEXT    PRIMARY KEY,
    board_id   TEXT    NOT NULL,
    kind       TEXT    NOT NULL DEFAULT '',
    removed_by TEXT    NOT NULL DEFAULT '',
    removed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_digest_entry_removals_board_kind
    ON digest_entry_removals(board_id, kind, removed_at DESC);

CREATE TABLE IF NOT EXISTS digest_path_mutations (
    event_id  TEXT    PRIMARY KEY,
    action    TEXT    NOT NULL,
    board_id  TEXT    NOT NULL,
    kind      TEXT    NOT NULL DEFAULT '',
    from_path TEXT    NOT NULL DEFAULT '',
    to_path   TEXT    NOT NULL DEFAULT '',
    actor_id  TEXT    NOT NULL DEFAULT '',
    ts        INTEGER NOT NULL DEFAULT 0,
    count     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_digest_path_mutations_board_kind
    ON digest_path_mutations(board_id, kind, action, ts DESC);

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

CREATE TABLE IF NOT EXISTS attachment_blob_staging (
    id           TEXT    PRIMARY KEY,
    kind         TEXT    NOT NULL,
    data         BLOB    NOT NULL,
    content_type TEXT    NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    actor_id     TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0,
    expires_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_attachment_blob_staging_expiry
    ON attachment_blob_staging(expires_at, kind);

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

CREATE TABLE IF NOT EXISTS mail_group_deletions (
    event_id   TEXT    PRIMARY KEY,
    owner_id   TEXT    NOT NULL,
    group_id   TEXT    NOT NULL,
    deleted_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_group_deletions_owner_group
    ON mail_group_deletions(owner_id, group_id);

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

CREATE TABLE IF NOT EXISTS chat_rooms (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    topic      TEXT    NOT NULL DEFAULT '',
    created_by TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO chat_rooms (id, name, topic, created_by, created_at, updated_at)
    VALUES ('lobby', 'Lobby', 'Campus lobby chat', '', 0, 0);

CREATE TABLE IF NOT EXISTS chat_lines (
    id         TEXT    PRIMARY KEY,
    room_id    TEXT    NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_name  TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_chat_lines_room_created
    ON chat_lines(room_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS guest_presence_sessions (
    session_id     TEXT    PRIMARY KEY,
    status         TEXT    NOT NULL DEFAULT 'active',
    location_label TEXT    NOT NULL DEFAULT '',
    from_host      TEXT    NOT NULL DEFAULT '',
    last_seen      INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_guest_presence_sessions_last_seen
    ON guest_presence_sessions(last_seen DESC);

CREATE TABLE IF NOT EXISTS community_stats_snapshot (
    id                    TEXT    PRIMARY KEY DEFAULT 'default',
    total_users           INTEGER NOT NULL DEFAULT 0,
    total_boards          INTEGER NOT NULL DEFAULT 0,
    total_threads         INTEGER NOT NULL DEFAULT 0,
    total_posts           INTEGER NOT NULL DEFAULT 0,
    total_reactions       INTEGER NOT NULL DEFAULT 0,
    total_mail            INTEGER NOT NULL DEFAULT 0,
    total_direct_messages INTEGER NOT NULL DEFAULT 0,
    total_logins          INTEGER NOT NULL DEFAULT 0,
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    total_online_seconds  INTEGER NOT NULL DEFAULT 0,
    online_users          INTEGER NOT NULL DEFAULT 0,
    online_guests         INTEGER NOT NULL DEFAULT 0,
    max_online_users      INTEGER NOT NULL DEFAULT 0,
    max_online_at         INTEGER NOT NULL DEFAULT 0,
    max_online_guests     INTEGER NOT NULL DEFAULT 0,
    max_online_guests_at  INTEGER NOT NULL DEFAULT 0,
    head_seq              INTEGER NOT NULL DEFAULT 0,
    refreshed_at          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS community_counter_totals (
    id                    TEXT    PRIMARY KEY DEFAULT 'default',
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    updated_at            INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO community_counter_totals (id)
    VALUES ('default');

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
    total_logins          INTEGER NOT NULL DEFAULT 0,
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    total_online_seconds  INTEGER NOT NULL DEFAULT 0,
    online_users          INTEGER NOT NULL DEFAULT 0,
    online_guests         INTEGER NOT NULL DEFAULT 0,
    max_online_users      INTEGER NOT NULL DEFAULT 0,
    max_online_at         INTEGER NOT NULL DEFAULT 0,
    max_online_guests     INTEGER NOT NULL DEFAULT 0,
    max_online_guests_at  INTEGER NOT NULL DEFAULT 0,
    head_seq              INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot
    ON community_stat_history(snapshot_at DESC, day DESC);

CREATE TABLE IF NOT EXISTS derived_view_watermarks (
    view_name   TEXT    PRIMARY KEY,
    applied_seq INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS login_hourly_stats (
    day         TEXT    NOT NULL,
    hour        INTEGER NOT NULL CHECK(hour >= 0 AND hour <= 23),
    login_count INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, hour)
);
CREATE INDEX IF NOT EXISTS idx_login_hourly_stats_updated
    ON login_hourly_stats(updated_at DESC, day DESC, hour);

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

CREATE TABLE IF NOT EXISTS post_deletions (
    post_id         TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id       TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id        TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    deleted_by_id   TEXT    NOT NULL DEFAULT '',
    deleted_by_name TEXT    NOT NULL DEFAULT '',
    reason          TEXT    NOT NULL DEFAULT '',
    kind            TEXT    NOT NULL CHECK(kind IN ('recycle', 'junk')),
    deleted_at      INTEGER NOT NULL DEFAULT 0,
    seq             INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_deletions_board_kind
    ON post_deletions(board_id, kind, deleted_at DESC, seq DESC);

CREATE TABLE IF NOT EXISTS resident_feed_posts (
    post_id     TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq INTEGER NOT NULL DEFAULT 0,
    updated_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_created
    ON resident_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_board_created
    ON resident_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_thread
    ON resident_feed_posts(thread_id);

CREATE TABLE IF NOT EXISTS latest_feed_posts (
    post_id     TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq INTEGER NOT NULL DEFAULT 0,
    updated_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_created
    ON latest_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_board_created
    ON latest_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_thread
    ON latest_feed_posts(thread_id);

CREATE TABLE IF NOT EXISTS board_ranking_stats (
    board_id        TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    INTEGER NOT NULL DEFAULT 0,
    post_count      INTEGER NOT NULL DEFAULT 0,
    last_seq        INTEGER NOT NULL DEFAULT 0,
    last_post_at    INTEGER NOT NULL DEFAULT 0,
    moderator_count INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_ranking_stats_order
    ON board_ranking_stats(post_count DESC, thread_count DESC, last_seq DESC, board_id);

CREATE TABLE IF NOT EXISTS thread_ranking_stats (
    thread_id         TEXT    PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    post_count        INTEGER NOT NULL DEFAULT 0,
    participant_count INTEGER NOT NULL DEFAULT 0,
    reaction_count    INTEGER NOT NULL DEFAULT 0,
    last_seq          INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL DEFAULT 0,
    refreshed_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_thread_ranking_stats_order
    ON thread_ranking_stats(last_seq DESC, thread_id);

CREATE TABLE IF NOT EXISTS reply_ranking_posts (
    post_id       TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id     TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    created_seq   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT 0,
    refreshed_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reply_ranking_posts_created
    ON reply_ranking_posts(created_seq DESC, post_id);

CREATE TABLE IF NOT EXISTS user_ranking_stats (
    user_id              TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    posts_created        INTEGER NOT NULL DEFAULT 0,
    reactions_received   INTEGER NOT NULL DEFAULT 0,
    login_count          INTEGER NOT NULL DEFAULT 0,
    total_online_seconds INTEGER NOT NULL DEFAULT 0,
    trust_level          INTEGER NOT NULL DEFAULT 0,
    refreshed_at         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_ranking_stats_order
    ON user_ranking_stats(posts_created DESC, reactions_received DESC, login_count DESC, total_online_seconds DESC, user_id);

CREATE TABLE IF NOT EXISTS blessing_ranking_stats (
    user_id        TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    blessing_count INTEGER NOT NULL DEFAULT 0,
    last_blessed_at INTEGER NOT NULL DEFAULT 0,
    refreshed_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessing_ranking_stats_order
    ON blessing_ranking_stats(blessing_count DESC, last_blessed_at DESC, user_id);

CREATE TABLE IF NOT EXISTS archive_ranking_stats (
    board_id        TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind            TEXT    NOT NULL DEFAULT 'archive',
    path            TEXT    NOT NULL DEFAULT '',
    entry_count     INTEGER NOT NULL DEFAULT 0,
    edited_count    INTEGER NOT NULL DEFAULT 0,
    last_updated_at INTEGER NOT NULL DEFAULT 0,
    refreshed_at    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_archive_ranking_stats_order
    ON archive_ranking_stats(kind, entry_count DESC, edited_count DESC, last_updated_at DESC, board_id, path);

CREATE TABLE IF NOT EXISTS board_summary_stats (
    board_id        TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    INTEGER NOT NULL DEFAULT 0,
    post_count      INTEGER NOT NULL DEFAULT 0,
    last_seq        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT 0,
    moderator_count INTEGER NOT NULL DEFAULT 0,
    refreshed_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_summary_stats_activity
    ON board_summary_stats(last_seq DESC, post_count DESC, thread_count DESC, board_id);

CREATE TABLE IF NOT EXISTS unread_thread_summary_stats (
    thread_id    TEXT    PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    board_id     TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    author       TEXT    NOT NULL DEFAULT '',
    author_id    TEXT    NOT NULL DEFAULT '',
    title        TEXT    NOT NULL DEFAULT '',
    locked       INTEGER NOT NULL DEFAULT 0,
    post_count   INTEGER NOT NULL DEFAULT 0,
    last_seq     INTEGER NOT NULL DEFAULT 0,
    created_ts   INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0,
    refreshed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_board_last
    ON unread_thread_summary_stats(board_id, last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_last
    ON unread_thread_summary_stats(last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_posts_thread_created_seq
    ON posts(thread, created_seq, redacted, id);

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

CREATE TABLE IF NOT EXISTS processed_commands_v2 (
    partition_kind TEXT    NOT NULL DEFAULT 'global',
    partition_key  TEXT    NOT NULL DEFAULT 'global',
    actor_id       TEXT    NOT NULL DEFAULT '',
    cid            TEXT    NOT NULL,
    command_hash   TEXT    NOT NULL DEFAULT '',
    result_json    TEXT    NOT NULL,
    processed_at   INTEGER NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_processed_commands_v2_actor_cid
    ON processed_commands_v2(actor_id, cid);

CREATE TABLE IF NOT EXISTS command_log_receipts (
    partition_kind TEXT    NOT NULL DEFAULT 'global',
    partition_key  TEXT    NOT NULL DEFAULT 'global',
    actor_id       TEXT    NOT NULL DEFAULT '',
    cid            TEXT    NOT NULL,
    command_offset INTEGER NOT NULL DEFAULT 0,
    status         TEXT    NOT NULL DEFAULT '',
    error_json     TEXT    NOT NULL DEFAULT '',
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_actor_cid
    ON command_log_receipts(actor_id, cid);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_partition_offset
    ON command_log_receipts(partition_kind, partition_key, command_offset);

CREATE TABLE IF NOT EXISTS command_log_partition_offsets (
    partition_kind   TEXT    NOT NULL DEFAULT 'global',
    partition_key    TEXT    NOT NULL DEFAULT 'global',
    tail_offset      INTEGER NOT NULL DEFAULT 0,
    committed_offset INTEGER NOT NULL DEFAULT 0,
    updated_at       INTEGER NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_log_partition_offsets_tail
    ON command_log_partition_offsets(tail_offset DESC, partition_kind, partition_key);

CREATE TABLE IF NOT EXISTS command_partition_leases (
    partition_kind TEXT    NOT NULL,
    partition_key  TEXT    NOT NULL,
    owner_id       TEXT    NOT NULL,
    claimed_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_partition_leases_expires
    ON command_partition_leases(expires_at);

CREATE TABLE IF NOT EXISTS hot_thread_splits (
    thread_id  TEXT    PRIMARY KEY,
    shards     INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
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
    title        TEXT NOT NULL DEFAULT '',
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
    policy_accepted_at INTEGER NOT NULL DEFAULT 0,
    policy_version     TEXT NOT NULL DEFAULT '',
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
    -- No FK on reporter: it may be a synthetic system actor (e.g. "automod").
    reporter   TEXT NOT NULL,
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
CREATE TABLE IF NOT EXISTS post_reaction_count_shards (
    post_id     TEXT    NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_post_reaction_count_shards_post
    ON post_reaction_count_shards(post_id);

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
CREATE TABLE IF NOT EXISTS poll_vote_count_shards (
    poll_id     TEXT    NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    option_id   TEXT    NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (option_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_poll_vote_count_shards_poll
    ON poll_vote_count_shards(poll_id, option_id);

-- IS5: durable coarse checkpoints for high-volume unordered counter storage.
CREATE TABLE IF NOT EXISTS counter_checkpoints (
    counter_kind    TEXT    NOT NULL,
    target_id       TEXT    NOT NULL,
    parent_id       TEXT    NOT NULL DEFAULT '',
    count           INTEGER NOT NULL DEFAULT 0,
    source_head_seq INTEGER NOT NULL DEFAULT 0,
    checkpoint_seq  INTEGER NOT NULL DEFAULT 0,
    checkpointed_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (counter_kind, target_id)
);
CREATE INDEX IF NOT EXISTS idx_counter_checkpoints_parent
    ON counter_checkpoints(counter_kind, parent_id);

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
    total_online_seconds INTEGER NOT NULL DEFAULT 0,
    trust_level    INTEGER NOT NULL DEFAULT 0    -- 0–4
);
`
