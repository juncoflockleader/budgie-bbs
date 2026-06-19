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
		{"posts", "signature", "signature TEXT NOT NULL DEFAULT ''"},
		{"posts", "marked", "marked INTEGER NOT NULL DEFAULT 0"},
		{"posts", "recommended", "recommended INTEGER NOT NULL DEFAULT 0"},
		{"posts", "no_reply", "no_reply INTEGER NOT NULL DEFAULT 0"},
		{"posts", "tex", "tex INTEGER NOT NULL DEFAULT 0"},
		{"posts", "mail_back", "mail_back INTEGER NOT NULL DEFAULT 0"},
		{"posts", "source_post", "source_post TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_thread", "source_thread TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_board", "source_board TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_author", "source_author TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_author_id", "source_author_id TEXT NOT NULL DEFAULT ''"},
		{"posts", "source_title", "source_title TEXT NOT NULL DEFAULT ''"},
		{"posts", "created_at", "created_at INTEGER NOT NULL DEFAULT 0"},
		{"posts", "updated_at", "updated_at INTEGER NOT NULL DEFAULT 0"},
		{"user_profiles", "title", "title TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "signature", "signature TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "plan", "plan TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "homepage", "homepage TEXT NOT NULL DEFAULT ''"},
		{"processed_commands", "actor_id", "actor_id TEXT NOT NULL DEFAULT ''"},
		{"processed_commands", "command_hash", "command_hash TEXT NOT NULL DEFAULT ''"},
		{"users", "deactivated_at", "deactivated_at INTEGER NOT NULL DEFAULT 0"},
		{"users", "password_changed_at", "password_changed_at INTEGER NOT NULL DEFAULT 0"},
		{"users", "sessions_valid_after", "sessions_valid_after INTEGER NOT NULL DEFAULT 0"},
		{"users", "deactivated_by", "deactivated_by TEXT NOT NULL DEFAULT ''"},
		{"users", "deactivated_reason", "deactivated_reason TEXT NOT NULL DEFAULT ''"},
		{"users", "registration_status", "registration_status TEXT NOT NULL DEFAULT 'approved'"},
		{"users", "reviewed_at", "reviewed_at INTEGER NOT NULL DEFAULT 0"},
		{"users", "reviewed_by", "reviewed_by TEXT NOT NULL DEFAULT ''"},
		{"users", "review_reason", "review_reason TEXT NOT NULL DEFAULT ''"},
		{"users", "email_verified", "email_verified INTEGER NOT NULL DEFAULT 1"},
		{"users", "email_verified_at", "email_verified_at INTEGER NOT NULL DEFAULT 0"},
		{"board_favorites", "folder_id", "folder_id TEXT NOT NULL DEFAULT ''"},
		{"user_activity", "login_count", "login_count INTEGER NOT NULL DEFAULT 0"},
		{"user_activity", "total_online_seconds", "total_online_seconds INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_logins", "total_logins INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_online_seconds", "total_online_seconds INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "online_guests", "online_guests INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "max_online_guests", "max_online_guests INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "max_online_guests_at", "max_online_guests_at INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_logouts", "total_logouts INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_web_logins", "total_web_logins INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_web_logouts", "total_web_logouts INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_guest_logins", "total_guest_logins INTEGER NOT NULL DEFAULT 0"},
		{"community_stat_history", "total_guest_logouts", "total_guest_logouts INTEGER NOT NULL DEFAULT 0"},
		{"events", "partition_kind", "partition_kind TEXT NOT NULL DEFAULT 'global'"},
		{"events", "partition_key", "partition_key TEXT NOT NULL DEFAULT 'global'"},
		{"events", "partition_offset", "partition_offset INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_score", "min_score INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_post_count", "min_board_post_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_original_post_count", "min_board_original_post_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_digest_count", "min_board_digest_count INTEGER NOT NULL DEFAULT 0"},
		{"board_member_requirements", "min_board_mark_count", "min_board_mark_count INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_manage_members", "can_manage_members INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_curate", "can_curate INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_moderate_posts", "can_moderate_posts INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_moderate_threads", "can_moderate_threads INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_announce", "can_announce INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_manage_polls", "can_manage_polls INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "can_set_board_settings", "can_set_board_settings INTEGER NOT NULL DEFAULT 0"},
		{"board_members", "position", "position INTEGER NOT NULL DEFAULT 0"},
		{"board_settings", "stats_excluded", "stats_excluded INTEGER NOT NULL DEFAULT 0"},
		{"board_settings", "zap_allowed", "zap_allowed INTEGER NOT NULL DEFAULT 1"},
		{"board_settings", "guest_access", "guest_access TEXT NOT NULL DEFAULT ''"},
		{"digest_entries", "body", "body TEXT NOT NULL DEFAULT ''"},
		{"digest_entries", "body_edited", "body_edited INTEGER NOT NULL DEFAULT 0"},
		{"user_presence", "mode", "mode TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "board_id", "board_id TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "thread_id", "thread_id TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "location_label", "location_label TEXT NOT NULL DEFAULT ''"},
		{"user_presence", "from_host", "from_host TEXT NOT NULL DEFAULT ''"},
		{"user_private_profiles", "policy_accepted_at", "policy_accepted_at INTEGER NOT NULL DEFAULT 0"},
		{"user_private_profiles", "policy_version", "policy_version TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.ddl); err != nil {
			return err
		}
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_board_last_seen ON user_presence(board_id, last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence board index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS processed_commands_v2 (
		    partition_kind TEXT    NOT NULL DEFAULT 'global',
		    partition_key  TEXT    NOT NULL DEFAULT 'global',
		    actor_id       TEXT    NOT NULL DEFAULT '',
		    cid            TEXT    NOT NULL,
		    command_hash   TEXT    NOT NULL DEFAULT '',
		    result_json    TEXT    NOT NULL,
		    processed_at   INTEGER NOT NULL,
		    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
		)`); err != nil {
		return fmt.Errorf("ensure processed commands v2 table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_processed_commands_v2_actor_cid
		    ON processed_commands_v2(actor_id, cid)`); err != nil {
		return fmt.Errorf("ensure processed commands v2 actor index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS command_log_receipts (
		    partition_kind TEXT    NOT NULL DEFAULT 'global',
		    partition_key  TEXT    NOT NULL DEFAULT 'global',
		    actor_id       TEXT    NOT NULL DEFAULT '',
		    cid            TEXT    NOT NULL,
		    command_offset INTEGER NOT NULL DEFAULT 0,
		    status         TEXT    NOT NULL DEFAULT '',
		    error_json     TEXT    NOT NULL DEFAULT '',
		    updated_at     INTEGER NOT NULL,
		    PRIMARY KEY (partition_kind, partition_key, actor_id, cid)
		)`); err != nil {
		return fmt.Errorf("ensure command log receipts table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_command_log_receipts_actor_cid
		    ON command_log_receipts(actor_id, cid)`); err != nil {
		return fmt.Errorf("ensure command log receipts actor index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_command_log_receipts_partition_offset
		    ON command_log_receipts(partition_kind, partition_key, command_offset)`); err != nil {
		return fmt.Errorf("ensure command log receipts partition offset index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS digest_entry_removals (
		    id         TEXT    PRIMARY KEY,
		    board_id   TEXT    NOT NULL,
		    kind       TEXT    NOT NULL DEFAULT '',
		    removed_by TEXT    NOT NULL DEFAULT '',
		    removed_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure digest entry removals table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_digest_entry_removals_board_kind
		    ON digest_entry_removals(board_id, kind, removed_at DESC)`); err != nil {
		return fmt.Errorf("ensure digest entry removals board kind index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS digest_path_mutations (
		    event_id  TEXT    PRIMARY KEY,
		    action    TEXT    NOT NULL,
		    board_id  TEXT    NOT NULL,
		    kind      TEXT    NOT NULL DEFAULT '',
		    from_path TEXT    NOT NULL DEFAULT '',
		    to_path   TEXT    NOT NULL DEFAULT '',
		    actor_id  TEXT    NOT NULL DEFAULT '',
		    ts        INTEGER NOT NULL DEFAULT 0,
		    count     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure digest path mutations table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_digest_path_mutations_board_kind
		    ON digest_path_mutations(board_id, kind, action, ts DESC)`); err != nil {
		return fmt.Errorf("ensure digest path mutations board kind index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS mail_group_deletions (
		    event_id   TEXT    PRIMARY KEY,
		    owner_id   TEXT    NOT NULL,
		    group_id   TEXT    NOT NULL,
		    deleted_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure mail group deletions table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_mail_group_deletions_owner_group
		    ON mail_group_deletions(owner_id, group_id)`); err != nil {
		return fmt.Errorf("ensure mail group deletions owner group index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS attachment_blob_staging (
		    id           TEXT    PRIMARY KEY,
		    kind         TEXT    NOT NULL,
		    data         BLOB    NOT NULL,
		    content_type TEXT    NOT NULL DEFAULT '',
		    size_bytes   INTEGER NOT NULL DEFAULT 0,
		    actor_id     TEXT    NOT NULL DEFAULT '',
		    created_at   INTEGER NOT NULL DEFAULT 0,
		    expires_at   INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure attachment blob staging table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_attachment_blob_staging_expiry
		    ON attachment_blob_staging(expires_at, kind)`); err != nil {
		return fmt.Errorf("ensure attachment blob staging expiry index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS post_reaction_count_shards (
		    post_id     TEXT    NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
		    shard       INTEGER NOT NULL,
		    count_value INTEGER NOT NULL DEFAULT 0,
		    updated_at  INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (post_id, shard)
		)`); err != nil {
		return fmt.Errorf("ensure post reaction count shards table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_post_reaction_count_shards_post
		    ON post_reaction_count_shards(post_id)`); err != nil {
		return fmt.Errorf("ensure post reaction count shards post index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS poll_vote_count_shards (
		    poll_id     TEXT    NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
		    option_id   TEXT    NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
		    shard       INTEGER NOT NULL,
		    count_value INTEGER NOT NULL DEFAULT 0,
		    updated_at  INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (option_id, shard)
		)`); err != nil {
		return fmt.Errorf("ensure poll vote count shards table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_poll_vote_count_shards_poll
		    ON poll_vote_count_shards(poll_id, option_id)`); err != nil {
		return fmt.Errorf("ensure poll vote count shards poll index: %w", err)
	}
	if err := seedSQLiteCounterShards(db); err != nil {
		return err
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS counter_checkpoints (
		    counter_kind    TEXT    NOT NULL,
		    target_id       TEXT    NOT NULL,
		    parent_id       TEXT    NOT NULL DEFAULT '',
		    count           INTEGER NOT NULL DEFAULT 0,
		    source_head_seq INTEGER NOT NULL DEFAULT 0,
		    checkpoint_seq  INTEGER NOT NULL DEFAULT 0,
		    checkpointed_at INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (counter_kind, target_id)
		)`); err != nil {
		return fmt.Errorf("ensure counter checkpoints table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_counter_checkpoints_parent
		    ON counter_checkpoints(counter_kind, parent_id)`); err != nil {
		return fmt.Errorf("ensure counter checkpoints parent index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS command_partition_leases (
		    partition_kind TEXT    NOT NULL,
		    partition_key  TEXT    NOT NULL,
		    owner_id       TEXT    NOT NULL,
		    claimed_at     INTEGER NOT NULL,
		    expires_at     INTEGER NOT NULL,
		    PRIMARY KEY (partition_kind, partition_key)
		)`); err != nil {
		return fmt.Errorf("ensure command partition leases table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_command_partition_leases_expires
		    ON command_partition_leases(expires_at)`); err != nil {
		return fmt.Errorf("ensure command partition leases expiry index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS hot_thread_splits (
		    thread_id  TEXT    PRIMARY KEY,
		    shards     INTEGER NOT NULL,
		    updated_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("ensure hot thread splits table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS derived_view_watermarks (
		    view_name   TEXT    PRIMARY KEY,
		    applied_seq INTEGER NOT NULL DEFAULT 0,
		    updated_at  INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure derived view watermarks table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS resident_feed_posts (
		    post_id     TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
		    thread_id   TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    created_seq INTEGER NOT NULL DEFAULT 0,
		    updated_seq INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure resident feed posts table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_created ON resident_feed_posts(created_seq DESC, post_id)`); err != nil {
		return fmt.Errorf("ensure resident feed created index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_board_created ON resident_feed_posts(board_id, created_seq DESC, post_id)`); err != nil {
		return fmt.Errorf("ensure resident feed board index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_resident_feed_posts_thread ON resident_feed_posts(thread_id)`); err != nil {
		return fmt.Errorf("ensure resident feed thread index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS latest_feed_posts (
		    post_id     TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
		    thread_id   TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		    board_id    TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    created_seq INTEGER NOT NULL DEFAULT 0,
		    updated_seq INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure latest feed posts table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_created ON latest_feed_posts(created_seq DESC, post_id)`); err != nil {
		return fmt.Errorf("ensure latest feed created index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_board_created ON latest_feed_posts(board_id, created_seq DESC, post_id)`); err != nil {
		return fmt.Errorf("ensure latest feed board index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_latest_feed_posts_thread ON latest_feed_posts(thread_id)`); err != nil {
		return fmt.Errorf("ensure latest feed thread index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS board_ranking_stats (
		    board_id        TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
		    thread_count    INTEGER NOT NULL DEFAULT 0,
		    post_count      INTEGER NOT NULL DEFAULT 0,
		    last_seq        INTEGER NOT NULL DEFAULT 0,
		    last_post_at    INTEGER NOT NULL DEFAULT 0,
		    moderator_count INTEGER NOT NULL DEFAULT 0,
		    updated_at      INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure board ranking stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_ranking_stats_order
		    ON board_ranking_stats(post_count DESC, thread_count DESC, last_seq DESC, board_id)`); err != nil {
		return fmt.Errorf("ensure board ranking stats order index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS thread_ranking_stats (
		    thread_id         TEXT    PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
		    post_count        INTEGER NOT NULL DEFAULT 0,
		    participant_count INTEGER NOT NULL DEFAULT 0,
		    reaction_count    INTEGER NOT NULL DEFAULT 0,
		    last_seq          INTEGER NOT NULL DEFAULT 0,
		    updated_at        INTEGER NOT NULL DEFAULT 0,
		    refreshed_at      INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure thread ranking stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_thread_ranking_stats_order
		    ON thread_ranking_stats(last_seq DESC, thread_id)`); err != nil {
		return fmt.Errorf("ensure thread ranking stats order index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS reply_ranking_posts (
		    post_id       TEXT    PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
		    thread_id     TEXT    NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		    created_seq   INTEGER NOT NULL DEFAULT 0,
		    created_at    INTEGER NOT NULL DEFAULT 0,
		    refreshed_at  INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure reply ranking posts table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_reply_ranking_posts_created
		    ON reply_ranking_posts(created_seq DESC, post_id)`); err != nil {
		return fmt.Errorf("ensure reply ranking posts created index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_ranking_stats (
		    user_id              TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    posts_created        INTEGER NOT NULL DEFAULT 0,
		    reactions_received   INTEGER NOT NULL DEFAULT 0,
		    login_count          INTEGER NOT NULL DEFAULT 0,
		    total_online_seconds INTEGER NOT NULL DEFAULT 0,
		    trust_level          INTEGER NOT NULL DEFAULT 0,
		    refreshed_at         INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user ranking stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_ranking_stats_order
		    ON user_ranking_stats(posts_created DESC, reactions_received DESC, login_count DESC, total_online_seconds DESC, user_id)`); err != nil {
		return fmt.Errorf("ensure user ranking stats order index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS blessing_ranking_stats (
		    user_id         TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    blessing_count  INTEGER NOT NULL DEFAULT 0,
		    last_blessed_at INTEGER NOT NULL DEFAULT 0,
		    refreshed_at    INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure blessing ranking stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_blessing_ranking_stats_order
		    ON blessing_ranking_stats(blessing_count DESC, last_blessed_at DESC, user_id)`); err != nil {
		return fmt.Errorf("ensure blessing ranking stats order index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS archive_ranking_stats (
		    board_id        TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    kind            TEXT    NOT NULL DEFAULT 'archive',
		    path            TEXT    NOT NULL DEFAULT '',
		    entry_count     INTEGER NOT NULL DEFAULT 0,
		    edited_count    INTEGER NOT NULL DEFAULT 0,
		    last_updated_at INTEGER NOT NULL DEFAULT 0,
		    refreshed_at    INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (board_id, kind, path)
		)`); err != nil {
		return fmt.Errorf("ensure archive ranking stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_archive_ranking_stats_order
		    ON archive_ranking_stats(kind, entry_count DESC, edited_count DESC, last_updated_at DESC, board_id, path)`); err != nil {
		return fmt.Errorf("ensure archive ranking stats order index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS board_summary_stats (
		    board_id        TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
		    thread_count    INTEGER NOT NULL DEFAULT 0,
		    post_count      INTEGER NOT NULL DEFAULT 0,
		    last_seq        INTEGER NOT NULL DEFAULT 0,
		    created_at      INTEGER NOT NULL DEFAULT 0,
		    moderator_count INTEGER NOT NULL DEFAULT 0,
		    refreshed_at    INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure board summary stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_summary_stats_activity
		    ON board_summary_stats(last_seq DESC, post_count DESC, thread_count DESC, board_id)`); err != nil {
		return fmt.Errorf("ensure board summary stats activity index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS unread_thread_summary_stats (
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
		)`); err != nil {
		return fmt.Errorf("ensure unread thread summary stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_board_last
		    ON unread_thread_summary_stats(board_id, last_seq DESC, thread_id)`); err != nil {
		return fmt.Errorf("ensure unread thread summary stats board index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_unread_thread_summary_stats_last
		    ON unread_thread_summary_stats(last_seq DESC, thread_id)`); err != nil {
		return fmt.Errorf("ensure unread thread summary stats last index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_posts_thread_created_seq
		    ON posts(thread, created_seq, redacted, id)`); err != nil {
		return fmt.Errorf("ensure posts thread created seq index: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO processed_commands_v2 (
		    partition_kind, partition_key, actor_id, cid, command_hash, result_json, processed_at
		)
		 SELECT 'global', 'global', actor_id, cid, command_hash, result_json, processed_at
		   FROM processed_commands`); err != nil {
		return fmt.Errorf("seed processed commands v2: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_events_partition_offset ON events(partition_kind, partition_key, partition_offset)`); err != nil {
		return fmt.Errorf("ensure events partition index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS event_partition_offsets (
		    partition_kind TEXT    NOT NULL,
		    partition_key  TEXT    NOT NULL,
		    last_offset    INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (partition_kind, partition_key)
		)`); err != nil {
		return fmt.Errorf("ensure event partition offsets table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS event_scalar_offsets (
		    id       TEXT    PRIMARY KEY,
		    last_seq INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure event scalar offsets table: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO event_scalar_offsets (id, last_seq)
		   SELECT 'broker_event_log', COALESCE(MAX(seq), 0) FROM events`); err != nil {
		return fmt.Errorf("seed event scalar offsets: %w", err)
	}
	if _, err := qExec(db, `UPDATE events SET partition_offset=seq WHERE partition_offset=0`); err != nil {
		return fmt.Errorf("backfill event partition offsets: %w", err)
	}
	if _, err := qExec(db, `INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
		   SELECT partition_kind, partition_key, MAX(partition_offset)
		     FROM events
		    GROUP BY partition_kind, partition_key
		ON CONFLICT (partition_kind, partition_key) DO UPDATE
		      SET last_offset=MAX(event_partition_offsets.last_offset, excluded.last_offset)`); err != nil {
		return fmt.Errorf("seed event partition offsets: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_members_board_position ON board_members(board_id, position, user_id)`); err != nil {
		return fmt.Errorf("ensure board member position index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS board_moderator_terms (
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
		)`); err != nil {
		return fmt.Errorf("ensure board moderator terms table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_board_time ON board_moderator_terms(board_id, ended_at, started_at DESC)`); err != nil {
		return fmt.Errorf("ensure board moderator terms board index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_moderator_terms_user_time ON board_moderator_terms(user_id, started_at DESC)`); err != nil {
		return fmt.Errorf("ensure board moderator terms user index: %w", err)
	}
	if _, err := qExec(db,
		`INSERT OR IGNORE INTO board_moderator_terms (
		    board_id, user_id, started_at, ended_at, appointed_by, removed_by,
		    position, created_at, updated_at
		)
		 SELECT board_id, user_id,
		        CASE WHEN created_at > 0 THEN created_at ELSE updated_at END,
		        0, '', '', position, created_at, updated_at
		   FROM board_moderators`,
	); err != nil {
		return fmt.Errorf("seed board moderator terms: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS board_zaps (
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (user_id, board_id)
		)`); err != nil {
		return fmt.Errorf("ensure board zaps table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_zaps_user ON board_zaps(user_id, board_id)`); err != nil {
		return fmt.Errorf("ensure board zaps index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_presence_sessions (
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
		)`); err != nil {
		return fmt.Errorf("ensure user presence sessions table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_last_seen ON user_presence_sessions(last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence sessions last_seen index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_presence_sessions_board_last_seen ON user_presence_sessions(board_id, last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure user presence sessions board index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS chat_rooms (
		    id         TEXT    PRIMARY KEY,
		    name       TEXT    NOT NULL,
		    topic      TEXT    NOT NULL DEFAULT '',
		    created_by TEXT    NOT NULL DEFAULT '',
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure chat rooms table: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO chat_rooms (id, name, topic, created_by, created_at, updated_at)
		VALUES ('lobby', 'Lobby', 'Campus lobby chat', '', 0, 0)`); err != nil {
		return fmt.Errorf("ensure lobby chat room: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS chat_lines (
		    id         TEXT    PRIMARY KEY,
		    room_id    TEXT    NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    user_name  TEXT    NOT NULL,
		    body       TEXT    NOT NULL,
		    created_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure chat lines table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_chat_lines_room_created ON chat_lines(room_id, created_at DESC, id DESC)`); err != nil {
		return fmt.Errorf("ensure chat lines room index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS guest_presence_sessions (
		    session_id     TEXT    PRIMARY KEY,
		    status         TEXT    NOT NULL DEFAULT 'active',
		    location_label TEXT    NOT NULL DEFAULT '',
		    from_host      TEXT    NOT NULL DEFAULT '',
		    last_seen      INTEGER NOT NULL DEFAULT 0,
		    updated_at     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure guest presence sessions table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_guest_presence_sessions_last_seen ON guest_presence_sessions(last_seen DESC)`); err != nil {
		return fmt.Errorf("ensure guest presence sessions last_seen index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS community_stats_snapshot (
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
		)`); err != nil {
		return fmt.Errorf("ensure community stats snapshot table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS community_counter_totals (
		    id                    TEXT    PRIMARY KEY DEFAULT 'default',
		    total_logouts         INTEGER NOT NULL DEFAULT 0,
		    total_web_logins      INTEGER NOT NULL DEFAULT 0,
		    total_web_logouts     INTEGER NOT NULL DEFAULT 0,
		    total_guest_logins    INTEGER NOT NULL DEFAULT 0,
		    total_guest_logouts   INTEGER NOT NULL DEFAULT 0,
		    updated_at            INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure community counter totals table: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO community_counter_totals (id) VALUES ('default')`); err != nil {
		return fmt.Errorf("ensure community counter totals row: %w", err)
	}
	if _, err := qExec(db,
		`UPDATE community_counter_totals
		    SET total_web_logins=(SELECT COALESCE(SUM(login_count), 0) FROM user_activity)
		  WHERE id='default' AND total_web_logins=0`,
	); err != nil {
		return fmt.Errorf("seed community web login counter: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS community_stat_history (
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
		)`); err != nil {
		return fmt.Errorf("ensure community stat history table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_community_stat_history_snapshot ON community_stat_history(snapshot_at DESC, day DESC)`); err != nil {
		return fmt.Errorf("ensure community stat history index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS login_hourly_stats (
		    day         TEXT    NOT NULL,
		    hour        INTEGER NOT NULL CHECK(hour >= 0 AND hour <= 23),
		    login_count INTEGER NOT NULL DEFAULT 0,
		    updated_at  INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (day, hour)
		)`); err != nil {
		return fmt.Errorf("ensure login hourly stats table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_login_hourly_stats_updated ON login_hourly_stats(updated_at DESC, day DESC, hour)`); err != nil {
		return fmt.Errorf("ensure login hourly stats index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS content_filters (
		    id         TEXT PRIMARY KEY,
		    pattern    TEXT NOT NULL DEFAULT '',
		    scope      TEXT NOT NULL DEFAULT 'global',
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_by TEXT NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure content filters table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_content_filters_active_scope ON content_filters(active, scope, updated_at DESC)`); err != nil {
		return fmt.Errorf("ensure content filters index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS account_registration_settings (
		    id               TEXT PRIMARY KEY DEFAULT 'default',
		    require_approval INTEGER NOT NULL DEFAULT 0,
		    updated_at       INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure account registration settings table: %w", err)
	}
	if _, err := qExec(db,
		`INSERT OR IGNORE INTO account_registration_settings (id, require_approval, updated_at)
		 VALUES ('default', 0, 0)`,
	); err != nil {
		return fmt.Errorf("ensure account registration settings row: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS password_recovery_requests (
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
		)`); err != nil {
		return fmt.Errorf("ensure password recovery requests table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_password_recovery_status_updated ON password_recovery_requests(status, updated_at DESC, created_at DESC)`); err != nil {
		return fmt.Errorf("ensure password recovery index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS digest_directories (
		    id         TEXT    PRIMARY KEY,
		    board_id   TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    kind       TEXT    NOT NULL DEFAULT 'archive',
		    path       TEXT    NOT NULL DEFAULT '',
		    created_by TEXT    NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0,
		    UNIQUE(board_id, kind, path)
		)`); err != nil {
		return fmt.Errorf("ensure digest directories table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_digest_directories_board_kind_path ON digest_directories(board_id, kind, path)`); err != nil {
		return fmt.Errorf("ensure digest directories index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_signatures (
		    id         TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    label      TEXT    NOT NULL DEFAULT '',
		    body       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user signatures table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_signatures_user_position ON user_signatures(user_id, position, updated_at, id)`); err != nil {
		return fmt.Errorf("ensure user signatures index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_signature_settings (
		    user_id               TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    selected_signature_id TEXT    NOT NULL DEFAULT '',
		    random_enabled        INTEGER NOT NULL DEFAULT 0,
		    updated_at            INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user signature settings table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_login_acl_settings (
		    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    enabled    INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user login acl settings table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_login_acl_rules (
		    id         TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    pattern    TEXT    NOT NULL DEFAULT '',
		    note       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    active     INTEGER NOT NULL DEFAULT 1,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user login acl rules table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_user_login_acl_rules_user_position ON user_login_acl_rules(user_id, position, updated_at, id)`); err != nil {
		return fmt.Errorf("ensure user login acl rules index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS blessings (
		    id           TEXT    PRIMARY KEY,
		    from_user_id TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    to_user_id   TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    message      TEXT    NOT NULL DEFAULT '',
		    created_at   INTEGER NOT NULL DEFAULT 0,
		    seq          INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure blessings table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_blessings_to_created ON blessings(to_user_id, created_at DESC, seq DESC)`); err != nil {
		return fmt.Errorf("ensure blessings target index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_blessings_from_created ON blessings(from_user_id, created_at DESC, seq DESC)`); err != nil {
		return fmt.Errorf("ensure blessings sender index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS recommended_boards (
		    board_id   TEXT    PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
		    note       TEXT    NOT NULL DEFAULT '',
		    position   INTEGER NOT NULL DEFAULT 0,
		    curated_by TEXT    NOT NULL REFERENCES users(id),
		    created_at INTEGER NOT NULL DEFAULT 0,
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure recommended boards table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_recommended_boards_position ON recommended_boards(position, updated_at DESC, board_id)`); err != nil {
		return fmt.Errorf("ensure recommended boards index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS captcha_challenges (
		    id          TEXT    PRIMARY KEY,
		    answer_hash TEXT    NOT NULL,
		    created_at  INTEGER NOT NULL DEFAULT 0,
		    expires_at  INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure captcha challenges table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expires ON captcha_challenges(expires_at)`); err != nil {
		return fmt.Errorf("ensure captcha challenges index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS email_verification_tokens (
		    token      TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    email      TEXT    NOT NULL DEFAULT '',
		    created_at INTEGER NOT NULL DEFAULT 0,
		    expires_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure email verification tokens table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_user ON email_verification_tokens(user_id)`); err != nil {
		return fmt.Errorf("ensure email verification tokens index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS security_settings (
		    id                TEXT    PRIMARY KEY DEFAULT 'default',
		    staff_2fa_required INTEGER NOT NULL DEFAULT 0,
		    updated_at        INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure security settings table: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO security_settings (id, staff_2fa_required, updated_at) VALUES ('default', 0, 0)`); err != nil {
		return fmt.Errorf("seed security settings: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS site_appearance_settings (
		    id             TEXT    PRIMARY KEY DEFAULT 'default',
		    site_title     TEXT    NOT NULL DEFAULT 'Budgie BBS',
		    tagline        TEXT    NOT NULL DEFAULT '',
		    banner_message TEXT    NOT NULL DEFAULT '',
		    accent_color   TEXT    NOT NULL DEFAULT '',
		    default_theme  TEXT    NOT NULL DEFAULT 'light',
		    logo           TEXT    NOT NULL DEFAULT '',
		    tui_main_menu_layout TEXT NOT NULL DEFAULT '',
		    updated_at     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure site appearance settings table: %w", err)
	}
	if err := ensureColumn(db, "site_appearance_settings", "logo", "logo TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add site_appearance_settings.logo: %w", err)
	}
	if err := ensureColumn(db, "site_appearance_settings", "tui_main_menu_layout", "tui_main_menu_layout TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add site_appearance_settings.tui_main_menu_layout: %w", err)
	}
	if _, err := qExec(db, `INSERT OR IGNORE INTO site_appearance_settings (id, updated_at) VALUES ('default', 0)`); err != nil {
		return fmt.Errorf("seed site appearance settings: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS mud_players (
		    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    room_id    TEXT    NOT NULL DEFAULT '',
		    updated_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure mud players table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS site_assets (
		    name         TEXT    PRIMARY KEY,
		    content_type TEXT    NOT NULL DEFAULT '',
		    data         BLOB,
		    byte_size    INTEGER NOT NULL DEFAULT 0,
		    updated_at   INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure site assets table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS user_2fa_settings (
		    user_id        TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    totp_secret    TEXT    NOT NULL DEFAULT '',
		    totp_pending   TEXT    NOT NULL DEFAULT '',
		    totp_enrolled  INTEGER NOT NULL DEFAULT 0,
		    email_enrolled INTEGER NOT NULL DEFAULT 0,
		    totp_last_step INTEGER NOT NULL DEFAULT 0,
		    updated_at     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure user 2fa settings table: %w", err)
	}
	// Upgrade path for DBs created before TOTP replay protection existed.
	if err := ensureColumn(db, "user_2fa_settings", "totp_last_step", "totp_last_step INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS two_factor_email_codes (
		    user_id    TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    code_hash  TEXT    NOT NULL,
		    created_at INTEGER NOT NULL DEFAULT 0,
		    expires_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure two factor email codes table: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
		    id         TEXT    PRIMARY KEY,
		    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    code_hash  TEXT    NOT NULL,
		    used       INTEGER NOT NULL DEFAULT 0,
		    created_at INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure two factor backup codes table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_2fa_backup_user ON two_factor_backup_codes(user_id, used)`); err != nil {
		return fmt.Errorf("ensure two factor backup codes index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS board_automod_rules (
		    id           TEXT    PRIMARY KEY,
		    board_id     TEXT    NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
		    enabled      INTEGER NOT NULL DEFAULT 1,
		    priority     INTEGER NOT NULL DEFAULT 0,
		    match_type   TEXT    NOT NULL,
		    pattern      TEXT    NOT NULL DEFAULT '',
		    threshold    INTEGER NOT NULL DEFAULT 0,
		    window_sec   INTEGER NOT NULL DEFAULT 0,
		    action       TEXT    NOT NULL,
		    duration_sec INTEGER NOT NULL DEFAULT 0,
		    reason       TEXT    NOT NULL DEFAULT '',
		    note         TEXT    NOT NULL DEFAULT '',
		    created_by   TEXT    NOT NULL DEFAULT '',
		    created_at   INTEGER NOT NULL DEFAULT 0,
		    updated_by   TEXT    NOT NULL DEFAULT '',
		    updated_at   INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure board automod rules table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_board_automod_rules_board ON board_automod_rules(board_id, enabled, priority, id)`); err != nil {
		return fmt.Errorf("ensure board automod rules index: %w", err)
	}
	if _, err := qExec(db, `CREATE TABLE IF NOT EXISTS automod_audit_log (
		    id             TEXT    PRIMARY KEY,
		    board_id       TEXT    NOT NULL,
		    rule_id        TEXT    NOT NULL DEFAULT '',
		    match_type     TEXT    NOT NULL DEFAULT '',
		    action         TEXT    NOT NULL DEFAULT '',
		    target_user_id TEXT    NOT NULL DEFAULT '',
		    post_id        TEXT    NOT NULL DEFAULT '',
		    thread_id      TEXT    NOT NULL DEFAULT '',
		    reason         TEXT    NOT NULL DEFAULT '',
		    ts             INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("ensure automod audit log table: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_automod_audit_board ON automod_audit_log(board_id, ts DESC)`); err != nil {
		return fmt.Errorf("ensure automod audit log index: %w", err)
	}
	if _, err := qExec(db, `CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts(author_id, created_at)`); err != nil {
		return fmt.Errorf("ensure posts author/created index: %w", err)
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
		`INSERT OR IGNORE INTO user_signatures (id, user_id, label, body, position, active, created_at, updated_at)
		 SELECT 'sig_profile_' || u.id, u.id, 'Profile signature', up.signature, 0, 1, ?, ?
		   FROM users u
		   JOIN user_profiles up ON up.user_id=u.id
		  WHERE TRIM(COALESCE(up.signature,'')) <> ''`,
		`INSERT OR IGNORE INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 SELECT u.id, 'sig_profile_' || u.id, 0, ?
		   FROM users u
		   JOIN user_profiles up ON up.user_id=u.id
		  WHERE TRIM(COALESCE(up.signature,'')) <> ''`,
		`INSERT OR IGNORE INTO user_presence_sessions (
		    user_id, session_id, status, mode, board_id, thread_id,
		    location_label, from_host, last_seen, updated_at
		 )
		 SELECT user_id, 'default', status, mode, board_id, thread_id,
		        location_label, from_host, last_seen, updated_at
		   FROM user_presence`,
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
		{ts, ts},
		{ts},
		{},
		{ts},
	}
	for i, stmt := range updates {
		if _, err := qExec(db, stmt, args[i]...); err != nil {
			return fmt.Errorf("apply sqlite data migration %d: %w", i+1, err)
		}
	}
	return nil
}

func seedSQLiteCounterShards(db *sql.DB) error {
	if err := seedSQLitePostReactionCountShards(db); err != nil {
		return err
	}
	return seedSQLitePollVoteCountShards(db)
}

func seedSQLitePostReactionCountShards(db *sql.DB) error {
	var identityRows, shardRows int
	if err := qQueryRow(db, `SELECT COUNT(*) FROM post_reactions`).Scan(&identityRows); err != nil {
		return fmt.Errorf("count post reactions for shard seed: %w", err)
	}
	if identityRows == 0 {
		return nil
	}
	if err := qQueryRow(db, `SELECT COUNT(*) FROM post_reaction_count_shards`).Scan(&shardRows); err != nil {
		return fmt.Errorf("count post reaction shards for seed: %w", err)
	}
	if shardRows > 0 {
		return nil
	}
	rows, err := qQuery(db, `SELECT post_id, user_id, ts FROM post_reactions ORDER BY post_id, user_id`)
	if err != nil {
		return fmt.Errorf("list post reactions for shard seed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var postID, userID string
		var ts int64
		if err := rows.Scan(&postID, &userID, &ts); err != nil {
			return fmt.Errorf("scan post reaction shard seed: %w", err)
		}
		if _, err := qExec(db,
			`INSERT INTO post_reaction_count_shards (post_id, shard, count_value, updated_at)
			 VALUES (?,?,1,?)
			 ON CONFLICT(post_id, shard)
			 DO UPDATE SET count_value=post_reaction_count_shards.count_value+1,
			               updated_at=excluded.updated_at`,
			postID, counterShardForIdentity(userID), ts,
		); err != nil {
			return fmt.Errorf("seed post reaction shard %s/%s: %w", postID, userID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate post reaction shard seed: %w", err)
	}
	return nil
}

func seedSQLitePollVoteCountShards(db *sql.DB) error {
	var identityRows, shardRows int
	if err := qQueryRow(db, `SELECT COUNT(*) FROM poll_votes`).Scan(&identityRows); err != nil {
		return fmt.Errorf("count poll votes for shard seed: %w", err)
	}
	if identityRows == 0 {
		return nil
	}
	if err := qQueryRow(db, `SELECT COUNT(*) FROM poll_vote_count_shards`).Scan(&shardRows); err != nil {
		return fmt.Errorf("count poll vote shards for seed: %w", err)
	}
	if shardRows > 0 {
		return nil
	}
	rows, err := qQuery(db, `SELECT poll_id, option_id, user_id, ts FROM poll_votes ORDER BY poll_id, user_id`)
	if err != nil {
		return fmt.Errorf("list poll votes for shard seed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pollID, optionID, userID string
		var ts int64
		if err := rows.Scan(&pollID, &optionID, &userID, &ts); err != nil {
			return fmt.Errorf("scan poll vote shard seed: %w", err)
		}
		if _, err := qExec(db,
			`INSERT INTO poll_vote_count_shards (poll_id, option_id, shard, count_value, updated_at)
			 VALUES (?,?,?,?,?)
			 ON CONFLICT(option_id, shard)
			 DO UPDATE SET count_value=poll_vote_count_shards.count_value+1,
			               updated_at=excluded.updated_at`,
			pollID, optionID, counterShardForIdentity(userID), 1, ts,
		); err != nil {
			return fmt.Errorf("seed poll vote shard %s/%s/%s: %w", pollID, optionID, userID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate poll vote shard seed: %w", err)
	}
	return nil
}

func counterShardForIdentity(identity string) int {
	if identity == "" {
		return 0
	}
	var sum int
	for i := 0; i < len(identity); i++ {
		sum += int(identity[i])
	}
	return sum % 64
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
