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
    seq              BIGSERIAL PRIMARY KEY,
    id               TEXT NOT NULL UNIQUE,
    kind             TEXT NOT NULL,
    payload          JSONB NOT NULL,
    created_at       BIGINT NOT NULL,
    partition_kind   TEXT NOT NULL DEFAULT 'global',
    partition_key    TEXT NOT NULL DEFAULT 'global',
    partition_offset BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_events_partition_offset
    ON events(partition_kind, partition_key, partition_offset);

CREATE TABLE IF NOT EXISTS event_partition_offsets (
    partition_kind TEXT NOT NULL,
    partition_key  TEXT NOT NULL,
    last_offset    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (partition_kind, partition_key)
);

CREATE TABLE IF NOT EXISTS event_scalar_offsets (
    id       TEXT PRIMARY KEY,
    last_seq BIGINT NOT NULL DEFAULT 0
);
INSERT INTO event_scalar_offsets (id, last_seq)
   SELECT 'broker_event_log', COALESCE(MAX(seq), 0) FROM events
ON CONFLICT (id) DO NOTHING;

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
    result_json  TEXT NOT NULL,
    processed_at BIGINT NOT NULL,
    PRIMARY KEY (actor_id, cid)
);

CREATE TABLE IF NOT EXISTS processed_commands_v2 (
    partition_kind TEXT NOT NULL DEFAULT 'global',
    partition_key  TEXT NOT NULL DEFAULT 'global',
    actor_id       TEXT NOT NULL,
    cid            TEXT NOT NULL,
    command_hash   TEXT NOT NULL,
    result_json    TEXT NOT NULL,
    processed_at   BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_processed_commands_v2_actor_cid
    ON processed_commands_v2(actor_id, cid);

CREATE TABLE IF NOT EXISTS command_log_receipts (
    partition_kind TEXT NOT NULL DEFAULT 'global',
    partition_key  TEXT NOT NULL DEFAULT 'global',
    actor_id       TEXT NOT NULL DEFAULT '',
    cid            TEXT NOT NULL,
    command_offset BIGINT NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT '',
    error_json     TEXT NOT NULL DEFAULT '',
    updated_at     BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_actor_cid
    ON command_log_receipts(actor_id, cid);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_partition_offset
    ON command_log_receipts(partition_kind, partition_key, command_offset);

CREATE TABLE IF NOT EXISTS command_log_partition_offsets (
    partition_kind   TEXT NOT NULL DEFAULT 'global',
    partition_key    TEXT NOT NULL DEFAULT 'global',
    tail_offset      BIGINT NOT NULL DEFAULT 0,
    committed_offset BIGINT NOT NULL DEFAULT 0,
    updated_at       BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_log_partition_offsets_tail
    ON command_log_partition_offsets(tail_offset DESC, partition_kind, partition_key);

CREATE TABLE IF NOT EXISTS attachment_blob_staging (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    data         BYTEA NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    actor_id     TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    expires_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_attachment_blob_staging_expiry
    ON attachment_blob_staging(expires_at, kind);

CREATE TABLE IF NOT EXISTS command_partition_leases (
    partition_kind TEXT NOT NULL,
    partition_key  TEXT NOT NULL,
    owner_id       TEXT NOT NULL,
    claimed_at     BIGINT NOT NULL,
    expires_at     BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_partition_leases_expires
    ON command_partition_leases(expires_at);

CREATE TABLE IF NOT EXISTS hot_thread_splits (
    thread_id  TEXT PRIMARY KEY,
    shards     INTEGER NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox_jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      TEXT NOT NULL,
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
-- Seed the default 'general' category (matches the SQLite schema). Boards can
-- be nested under it via ParentID.
INSERT INTO categories (id, name, description, position)
VALUES ('general', 'General', 'General discussion', 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS users (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    role                TEXT NOT NULL DEFAULT 'user',
    password            TEXT NOT NULL,
    created             BIGINT NOT NULL,
    registration_status TEXT NOT NULL DEFAULT 'approved',
    reviewed_at         BIGINT NOT NULL DEFAULT 0,
    reviewed_by         TEXT NOT NULL DEFAULT '',
    review_reason       TEXT NOT NULL DEFAULT '',
    deactivated_at      BIGINT NOT NULL DEFAULT 0,
    deactivated_by      TEXT NOT NULL DEFAULT '',
    deactivated_reason  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS account_registration_settings (
    id               TEXT PRIMARY KEY DEFAULT 'default',
    require_approval INTEGER NOT NULL DEFAULT 0,
    updated_at       BIGINT NOT NULL DEFAULT 0
);
INSERT INTO account_registration_settings (id, require_approval, updated_at)
VALUES ('default', 0, 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS password_recovery_requests (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending',
    submitted_name  TEXT NOT NULL DEFAULT '',
    submitted_email TEXT NOT NULL DEFAULT '',
    note            TEXT NOT NULL DEFAULT '',
    reviewer_id     TEXT NOT NULL DEFAULT '',
    review_note     TEXT NOT NULL DEFAULT '',
    created_at      BIGINT NOT NULL DEFAULT 0,
    updated_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_password_recovery_status_updated
    ON password_recovery_requests(status, updated_at DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id      TEXT PRIMARY KEY REFERENCES users(id),
    display_name TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    avatar       TEXT NOT NULL DEFAULT '',
    signature    TEXT NOT NULL DEFAULT '',
    plan         TEXT NOT NULL DEFAULT '',
    homepage     TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL DEFAULT 0
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
    updated_at         BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_personal_files (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    public     INTEGER NOT NULL DEFAULT 1,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_user_personal_files_user_public
    ON user_personal_files(user_id, public, name);

CREATE TABLE IF NOT EXISTS user_signatures (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_signatures_user_position
    ON user_signatures(user_id, position, updated_at, id);

CREATE TABLE IF NOT EXISTS user_signature_settings (
    user_id               TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    selected_signature_id TEXT NOT NULL DEFAULT '',
    random_enabled        INTEGER NOT NULL DEFAULT 0,
    updated_at            BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_login_acl_settings (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_login_acl_rules (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pattern    TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_login_acl_rules_user_position
    ON user_login_acl_rules(user_id, position, updated_at, id);

CREATE TABLE IF NOT EXISTS favorite_folders (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id  TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_favorite_folders_user_parent_position
    ON favorite_folders(user_id, parent_id, position, name);

CREATE TABLE IF NOT EXISTS board_favorites (
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    folder_id  TEXT    NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT  NOT NULL DEFAULT 0,
    updated_at BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_favorites_user_position
    ON board_favorites(user_id, folder_id, position, board_id);
CREATE INDEX IF NOT EXISTS idx_board_favorites_user_folder_position
    ON board_favorites(user_id, folder_id, position, board_id);

CREATE TABLE IF NOT EXISTS board_zaps (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_zaps_user
    ON board_zaps(user_id, board_id);

CREATE TABLE IF NOT EXISTS board_settings (
    board_id            TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
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
    updated_at          BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS board_moderators (
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_moderators_board_position
    ON board_moderators(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_moderator_terms (
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at   BIGINT NOT NULL DEFAULT 0,
    ended_at     BIGINT NOT NULL DEFAULT 0,
    appointed_by TEXT NOT NULL DEFAULT '',
    removed_by   TEXT NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id, started_at)
);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_board_time
    ON board_moderator_terms(board_id, ended_at, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_user_time
    ON board_moderator_terms(user_id, started_at DESC);

CREATE TABLE IF NOT EXISTS board_members (
    board_id           TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title              TEXT NOT NULL DEFAULT '',
    position           INTEGER NOT NULL DEFAULT 0,
    can_manage_members INTEGER NOT NULL DEFAULT 0,
    can_curate         INTEGER NOT NULL DEFAULT 0,
    can_moderate_posts INTEGER NOT NULL DEFAULT 0,
    can_moderate_threads INTEGER NOT NULL DEFAULT 0,
    can_announce       INTEGER NOT NULL DEFAULT 0,
    can_manage_polls   INTEGER NOT NULL DEFAULT 0,
    can_set_board_settings INTEGER NOT NULL DEFAULT 0,
    created_at         BIGINT NOT NULL DEFAULT 0,
    updated_at         BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_members_board_user
    ON board_members(board_id, user_id);
CREATE INDEX IF NOT EXISTS idx_board_members_user_board
    ON board_members(user_id, board_id);
CREATE INDEX IF NOT EXISTS idx_board_members_board_position
    ON board_members(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_member_applications (
    id           TEXT PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    note         TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    reviewer_id  TEXT NOT NULL DEFAULT '',
    review_note  TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    reviewed_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_board_status
    ON board_member_applications(board_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_user_board
    ON board_member_applications(user_id, board_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS board_member_requirements (
    board_id                      TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    min_login_count               INTEGER NOT NULL DEFAULT 0,
    min_post_count                INTEGER NOT NULL DEFAULT 0,
    min_trust_level               INTEGER NOT NULL DEFAULT 0,
    min_score                     INTEGER NOT NULL DEFAULT 0,
    min_board_post_count          INTEGER NOT NULL DEFAULT 0,
    min_board_original_post_count INTEGER NOT NULL DEFAULT 0,
    min_board_digest_count        INTEGER NOT NULL DEFAULT 0,
    min_board_mark_count          INTEGER NOT NULL DEFAULT 0,
    max_members                   INTEGER NOT NULL DEFAULT 0,
    approval_mode                 TEXT NOT NULL DEFAULT 'manual',
    updated_at                    BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS digest_entries (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL DEFAULT 'post',
    target_id   TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT 'digest',
	title       TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	note        TEXT NOT NULL DEFAULT '',
	body        TEXT NOT NULL DEFAULT '',
	body_edited INTEGER NOT NULL DEFAULT 0,
	created_by  TEXT NOT NULL REFERENCES users(id),
    created_at  BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    UNIQUE(board_id, target_kind, target_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_entries_board_kind_path
    ON digest_entries(board_id, kind, path, updated_at DESC);

CREATE TABLE IF NOT EXISTS digest_directories (
    id         TEXT PRIMARY KEY,
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'archive',
    path       TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path
    ON digest_directories(board_id, kind, path);

CREATE TABLE IF NOT EXISTS mail_messages (
    id           TEXT PRIMARY KEY,
    from_user_id TEXT NOT NULL REFERENCES users(id),
    subject      TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    parent_id    TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    seq          BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_messages_created
    ON mail_messages(created_at DESC);

CREATE TABLE IF NOT EXISTS mail_copies (
    message_id TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'recipient',
    mailbox    TEXT NOT NULL DEFAULT 'inbox',
    read       INTEGER NOT NULL DEFAULT 0,
    kept       INTEGER NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (message_id, user_id, role)
);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_box
    ON mail_copies(user_id, mailbox, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_unread
    ON mail_copies(user_id, read, updated_at DESC);

CREATE TABLE IF NOT EXISTS mail_attachments (
    id           TEXT PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    url          TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_attachments_message
    ON mail_attachments(message_id, created_at, id);

CREATE TABLE IF NOT EXISTS mail_attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES mail_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mail_groups (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mail_groups_user_name
    ON mail_groups(user_id, name);

CREATE TABLE IF NOT EXISTS mail_group_members (
    group_id   TEXT NOT NULL REFERENCES mail_groups(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_group_members_group_position
    ON mail_group_members(group_id, position, user_id);

CREATE TABLE IF NOT EXISTS mail_group_deletions (
    event_id   TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL,
    group_id   TEXT NOT NULL,
    deleted_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_group_deletions_owner_group
    ON mail_group_deletions(owner_id, group_id);

CREATE TABLE IF NOT EXISTS direct_messages (
    id                TEXT PRIMARY KEY,
    conversation_id   TEXT NOT NULL,
    from_user_id      TEXT NOT NULL REFERENCES users(id),
    to_user_id        TEXT NOT NULL REFERENCES users(id),
    body              TEXT NOT NULL DEFAULT '',
    read_at           BIGINT NOT NULL DEFAULT 0,
    sender_deleted    INTEGER NOT NULL DEFAULT 0,
    recipient_deleted INTEGER NOT NULL DEFAULT 0,
    created_at        BIGINT NOT NULL DEFAULT 0,
    seq               BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_direct_messages_conversation
    ON direct_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_direct_messages_recipient_unread
    ON direct_messages(to_user_id, read_at, created_at DESC);

CREATE TABLE IF NOT EXISTS direct_message_settings (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    policy     TEXT NOT NULL DEFAULT 'all',
    updated_at BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_relationships (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    created_at     BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, target_user_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_user_relationships_target_kind
    ON user_relationships(target_user_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_relationships_user_kind
    ON user_relationships(user_id, kind, updated_at DESC);

CREATE TABLE IF NOT EXISTS blessings (
    id           TEXT PRIMARY KEY,
    from_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message      TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    seq          BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessings_to_created
    ON blessings(to_user_id, created_at DESC, seq DESC);
CREATE INDEX IF NOT EXISTS idx_blessings_from_created
    ON blessings(from_user_id, created_at DESC, seq DESC);

CREATE TABLE IF NOT EXISTS recommended_boards (
    board_id   TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    note       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    curated_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recommended_boards_position
    ON recommended_boards(position, updated_at DESC, board_id);

CREATE TABLE IF NOT EXISTS user_presence (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'active',
    mode           TEXT NOT NULL DEFAULT '',
    board_id       TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_presence_last_seen
    ON user_presence(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen
    ON user_presence(board_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS user_presence_sessions (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id     TEXT NOT NULL DEFAULT 'default',
    status         TEXT NOT NULL DEFAULT 'active',
    mode           TEXT NOT NULL DEFAULT '',
    board_id       TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen
    ON user_presence_sessions(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen
    ON user_presence_sessions(board_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS chat_rooms (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    topic      TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
INSERT INTO chat_rooms (id, name, topic, created_by, created_at, updated_at)
VALUES ('lobby', 'Lobby', 'Campus lobby chat', '', 0, 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS chat_lines (
    id         TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_name  TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_chat_lines_room_created
    ON chat_lines(room_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS guest_presence_sessions (
    session_id     TEXT PRIMARY KEY,
    status         TEXT NOT NULL DEFAULT 'active',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_guest_presence_sessions_last_seen
    ON guest_presence_sessions(last_seen DESC);

CREATE TABLE IF NOT EXISTS community_stats_snapshot (
    id                    TEXT PRIMARY KEY DEFAULT 'default',
    total_users           BIGINT NOT NULL DEFAULT 0,
    total_boards          BIGINT NOT NULL DEFAULT 0,
    total_threads         BIGINT NOT NULL DEFAULT 0,
    total_posts           BIGINT NOT NULL DEFAULT 0,
    total_reactions       BIGINT NOT NULL DEFAULT 0,
    total_mail            BIGINT NOT NULL DEFAULT 0,
    total_direct_messages BIGINT NOT NULL DEFAULT 0,
    total_logins          BIGINT NOT NULL DEFAULT 0,
    total_logouts         BIGINT NOT NULL DEFAULT 0,
    total_web_logins      BIGINT NOT NULL DEFAULT 0,
    total_web_logouts     BIGINT NOT NULL DEFAULT 0,
    total_guest_logins    BIGINT NOT NULL DEFAULT 0,
    total_guest_logouts   BIGINT NOT NULL DEFAULT 0,
    total_online_seconds  BIGINT NOT NULL DEFAULT 0,
    online_users          BIGINT NOT NULL DEFAULT 0,
    online_guests         BIGINT NOT NULL DEFAULT 0,
    max_online_users      BIGINT NOT NULL DEFAULT 0,
    max_online_at         BIGINT NOT NULL DEFAULT 0,
    max_online_guests     BIGINT NOT NULL DEFAULT 0,
    max_online_guests_at  BIGINT NOT NULL DEFAULT 0,
    head_seq              BIGINT NOT NULL DEFAULT 0,
    refreshed_at          BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS community_counter_totals (
    id                    TEXT PRIMARY KEY DEFAULT 'default',
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    updated_at            BIGINT NOT NULL DEFAULT 0
);
INSERT INTO community_counter_totals (id)
VALUES ('default')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS community_stat_history (
    day                   TEXT PRIMARY KEY,
    snapshot_at           BIGINT NOT NULL DEFAULT 0,
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
    total_online_seconds  BIGINT NOT NULL DEFAULT 0,
    online_users          INTEGER NOT NULL DEFAULT 0,
    online_guests         INTEGER NOT NULL DEFAULT 0,
    max_online_users      INTEGER NOT NULL DEFAULT 0,
    max_online_at         BIGINT NOT NULL DEFAULT 0,
    max_online_guests     INTEGER NOT NULL DEFAULT 0,
    max_online_guests_at  BIGINT NOT NULL DEFAULT 0,
    head_seq              BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot
    ON community_stat_history(snapshot_at DESC, day DESC);

CREATE TABLE IF NOT EXISTS derived_view_watermarks (
    view_name   TEXT PRIMARY KEY,
    applied_seq BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS login_hourly_stats (
    day         TEXT NOT NULL,
    hour        INTEGER NOT NULL CHECK(hour >= 0 AND hour <= 23),
    login_count INTEGER NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, hour)
);
CREATE INDEX IF NOT EXISTS idx_login_hourly_stats_updated
    ON login_hourly_stats(updated_at DESC, day DESC, hour);

CREATE TABLE IF NOT EXISTS board_read_markers (
    user_id      TEXT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id     TEXT   NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    last_seq     BIGINT NOT NULL DEFAULT 0,
    previous_seq BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_read_markers_user
    ON board_read_markers(user_id, board_id);

CREATE TABLE IF NOT EXISTS auth_pubkeys (
    user_id TEXT NOT NULL REFERENCES users(id),
    pubkey  TEXT NOT NULL,
    PRIMARY KEY (user_id, pubkey)
);

CREATE TABLE IF NOT EXISTS threads (
    id         TEXT PRIMARY KEY,
    board      TEXT NOT NULL REFERENCES boards(id),
    author     TEXT NOT NULL,
    -- No FK to users: system/announcement/anonymous threads use author_id=''
    -- (matches the SQLite schema, which has no FK here).
    author_id  TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0,
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
    -- No FK to users: system/announcement/anonymous posts use author_id=''
    -- (matches the SQLite schema).
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
    created_seq  BIGINT NOT NULL,
    updated_seq  BIGINT NOT NULL,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS post_deletions (
    post_id         TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id       TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    deleted_by_id   TEXT NOT NULL DEFAULT '',
    deleted_by_name TEXT NOT NULL DEFAULT '',
    reason          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK(kind IN ('recycle', 'junk')),
    deleted_at      BIGINT NOT NULL DEFAULT 0,
    seq             BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_deletions_board_kind
    ON post_deletions(board_id, kind, deleted_at DESC, seq DESC);

CREATE TABLE IF NOT EXISTS resident_feed_posts (
    post_id     TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq BIGINT NOT NULL DEFAULT 0,
    updated_seq BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_created
    ON resident_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_board_created
    ON resident_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_thread
    ON resident_feed_posts(thread_id);

CREATE TABLE IF NOT EXISTS latest_feed_posts (
    post_id     TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq BIGINT NOT NULL DEFAULT 0,
    updated_seq BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_created
    ON latest_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_board_created
    ON latest_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_thread
    ON latest_feed_posts(thread_id);

CREATE TABLE IF NOT EXISTS board_ranking_stats (
    board_id        TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    BIGINT NOT NULL DEFAULT 0,
    post_count      BIGINT NOT NULL DEFAULT 0,
    last_seq        BIGINT NOT NULL DEFAULT 0,
    last_post_at    BIGINT NOT NULL DEFAULT 0,
    moderator_count BIGINT NOT NULL DEFAULT 0,
    updated_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_ranking_stats_order
    ON board_ranking_stats(post_count DESC, thread_count DESC, last_seq DESC, board_id);

CREATE TABLE IF NOT EXISTS thread_ranking_stats (
    thread_id         TEXT PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    post_count        BIGINT NOT NULL DEFAULT 0,
    participant_count BIGINT NOT NULL DEFAULT 0,
    reaction_count    BIGINT NOT NULL DEFAULT 0,
    last_seq          BIGINT NOT NULL DEFAULT 0,
    updated_at        BIGINT NOT NULL DEFAULT 0,
    refreshed_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_thread_ranking_stats_order
    ON thread_ranking_stats(last_seq DESC, thread_id);

CREATE TABLE IF NOT EXISTS reply_ranking_posts (
    post_id      TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id    TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    created_seq  BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    refreshed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reply_ranking_posts_created
    ON reply_ranking_posts(created_seq DESC, post_id);

CREATE TABLE IF NOT EXISTS user_ranking_stats (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    posts_created        BIGINT NOT NULL DEFAULT 0,
    reactions_received   BIGINT NOT NULL DEFAULT 0,
    login_count          BIGINT NOT NULL DEFAULT 0,
    total_online_seconds BIGINT NOT NULL DEFAULT 0,
    trust_level          INTEGER NOT NULL DEFAULT 0,
    refreshed_at         BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_ranking_stats_order
    ON user_ranking_stats(posts_created DESC, reactions_received DESC, login_count DESC, total_online_seconds DESC, user_id);

CREATE TABLE IF NOT EXISTS blessing_ranking_stats (
    user_id         TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    blessing_count  BIGINT NOT NULL DEFAULT 0,
    last_blessed_at BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessing_ranking_stats_order
    ON blessing_ranking_stats(blessing_count DESC, last_blessed_at DESC, user_id);

CREATE TABLE IF NOT EXISTS archive_ranking_stats (
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL DEFAULT 'archive',
    path            TEXT NOT NULL DEFAULT '',
    entry_count     BIGINT NOT NULL DEFAULT 0,
    edited_count    BIGINT NOT NULL DEFAULT 0,
    last_updated_at BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_archive_ranking_stats_order
    ON archive_ranking_stats(kind, entry_count DESC, edited_count DESC, last_updated_at DESC, board_id, path);

CREATE TABLE IF NOT EXISTS board_summary_stats (
    board_id        TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    BIGINT NOT NULL DEFAULT 0,
    post_count      BIGINT NOT NULL DEFAULT 0,
    last_seq        BIGINT NOT NULL DEFAULT 0,
    created_at      BIGINT NOT NULL DEFAULT 0,
    moderator_count BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_summary_stats_activity
    ON board_summary_stats(last_seq DESC, post_count DESC, thread_count DESC, board_id);

CREATE TABLE IF NOT EXISTS unread_thread_summary_stats (
    thread_id    TEXT PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    author       TEXT NOT NULL DEFAULT '',
    author_id    TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    locked       INTEGER NOT NULL DEFAULT 0,
    post_count   BIGINT NOT NULL DEFAULT 0,
    last_seq     BIGINT NOT NULL DEFAULT 0,
    created_ts   BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    refreshed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_board_last
    ON unread_thread_summary_stats(board_id, last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_last
    ON unread_thread_summary_stats(last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_posts_thread_created_seq
    ON posts(thread, created_seq, redacted, id);

CREATE TABLE IF NOT EXISTS post_attachments (
    id           TEXT PRIMARY KEY,
    post_id      TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    url          TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_attachments_post
    ON post_attachments(post_id, created_at, id);

CREATE TABLE IF NOT EXISTS attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES post_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS thread_read_markers (
    user_id      TEXT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id    TEXT   NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    last_seq     BIGINT NOT NULL DEFAULT 0,
    previous_seq BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, thread_id)
);
CREATE INDEX IF NOT EXISTS idx_thread_read_markers_user
    ON thread_read_markers(user_id, thread_id);

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
    login_count    INTEGER NOT NULL DEFAULT 0,
    posts_created  INTEGER NOT NULL DEFAULT 0,
    days_visited   INTEGER NOT NULL DEFAULT 0,
    last_visit_day TEXT NOT NULL DEFAULT '',
    reactions_recv INTEGER NOT NULL DEFAULT 0,
    total_online_seconds BIGINT NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS post_reaction_count_shards (
    post_id     TEXT    NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value BIGINT  NOT NULL DEFAULT 0,
    updated_at  BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_post_reaction_count_shards_post
    ON post_reaction_count_shards(post_id);

CREATE TABLE IF NOT EXISTS poll_vote_count_shards (
    poll_id     TEXT    NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    option_id   TEXT    NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value BIGINT  NOT NULL DEFAULT 0,
    updated_at  BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (option_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_poll_vote_count_shards_poll
    ON poll_vote_count_shards(poll_id, option_id);

CREATE TABLE IF NOT EXISTS counter_checkpoints (
    counter_kind    TEXT    NOT NULL,
    target_id       TEXT    NOT NULL,
    parent_id       TEXT    NOT NULL DEFAULT '',
    count           BIGINT  NOT NULL DEFAULT 0,
    source_head_seq BIGINT  NOT NULL DEFAULT 0,
    checkpoint_seq  BIGINT  NOT NULL DEFAULT 0,
    checkpointed_at BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (counter_kind, target_id)
);
CREATE INDEX IF NOT EXISTS idx_counter_checkpoints_parent
    ON counter_checkpoints(counter_kind, parent_id);

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

CREATE TABLE IF NOT EXISTS content_filters (
    id         TEXT PRIMARY KEY,
    pattern    TEXT NOT NULL DEFAULT '',
    scope      TEXT NOT NULL DEFAULT 'global',
    active     INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_content_filters_active_scope
    ON content_filters(active, scope, updated_at DESC);

CREATE TABLE IF NOT EXISTS cursors (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    seq     BIGINT NOT NULL DEFAULT 0
);

-- posts_fts mirrors the SQLite FTS5 virtual table as a plain table. The write
-- paths (INSERT/UPDATE/DELETE) use identical SQL on both backends; full-text
-- search on Postgres uses an ILIKE fallback over body (see the search readers).
CREATE TABLE IF NOT EXISTS posts_fts (
    post_id   TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL DEFAULT '',
    board_id  TEXT NOT NULL DEFAULT '',
    author    TEXT NOT NULL DEFAULT '',
    body      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_posts_fts_board ON posts_fts(board_id);

-- relay_deliveries lives here (not in migration 1) because its foreign keys
-- reference boards, threads, and posts, which are created above in this
-- migration.
CREATE TABLE IF NOT EXISTS relay_deliveries (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    post_id     TEXT NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id   TEXT NOT NULL DEFAULT '',
    author_name TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    seq         BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_status_created
    ON relay_deliveries(status, created_at, id);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_board_created
    ON relay_deliveries(board_id, created_at, id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (2, 'postgres-forum-projections', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 3,
			Name:    "postgres-favorite-folders",
			SQL: `
CREATE TABLE IF NOT EXISTS favorite_folders (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id  TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_favorite_folders_user_parent_position
    ON favorite_folders(user_id, parent_id, position, name);

ALTER TABLE board_favorites
    ADD COLUMN IF NOT EXISTS folder_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_board_favorites_user_folder_position
    ON board_favorites(user_id, folder_id, position, board_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (3, 'postgres-favorite-folders', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 4,
			Name:    "postgres-board-policy-moderators",
			SQL: `
CREATE TABLE IF NOT EXISTS board_settings (
    board_id            TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
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
    updated_at          BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS board_moderators (
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_moderators_board_position
    ON board_moderators(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_moderator_terms (
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at   BIGINT NOT NULL DEFAULT 0,
    ended_at     BIGINT NOT NULL DEFAULT 0,
    appointed_by TEXT NOT NULL DEFAULT '',
    removed_by   TEXT NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id, started_at)
);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_board_time
    ON board_moderator_terms(board_id, ended_at, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_user_time
    ON board_moderator_terms(user_id, started_at DESC);

CREATE TABLE IF NOT EXISTS board_members (
    board_id           TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title              TEXT NOT NULL DEFAULT '',
    position           INTEGER NOT NULL DEFAULT 0,
    can_manage_members INTEGER NOT NULL DEFAULT 0,
    can_curate         INTEGER NOT NULL DEFAULT 0,
    can_moderate_posts INTEGER NOT NULL DEFAULT 0,
    can_moderate_threads INTEGER NOT NULL DEFAULT 0,
    can_announce       INTEGER NOT NULL DEFAULT 0,
    can_manage_polls   INTEGER NOT NULL DEFAULT 0,
    can_set_board_settings INTEGER NOT NULL DEFAULT 0,
    created_at         BIGINT NOT NULL DEFAULT 0,
    updated_at         BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_members_board_user
    ON board_members(board_id, user_id);
CREATE INDEX IF NOT EXISTS idx_board_members_user_board
    ON board_members(user_id, board_id);
CREATE INDEX IF NOT EXISTS idx_board_members_board_position
    ON board_members(board_id, position, user_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (4, 'postgres-board-policy-moderators', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 5,
			Name:    "postgres-digest-entries",
			SQL: `
CREATE TABLE IF NOT EXISTS digest_entries (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL DEFAULT 'post',
    target_id   TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT 'digest',
	title       TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	note        TEXT NOT NULL DEFAULT '',
	body        TEXT NOT NULL DEFAULT '',
	body_edited INTEGER NOT NULL DEFAULT 0,
	created_by  TEXT NOT NULL REFERENCES users(id),
    created_at  BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    UNIQUE(board_id, target_kind, target_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_entries_board_kind_path
    ON digest_entries(board_id, kind, path, updated_at DESC);

CREATE TABLE IF NOT EXISTS digest_directories (
    id         TEXT PRIMARY KEY,
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'archive',
    path       TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path
    ON digest_directories(board_id, kind, path);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (5, 'postgres-digest-entries', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 6,
			Name:    "postgres-private-communication",
			SQL: `
CREATE TABLE IF NOT EXISTS mail_messages (
    id           TEXT PRIMARY KEY,
    from_user_id TEXT NOT NULL REFERENCES users(id),
    subject      TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    parent_id    TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    seq          BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_messages_created
    ON mail_messages(created_at DESC);

CREATE TABLE IF NOT EXISTS mail_copies (
    message_id TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'recipient',
    mailbox    TEXT NOT NULL DEFAULT 'inbox',
    read       INTEGER NOT NULL DEFAULT 0,
    kept       INTEGER NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (message_id, user_id, role)
);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_box
    ON mail_copies(user_id, mailbox, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_copies_user_unread
    ON mail_copies(user_id, read, updated_at DESC);

CREATE TABLE IF NOT EXISTS mail_attachments (
    id           TEXT PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    url          TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_attachments_message
    ON mail_attachments(message_id, created_at, id);

CREATE TABLE IF NOT EXISTS mail_attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES mail_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mail_groups (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mail_groups_user_name
    ON mail_groups(user_id, name);

CREATE TABLE IF NOT EXISTS mail_group_members (
    group_id   TEXT NOT NULL REFERENCES mail_groups(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_group_members_group_position
    ON mail_group_members(group_id, position, user_id);

CREATE TABLE IF NOT EXISTS mail_group_deletions (
    event_id   TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL,
    group_id   TEXT NOT NULL,
    deleted_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_group_deletions_owner_group
    ON mail_group_deletions(owner_id, group_id);

CREATE TABLE IF NOT EXISTS direct_messages (
    id                TEXT PRIMARY KEY,
    conversation_id   TEXT NOT NULL,
    from_user_id      TEXT NOT NULL REFERENCES users(id),
    to_user_id        TEXT NOT NULL REFERENCES users(id),
    body              TEXT NOT NULL DEFAULT '',
    read_at           BIGINT NOT NULL DEFAULT 0,
    sender_deleted    INTEGER NOT NULL DEFAULT 0,
    recipient_deleted INTEGER NOT NULL DEFAULT 0,
    created_at        BIGINT NOT NULL DEFAULT 0,
    seq               BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_direct_messages_conversation
    ON direct_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_direct_messages_recipient_unread
    ON direct_messages(to_user_id, read_at, created_at DESC);

CREATE TABLE IF NOT EXISTS direct_message_settings (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    policy     TEXT NOT NULL DEFAULT 'all',
    updated_at BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (6, 'postgres-private-communication', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 7,
			Name:    "postgres-social-graph-presence",
			SQL: `
CREATE TABLE IF NOT EXISTS user_relationships (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    created_at     BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, target_user_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_user_relationships_target_kind
    ON user_relationships(target_user_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_relationships_user_kind
    ON user_relationships(user_id, kind, updated_at DESC);

CREATE TABLE IF NOT EXISTS user_presence (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'active',
    mode           TEXT NOT NULL DEFAULT '',
    board_id       TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_presence_last_seen
    ON user_presence(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen
    ON user_presence(board_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS user_presence_sessions (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id     TEXT NOT NULL DEFAULT 'default',
    status         TEXT NOT NULL DEFAULT 'active',
    mode           TEXT NOT NULL DEFAULT '',
    board_id       TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen
    ON user_presence_sessions(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen
    ON user_presence_sessions(board_id, last_seen DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (7, 'postgres-social-graph-presence', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 8,
			Name:    "postgres-board-members",
			SQL: `
CREATE TABLE IF NOT EXISTS board_members (
    board_id           TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title              TEXT NOT NULL DEFAULT '',
    position           INTEGER NOT NULL DEFAULT 0,
    can_manage_members INTEGER NOT NULL DEFAULT 0,
    can_curate         INTEGER NOT NULL DEFAULT 0,
    can_moderate_posts INTEGER NOT NULL DEFAULT 0,
    can_moderate_threads INTEGER NOT NULL DEFAULT 0,
    can_announce       INTEGER NOT NULL DEFAULT 0,
    can_manage_polls   INTEGER NOT NULL DEFAULT 0,
    can_set_board_settings INTEGER NOT NULL DEFAULT 0,
    created_at         BIGINT NOT NULL DEFAULT 0,
    updated_at         BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_board_members_board_user
    ON board_members(board_id, user_id);
CREATE INDEX IF NOT EXISTS idx_board_members_user_board
    ON board_members(user_id, board_id);
CREATE INDEX IF NOT EXISTS idx_board_members_board_position
    ON board_members(board_id, position, user_id);

CREATE TABLE IF NOT EXISTS board_member_applications (
    id           TEXT NOT NULL PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    note         TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    reviewer_id  TEXT NOT NULL DEFAULT '',
    review_note  TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    reviewed_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_board_status
    ON board_member_applications(board_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_user_board
    ON board_member_applications(user_id, board_id, updated_at DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (8, 'postgres-board-members', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 9,
			Name:    "postgres-post-attachments",
			SQL: `
CREATE TABLE IF NOT EXISTS post_attachments (
    id           TEXT PRIMARY KEY,
    post_id      TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    url          TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_attachments_post
    ON post_attachments(post_id, created_at, id);

CREATE TABLE IF NOT EXISTS attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES post_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (9, 'postgres-post-attachments', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 10,
			Name:    "postgres-board-member-applications",
			SQL: `
CREATE TABLE IF NOT EXISTS board_member_applications (
    id           TEXT PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    note         TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    reviewer_id  TEXT NOT NULL DEFAULT '',
    review_note  TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    reviewed_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_board_status
    ON board_member_applications(board_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_member_applications_user_board
    ON board_member_applications(user_id, board_id, updated_at DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (10, 'postgres-board-member-applications', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 11,
			Name:    "postgres-attachment-blobs",
			SQL: `
CREATE TABLE IF NOT EXISTS attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES post_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (11, 'postgres-attachment-blobs', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 12,
			Name:    "postgres-board-member-requirements",
			SQL: `
CREATE TABLE IF NOT EXISTS board_member_requirements (
    board_id                      TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    min_login_count               INTEGER NOT NULL DEFAULT 0,
    min_post_count                INTEGER NOT NULL DEFAULT 0,
    min_trust_level               INTEGER NOT NULL DEFAULT 0,
    min_score                     INTEGER NOT NULL DEFAULT 0,
    min_board_post_count          INTEGER NOT NULL DEFAULT 0,
    min_board_original_post_count INTEGER NOT NULL DEFAULT 0,
    min_board_digest_count        INTEGER NOT NULL DEFAULT 0,
    min_board_mark_count          INTEGER NOT NULL DEFAULT 0,
    max_members                   INTEGER NOT NULL DEFAULT 0,
    approval_mode                 TEXT NOT NULL DEFAULT 'manual',
    updated_at                    BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (12, 'postgres-board-member-requirements', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 13,
			Name:    "postgres-user-activity-login-count",
			SQL: `
ALTER TABLE user_activity
    ADD COLUMN IF NOT EXISTS login_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_activity
    ADD COLUMN IF NOT EXISTS total_online_seconds BIGINT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (13, 'postgres-user-activity-login-count', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 14,
			Name:    "postgres-board-local-member-requirements",
			SQL: `
ALTER TABLE board_member_requirements
    ADD COLUMN IF NOT EXISTS min_board_post_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_member_requirements
    ADD COLUMN IF NOT EXISTS min_board_original_post_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_member_requirements
    ADD COLUMN IF NOT EXISTS min_board_digest_count INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (14, 'postgres-board-local-member-requirements', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 15,
			Name:    "postgres-board-member-score-marks",
			SQL: `
ALTER TABLE board_member_requirements
    ADD COLUMN IF NOT EXISTS min_score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_member_requirements
    ADD COLUMN IF NOT EXISTS min_board_mark_count INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (15, 'postgres-board-member-score-marks', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 16,
			Name:    "postgres-board-member-permissions",
			SQL: `
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS position INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_board_members_board_position
    ON board_members(board_id, position, user_id);
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_manage_members INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_curate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_moderate_posts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_moderate_threads INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_announce INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_manage_polls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE board_members
    ADD COLUMN IF NOT EXISTS can_set_board_settings INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (16, 'postgres-board-member-permissions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 17,
			Name:    "postgres-rich-presence",
			SQL: `
ALTER TABLE user_presence
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT '';
ALTER TABLE user_presence
    ADD COLUMN IF NOT EXISTS board_id TEXT NOT NULL DEFAULT '';
ALTER TABLE user_presence
    ADD COLUMN IF NOT EXISTS thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE user_presence
    ADD COLUMN IF NOT EXISTS location_label TEXT NOT NULL DEFAULT '';
ALTER TABLE user_presence
    ADD COLUMN IF NOT EXISTS from_host TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen
    ON user_presence(board_id, last_seen DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (17, 'postgres-rich-presence', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 18,
			Name:    "postgres-mail-groups-pager-policy",
			SQL: `
CREATE TABLE IF NOT EXISTS mail_groups (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mail_groups_user_name
    ON mail_groups(user_id, name);

CREATE TABLE IF NOT EXISTS mail_group_members (
    group_id   TEXT NOT NULL REFERENCES mail_groups(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_group_members_group_position
    ON mail_group_members(group_id, position, user_id);

CREATE TABLE IF NOT EXISTS direct_message_settings (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    policy     TEXT NOT NULL DEFAULT 'all',
    updated_at BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (18, 'postgres-mail-groups-pager-policy', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 19,
			Name:    "postgres-mail-attachments",
			SQL: `
CREATE TABLE IF NOT EXISTS mail_attachments (
    id           TEXT PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    url          TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_attachments_message
    ON mail_attachments(message_id, created_at, id);

CREATE TABLE IF NOT EXISTS mail_attachment_blobs (
    attachment_id TEXT PRIMARY KEY REFERENCES mail_attachments(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    uploaded_at   BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (19, 'postgres-mail-attachments', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 20,
			Name:    "postgres-relay-deliveries",
			SQL: `
CREATE TABLE IF NOT EXISTS relay_deliveries (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    post_id     TEXT NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id   TEXT NOT NULL DEFAULT '',
    author_name TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    seq         BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_status_created
    ON relay_deliveries(status, created_at, id);
CREATE INDEX IF NOT EXISTS idx_relay_deliveries_board_created
    ON relay_deliveries(board_id, created_at, id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (20, 'postgres-relay-deliveries', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 21,
			Name:    "postgres-digest-entry-body",
			SQL: `
ALTER TABLE digest_entries
    ADD COLUMN IF NOT EXISTS body TEXT NOT NULL DEFAULT '';
ALTER TABLE digest_entries
    ADD COLUMN IF NOT EXISTS body_edited INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (21, 'postgres-digest-entry-body', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 22,
			Name:    "postgres-digest-directories",
			SQL: `
CREATE TABLE IF NOT EXISTS digest_directories (
    id         TEXT PRIMARY KEY,
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'archive',
    path       TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE(board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path
    ON digest_directories(board_id, kind, path);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (22, 'postgres-digest-directories', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 23,
			Name:    "postgres-presence-sessions",
			SQL: `
CREATE TABLE IF NOT EXISTS user_presence_sessions (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id     TEXT NOT NULL DEFAULT 'default',
    status         TEXT NOT NULL DEFAULT 'active',
    mode           TEXT NOT NULL DEFAULT '',
    board_id       TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen
    ON user_presence_sessions(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen
    ON user_presence_sessions(board_id, last_seen DESC);

INSERT INTO user_presence_sessions (
    user_id, session_id, status, mode, board_id, thread_id,
    location_label, from_host, last_seen, updated_at
)
SELECT user_id, 'default', status, mode, board_id, thread_id,
       location_label, from_host, last_seen, updated_at
  FROM user_presence
ON CONFLICT (user_id, session_id) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (23, 'postgres-presence-sessions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 24,
			Name:    "postgres-user-private-profiles",
			SQL: `
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
    updated_at         BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (24, 'postgres-user-private-profiles', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 25,
			Name:    "postgres-user-personal-files",
			SQL: `
CREATE TABLE IF NOT EXISTS user_personal_files (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    public     INTEGER NOT NULL DEFAULT 1,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_user_personal_files_user_public
    ON user_personal_files(user_id, public, name);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (25, 'postgres-user-personal-files', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 26,
			Name:    "postgres-account-registration-approval",
			SQL: `
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS registration_status TEXT NOT NULL DEFAULT 'approved';
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS reviewed_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS review_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS account_registration_settings (
    id               TEXT PRIMARY KEY DEFAULT 'default',
    require_approval INTEGER NOT NULL DEFAULT 0,
    updated_at       BIGINT NOT NULL DEFAULT 0
);
INSERT INTO account_registration_settings (id, require_approval, updated_at)
VALUES ('default', 0, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (26, 'postgres-account-registration-approval', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 27,
			Name:    "postgres-password-recovery-requests",
			SQL: `
CREATE TABLE IF NOT EXISTS password_recovery_requests (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending',
    submitted_name  TEXT NOT NULL DEFAULT '',
    submitted_email TEXT NOT NULL DEFAULT '',
    note            TEXT NOT NULL DEFAULT '',
    reviewer_id     TEXT NOT NULL DEFAULT '',
    review_note     TEXT NOT NULL DEFAULT '',
    created_at      BIGINT NOT NULL DEFAULT 0,
    updated_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_password_recovery_status_updated
    ON password_recovery_requests(status, updated_at DESC, created_at DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (27, 'postgres-password-recovery-requests', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 28,
			Name:    "postgres-blessings",
			SQL: `
CREATE TABLE IF NOT EXISTS blessings (
    id           TEXT PRIMARY KEY,
    from_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message      TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    seq          BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessings_to_created
    ON blessings(to_user_id, created_at DESC, seq DESC);
CREATE INDEX IF NOT EXISTS idx_blessings_from_created
    ON blessings(from_user_id, created_at DESC, seq DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (28, 'postgres-blessings', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 29,
			Name:    "postgres-community-stat-history",
			SQL: `
CREATE TABLE IF NOT EXISTS community_stat_history (
    day                   TEXT PRIMARY KEY,
    snapshot_at           BIGINT NOT NULL DEFAULT 0,
    total_users           INTEGER NOT NULL DEFAULT 0,
    total_boards          INTEGER NOT NULL DEFAULT 0,
    total_threads         INTEGER NOT NULL DEFAULT 0,
    total_posts           INTEGER NOT NULL DEFAULT 0,
    total_reactions       INTEGER NOT NULL DEFAULT 0,
    total_mail            INTEGER NOT NULL DEFAULT 0,
    total_direct_messages INTEGER NOT NULL DEFAULT 0,
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    total_online_seconds  BIGINT NOT NULL DEFAULT 0,
    online_users          INTEGER NOT NULL DEFAULT 0,
    online_guests         INTEGER NOT NULL DEFAULT 0,
    max_online_users      INTEGER NOT NULL DEFAULT 0,
    max_online_at         BIGINT NOT NULL DEFAULT 0,
    max_online_guests     INTEGER NOT NULL DEFAULT 0,
    max_online_guests_at  BIGINT NOT NULL DEFAULT 0,
    head_seq              BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot
    ON community_stat_history(snapshot_at DESC, day DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (29, 'postgres-community-stat-history', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 30,
			Name:    "postgres-post-article-flags",
			SQL: `
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS marked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS recommended INTEGER NOT NULL DEFAULT 0;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS no_reply INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (30, 'postgres-post-article-flags', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 31,
			Name:    "postgres-post-source-lineage",
			SQL: `
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_post TEXT NOT NULL DEFAULT '';
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_thread TEXT NOT NULL DEFAULT '';
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_board TEXT NOT NULL DEFAULT '';
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_author TEXT NOT NULL DEFAULT '';
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_author_id TEXT NOT NULL DEFAULT '';
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS source_title TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (31, 'postgres-post-source-lineage', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 32,
			Name:    "postgres-post-tex-mailback-flags",
			SQL: `
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS tex INTEGER NOT NULL DEFAULT 0;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS mail_back INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (32, 'postgres-post-tex-mailback-flags', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 33,
			Name:    "postgres-total-online-seconds",
			SQL: `
ALTER TABLE user_activity
    ADD COLUMN IF NOT EXISTS total_online_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_online_seconds BIGINT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (33, 'postgres-total-online-seconds', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 34,
			Name:    "postgres-guest-presence-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS guest_presence_sessions (
    session_id     TEXT PRIMARY KEY,
    status         TEXT NOT NULL DEFAULT 'active',
    location_label TEXT NOT NULL DEFAULT '',
    from_host      TEXT NOT NULL DEFAULT '',
    last_seen      BIGINT NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_guest_presence_sessions_last_seen
    ON guest_presence_sessions(last_seen DESC);

ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS online_guests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS max_online_guests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS max_online_guests_at BIGINT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (34, 'postgres-guest-presence-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 35,
			Name:    "postgres-user-profile-title",
			SQL: `
ALTER TABLE user_profiles
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (35, 'postgres-user-profile-title', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 36,
			Name:    "postgres-community-total-logins",
			SQL: `
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_logins INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (36, 'postgres-community-total-logins', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 37,
			Name:    "postgres-board-settings-stats-excluded",
			SQL: `
ALTER TABLE board_settings
    ADD COLUMN IF NOT EXISTS stats_excluded INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (37, 'postgres-board-settings-stats-excluded', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 38,
			Name:    "postgres-login-hourly-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS login_hourly_stats (
    day         TEXT NOT NULL,
    hour        INTEGER NOT NULL CHECK(hour >= 0 AND hour <= 23),
    login_count INTEGER NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, hour)
);
CREATE INDEX IF NOT EXISTS idx_login_hourly_stats_updated
    ON login_hourly_stats(updated_at DESC, day DESC, hour);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (38, 'postgres-login-hourly-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 39,
			Name:    "postgres-recommended-boards",
			SQL: `
CREATE TABLE IF NOT EXISTS recommended_boards (
    board_id   TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    note       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    curated_by TEXT NOT NULL REFERENCES users(id),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recommended_boards_position
    ON recommended_boards(position, updated_at DESC, board_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (39, 'postgres-recommended-boards', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 40,
			Name:    "postgres-board-zaps",
			SQL: `
ALTER TABLE board_settings
    ADD COLUMN IF NOT EXISTS zap_allowed INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS board_zaps (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, board_id)
);
CREATE INDEX IF NOT EXISTS idx_board_zaps_user
    ON board_zaps(user_id, board_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (40, 'postgres-board-zaps', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 41,
			Name:    "postgres-post-deletions",
			SQL: `
CREATE TABLE IF NOT EXISTS post_deletions (
    post_id         TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id       TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    deleted_by_id   TEXT NOT NULL DEFAULT '',
    deleted_by_name TEXT NOT NULL DEFAULT '',
    reason          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK(kind IN ('recycle', 'junk')),
    deleted_at      BIGINT NOT NULL DEFAULT 0,
    seq             BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_post_deletions_board_kind
    ON post_deletions(board_id, kind, deleted_at DESC, seq DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (41, 'postgres-post-deletions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 42,
			Name:    "postgres-board-moderator-terms",
			SQL: `
CREATE TABLE IF NOT EXISTS board_moderator_terms (
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at   BIGINT NOT NULL DEFAULT 0,
    ended_at     BIGINT NOT NULL DEFAULT 0,
    appointed_by TEXT NOT NULL DEFAULT '',
    removed_by   TEXT NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, user_id, started_at)
);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_board_time
    ON board_moderator_terms(board_id, ended_at, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_user_time
    ON board_moderator_terms(user_id, started_at DESC);

INSERT INTO board_moderator_terms (
    board_id, user_id, started_at, ended_at, appointed_by, removed_by,
    position, created_at, updated_at
)
SELECT board_id, user_id,
       CASE WHEN created_at > 0 THEN created_at ELSE updated_at END,
       0, '', '', position, created_at, updated_at
  FROM board_moderators
ON CONFLICT (board_id, user_id, started_at) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (42, 'postgres-board-moderator-terms', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 43,
			Name:    "postgres-community-static-counters",
			SQL: `
CREATE TABLE IF NOT EXISTS community_counter_totals (
    id                    TEXT PRIMARY KEY DEFAULT 'default',
    total_logouts         INTEGER NOT NULL DEFAULT 0,
    total_web_logins      INTEGER NOT NULL DEFAULT 0,
    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
    updated_at            BIGINT NOT NULL DEFAULT 0
);
INSERT INTO community_counter_totals (id)
VALUES ('default')
ON CONFLICT (id) DO NOTHING;
UPDATE community_counter_totals
   SET total_web_logins=(SELECT COALESCE(SUM(login_count), 0) FROM user_activity)
 WHERE id='default' AND total_web_logins=0;

ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_logouts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_web_logins INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_web_logouts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_guest_logins INTEGER NOT NULL DEFAULT 0;
ALTER TABLE community_stat_history
    ADD COLUMN IF NOT EXISTS total_guest_logouts INTEGER NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (43, 'postgres-community-static-counters', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 44,
			Name:    "postgres-chat-rooms",
			SQL: `
CREATE TABLE IF NOT EXISTS chat_rooms (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    topic      TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);
INSERT INTO chat_rooms (id, name, topic, created_by, created_at, updated_at)
VALUES ('lobby', 'Lobby', 'Campus lobby chat', '', 0, 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS chat_lines (
    id         TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_name  TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_chat_lines_room_created
    ON chat_lines(room_id, created_at DESC, id DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (44, 'postgres-chat-rooms', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 45,
			Name:    "postgres-event-partitions",
			SQL: `
ALTER TABLE events ADD COLUMN IF NOT EXISTS partition_kind TEXT NOT NULL DEFAULT 'global';
ALTER TABLE events ADD COLUMN IF NOT EXISTS partition_key TEXT NOT NULL DEFAULT 'global';
ALTER TABLE events ADD COLUMN IF NOT EXISTS partition_offset BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_events_partition_offset
    ON events(partition_kind, partition_key, partition_offset);
UPDATE events SET partition_offset=seq WHERE partition_offset=0;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (45, 'postgres-event-partitions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 46,
			Name:    "postgres-event-partition-offsets",
			SQL: `
CREATE TABLE IF NOT EXISTS event_partition_offsets (
    partition_kind TEXT NOT NULL,
    partition_key  TEXT NOT NULL,
    last_offset    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (partition_kind, partition_key)
);
INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
   SELECT partition_kind, partition_key, MAX(partition_offset)
     FROM events
    GROUP BY partition_kind, partition_key
ON CONFLICT (partition_kind, partition_key) DO UPDATE
      SET last_offset=GREATEST(event_partition_offsets.last_offset, excluded.last_offset);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (46, 'postgres-event-partition-offsets', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 47,
			Name:    "postgres-processed-commands-partitions",
			SQL: `
CREATE TABLE IF NOT EXISTS processed_commands_v2 (
    partition_kind TEXT NOT NULL DEFAULT 'global',
    partition_key  TEXT NOT NULL DEFAULT 'global',
    actor_id       TEXT NOT NULL,
    cid            TEXT NOT NULL,
    command_hash   TEXT NOT NULL,
    result_json    TEXT NOT NULL,
    processed_at   BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_processed_commands_v2_actor_cid
    ON processed_commands_v2(actor_id, cid);
INSERT INTO processed_commands_v2 (
    partition_kind, partition_key, actor_id, cid, command_hash, result_json, processed_at
)
 SELECT 'global', 'global', actor_id, cid, command_hash, result_json, processed_at
   FROM processed_commands
ON CONFLICT (partition_kind, partition_key, actor_id, cid) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (47, 'postgres-processed-commands-partitions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 48,
			Name:    "postgres-command-partition-leases",
			SQL: `
CREATE TABLE IF NOT EXISTS command_partition_leases (
    partition_kind TEXT NOT NULL,
    partition_key  TEXT NOT NULL,
    owner_id       TEXT NOT NULL,
    claimed_at     BIGINT NOT NULL,
    expires_at     BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_partition_leases_expires
    ON command_partition_leases(expires_at);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (48, 'postgres-command-partition-leases', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 49,
			Name:    "postgres-derived-view-watermarks",
			SQL: `
CREATE TABLE IF NOT EXISTS derived_view_watermarks (
    view_name   TEXT PRIMARY KEY,
    applied_seq BIGINT NOT NULL DEFAULT 0,
    updated_at  BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (49, 'postgres-derived-view-watermarks', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 50,
			Name:    "postgres-resident-feed-posts",
			SQL: `
CREATE TABLE IF NOT EXISTS resident_feed_posts (
    post_id     TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq BIGINT NOT NULL DEFAULT 0,
    updated_seq BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_created
    ON resident_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_board_created
    ON resident_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_thread
    ON resident_feed_posts(thread_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (50, 'postgres-resident-feed-posts', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 51,
			Name:    "postgres-board-ranking-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS board_ranking_stats (
    board_id        TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    BIGINT NOT NULL DEFAULT 0,
    post_count      BIGINT NOT NULL DEFAULT 0,
    last_seq        BIGINT NOT NULL DEFAULT 0,
    last_post_at    BIGINT NOT NULL DEFAULT 0,
    moderator_count BIGINT NOT NULL DEFAULT 0,
    updated_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_ranking_stats_order
    ON board_ranking_stats(post_count DESC, thread_count DESC, last_seq DESC, board_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (51, 'postgres-board-ranking-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 52,
			Name:    "postgres-thread-ranking-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS thread_ranking_stats (
    thread_id         TEXT PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    post_count        BIGINT NOT NULL DEFAULT 0,
    participant_count BIGINT NOT NULL DEFAULT 0,
    reaction_count    BIGINT NOT NULL DEFAULT 0,
    last_seq          BIGINT NOT NULL DEFAULT 0,
    updated_at        BIGINT NOT NULL DEFAULT 0,
    refreshed_at      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_thread_ranking_stats_order
    ON thread_ranking_stats(last_seq DESC, thread_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (52, 'postgres-thread-ranking-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 53,
			Name:    "postgres-reply-ranking-posts",
			SQL: `
CREATE TABLE IF NOT EXISTS reply_ranking_posts (
    post_id      TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id    TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    created_seq  BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    refreshed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reply_ranking_posts_created
    ON reply_ranking_posts(created_seq DESC, post_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (53, 'postgres-reply-ranking-posts', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 54,
			Name:    "postgres-user-ranking-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS user_ranking_stats (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    posts_created        BIGINT NOT NULL DEFAULT 0,
    reactions_received   BIGINT NOT NULL DEFAULT 0,
    login_count          BIGINT NOT NULL DEFAULT 0,
    total_online_seconds BIGINT NOT NULL DEFAULT 0,
    trust_level          INTEGER NOT NULL DEFAULT 0,
    refreshed_at         BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_ranking_stats_order
    ON user_ranking_stats(posts_created DESC, reactions_received DESC, login_count DESC, total_online_seconds DESC, user_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (54, 'postgres-user-ranking-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 55,
			Name:    "postgres-blessing-ranking-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS blessing_ranking_stats (
    user_id         TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    blessing_count  BIGINT NOT NULL DEFAULT 0,
    last_blessed_at BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_blessing_ranking_stats_order
    ON blessing_ranking_stats(blessing_count DESC, last_blessed_at DESC, user_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (55, 'postgres-blessing-ranking-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 56,
			Name:    "postgres-archive-ranking-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS archive_ranking_stats (
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL DEFAULT 'archive',
    path            TEXT NOT NULL DEFAULT '',
    entry_count     BIGINT NOT NULL DEFAULT 0,
    edited_count    BIGINT NOT NULL DEFAULT 0,
    last_updated_at BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, kind, path)
);
CREATE INDEX IF NOT EXISTS idx_archive_ranking_stats_order
    ON archive_ranking_stats(kind, entry_count DESC, edited_count DESC, last_updated_at DESC, board_id, path);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (56, 'postgres-archive-ranking-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 57,
			Name:    "postgres-board-summary-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS board_summary_stats (
    board_id        TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
    thread_count    BIGINT NOT NULL DEFAULT 0,
    post_count      BIGINT NOT NULL DEFAULT 0,
    last_seq        BIGINT NOT NULL DEFAULT 0,
    created_at      BIGINT NOT NULL DEFAULT 0,
    moderator_count BIGINT NOT NULL DEFAULT 0,
    refreshed_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_summary_stats_activity
    ON board_summary_stats(last_seq DESC, post_count DESC, thread_count DESC, board_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (57, 'postgres-board-summary-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 58,
			Name:    "postgres-unread-thread-summary-stats",
			SQL: `
CREATE TABLE IF NOT EXISTS unread_thread_summary_stats (
    thread_id    TEXT PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    author       TEXT NOT NULL DEFAULT '',
    author_id    TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    locked       INTEGER NOT NULL DEFAULT 0,
    post_count   BIGINT NOT NULL DEFAULT 0,
    last_seq     BIGINT NOT NULL DEFAULT 0,
    created_ts   BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_at   BIGINT NOT NULL DEFAULT 0,
    refreshed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_board_last
    ON unread_thread_summary_stats(board_id, last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_last
    ON unread_thread_summary_stats(last_seq DESC, thread_id);
CREATE INDEX IF NOT EXISTS idx_posts_thread_created_seq
    ON posts(thread, created_seq, redacted, id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (58, 'postgres-unread-thread-summary-stats', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 59,
			Name:    "postgres-community-stats-snapshot",
			SQL: `
CREATE TABLE IF NOT EXISTS community_stats_snapshot (
    id                    TEXT PRIMARY KEY DEFAULT 'default',
    total_users           BIGINT NOT NULL DEFAULT 0,
    total_boards          BIGINT NOT NULL DEFAULT 0,
    total_threads         BIGINT NOT NULL DEFAULT 0,
    total_posts           BIGINT NOT NULL DEFAULT 0,
    total_reactions       BIGINT NOT NULL DEFAULT 0,
    total_mail            BIGINT NOT NULL DEFAULT 0,
    total_direct_messages BIGINT NOT NULL DEFAULT 0,
    total_logins          BIGINT NOT NULL DEFAULT 0,
    total_logouts         BIGINT NOT NULL DEFAULT 0,
    total_web_logins      BIGINT NOT NULL DEFAULT 0,
    total_web_logouts     BIGINT NOT NULL DEFAULT 0,
    total_guest_logins    BIGINT NOT NULL DEFAULT 0,
    total_guest_logouts   BIGINT NOT NULL DEFAULT 0,
    total_online_seconds  BIGINT NOT NULL DEFAULT 0,
    online_users          BIGINT NOT NULL DEFAULT 0,
    online_guests         BIGINT NOT NULL DEFAULT 0,
    max_online_users      BIGINT NOT NULL DEFAULT 0,
    max_online_at         BIGINT NOT NULL DEFAULT 0,
    max_online_guests     BIGINT NOT NULL DEFAULT 0,
    max_online_guests_at  BIGINT NOT NULL DEFAULT 0,
    head_seq              BIGINT NOT NULL DEFAULT 0,
    refreshed_at          BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (59, 'postgres-community-stats-snapshot', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 60,
			Name:    "postgres-hot-thread-splits",
			SQL: `
CREATE TABLE IF NOT EXISTS hot_thread_splits (
    thread_id  TEXT PRIMARY KEY,
    shards     INTEGER NOT NULL,
    updated_at BIGINT NOT NULL
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (60, 'postgres-hot-thread-splits', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 61,
			Name:    "postgres-counter-checkpoints",
			SQL: `
CREATE TABLE IF NOT EXISTS counter_checkpoints (
    counter_kind    TEXT    NOT NULL,
    target_id       TEXT    NOT NULL,
    parent_id       TEXT    NOT NULL DEFAULT '',
    count           BIGINT  NOT NULL DEFAULT 0,
    source_head_seq BIGINT  NOT NULL DEFAULT 0,
    checkpoint_seq  BIGINT  NOT NULL DEFAULT 0,
    checkpointed_at BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (counter_kind, target_id)
);
CREATE INDEX IF NOT EXISTS idx_counter_checkpoints_parent
    ON counter_checkpoints(counter_kind, parent_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (61, 'postgres-counter-checkpoints', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 62,
			Name:    "postgres-command-log-receipts",
			SQL: `
CREATE TABLE IF NOT EXISTS command_log_receipts (
    partition_kind TEXT NOT NULL DEFAULT 'global',
    partition_key  TEXT NOT NULL DEFAULT 'global',
    actor_id       TEXT NOT NULL DEFAULT '',
    cid            TEXT NOT NULL,
    command_offset BIGINT NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT '',
    error_json     TEXT NOT NULL DEFAULT '',
    updated_at     BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_actor_cid
    ON command_log_receipts(actor_id, cid);
CREATE INDEX IF NOT EXISTS idx_command_log_receipts_partition_offset
    ON command_log_receipts(partition_kind, partition_key, command_offset);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (62, 'postgres-command-log-receipts', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 63,
			Name:    "postgres-attachment-blob-staging",
			SQL: `
CREATE TABLE IF NOT EXISTS attachment_blob_staging (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    data         BYTEA NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    actor_id     TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    expires_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_attachment_blob_staging_expiry
    ON attachment_blob_staging(expires_at, kind);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (63, 'postgres-attachment-blob-staging', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 64,
			Name:    "postgres-counter-shards",
			SQL: `
CREATE TABLE IF NOT EXISTS post_reaction_count_shards (
    post_id     TEXT    NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value BIGINT  NOT NULL DEFAULT 0,
    updated_at  BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_post_reaction_count_shards_post
    ON post_reaction_count_shards(post_id);

CREATE TABLE IF NOT EXISTS poll_vote_count_shards (
    poll_id     TEXT    NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    option_id   TEXT    NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
    shard       INTEGER NOT NULL,
    count_value BIGINT  NOT NULL DEFAULT 0,
    updated_at  BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (option_id, shard)
);
CREATE INDEX IF NOT EXISTS idx_poll_vote_count_shards_poll
    ON poll_vote_count_shards(poll_id, option_id);

INSERT INTO post_reaction_count_shards (post_id, shard, count_value, updated_at)
SELECT post_id, shard, COUNT(*), COALESCE(MAX(ts), 0)
  FROM (
        SELECT post_id,
               ts,
               (COALESCE((
                  SELECT SUM(ascii(substr(pr.user_id, i, 1)))
                    FROM generate_series(1, length(pr.user_id)) AS i
                ), 0)::BIGINT % 64)::INTEGER AS shard
          FROM post_reactions pr
       ) seeded
 GROUP BY post_id, shard
ON CONFLICT (post_id, shard)
DO UPDATE SET count_value=EXCLUDED.count_value,
              updated_at=EXCLUDED.updated_at;

INSERT INTO poll_vote_count_shards (poll_id, option_id, shard, count_value, updated_at)
SELECT poll_id, option_id, shard, COUNT(*), COALESCE(MAX(ts), 0)
  FROM (
        SELECT poll_id,
               option_id,
               ts,
               (COALESCE((
                  SELECT SUM(ascii(substr(pv.user_id, i, 1)))
                    FROM generate_series(1, length(pv.user_id)) AS i
                ), 0)::BIGINT % 64)::INTEGER AS shard
          FROM poll_votes pv
       ) seeded
 GROUP BY poll_id, option_id, shard
ON CONFLICT (option_id, shard)
DO UPDATE SET count_value=EXCLUDED.count_value,
              updated_at=EXCLUDED.updated_at;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (64, 'postgres-counter-shards', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 65,
			Name:    "postgres-latest-feed-posts",
			SQL: `
CREATE TABLE IF NOT EXISTS latest_feed_posts (
    post_id     TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    thread_id   TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    created_seq BIGINT NOT NULL DEFAULT 0,
    updated_seq BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_created
    ON latest_feed_posts(created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_board_created
    ON latest_feed_posts(board_id, created_seq DESC, post_id);
CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_thread
    ON latest_feed_posts(thread_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (65, 'postgres-latest-feed-posts', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 66,
			Name:    "postgres-digest-entry-removals",
			SQL: `
CREATE TABLE IF NOT EXISTS digest_entry_removals (
    id         TEXT PRIMARY KEY,
    board_id   TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT '',
    removed_by TEXT NOT NULL DEFAULT '',
    removed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_digest_entry_removals_board_kind
    ON digest_entry_removals(board_id, kind, removed_at DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (66, 'postgres-digest-entry-removals', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 67,
			Name:    "postgres-digest-path-mutations",
			SQL: `
CREATE TABLE IF NOT EXISTS digest_path_mutations (
    event_id  TEXT PRIMARY KEY,
    action    TEXT NOT NULL,
    board_id  TEXT NOT NULL,
    kind      TEXT NOT NULL DEFAULT '',
    from_path TEXT NOT NULL DEFAULT '',
    to_path   TEXT NOT NULL DEFAULT '',
    actor_id  TEXT NOT NULL DEFAULT '',
    ts        BIGINT NOT NULL DEFAULT 0,
    count     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_digest_path_mutations_board_kind
    ON digest_path_mutations(board_id, kind, action, ts DESC);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (67, 'postgres-digest-path-mutations', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 68,
			Name:    "postgres-mail-group-deletions",
			SQL: `
CREATE TABLE IF NOT EXISTS mail_group_deletions (
    event_id   TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL,
    group_id   TEXT NOT NULL,
    deleted_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mail_group_deletions_owner_group
    ON mail_group_deletions(owner_id, group_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (68, 'postgres-mail-group-deletions', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 69,
			Name:    "postgres-command-log-partition-offsets",
			SQL: `
CREATE TABLE IF NOT EXISTS command_log_partition_offsets (
    partition_kind   TEXT NOT NULL DEFAULT 'global',
    partition_key    TEXT NOT NULL DEFAULT 'global',
    tail_offset      BIGINT NOT NULL DEFAULT 0,
    committed_offset BIGINT NOT NULL DEFAULT 0,
    updated_at       BIGINT NOT NULL,
    PRIMARY KEY (partition_kind, partition_key)
);
CREATE INDEX IF NOT EXISTS idx_command_log_partition_offsets_tail
    ON command_log_partition_offsets(tail_offset DESC, partition_kind, partition_key);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (69, 'postgres-command-log-partition-offsets', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 70,
			Name:    "postgres-event-scalar-offsets",
			SQL: `
CREATE TABLE IF NOT EXISTS event_scalar_offsets (
    id       TEXT PRIMARY KEY,
    last_seq BIGINT NOT NULL DEFAULT 0
);
INSERT INTO event_scalar_offsets (id, last_seq)
   SELECT 'broker_event_log', COALESCE(MAX(seq), 0) FROM events
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (70, 'postgres-event-scalar-offsets', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 71,
			Name:    "postgres-captcha-challenges",
			SQL: `
CREATE TABLE IF NOT EXISTS captcha_challenges (
    id          TEXT PRIMARY KEY,
    answer_hash TEXT NOT NULL,
    created_at  BIGINT NOT NULL DEFAULT 0,
    expires_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expires ON captcha_challenges(expires_at);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (71, 'postgres-captcha-challenges', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 72,
			Name:    "postgres-email-verification",
			SQL: `
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS email_verification_tokens (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0,
    expires_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_user ON email_verification_tokens(user_id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (72, 'postgres-email-verification', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 73,
			Name:    "postgres-registration-policy-acceptance",
			SQL: `
ALTER TABLE user_private_profiles ADD COLUMN IF NOT EXISTS policy_accepted_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE user_private_profiles ADD COLUMN IF NOT EXISTS policy_version TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (73, 'postgres-registration-policy-acceptance', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 74,
			Name:    "postgres-staff-2fa",
			SQL: `
CREATE TABLE IF NOT EXISTS security_settings (
    id                 TEXT PRIMARY KEY DEFAULT 'default',
    staff_2fa_required INTEGER NOT NULL DEFAULT 0,
    updated_at         BIGINT NOT NULL DEFAULT 0
);
INSERT INTO security_settings (id, staff_2fa_required, updated_at)
VALUES ('default', 0, 0) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS user_2fa_settings (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    totp_secret    TEXT NOT NULL DEFAULT '',
    totp_pending   TEXT NOT NULL DEFAULT '',
    totp_enrolled  INTEGER NOT NULL DEFAULT 0,
    email_enrolled INTEGER NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS two_factor_email_codes (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT 0,
    expires_at BIGINT NOT NULL DEFAULT 0
);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (74, 'postgres-staff-2fa', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 75,
			Name:    "postgres-board-automod-rules",
			SQL: `
CREATE TABLE IF NOT EXISTS board_automod_rules (
    id           TEXT PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    enabled      INTEGER NOT NULL DEFAULT 1,
    priority     INTEGER NOT NULL DEFAULT 0,
    match_type   TEXT NOT NULL,
    pattern      TEXT NOT NULL DEFAULT '',
    threshold    INTEGER NOT NULL DEFAULT 0,
    window_sec   INTEGER NOT NULL DEFAULT 0,
    action       TEXT NOT NULL,
    duration_sec BIGINT NOT NULL DEFAULT 0,
    reason       TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL DEFAULT 0,
    updated_by   TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_board_automod_rules_board ON board_automod_rules(board_id, enabled, priority, id);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (75, 'postgres-board-automod-rules', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 76,
			Name:    "postgres-automod-audit-log",
			SQL: `
CREATE TABLE IF NOT EXISTS automod_audit_log (
    id             TEXT PRIMARY KEY,
    board_id       TEXT NOT NULL,
    rule_id        TEXT NOT NULL DEFAULT '',
    match_type     TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL DEFAULT '',
    target_user_id TEXT NOT NULL DEFAULT '',
    post_id        TEXT NOT NULL DEFAULT '',
    thread_id      TEXT NOT NULL DEFAULT '',
    reason         TEXT NOT NULL DEFAULT '',
    ts             BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_automod_audit_board ON automod_audit_log(board_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts(author_id, created_at);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (76, 'postgres-automod-audit-log', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 77,
			Name:    "postgres-2fa-backup-codes",
			SQL: `
CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used       INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_2fa_backup_user ON two_factor_backup_codes(user_id, used);

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (77, 'postgres-2fa-backup-codes', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
		{
			Version: 78,
			Name:    "postgres-site-appearance",
			SQL: `
CREATE TABLE IF NOT EXISTS site_appearance_settings (
    id             TEXT PRIMARY KEY DEFAULT 'default',
    site_title     TEXT NOT NULL DEFAULT 'Budgie BBS',
    tagline        TEXT NOT NULL DEFAULT '',
    banner_message TEXT NOT NULL DEFAULT '',
    accent_color   TEXT NOT NULL DEFAULT '',
    default_theme  TEXT NOT NULL DEFAULT 'dark',
    updated_at     BIGINT NOT NULL DEFAULT 0
);
INSERT INTO site_appearance_settings (id, updated_at) VALUES ('default', 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version, name, applied_at)
VALUES (78, 'postgres-site-appearance', 0)
ON CONFLICT (version) DO NOTHING;
`,
		},
	}
}
