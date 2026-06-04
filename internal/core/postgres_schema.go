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
    signature    TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'markup',
    reply_to     TEXT,
    version      INTEGER NOT NULL DEFAULT 1,
    redacted     BOOLEAN NOT NULL DEFAULT FALSE,
    marked       BOOLEAN NOT NULL DEFAULT FALSE,
    recommended  BOOLEAN NOT NULL DEFAULT FALSE,
    no_reply     BOOLEAN NOT NULL DEFAULT FALSE,
    tex          BOOLEAN NOT NULL DEFAULT FALSE,
    mail_back    BOOLEAN NOT NULL DEFAULT FALSE,
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
    ADD COLUMN IF NOT EXISTS marked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS recommended BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS no_reply BOOLEAN NOT NULL DEFAULT FALSE;

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
    ADD COLUMN IF NOT EXISTS tex BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS mail_back BOOLEAN NOT NULL DEFAULT FALSE;

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
	}
}
