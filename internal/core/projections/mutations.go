package projections

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// --- Writers / mutators ---

var ErrDigestPathConflict = errors.New("digest path conflict")
var ErrStagedAttachmentBlobMissing = errors.New("staged attachment blob missing")
var ErrStagedAttachmentBlobMismatch = errors.New("staged attachment blob mismatch")
var ErrStagedAttachmentBlobConflict = errors.New("staged attachment blob conflict")

const (
	StagedBlobPostAttachment = "post_attachment"
	StagedBlobMailAttachment = "mail_attachment"
)

func InsertThread(tx *sql.Tx, t *Thread) error {
	_, err := QExec(tx,
		`INSERT INTO threads (id, board, author, author_id, title, locked, post_count, last_seq, created_ts, created_at, updated_at)
		 VALUES (?,?,?,?,?,0,0,?,?,?,?)`,
		t.ID, t.Board, t.Author, t.AuthorID, t.Title, t.LastSeq, t.CreatedTS, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func InsertPost(tx *sql.Tx, p *Post) error {
	_, err := QExec(tx,
		`INSERT INTO posts (id, thread, author, author_id, body, signature, content_type, reply_to, version, redacted,
		        tex, mail_back,
		        source_post, source_thread, source_board, source_author, source_author_id, source_title,
		        created_seq, updated_seq, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,1,0,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Thread, p.Author, p.AuthorID, p.Body, p.Signature, p.ContentType, NullStr(p.ReplyTo),
		boolInt(p.TeX), boolInt(p.MailBack),
		p.SourcePost, p.SourceThread, p.SourceBoard, p.SourceAuthor, p.SourceAuthorID, p.SourceTitle,
		p.CreatedSeq, p.CreatedSeq, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func InsertPostAttachment(tx *sql.Tx, id, postID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	_, err := QExec(tx,
		`INSERT INTO post_attachments (id, post_id, filename, content_type, size_bytes, url, created_by, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, postID, filename, contentType, sizeBytes, url, createdBy, createdAt,
	)
	return err
}

func StoreAttachmentBlob(db *sql.DB, attachmentID string, data []byte, contentType string) error {
	_, err := QExec(db,
		`INSERT INTO attachment_blobs (attachment_id, data, content_type, size_bytes, uploaded_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(attachment_id)
		 DO UPDATE SET data=excluded.data, content_type=excluded.content_type, size_bytes=excluded.size_bytes, uploaded_at=excluded.uploaded_at`,
		attachmentID, data, contentType, int64(len(data)), NowMS(),
	)
	return err
}

func StageAttachmentBlob(db *sql.DB, kind, id, actorID string, data []byte, contentType string, expiresAt int64) error {
	result, err := QExec(db,
		`INSERT INTO attachment_blob_staging (id, kind, data, content_type, size_bytes, actor_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		id, kind, data, contentType, int64(len(data)), actorID, NowMS(), expiresAt,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected > 0 {
		return nil
	}
	var (
		existingKind        string
		existingData        []byte
		existingContentType string
		existingSize        int64
	)
	err = QQueryRow(db,
		`SELECT kind, data, content_type, size_bytes FROM attachment_blob_staging WHERE id=?`,
		id,
	).Scan(&existingKind, &existingData, &existingContentType, &existingSize)
	if err != nil {
		return err
	}
	if existingKind != kind || existingContentType != contentType || existingSize != int64(len(data)) || !bytes.Equal(existingData, data) {
		return ErrStagedAttachmentBlobConflict
	}
	_, err = QExec(db,
		`UPDATE attachment_blob_staging
		    SET actor_id=?, expires_at=?
		  WHERE id=? AND kind=?`,
		actorID, expiresAt, id, kind,
	)
	return err
}

func DiscardStagedAttachmentBlob(db *sql.DB, kind, id string) error {
	_, err := QExec(db, `DELETE FROM attachment_blob_staging WHERE id=? AND kind=?`, id, kind)
	return err
}

func PruneExpiredStagedAttachmentBlobs(db *sql.DB, now int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	res, err := QExec(db,
		`DELETE FROM attachment_blob_staging
		  WHERE id IN (
		        SELECT id
		          FROM attachment_blob_staging
		         WHERE expires_at > 0 AND expires_at <= ?
		         ORDER BY expires_at, id
		         LIMIT ?
		  )`,
		now, limit,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func PromoteStagedAttachmentBlob(tx *sql.Tx, kind, stagingID, attachmentID string, expectedSize int64, contentType string) error {
	var (
		data              []byte
		stagedContentType string
		sizeBytes         int64
	)
	err := QQueryRow(tx,
		`SELECT data, content_type, size_bytes FROM attachment_blob_staging WHERE id=? AND kind=?`,
		stagingID, kind,
	).Scan(&data, &stagedContentType, &sizeBytes)
	if err == sql.ErrNoRows {
		return ErrStagedAttachmentBlobMissing
	}
	if err != nil {
		return err
	}
	if expectedSize >= 0 && sizeBytes != expectedSize {
		return fmt.Errorf("%w: staged size %d does not match command size %d", ErrStagedAttachmentBlobMismatch, sizeBytes, expectedSize)
	}
	if contentType == "" {
		contentType = stagedContentType
	}
	table := "attachment_blobs"
	if kind == StagedBlobMailAttachment {
		table = "mail_attachment_blobs"
	}
	if kind != StagedBlobPostAttachment && kind != StagedBlobMailAttachment {
		return fmt.Errorf("unknown staged attachment blob kind %q", kind)
	}
	if _, err := QExec(tx,
		`INSERT INTO `+table+` (attachment_id, data, content_type, size_bytes, uploaded_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(attachment_id)
		 DO UPDATE SET data=excluded.data, content_type=excluded.content_type,
		               size_bytes=excluded.size_bytes, uploaded_at=excluded.uploaded_at`,
		attachmentID, data, contentType, sizeBytes, NowMS(),
	); err != nil {
		return err
	}
	_, err = QExec(tx, `DELETE FROM attachment_blob_staging WHERE id=? AND kind=?`, stagingID, kind)
	return err
}

func InsertMailAttachment(tx *sql.Tx, id, mailID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error {
	_, err := QExec(tx,
		`INSERT INTO mail_attachments (id, message_id, filename, content_type, size_bytes, url, created_by, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, mailID, filename, contentType, sizeBytes, url, createdBy, createdAt,
	)
	return err
}

func StoreMailAttachmentBlob(db *sql.DB, attachmentID string, data []byte, contentType string) error {
	_, err := QExec(db,
		`INSERT INTO mail_attachment_blobs (attachment_id, data, content_type, size_bytes, uploaded_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(attachment_id)
		 DO UPDATE SET data=excluded.data, content_type=excluded.content_type, size_bytes=excluded.size_bytes, uploaded_at=excluded.uploaded_at`,
		attachmentID, data, contentType, int64(len(data)), NowMS(),
	)
	return err
}

func BumpThread(tx *sql.Tx, threadID string, seq int64) error {
	_, err := QExec(tx,
		`UPDATE threads SET post_count=post_count+1, last_seq=?, updated_at=? WHERE id=?`,
		seq, NowMS(), threadID,
	)
	return err
}

func UpdatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	_, err := QExec(tx,
		`UPDATE posts SET body=?, version=version+1, updated_seq=?, updated_at=? WHERE id=?`,
		body, seq, NowMS(), postID,
	)
	return err
}

func MarkPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	_, err := QExec(tx,
		`UPDATE posts SET redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
		seq, NowMS(), postID,
	)
	return err
}

func MarkPostRestored(tx *sql.Tx, postID string, seq int64) error {
	_, err := QExec(tx,
		`UPDATE posts SET redacted=0, updated_seq=?, updated_at=? WHERE id=?`,
		seq, NowMS(), postID,
	)
	return err
}

func RecordPostDeletion(tx *sql.Tx, postID, threadID, boardID, deletedByID, deletedByName, reason, kind string, deletedAt, seq int64) error {
	if kind != "junk" {
		kind = "recycle"
	}
	_, err := QExec(tx,
		`INSERT INTO post_deletions (
		    post_id, thread_id, board_id, deleted_by_id, deleted_by_name,
		    reason, kind, deleted_at, seq
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(post_id)
		 DO UPDATE SET
		    thread_id=excluded.thread_id,
		    board_id=excluded.board_id,
		    deleted_by_id=excluded.deleted_by_id,
		    deleted_by_name=excluded.deleted_by_name,
		    reason=excluded.reason,
		    kind=excluded.kind,
		    deleted_at=excluded.deleted_at,
		    seq=excluded.seq`,
		postID, threadID, boardID, deletedByID, deletedByName, reason, kind, deletedAt, seq,
	)
	return err
}

func ClearPostDeletion(tx *sql.Tx, postID string) error {
	_, err := QExec(tx, `DELETE FROM post_deletions WHERE post_id=?`, postID)
	return err
}

// MarkPostPurged irreversibly clears the post body from the projection (GDPR
// hard-delete escape hatch). The body is replaced with an empty string and the
// post is kept redacted. The event log still contains the original content.
func MarkPostPurged(tx *sql.Tx, postID string, seq int64) error {
	_, err := QExec(tx,
		`UPDATE posts SET body='', redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
		seq, NowMS(), postID,
	)
	return err
}

func RebuildResidentFeedPosts(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM resident_feed_posts`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO resident_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 SELECT p.id, p.thread, t.board, p.created_seq, p.updated_seq
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.redacted=0`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildLatestFeedPosts(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM latest_feed_posts`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO latest_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 SELECT p.id, p.thread, t.board, p.created_seq, p.updated_seq
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.redacted=0`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildCommunityStatsSnapshot(tx *sql.Tx) (int64, error) {
	stats, err := getCommunityStatsCurrent(tx)
	if err != nil {
		return 0, err
	}
	if err := applyCommunityStatsMaxOnline(tx, stats); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO community_stats_snapshot (
		    id, total_users, total_boards, total_threads, total_posts,
		    total_reactions, total_mail, total_direct_messages,
		    total_logins, total_logouts, total_web_logins, total_web_logouts,
		    total_guest_logins, total_guest_logouts, total_online_seconds,
		    online_users, online_guests, max_online_users, max_online_at,
		    max_online_guests, max_online_guests_at, head_seq, refreshed_at
		 ) VALUES (
		    'default', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 )
		 ON CONFLICT(id)
		 DO UPDATE SET
		    total_users=excluded.total_users,
		    total_boards=excluded.total_boards,
		    total_threads=excluded.total_threads,
		    total_posts=excluded.total_posts,
		    total_reactions=excluded.total_reactions,
		    total_mail=excluded.total_mail,
		    total_direct_messages=excluded.total_direct_messages,
		    total_logins=excluded.total_logins,
		    total_logouts=excluded.total_logouts,
		    total_web_logins=excluded.total_web_logins,
		    total_web_logouts=excluded.total_web_logouts,
		    total_guest_logins=excluded.total_guest_logins,
		    total_guest_logouts=excluded.total_guest_logouts,
		    total_online_seconds=excluded.total_online_seconds,
		    online_users=excluded.online_users,
		    online_guests=excluded.online_guests,
		    max_online_users=excluded.max_online_users,
		    max_online_at=excluded.max_online_at,
		    max_online_guests=excluded.max_online_guests,
		    max_online_guests_at=excluded.max_online_guests_at,
		    head_seq=excluded.head_seq,
		    refreshed_at=excluded.refreshed_at`,
		stats.TotalUsers,
		stats.TotalBoards,
		stats.TotalThreads,
		stats.TotalPosts,
		stats.TotalReactions,
		stats.TotalMail,
		stats.TotalDirectMessages,
		stats.TotalLogins,
		stats.TotalLogouts,
		stats.TotalWebLogins,
		stats.TotalWebLogouts,
		stats.TotalGuestLogins,
		stats.TotalGuestLogouts,
		stats.TotalOnlineSeconds,
		stats.OnlineUsers,
		stats.OnlineGuests,
		stats.MaxOnlineUsers,
		stats.MaxOnlineAt,
		stats.MaxOnlineGuests,
		stats.MaxOnlineGuestsAt,
		stats.HeadSeq,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildBoardSummaryStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM board_summary_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO board_summary_stats (
		    board_id, thread_count, post_count, last_seq, created_at, moderator_count, refreshed_at
		 )
		 SELECT b.id,
		        COUNT(DISTINCT t.id),
		        COUNT(p.id),
		        COALESCE(MAX(t.last_seq), 0),
		        COALESCE(c.created_at, 0),
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0),
		        ?
		   FROM boards b
		   LEFT JOIN categories c ON c.id=b.id
		   LEFT JOIN threads t ON t.board=b.id
		   LEFT JOIN posts p ON p.thread=t.id AND p.redacted=0
		  GROUP BY b.id, c.created_at`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildUnreadThreadSummaryStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM unread_thread_summary_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO unread_thread_summary_stats (
		    thread_id, board_id, author, author_id, title, locked, post_count,
		    last_seq, created_ts, created_at, updated_at, refreshed_at
		 )
		 SELECT t.id,
		        t.board,
		        t.author,
		        COALESCE(t.author_id, ''),
		        t.title,
		        t.locked,
		        t.post_count,
		        t.last_seq,
		        t.created_ts,
		        t.created_at,
		        t.updated_at,
		        ?
		   FROM threads t`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildBoardRankingStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM board_ranking_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO board_ranking_stats (
		    board_id, thread_count, post_count, last_seq, last_post_at, moderator_count, updated_at
		 )
		 SELECT b.id,
		        COUNT(DISTINCT t.id),
		        COUNT(p.id),
		        COALESCE(MAX(t.last_seq), 0),
		        COALESCE(MAX(p.created_at), 0),
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0),
		        ?
		   FROM boards b
		   LEFT JOIN threads t ON t.board=b.id
		   LEFT JOIN posts p ON p.thread=t.id AND p.redacted=0
		  GROUP BY b.id`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildThreadRankingStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM thread_ranking_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO thread_ranking_stats (
		    thread_id, post_count, participant_count, reaction_count, last_seq, updated_at, refreshed_at
		 )
		 SELECT t.id,
		        COUNT(DISTINCT p.id),
		        COUNT(DISTINCT COALESCE(NULLIF(p.author_id, ''), p.author)),
		        `+postReactionAggregateSumSQL("prc", "p.id")+`,
		        t.last_seq,
		        t.updated_at,
		        ?
		   FROM threads t
		   LEFT JOIN posts p ON p.thread=t.id AND p.redacted=0
		   `+postReactionAggregateJoinSQL("prc", "p.id")+`
		  GROUP BY t.id, t.last_seq, t.updated_at`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildReplyRankingPosts(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM reply_ranking_posts`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO reply_ranking_posts (post_id, thread_id, created_seq, created_at, refreshed_at)
		 SELECT p.id, p.thread, p.created_seq, p.created_at, ?
		   FROM posts p
		  WHERE p.redacted=0
		    AND p.created_seq > (SELECT MIN(root.created_seq) FROM posts root WHERE root.thread=p.thread)`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildUserRankingStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM user_ranking_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO user_ranking_stats (
		    user_id, posts_created, reactions_received, login_count, total_online_seconds, trust_level, refreshed_at
		 )
		 SELECT u.id,
		        COUNT(DISTINCT p.id),
		        `+postReactionAggregateSumSQL("prc", "p.id")+`,
		        COALESCE(ua.login_count, 0),
		        COALESCE(ua.total_online_seconds, 0),
		        COALESCE(ua.trust_level, 0),
		        ?
		   FROM users u
		   LEFT JOIN user_activity ua ON ua.user_id=u.id
		   LEFT JOIN posts p ON p.author_id=u.id AND p.redacted=0
		        AND NOT EXISTS (
		          SELECT 1
		            FROM threads pt
		            LEFT JOIN board_settings ps ON ps.board_id=pt.board
		           WHERE pt.id=p.thread
		             AND (pt.board IN (`+generatedSystemBoardSQLList+`) OR COALESCE(ps.stats_excluded, 0)!=0)
		        )
		   `+postReactionAggregateJoinSQL("prc", "p.id")+`
		  GROUP BY u.id, ua.login_count, ua.total_online_seconds, ua.trust_level`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildBlessingRankingStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM blessing_ranking_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO blessing_ranking_stats (user_id, blessing_count, last_blessed_at, refreshed_at)
		 SELECT b.to_user_id, COUNT(b.id), COALESCE(MAX(b.created_at), 0), ?
		   FROM blessings b
		  GROUP BY b.to_user_id`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func RebuildArchiveRankingStats(tx *sql.Tx) (int64, error) {
	if _, err := QExec(tx, `DELETE FROM archive_ranking_stats`); err != nil {
		return 0, err
	}
	res, err := QExec(tx,
		`INSERT INTO archive_ranking_stats (
		    board_id, kind, path, entry_count, edited_count, last_updated_at, refreshed_at
		 )
		 SELECT d.board_id,
		        d.kind,
		        d.path,
		        COUNT(*),
		        COALESCE(SUM(CASE WHEN d.body_edited != 0 THEN 1 ELSE 0 END), 0),
		        COALESCE(MAX(d.updated_at), 0),
		        ?
		   FROM digest_entries d
		  GROUP BY d.board_id, d.kind, d.path`,
		NowMS(),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func UpsertResidentFeedPost(tx *sql.Tx, postID string) (bool, error) {
	res, err := QExec(tx,
		`INSERT INTO resident_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 SELECT p.id, p.thread, t.board, p.created_seq, p.updated_seq
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.id=? AND p.redacted=0
		 ON CONFLICT(post_id) DO UPDATE SET
		    thread_id=excluded.thread_id,
		    board_id=excluded.board_id,
		    created_seq=excluded.created_seq,
		    updated_seq=excluded.updated_seq`,
		postID,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func DeleteResidentFeedPost(tx *sql.Tx, postID string) (bool, error) {
	res, err := QExec(tx, `DELETE FROM resident_feed_posts WHERE post_id=?`, postID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func MoveResidentFeedThread(tx *sql.Tx, threadID, toBoard string) (bool, error) {
	res, err := QExec(tx, `UPDATE resident_feed_posts SET board_id=? WHERE thread_id=?`, toBoard, threadID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func UpsertLatestFeedPost(tx *sql.Tx, postID string) (bool, error) {
	res, err := QExec(tx,
		`INSERT INTO latest_feed_posts (post_id, thread_id, board_id, created_seq, updated_seq)
		 SELECT p.id, p.thread, t.board, p.created_seq, p.updated_seq
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.id=? AND p.redacted=0
		 ON CONFLICT(post_id) DO UPDATE SET
		    thread_id=excluded.thread_id,
		    board_id=excluded.board_id,
		    created_seq=excluded.created_seq,
		    updated_seq=excluded.updated_seq`,
		postID,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func DeleteLatestFeedPost(tx *sql.Tx, postID string) (bool, error) {
	res, err := QExec(tx, `DELETE FROM latest_feed_posts WHERE post_id=?`, postID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func MoveLatestFeedThread(tx *sql.Tx, threadID, toBoard string) (bool, error) {
	res, err := QExec(tx, `UPDATE latest_feed_posts SET board_id=? WHERE thread_id=?`, toBoard, threadID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func SetPostFlags(tx *sql.Tx, postID string, marked, recommended, noReply, tex, mailBack bool, seq int64) error {
	_, err := QExec(tx,
		`UPDATE posts SET marked=?, recommended=?, no_reply=?, tex=?, mail_back=?, updated_seq=?, updated_at=? WHERE id=?`,
		boolInt(marked), boolInt(recommended), boolInt(noReply), boolInt(tex), boolInt(mailBack), seq, NowMS(), postID,
	)
	return err
}

func SetThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := QExec(tx, `UPDATE threads SET locked=? WHERE id=?`, v, threadID)
	return err
}

func SetThreadTitle(tx *sql.Tx, threadID, title string, ts int64) error {
	_, err := QExec(tx, `UPDATE threads SET title=?, updated_at=? WHERE id=?`, title, ts, threadID)
	return err
}

func MoveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	_, err := QExec(tx, `UPDATE threads SET board=? WHERE id=?`, toBoard, threadID)
	return err
}

func SetUserRole(tx *sql.Tx, userID, role string) error {
	_, err := QExec(tx, `UPDATE users SET role=? WHERE id=?`, role, userID)
	return err
}

func TransferUserID(db *sql.DB, userID, newName string) (*User, error) {
	userID = strings.TrimSpace(userID)
	newName = strings.TrimSpace(newName)
	if userID == "" || newName == "" {
		return nil, fmt.Errorf("user and new name required")
	}
	if len(newName) > 64 {
		return nil, fmt.Errorf("new name too long")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var oldName string
	if err := QQueryRow(tx, `SELECT name FROM users WHERE id=?`, userID).Scan(&oldName); err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	} else if err != nil {
		return nil, err
	}
	var existing string
	err = QQueryRow(tx, `SELECT id FROM users WHERE name=? AND id<>?`, newName, userID).Scan(&existing)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if existing != "" {
		return nil, fmt.Errorf("name already in use")
	}
	if _, err := QExec(tx, `UPDATE users SET name=? WHERE id=?`, newName, userID); err != nil {
		return nil, err
	}
	if _, err := QExec(tx,
		`UPDATE user_profiles
		    SET display_name=CASE WHEN display_name='' OR display_name=? THEN ? ELSE display_name END,
		        updated_at=?
		  WHERE user_id=?`,
		oldName, newName, NowMS(), userID,
	); err != nil {
		return nil, err
	}
	if _, err := QExec(tx, `UPDATE threads SET author=? WHERE author_id=?`, newName, userID); err != nil {
		return nil, err
	}
	if _, err := QExec(tx, `UPDATE posts SET author=? WHERE author_id=?`, newName, userID); err != nil {
		return nil, err
	}
	if _, err := QExec(tx,
		`UPDATE posts_fts
		    SET author=?
		  WHERE post_id IN (SELECT id FROM posts WHERE author_id=?)`,
		newName, userID,
	); err != nil {
		return nil, err
	}
	if _, err := QExec(tx, `UPDATE relay_deliveries SET author_name=? WHERE author_id=?`, newName, userID); err != nil {
		return nil, err
	}
	if _, err := QExec(tx, `UPDATE notifications SET actor=? WHERE actor=?`, newName, oldName); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetUserByID(db, userID)
}

func InsertBoard(tx *sql.Tx, id, name, description, parentID string, position int) error {
	if _, err := QExec(tx,
		`INSERT INTO boards (id, name, description) VALUES (?,?,?)`,
		id, name, description,
	); err != nil {
		return err
	}
	_, err := QExec(tx,
		`INSERT OR IGNORE INTO categories (id, name, description, parent_id, position, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		id, name, description, parentID, position, NowMS(), NowMS(),
	)
	return err
}

func SetBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int, favorite bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setBoardFavorite(tx, userID, boardID, folderID, position, favorite, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func SetBoardFavoriteTx(tx *sql.Tx, userID, boardID, folderID string, position *int, favorite bool, updatedAt int64) error {
	return setBoardFavorite(tx, userID, boardID, folderID, position, favorite, updatedAt)
}

func setBoardFavorite(tx *sql.Tx, userID, boardID, folderID string, position *int, favorite bool, updatedAt int64) error {
	if !favorite {
		_, err := QExec(tx, `DELETE FROM board_favorites WHERE user_id=? AND board_id=?`, userID, boardID)
		return err
	}
	targetPosition, err := favoriteTargetPosition(tx, userID, folderID, position)
	if err != nil {
		return err
	}
	if err := shiftFavoriteBoards(tx, userID, folderID, boardID, targetPosition); err != nil {
		return err
	}
	_, err = QExec(tx,
		`INSERT INTO board_favorites (user_id, board_id, folder_id, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, board_id)
		 DO UPDATE SET
		    folder_id=excluded.folder_id,
		    position=excluded.position,
		    updated_at=excluded.updated_at`,
		userID, boardID, folderID, targetPosition, updatedAt, updatedAt,
	)
	return err
}

func CreateFavoriteFolder(db *sql.DB, userID, folderID, parentID, name string, position *int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	targetPosition, err := favoriteFolderTargetPosition(tx, userID, parentID, position)
	if err != nil {
		return err
	}
	if err := createFavoriteFolderAtPosition(tx, userID, folderID, parentID, name, targetPosition, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func CreateFavoriteFolderTx(tx *sql.Tx, userID, folderID, parentID, name string, position int, createdAt int64) error {
	return createFavoriteFolderAtPosition(tx, userID, folderID, parentID, name, position, createdAt)
}

func createFavoriteFolderAtPosition(tx *sql.Tx, userID, folderID, parentID, name string, position int, createdAt int64) error {
	if position < 0 {
		position = 0
	}
	if err := shiftFavoriteFolders(tx, userID, parentID, folderID, position); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`INSERT INTO favorite_folders (id, user_id, parent_id, name, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		folderID, userID, parentID, name, position, createdAt, createdAt,
	); err != nil {
		return err
	}
	return nil
}

func UpdateFavoriteFolder(db *sql.DB, userID, folderID, name string, parentID *string, position *int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentParent string
	var currentName string
	var currentPosition int
	if err := QQueryRow(tx,
		`SELECT parent_id, name, position FROM favorite_folders WHERE user_id=? AND id=?`,
		userID, folderID,
	).Scan(&currentParent, &currentName, &currentPosition); err != nil {
		return err
	}
	if name == "" {
		name = currentName
	}
	nextParent := currentParent
	if parentID != nil {
		nextParent = *parentID
	}
	targetPosition := currentPosition
	if position != nil {
		targetPosition = *position
	} else if nextParent != currentParent {
		targetPosition, err = favoriteFolderTargetPosition(tx, userID, nextParent, nil)
		if err != nil {
			return err
		}
	}
	if targetPosition < 0 {
		targetPosition = 0
	}
	if err := updateFavoriteFolderFinalState(tx, userID, folderID, nextParent, name, targetPosition, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func UpdateFavoriteFolderTx(tx *sql.Tx, userID, folderID, parentID, name string, position int, updatedAt int64) error {
	return updateFavoriteFolderFinalState(tx, userID, folderID, parentID, name, position, updatedAt)
}

func updateFavoriteFolderFinalState(tx *sql.Tx, userID, folderID, parentID, name string, position int, updatedAt int64) error {
	var exists int
	if err := QQueryRow(tx, `SELECT 1 FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&exists); err != nil {
		return err
	}
	if position < 0 {
		position = 0
	}
	if err := shiftFavoriteFolders(tx, userID, parentID, folderID, position); err != nil {
		return err
	}
	_, err := QExec(tx,
		`UPDATE favorite_folders
		    SET parent_id=?, name=?, position=?, updated_at=?
		  WHERE user_id=? AND id=?`,
		parentID, name, position, updatedAt, userID, folderID,
	)
	return err
}

func DeleteFavoriteFolder(db *sql.DB, userID, folderID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var parentID string
	if err := QQueryRow(tx, `SELECT parent_id FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID).Scan(&parentID); err != nil {
		return err
	}
	if err := deleteFavoriteFolderWithParent(tx, userID, folderID, parentID, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func DeleteFavoriteFolderTx(tx *sql.Tx, userID, folderID, parentID string, updatedAt int64) error {
	return deleteFavoriteFolderWithParent(tx, userID, folderID, parentID, updatedAt)
}

func deleteFavoriteFolderWithParent(tx *sql.Tx, userID, folderID, parentID string, updatedAt int64) error {
	if _, err := QExec(tx, `UPDATE board_favorites SET folder_id=?, updated_at=? WHERE user_id=? AND folder_id=?`, parentID, updatedAt, userID, folderID); err != nil {
		return err
	}
	if _, err := QExec(tx, `UPDATE favorite_folders SET parent_id=?, updated_at=? WHERE user_id=? AND parent_id=?`, parentID, updatedAt, userID, folderID); err != nil {
		return err
	}
	_, err := QExec(tx, `DELETE FROM favorite_folders WHERE user_id=? AND id=?`, userID, folderID)
	return err
}

func MoveBoardFavorite(db *sql.DB, userID, boardID, folderID string, position *int) error {
	return SetBoardFavorite(db, userID, boardID, folderID, position, true)
}

func ImportFavoriteTree(db *sql.DB, userID string, tree *FavoriteTree, replace bool, newID func(string) string) error {
	if tree == nil {
		tree = &FavoriteTree{}
	}
	if len(tree.Folders) > 200 {
		return fmt.Errorf("favorite import supports at most 200 folders")
	}
	if len(tree.Boards) > 500 {
		return fmt.Errorf("favorite import supports at most 500 boards")
	}

	sourceFolderIDs := map[string]bool{}
	for i, folder := range tree.Folders {
		if folder.ID == "" {
			return fmt.Errorf("folder %d is missing id", i+1)
		}
		if sourceFolderIDs[folder.ID] {
			return fmt.Errorf("duplicate folder id %q", folder.ID)
		}
		name := strings.TrimSpace(folder.Name)
		if name == "" {
			return fmt.Errorf("folder %q is missing name", folder.ID)
		}
		if len(name) > 80 {
			return fmt.Errorf("folder %q name must be 80 characters or less", folder.ID)
		}
		sourceFolderIDs[folder.ID] = true
	}
	for _, folder := range tree.Folders {
		if folder.ParentID != "" && !sourceFolderIDs[folder.ParentID] {
			return fmt.Errorf("folder %q references missing parent %q", folder.ID, folder.ParentID)
		}
	}
	for _, board := range tree.Boards {
		if strings.TrimSpace(board.ID) == "" {
			return fmt.Errorf("favorite board is missing id")
		}
		if board.FolderID != "" && !sourceFolderIDs[board.FolderID] {
			return fmt.Errorf("board %q references missing folder %q", board.ID, board.FolderID)
		}
		var exists int
		if err := QQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, board.ID).Scan(&exists); err == sql.ErrNoRows {
			return fmt.Errorf("board %q not found", board.ID)
		} else if err != nil {
			return err
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if replace {
		if _, err := QExec(tx, `DELETE FROM board_favorites WHERE user_id=?`, userID); err != nil {
			return err
		}
		if _, err := QExec(tx, `DELETE FROM favorite_folders WHERE user_id=?`, userID); err != nil {
			return err
		}
	}

	now := NowMS()
	folderIDMap := map[string]string{}
	remaining := append([]FavoriteFolder(nil), tree.Folders...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, folder := range remaining {
			parentID := ""
			if folder.ParentID != "" {
				mapped, ok := folderIDMap[folder.ParentID]
				if !ok {
					next = append(next, folder)
					continue
				}
				parentID = mapped
			}
			id := newID("favfld_")
			folderIDMap[folder.ID] = id
			if _, err := QExec(tx,
				`INSERT INTO favorite_folders (id, user_id, parent_id, name, position, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id, userID, parentID, strings.TrimSpace(folder.Name), folder.Position, now, now,
			); err != nil {
				return err
			}
			progressed = true
		}
		if !progressed {
			return fmt.Errorf("favorite folder import contains a cycle")
		}
		remaining = next
	}

	seenBoards := map[string]bool{}
	for _, board := range tree.Boards {
		if seenBoards[board.ID] {
			continue
		}
		seenBoards[board.ID] = true
		folderID := ""
		if board.FolderID != "" {
			folderID = folderIDMap[board.FolderID]
		}
		if _, err := QExec(tx,
			`INSERT INTO board_favorites (user_id, board_id, folder_id, position, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, board_id)
			 DO UPDATE SET folder_id=excluded.folder_id,
			               position=excluded.position,
			               updated_at=excluded.updated_at`,
			userID, board.ID, folderID, board.Position, now, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ImportFavoriteTreeTx(tx *sql.Tx, userID string, tree *FavoriteTree, replace bool, importedAt int64) error {
	if tree == nil {
		tree = &FavoriteTree{}
	}
	if len(tree.Folders) > 200 {
		return fmt.Errorf("favorite import supports at most 200 folders")
	}
	if len(tree.Boards) > 500 {
		return fmt.Errorf("favorite import supports at most 500 boards")
	}

	folderIDs := map[string]bool{}
	for i, folder := range tree.Folders {
		folderID := strings.TrimSpace(folder.ID)
		if folderID == "" {
			return fmt.Errorf("folder %d is missing id", i+1)
		}
		if folderIDs[folderID] {
			return fmt.Errorf("duplicate folder id %q", folderID)
		}
		name := strings.TrimSpace(folder.Name)
		if name == "" {
			return fmt.Errorf("folder %q is missing name", folderID)
		}
		if len(name) > 80 {
			return fmt.Errorf("folder %q name must be 80 characters or less", folderID)
		}
		folderIDs[folderID] = true
	}
	for _, folder := range tree.Folders {
		folderID := strings.TrimSpace(folder.ID)
		parentID := strings.TrimSpace(folder.ParentID)
		if parentID != "" && !folderIDs[parentID] {
			return fmt.Errorf("folder %q references missing parent %q", folderID, parentID)
		}
	}
	for _, board := range tree.Boards {
		boardID := strings.TrimSpace(board.ID)
		if boardID == "" {
			return fmt.Errorf("favorite board is missing id")
		}
		folderID := strings.TrimSpace(board.FolderID)
		if folderID != "" && !folderIDs[folderID] {
			return fmt.Errorf("board %q references missing folder %q", boardID, folderID)
		}
		var exists int
		if err := QQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists); err == sql.ErrNoRows {
			return fmt.Errorf("board %q not found", boardID)
		} else if err != nil {
			return err
		}
	}

	if replace {
		if _, err := QExec(tx, `DELETE FROM board_favorites WHERE user_id=?`, userID); err != nil {
			return err
		}
		if _, err := QExec(tx, `DELETE FROM favorite_folders WHERE user_id=?`, userID); err != nil {
			return err
		}
	}

	insertedFolders := map[string]bool{}
	remaining := append([]FavoriteFolder(nil), tree.Folders...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, folder := range remaining {
			folderID := strings.TrimSpace(folder.ID)
			parentID := strings.TrimSpace(folder.ParentID)
			if parentID != "" && !insertedFolders[parentID] {
				next = append(next, folder)
				continue
			}
			if _, err := QExec(tx,
				`INSERT INTO favorite_folders (id, user_id, parent_id, name, position, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(id)
				 DO UPDATE SET user_id=excluded.user_id,
				               parent_id=excluded.parent_id,
				               name=excluded.name,
				               position=excluded.position,
				               updated_at=excluded.updated_at`,
				folderID, userID, parentID, strings.TrimSpace(folder.Name), folder.Position, importedAt, importedAt,
			); err != nil {
				return err
			}
			insertedFolders[folderID] = true
			progressed = true
		}
		if !progressed {
			return fmt.Errorf("favorite folder import contains a cycle")
		}
		remaining = next
	}

	seenBoards := map[string]bool{}
	for _, board := range tree.Boards {
		boardID := strings.TrimSpace(board.ID)
		if seenBoards[boardID] {
			continue
		}
		seenBoards[boardID] = true
		if _, err := QExec(tx,
			`INSERT INTO board_favorites (user_id, board_id, folder_id, position, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, board_id)
			 DO UPDATE SET folder_id=excluded.folder_id,
			               position=excluded.position,
			               updated_at=excluded.updated_at`,
			userID, boardID, strings.TrimSpace(board.FolderID), board.Position, importedAt, importedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func SetBoardSettings(db *sql.DB, boardID string, patch BoardSettingsPatch) error {
	settings, err := GetBoardSettings(db, boardID)
	if err != nil {
		return err
	}
	if settings == nil {
		return sql.ErrNoRows
	}
	ApplyBoardSettingsPatch(settings, patch)
	settings.UpdatedAt = NowMS()
	return setBoardSettingsFinal(db, *settings)
}

func ApplyBoardSettingsPatch(settings *BoardSettings, patch BoardSettingsPatch) {
	if settings == nil {
		return
	}
	if patch.AnonymousAllowed != nil {
		settings.AnonymousAllowed = *patch.AnonymousAllowed
	}
	if patch.ReadOnly != nil {
		settings.ReadOnly = *patch.ReadOnly
	}
	if patch.NoReply != nil {
		settings.NoReply = *patch.NoReply
	}
	if patch.AttachmentsAllowed != nil {
		settings.AttachmentsAllowed = *patch.AttachmentsAllowed
	}
	if patch.MailInAllowed != nil {
		settings.MailInAllowed = *patch.MailInAllowed
	}
	if patch.RelayEnabled != nil {
		settings.RelayEnabled = *patch.RelayEnabled
	}
	if patch.MemberReadMode != nil {
		settings.MemberReadMode = *patch.MemberReadMode
	}
	if patch.MemberPostMode != nil {
		settings.MemberPostMode = *patch.MemberPostMode
	}
	if patch.StatsExcluded != nil {
		settings.StatsExcluded = *patch.StatsExcluded
	}
	if patch.ZapAllowed != nil {
		settings.ZapAllowed = *patch.ZapAllowed
	}
	if patch.GuestAccess != nil {
		settings.GuestAccess = NormalizeGuestAccess(*patch.GuestAccess)
	}
}

// NormalizeGuestAccess canonicalizes a guest-access value to "" (default),
// "hidden", or "public". Any unrecognized value (including "default") maps to ""
// so callers and storage only ever see the three canonical states.
func NormalizeGuestAccess(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "hidden":
		return "hidden"
	case "public":
		return "public"
	default:
		return ""
	}
}

func SetBoardSettingsTx(tx *sql.Tx, settings BoardSettings) error {
	return setBoardSettingsFinal(tx, settings)
}

func setBoardSettingsFinal(execable sqlLike, settings BoardSettings) error {
	if settings.UpdatedAt <= 0 {
		settings.UpdatedAt = NowMS()
	}
	_, err := QExec(execable,
		`INSERT INTO board_settings (
		    board_id, anonymous_allowed, read_only, no_reply, attachments_allowed,
		    mail_in_allowed, relay_enabled, member_read_mode, member_post_mode, stats_excluded, zap_allowed, guest_access, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id)
		 DO UPDATE SET
		    anonymous_allowed=excluded.anonymous_allowed,
		    read_only=excluded.read_only,
		    no_reply=excluded.no_reply,
		    attachments_allowed=excluded.attachments_allowed,
		    mail_in_allowed=excluded.mail_in_allowed,
		    relay_enabled=excluded.relay_enabled,
		    member_read_mode=excluded.member_read_mode,
		    member_post_mode=excluded.member_post_mode,
		    stats_excluded=excluded.stats_excluded,
		    zap_allowed=excluded.zap_allowed,
		    guest_access=excluded.guest_access,
		    updated_at=excluded.updated_at`,
		settings.BoardID,
		boolInt(settings.AnonymousAllowed),
		boolInt(settings.ReadOnly),
		boolInt(settings.NoReply),
		boolInt(settings.AttachmentsAllowed),
		boolInt(settings.MailInAllowed),
		boolInt(settings.RelayEnabled),
		boolInt(settings.MemberReadMode),
		boolInt(settings.MemberPostMode),
		boolInt(settings.StatsExcluded),
		boolInt(settings.ZapAllowed),
		NormalizeGuestAccess(settings.GuestAccess),
		settings.UpdatedAt,
	)
	return err
}

func SetBoardZap(db *sql.DB, userID, boardID string, zapped bool) error {
	return setBoardZap(db, userID, boardID, zapped, NowMS())
}

func SetBoardZapTx(tx *sql.Tx, userID, boardID string, zapped bool, updatedAt int64) error {
	return setBoardZap(tx, userID, boardID, zapped, updatedAt)
}

func setBoardZap(execable sqlLike, userID, boardID string, zapped bool, updatedAt int64) error {
	if !zapped {
		_, err := QExec(execable, `DELETE FROM board_zaps WHERE user_id=? AND board_id=?`, userID, boardID)
		return err
	}
	_, err := QExec(execable,
		`INSERT INTO board_zaps (user_id, board_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, board_id)
		 DO UPDATE SET updated_at=excluded.updated_at`,
		userID, boardID, updatedAt, updatedAt,
	)
	return err
}

func SetRecommendedBoard(db *sql.DB, boardID, note, curatedBy string, position *int, recommended bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	pos := 0
	if recommended && position != nil {
		pos = *position
	} else if recommended {
		err := QQueryRow(tx, `SELECT position FROM recommended_boards WHERE board_id=?`, boardID).Scan(&pos)
		if err == sql.ErrNoRows {
			if err := QQueryRow(tx, `SELECT COALESCE(MAX(position), -10) + 10 FROM recommended_boards`).Scan(&pos); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}

	if err := SetRecommendedBoardTx(tx, boardID, note, curatedBy, pos, recommended, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func SetRecommendedBoardTx(tx *sql.Tx, boardID, note, curatedBy string, position int, recommended bool, updatedAt int64) error {
	if !recommended {
		_, err := QExec(tx, `DELETE FROM recommended_boards WHERE board_id=?`, boardID)
		return err
	}
	if updatedAt <= 0 {
		updatedAt = NowMS()
	}
	_, err := QExec(tx,
		`INSERT INTO recommended_boards (board_id, note, position, curated_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id)
		 DO UPDATE SET note=excluded.note,
		               position=excluded.position,
		               curated_by=excluded.curated_by,
		               updated_at=excluded.updated_at`,
		boardID, note, position, curatedBy, updatedAt, updatedAt,
	)
	return err
}

func SetBoardMemberRequirements(db *sql.DB, boardID string, patch BoardMemberRequirementsPatch) error {
	req, err := GetBoardMemberRequirements(db, boardID)
	if err != nil {
		return err
	}
	if req == nil {
		return sql.ErrNoRows
	}
	ApplyBoardMemberRequirementsPatch(req, patch)
	req.UpdatedAt = NowMS()
	return setBoardMemberRequirementsFinal(db, *req)
}

func ApplyBoardMemberRequirementsPatch(req *BoardMemberRequirements, patch BoardMemberRequirementsPatch) {
	if req == nil {
		return
	}
	if patch.MinLoginCount != nil {
		req.MinLoginCount = *patch.MinLoginCount
	}
	if patch.MinPostCount != nil {
		req.MinPostCount = *patch.MinPostCount
	}
	if patch.MinTrustLevel != nil {
		req.MinTrustLevel = *patch.MinTrustLevel
	}
	if patch.MinScore != nil {
		req.MinScore = *patch.MinScore
	}
	if patch.MinBoardPostCount != nil {
		req.MinBoardPostCount = *patch.MinBoardPostCount
	}
	if patch.MinBoardOriginalPostCount != nil {
		req.MinBoardOriginalPostCount = *patch.MinBoardOriginalPostCount
	}
	if patch.MinBoardDigestCount != nil {
		req.MinBoardDigestCount = *patch.MinBoardDigestCount
	}
	if patch.MinBoardMarkCount != nil {
		req.MinBoardMarkCount = *patch.MinBoardMarkCount
	}
	if patch.MaxMembers != nil {
		req.MaxMembers = *patch.MaxMembers
	}
	if patch.ApprovalMode != nil {
		req.ApprovalMode = strings.ToLower(strings.TrimSpace(*patch.ApprovalMode))
	}
	if req.ApprovalMode == "" {
		req.ApprovalMode = "manual"
	}
}

func SetBoardMemberRequirementsTx(tx *sql.Tx, req BoardMemberRequirements) error {
	return setBoardMemberRequirementsFinal(tx, req)
}

func setBoardMemberRequirementsFinal(execable sqlLike, req BoardMemberRequirements) error {
	req.ApprovalMode = strings.ToLower(strings.TrimSpace(req.ApprovalMode))
	if req.ApprovalMode == "" {
		req.ApprovalMode = "manual"
	}
	if req.UpdatedAt <= 0 {
		req.UpdatedAt = NowMS()
	}
	_, err := QExec(execable,
		`INSERT INTO board_member_requirements (
		    board_id, min_login_count, min_post_count, min_trust_level,
		    min_score, min_board_post_count, min_board_original_post_count,
		    min_board_digest_count, min_board_mark_count,
		    max_members, approval_mode, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id)
		 DO UPDATE SET
		    min_login_count=excluded.min_login_count,
		    min_post_count=excluded.min_post_count,
		    min_trust_level=excluded.min_trust_level,
		    min_score=excluded.min_score,
		    min_board_post_count=excluded.min_board_post_count,
		    min_board_original_post_count=excluded.min_board_original_post_count,
		    min_board_digest_count=excluded.min_board_digest_count,
		    min_board_mark_count=excluded.min_board_mark_count,
		    max_members=excluded.max_members,
		    approval_mode=excluded.approval_mode,
		    updated_at=excluded.updated_at`,
		req.BoardID,
		req.MinLoginCount,
		req.MinPostCount,
		req.MinTrustLevel,
		req.MinScore,
		req.MinBoardPostCount,
		req.MinBoardOriginalPostCount,
		req.MinBoardDigestCount,
		req.MinBoardMarkCount,
		req.MaxMembers,
		req.ApprovalMode,
		req.UpdatedAt,
	)
	return err
}

func SetBoardModerator(db *sql.DB, boardID, userID, actorID string, moderator bool, position *int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	ts := NowMS()
	targetPosition := 0
	if moderator {
		if position != nil {
			targetPosition = *position
			if targetPosition < 0 {
				targetPosition = 0
			}
		} else {
			var currentPosition int
			err := QQueryRow(tx,
				`SELECT position FROM board_moderators WHERE board_id=? AND user_id=?`,
				boardID, userID,
			).Scan(&currentPosition)
			if err == nil {
				targetPosition = currentPosition
			} else if err == sql.ErrNoRows {
				if err := QQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM board_moderators WHERE board_id=?`, boardID).Scan(&targetPosition); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	if err := SetBoardModeratorTx(tx, boardID, userID, actorID, moderator, targetPosition, ts); err != nil {
		return err
	}
	return tx.Commit()
}

func SetBoardModeratorTx(tx *sql.Tx, boardID, userID, actorID string, moderator bool, position int, ts int64) error {
	if ts <= 0 {
		ts = NowMS()
	}
	if position < 0 {
		position = 0
	}
	var currentPosition int
	var currentCreatedAt int64
	err := QQueryRow(tx,
		`SELECT position, created_at FROM board_moderators WHERE board_id=? AND user_id=?`,
		boardID, userID,
	).Scan(&currentPosition, &currentCreatedAt)
	hasCurrent := err == nil
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if !moderator {
		if hasCurrent {
			if _, err := QExec(tx, `DELETE FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, userID); err != nil {
				return err
			}
			res, err := QExec(tx,
				`UPDATE board_moderator_terms
				    SET ended_at=?, removed_by=?, updated_at=?
				  WHERE board_id=? AND user_id=? AND ended_at=0`,
				ts, actorID, ts, boardID, userID,
			)
			if err != nil {
				return err
			}
			affected, _ := res.RowsAffected()
			if affected == 0 {
				startedAt := currentCreatedAt
				if startedAt <= 0 {
					startedAt = ts
				}
				if err := insertBoardModeratorTermTx(tx, boardID, userID, startedAt, ts, "", actorID, currentPosition, startedAt, ts); err != nil {
					return err
				}
			}
		}
		return nil
	}

	_, err = QExec(tx,
		`INSERT INTO board_moderators (board_id, user_id, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(board_id, user_id)
		 DO UPDATE SET position=excluded.position, updated_at=excluded.updated_at`,
		boardID, userID, position, ts, ts,
	)
	if err != nil {
		return err
	}

	if hasCurrent {
		res, err := QExec(tx,
			`UPDATE board_moderator_terms
			    SET position=?, updated_at=?
			  WHERE board_id=? AND user_id=? AND ended_at=0`,
			position, ts, boardID, userID,
		)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			startedAt := currentCreatedAt
			if startedAt <= 0 {
				startedAt = ts
			}
			startedAt, err = nextBoardModeratorTermStartedAt(tx, boardID, userID, startedAt)
			if err != nil {
				return err
			}
			if err := insertBoardModeratorTermTx(tx, boardID, userID, startedAt, 0, actorID, "", position, startedAt, ts); err != nil {
				return err
			}
		}
		return nil
	}

	startedAt, err := nextBoardModeratorTermStartedAt(tx, boardID, userID, ts)
	if err != nil {
		return err
	}
	if err := insertBoardModeratorTermTx(tx, boardID, userID, startedAt, 0, actorID, "", position, ts, ts); err != nil {
		return err
	}
	return nil
}

func insertBoardModeratorTermTx(tx *sql.Tx, boardID, userID string, startedAt, endedAt int64, appointedBy, removedBy string, position int, createdAt, updatedAt int64) error {
	_, err := QExec(tx,
		`INSERT INTO board_moderator_terms (
		    board_id, user_id, started_at, ended_at, appointed_by, removed_by,
		    position, created_at, updated_at
		)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id, user_id, started_at)
		 DO UPDATE SET ended_at=excluded.ended_at,
		               appointed_by=excluded.appointed_by,
		               removed_by=excluded.removed_by,
		               position=excluded.position,
		               updated_at=excluded.updated_at`,
		boardID, userID, startedAt, endedAt, appointedBy, removedBy, position, createdAt, updatedAt,
	)
	return err
}

func nextBoardModeratorTermStartedAt(tx *sql.Tx, boardID, userID string, desired int64) (int64, error) {
	if desired <= 0 {
		desired = NowMS()
	}
	for {
		var exists int
		err := QQueryRow(tx,
			`SELECT 1 FROM board_moderator_terms WHERE board_id=? AND user_id=? AND started_at=?`,
			boardID, userID, desired,
		).Scan(&exists)
		if err == sql.ErrNoRows {
			return desired, nil
		}
		if err != nil {
			return 0, err
		}
		desired++
	}
}

func SetBoardMember(db *sql.DB, boardID, userID string, member bool, patch BoardMemberPatch) error {
	if !member {
		return setBoardMemberFinal(db, boardID, BoardMember{UserID: userID}, false, NowMS())
	}
	canManageMembers := 0
	canCurate := 0
	canModeratePosts := 0
	canModerateThreads := 0
	canAnnounce := 0
	canManagePolls := 0
	canSetBoardSettings := 0
	position := 0
	err := QQueryRow(db,
		`SELECT can_manage_members, can_curate, can_moderate_posts, can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings, COALESCE(position, 0)
		   FROM board_members WHERE board_id=? AND user_id=?`,
		boardID, userID,
	).Scan(&canManageMembers, &canCurate, &canModeratePosts, &canModerateThreads, &canAnnounce, &canManagePolls, &canSetBoardSettings, &position)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		if err := QQueryRow(db, `SELECT COALESCE(MAX(position) + 1, 0) FROM board_members WHERE board_id=?`, boardID).Scan(&position); err != nil {
			return err
		}
	}
	if patch.Position != nil {
		position = *patch.Position
	}
	if patch.CanManageMembers != nil && *patch.CanManageMembers {
		canManageMembers = 1
	} else if patch.CanManageMembers != nil {
		canManageMembers = 0
	}
	if patch.CanCurate != nil && *patch.CanCurate {
		canCurate = 1
	} else if patch.CanCurate != nil {
		canCurate = 0
	}
	if patch.CanModeratePosts != nil && *patch.CanModeratePosts {
		canModeratePosts = 1
	} else if patch.CanModeratePosts != nil {
		canModeratePosts = 0
	}
	if patch.CanModerateThreads != nil && *patch.CanModerateThreads {
		canModerateThreads = 1
	} else if patch.CanModerateThreads != nil {
		canModerateThreads = 0
	}
	if patch.CanAnnounce != nil && *patch.CanAnnounce {
		canAnnounce = 1
	} else if patch.CanAnnounce != nil {
		canAnnounce = 0
	}
	if patch.CanManagePolls != nil && *patch.CanManagePolls {
		canManagePolls = 1
	} else if patch.CanManagePolls != nil {
		canManagePolls = 0
	}
	if patch.CanSetBoardSettings != nil && *patch.CanSetBoardSettings {
		canSetBoardSettings = 1
	} else if patch.CanSetBoardSettings != nil {
		canSetBoardSettings = 0
	}
	return setBoardMemberFinal(db, boardID, BoardMember{
		UserID:              userID,
		Title:               strings.TrimSpace(patch.Title),
		Position:            position,
		CanManageMembers:    canManageMembers != 0,
		CanCurate:           canCurate != 0,
		CanModeratePosts:    canModeratePosts != 0,
		CanModerateThreads:  canModerateThreads != 0,
		CanAnnounce:         canAnnounce != 0,
		CanManagePolls:      canManagePolls != 0,
		CanSetBoardSettings: canSetBoardSettings != 0,
	}, true, NowMS())
}

func SetBoardMemberTx(tx *sql.Tx, boardID string, member BoardMember, active bool, updatedAt int64) error {
	return setBoardMemberFinal(tx, boardID, member, active, updatedAt)
}

func setBoardMemberFinal(execable sqlLike, boardID string, member BoardMember, active bool, updatedAt int64) error {
	if !active {
		_, err := QExec(execable, `DELETE FROM board_members WHERE board_id=? AND user_id=?`, boardID, member.UserID)
		return err
	}
	if updatedAt <= 0 {
		updatedAt = NowMS()
	}
	if member.Position < 0 {
		member.Position = 0
	}
	_, err := QExec(execable,
		`INSERT INTO board_members (
		    board_id, user_id, title, position, can_manage_members, can_curate,
		    can_moderate_posts, can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings,
		    created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id, user_id)
		 DO UPDATE SET
		    title=excluded.title,
		    position=excluded.position,
		    can_manage_members=excluded.can_manage_members,
		    can_curate=excluded.can_curate,
		    can_moderate_posts=excluded.can_moderate_posts,
		    can_moderate_threads=excluded.can_moderate_threads,
		    can_announce=excluded.can_announce,
		    can_manage_polls=excluded.can_manage_polls,
		    can_set_board_settings=excluded.can_set_board_settings,
		    updated_at=excluded.updated_at`,
		boardID,
		member.UserID,
		strings.TrimSpace(member.Title),
		member.Position,
		boolInt(member.CanManageMembers),
		boolInt(member.CanCurate),
		boolInt(member.CanModeratePosts),
		boolInt(member.CanModerateThreads),
		boolInt(member.CanAnnounce),
		boolInt(member.CanManagePolls),
		boolInt(member.CanSetBoardSettings),
		updatedAt,
		updatedAt,
	)
	return err
}

func InsertBoardMemberApplication(db *sql.DB, id, boardID, userID, note string) error {
	ts := NowMS()
	return InsertBoardMemberApplicationTx(db, id, boardID, userID, note, ts)
}

func InsertBoardMemberApplicationTx(execable sqlLike, id, boardID, userID, note string, createdAt int64) error {
	_, err := QExec(execable,
		`INSERT INTO board_member_applications (id, board_id, user_id, status, note, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
		id, boardID, userID, strings.TrimSpace(note), createdAt, createdAt,
	)
	return err
}

func ReviewBoardMemberApplication(db *sql.DB, applicationID, reviewerID, status, title, reviewNote string) error {
	ts := NowMS()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var boardID, userID string
	if err := QQueryRow(tx, `SELECT board_id, user_id FROM board_member_applications WHERE id=?`, applicationID).Scan(&boardID, &userID); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`UPDATE board_member_applications
		    SET status=?, title=?, reviewer_id=?, review_note=?, updated_at=?, reviewed_at=?
		  WHERE id=?`,
		status, strings.TrimSpace(title), reviewerID, strings.TrimSpace(reviewNote), ts, ts, applicationID,
	); err != nil {
		return err
	}
	switch status {
	case "approved":
		if _, err := QExec(tx,
			`INSERT INTO board_members (board_id, user_id, title, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(board_id, user_id)
			 DO UPDATE SET title=excluded.title, updated_at=excluded.updated_at`,
			boardID, userID, strings.TrimSpace(title), ts, ts,
		); err != nil {
			return err
		}
	case "blacklisted":
		if _, err := QExec(tx, `DELETE FROM board_members WHERE board_id=? AND user_id=?`, boardID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ReviewBoardMemberApplicationTx(tx *sql.Tx, applicationID, boardID, userID, reviewerID, status, title, reviewNote string, reviewedAt int64) error {
	if _, err := QExec(tx,
		`INSERT INTO board_member_applications (id, board_id, user_id, status, note, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', '', ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		applicationID, boardID, userID, reviewedAt, reviewedAt,
	); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`UPDATE board_member_applications
		    SET status=?, title=?, reviewer_id=?, review_note=?, updated_at=?, reviewed_at=?
		  WHERE id=?`,
		status, strings.TrimSpace(title), reviewerID, strings.TrimSpace(reviewNote), reviewedAt, reviewedAt, applicationID,
	); err != nil {
		return err
	}
	switch status {
	case "approved":
		if _, err := QExec(tx,
			`INSERT INTO board_members (board_id, user_id, title, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(board_id, user_id)
			 DO UPDATE SET title=excluded.title, updated_at=excluded.updated_at`,
			boardID, userID, strings.TrimSpace(title), reviewedAt, reviewedAt,
		); err != nil {
			return err
		}
	case "blacklisted":
		if _, err := QExec(tx, `DELETE FROM board_members WHERE board_id=? AND user_id=?`, boardID, userID); err != nil {
			return err
		}
	}
	return nil
}

func UpsertDigestEntry(db *sql.DB, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	entryID, err := UpsertDigestEntryTx(tx, id, boardID, targetKind, targetID, kind, title, path, note, createdBy, NowMS())
	if err != nil {
		return "", err
	}
	return entryID, tx.Commit()
}

func UpsertDigestEntryTx(tx *sql.Tx, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string, ts int64) (string, error) {
	if ts <= 0 {
		ts = NowMS()
	}
	if _, err := QExec(tx,
		`INSERT INTO digest_entries (
		    id, board_id, target_kind, target_id, kind, title, path, note, created_by, created_at, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id, target_kind, target_id, kind, path) DO NOTHING`,
		id, boardID, targetKind, targetID, kind, title, path, note, createdBy, ts, ts,
	); err != nil {
		return "", err
	}
	if _, err := QExec(tx,
		`UPDATE digest_entries
		    SET title=?, note=?, updated_at=?
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?`,
		title, note, ts, boardID, targetKind, targetID, kind, path,
	); err != nil {
		return "", err
	}
	var entryID string
	if err := QQueryRow(tx,
		`SELECT id FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?`,
		boardID, targetKind, targetID, kind, path,
	).Scan(&entryID); err != nil {
		return "", err
	}
	return entryID, nil
}

func RemoveDigestEntry(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := RemoveDigestEntryTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func RemoveDigestEntryTx(tx *sql.Tx, id string) error {
	_, err := QExec(tx, `DELETE FROM digest_entries WHERE id=?`, id)
	return err
}

func RemoveDigestEntryFinalTx(tx *sql.Tx, id, boardID, kind, removedBy string, ts int64) error {
	if err := RecordDigestEntryRemovalTx(tx, id, boardID, kind, removedBy, ts); err != nil {
		return err
	}
	return RemoveDigestEntryTx(tx, id)
}

func RecordDigestEntryRemovalTx(tx *sql.Tx, id, boardID, kind, removedBy string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO digest_entry_removals (id, board_id, kind, removed_by, removed_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE
		       SET board_id=excluded.board_id,
		           kind=excluded.kind,
		           removed_by=excluded.removed_by,
		           removed_at=excluded.removed_at`,
		id, boardID, kind, removedBy, ts,
	)
	return err
}

func UpdateDigestEntry(db *sql.DB, id, title, path, note string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := UpdateDigestEntryTx(tx, id, title, path, note, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func UpdateDigestEntryTx(tx *sql.Tx, id, title, path, note string, ts int64) error {
	if ts <= 0 {
		ts = NowMS()
	}
	_, err := QExec(tx,
		`UPDATE digest_entries
		    SET title=?, path=?, note=?, updated_at=?
		  WHERE id=?`,
		title, path, note, ts, id,
	)
	return err
}

func SetDigestEntryBody(db *sql.DB, id, body string, edited bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := SetDigestEntryBodyTx(tx, id, body, edited, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func SetDigestEntryBodyTx(tx *sql.Tx, id, body string, edited bool, ts int64) error {
	if ts <= 0 {
		ts = NowMS()
	}
	_, err := QExec(tx,
		`UPDATE digest_entries
		    SET body=?, body_edited=?, updated_at=?
		  WHERE id=?`,
		body, boolInt(edited), ts, id,
	)
	return err
}

func UpsertDigestDirectory(db *sql.DB, id, boardID, kind, path, createdBy string) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	directoryID, err := UpsertDigestDirectoryTx(tx, id, boardID, kind, path, createdBy, NowMS())
	if err != nil {
		return "", err
	}
	return directoryID, tx.Commit()
}

func UpsertDigestDirectoryTx(tx *sql.Tx, id, boardID, kind, path, createdBy string, ts int64) (string, error) {
	if ts <= 0 {
		ts = NowMS()
	}
	if _, err := QExec(tx,
		`INSERT INTO digest_directories (id, board_id, kind, path, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id, kind, path) DO NOTHING`,
		id, boardID, kind, path, createdBy, ts, ts,
	); err != nil {
		return "", err
	}
	if _, err := QExec(tx,
		`UPDATE digest_directories
		    SET updated_at=?
		  WHERE board_id=? AND kind=? AND path=?`,
		ts, boardID, kind, path,
	); err != nil {
		return "", err
	}
	var directoryID string
	if err := QQueryRow(tx,
		`SELECT id FROM digest_directories
		  WHERE board_id=? AND kind=? AND path=?`,
		boardID, kind, path,
	).Scan(&directoryID); err != nil {
		return "", err
	}
	return directoryID, nil
}

func CountDigestPathEntries(db *sql.DB, boardID, kind, path string) (int, error) {
	entries, err := digestPathEntries(db, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func CountDigestPathDirectories(db *sql.DB, boardID, kind, path string) (int, error) {
	dirs, err := digestPathDirectories(db, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	return len(dirs), nil
}

type digestPathEntryRow struct {
	ID         string
	BoardID    string
	TargetKind string
	TargetID   string
	Kind       string
	Title      string
	Path       string
	Note       string
	Body       string
	BodyEdited int
	CreatedBy  string
	CreatedAt  int64
	UpdatedAt  int64
}

type digestPathDirectoryRow struct {
	ID        string
	BoardID   string
	Kind      string
	Path      string
	CreatedBy string
	CreatedAt int64
	UpdatedAt int64
}

func MoveDigestPath(db *sql.DB, boardID, kind, fromPath, toPath string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count, err := MoveDigestPathTx(tx, boardID, kind, fromPath, toPath, NowMS())
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func MoveDigestPathTx(tx *sql.Tx, boardID, kind, fromPath, toPath string, ts int64) (int, error) {
	if ts <= 0 {
		ts = NowMS()
	}
	entries, err := digestPathEntries(tx, boardID, kind, fromPath)
	if err != nil {
		return 0, err
	}
	dirs, err := digestPathDirectories(tx, boardID, kind, fromPath)
	if err != nil {
		return 0, err
	}
	movingIDs := map[string]struct{}{}
	for _, entry := range entries {
		movingIDs[entry.ID] = struct{}{}
	}
	movingDirIDs := map[string]struct{}{}
	for _, dir := range dirs {
		movingDirIDs[dir.ID] = struct{}{}
	}
	newPaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		newPath := remapDigestPath(entry.Path, fromPath, toPath)
		if err := ensureDigestPathAvailable(tx, entry, newPath, movingIDs); err != nil {
			return 0, err
		}
		newPaths[entry.ID] = newPath
	}
	newDirPaths := make(map[string]string, len(dirs))
	for _, dir := range dirs {
		newPath := remapDigestPath(dir.Path, fromPath, toPath)
		if err := ensureDigestDirectoryAvailable(tx, dir, newPath, movingDirIDs); err != nil {
			return 0, err
		}
		newDirPaths[dir.ID] = newPath
	}
	for _, entry := range entries {
		if _, err := QExec(tx, `UPDATE digest_entries SET path=?, updated_at=? WHERE id=?`, newPaths[entry.ID], ts, entry.ID); err != nil {
			return 0, err
		}
	}
	for _, dir := range dirs {
		if _, err := QExec(tx, `UPDATE digest_directories SET path=?, updated_at=? WHERE id=?`, newDirPaths[dir.ID], ts, dir.ID); err != nil {
			return 0, err
		}
	}
	return len(entries) + len(dirs), nil
}

func MoveDigestPathFinalTx(tx *sql.Tx, eventID, boardID, kind, fromPath, toPath, actorID string, ts int64) (int, error) {
	count, err := MoveDigestPathTx(tx, boardID, kind, fromPath, toPath, ts)
	if err != nil {
		return 0, err
	}
	if err := RecordDigestPathMutationTx(tx, eventID, "move", boardID, kind, fromPath, toPath, actorID, ts, count); err != nil {
		return 0, err
	}
	return count, nil
}

func CopyDigestPath(db *sql.DB, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count, err := CopyDigestPathTx(tx, boardID, kind, fromPath, toPath, createdBy, entryIDs, directoryIDs, NowMS())
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func CopyDigestPathTx(tx *sql.Tx, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string, ts int64) (int, error) {
	if ts <= 0 {
		ts = NowMS()
	}
	entries, err := digestPathEntries(tx, boardID, kind, fromPath)
	if err != nil {
		return 0, err
	}
	dirs, err := digestPathDirectories(tx, boardID, kind, fromPath)
	if err != nil {
		return 0, err
	}
	if len(entryIDs) < len(entries) {
		return 0, fmt.Errorf("not enough digest copy ids")
	}
	if len(directoryIDs) < len(dirs) {
		return 0, fmt.Errorf("not enough digest directory copy ids")
	}
	for _, entry := range entries {
		newPath := remapDigestPath(entry.Path, fromPath, toPath)
		if err := ensureDigestPathAvailable(tx, entry, newPath, nil); err != nil {
			return 0, err
		}
	}
	for _, dir := range dirs {
		newPath := remapDigestPath(dir.Path, fromPath, toPath)
		if err := ensureDigestDirectoryAvailable(tx, dir, newPath, nil); err != nil {
			return 0, err
		}
	}
	for i, entry := range entries {
		newPath := remapDigestPath(entry.Path, fromPath, toPath)
		if _, err := QExec(tx,
			`INSERT INTO digest_entries (
			    id, board_id, target_kind, target_id, kind, title, path, note,
			    body, body_edited, created_by, created_at, updated_at
			 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entryIDs[i], entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, entry.Title, newPath, entry.Note,
			entry.Body, entry.BodyEdited, createdBy, ts, ts,
		); err != nil {
			return 0, err
		}
	}
	for i, dir := range dirs {
		newPath := remapDigestPath(dir.Path, fromPath, toPath)
		if _, err := QExec(tx,
			`INSERT INTO digest_directories (id, board_id, kind, path, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			directoryIDs[i], dir.BoardID, dir.Kind, newPath, createdBy, ts, ts,
		); err != nil {
			return 0, err
		}
	}
	return len(entries) + len(dirs), nil
}

func DeleteDigestPath(db *sql.DB, boardID, kind, path string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count, err := DeleteDigestPathTx(tx, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func DeleteDigestPathTx(tx *sql.Tx, boardID, kind, path string) (int, error) {
	entries, err := digestPathEntries(tx, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	dirs, err := digestPathDirectories(tx, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if _, err := QExec(tx, `DELETE FROM digest_entries WHERE id=?`, entry.ID); err != nil {
			return 0, err
		}
	}
	for _, dir := range dirs {
		if _, err := QExec(tx, `DELETE FROM digest_directories WHERE id=?`, dir.ID); err != nil {
			return 0, err
		}
	}
	return len(entries) + len(dirs), nil
}

func DeleteDigestPathFinalTx(tx *sql.Tx, eventID, boardID, kind, path, actorID string, ts int64) (int, error) {
	count, err := DeleteDigestPathTx(tx, boardID, kind, path)
	if err != nil {
		return 0, err
	}
	if err := RecordDigestPathMutationTx(tx, eventID, "delete", boardID, kind, path, "", actorID, ts, count); err != nil {
		return 0, err
	}
	return count, nil
}

func RecordDigestPathMutationTx(tx *sql.Tx, eventID, action, boardID, kind, fromPath, toPath, actorID string, ts int64, count int) error {
	if eventID == "" {
		return nil
	}
	_, err := QExec(tx,
		`INSERT INTO digest_path_mutations (event_id, action, board_id, kind, from_path, to_path, actor_id, ts, count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(event_id) DO UPDATE
		       SET action=excluded.action,
		           board_id=excluded.board_id,
		           kind=excluded.kind,
		           from_path=excluded.from_path,
		           to_path=excluded.to_path,
		           actor_id=excluded.actor_id,
		           ts=excluded.ts,
		           count=excluded.count`,
		eventID, action, boardID, kind, fromPath, toPath, actorID, ts, count,
	)
	return err
}

func digestPathEntries(db sqlLike, boardID, kind, path string) ([]digestPathEntryRow, error) {
	rows, err := QQuery(db,
		`SELECT id, board_id, target_kind, target_id, kind, title, path, note,
		        body, body_edited, created_by, created_at, updated_at
		   FROM digest_entries
		  WHERE board_id=? AND kind=?
		  ORDER BY path, title, id`,
		boardID, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []digestPathEntryRow{}
	for rows.Next() {
		var entry digestPathEntryRow
		if err := rows.Scan(&entry.ID, &entry.BoardID, &entry.TargetKind, &entry.TargetID, &entry.Kind, &entry.Title, &entry.Path, &entry.Note, &entry.Body, &entry.BodyEdited, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		if digestPathContains(path, entry.Path) {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func digestPathDirectories(db sqlLike, boardID, kind, path string) ([]digestPathDirectoryRow, error) {
	rows, err := QQuery(db,
		`SELECT id, board_id, kind, path, created_by, created_at, updated_at
		   FROM digest_directories
		  WHERE board_id=? AND kind=?
		  ORDER BY path, id`,
		boardID, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dirs := []digestPathDirectoryRow{}
	for rows.Next() {
		var dir digestPathDirectoryRow
		if err := rows.Scan(&dir.ID, &dir.BoardID, &dir.Kind, &dir.Path, &dir.CreatedBy, &dir.CreatedAt, &dir.UpdatedAt); err != nil {
			return nil, err
		}
		if digestPathContains(path, dir.Path) {
			dirs = append(dirs, dir)
		}
	}
	return dirs, rows.Err()
}

func ensureDigestPathAvailable(db sqlLike, entry digestPathEntryRow, newPath string, ignoredIDs map[string]struct{}) error {
	var conflictID string
	err := QQueryRow(
		db,
		`SELECT id
		   FROM digest_entries
		  WHERE board_id=? AND target_kind=? AND target_id=? AND kind=? AND path=?
		  LIMIT 1`,
		entry.BoardID, entry.TargetKind, entry.TargetID, entry.Kind, newPath,
	).Scan(&conflictID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if ignoredIDs != nil {
		if _, ok := ignoredIDs[conflictID]; ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrDigestPathConflict, newPath)
}

func ensureDigestDirectoryAvailable(db sqlLike, dir digestPathDirectoryRow, newPath string, ignoredIDs map[string]struct{}) error {
	var conflictID string
	err := QQueryRow(
		db,
		`SELECT id
		   FROM digest_directories
		  WHERE board_id=? AND kind=? AND path=?
		  LIMIT 1`,
		dir.BoardID, dir.Kind, newPath,
	).Scan(&conflictID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if ignoredIDs != nil {
		if _, ok := ignoredIDs[conflictID]; ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrDigestPathConflict, newPath)
}

func digestPathContains(parent, child string) bool {
	if parent == "" {
		return child == ""
	}
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func remapDigestPath(path, fromPath, toPath string) string {
	if path == fromPath {
		return toPath
	}
	suffix := strings.TrimPrefix(path, fromPath+"/")
	if toPath == "" {
		return suffix
	}
	if suffix == "" {
		return toPath
	}
	return toPath + "/" + suffix
}

func InsertMailMessage(tx *sql.Tx, id, fromUserID, subject, body, parentID string, createdAt, seq int64) error {
	_, err := QExec(tx,
		`INSERT INTO mail_messages (id, from_user_id, subject, body, parent_id, created_at, seq)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, fromUserID, subject, body, parentID, createdAt, seq,
	)
	return err
}

func InsertMailCopy(tx *sql.Tx, messageID, userID, role, mailbox string, read, kept bool, updatedAt int64) error {
	_, err := QExec(tx,
		`INSERT INTO mail_copies (message_id, user_id, role, mailbox, read, kept, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(message_id, user_id, role)
		 DO UPDATE SET mailbox=excluded.mailbox, read=excluded.read, kept=excluded.kept, updated_at=excluded.updated_at`,
		messageID, userID, role, mailbox, boolInt(read), boolInt(kept), updatedAt,
	)
	return err
}

func UpdateMailCopy(db *sql.DB, userID, messageID string, mailbox *string, read, kept *bool) (bool, error) {
	return updateMailCopy(db, userID, messageID, mailbox, read, kept, NowMS())
}

func UpdateMailCopyTx(tx *sql.Tx, userID, messageID string, mailbox *string, read, kept *bool, updatedAt int64) (bool, error) {
	return updateMailCopy(tx, userID, messageID, mailbox, read, kept, updatedAt)
}

func updateMailCopy(execable sqlLike, userID, messageID string, mailbox *string, read, kept *bool, updatedAt int64) (bool, error) {
	clauses := []string{}
	args := []any{}
	if mailbox != nil {
		clauses = append(clauses, "mailbox=?")
		args = append(args, *mailbox)
	}
	if read != nil {
		clauses = append(clauses, "read=?")
		args = append(args, boolInt(*read))
	}
	if kept != nil {
		clauses = append(clauses, "kept=?")
		args = append(args, boolInt(*kept))
	}
	if len(clauses) == 0 {
		return false, nil
	}
	clauses = append(clauses, "updated_at=?")
	args = append(args, updatedAt, userID, messageID)

	res, err := QExec(execable,
		`UPDATE mail_copies SET `+strings.Join(clauses, ", ")+` WHERE user_id=? AND message_id=?`,
		args...,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return true, nil
	}
	return n > 0, nil
}

func TrashMailCopy(db *sql.DB, userID, messageID string) (bool, error) {
	mailbox := "trash"
	return UpdateMailCopy(db, userID, messageID, &mailbox, nil, nil)
}

func SetMailGroup(db *sql.DB, ownerID, groupID, name string, memberIDs []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := setMailGroup(tx, ownerID, groupID, name, memberIDs, NowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

func SetMailGroupTx(tx *sql.Tx, ownerID, groupID, name string, memberIDs []string, updatedAt int64) error {
	return setMailGroup(tx, ownerID, groupID, name, memberIDs, updatedAt)
}

func setMailGroup(execable sqlLike, ownerID, groupID, name string, memberIDs []string, updatedAt int64) error {
	if _, err := QExec(execable,
		`INSERT INTO mail_groups (id, user_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id)
		 DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		groupID, ownerID, strings.TrimSpace(name), updatedAt, updatedAt,
	); err != nil {
		return err
	}
	if _, err := QExec(execable, `DELETE FROM mail_group_members WHERE group_id=?`, groupID); err != nil {
		return err
	}
	for i, memberID := range memberIDs {
		if _, err := QExec(execable,
			`INSERT INTO mail_group_members (group_id, user_id, position, created_at)
			 VALUES (?, ?, ?, ?)`,
			groupID, memberID, i, updatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func DeleteMailGroup(db *sql.DB, ownerID, groupID string) (bool, error) {
	return deleteMailGroup(db, ownerID, groupID)
}

func DeleteMailGroupTx(tx *sql.Tx, ownerID, groupID string) (bool, error) {
	return deleteMailGroup(tx, ownerID, groupID)
}

func DeleteMailGroupFinalTx(tx *sql.Tx, eventID, ownerID, groupID string, ts int64) (bool, error) {
	if err := RecordMailGroupDeletionTx(tx, eventID, ownerID, groupID, ts); err != nil {
		return false, err
	}
	return DeleteMailGroupTx(tx, ownerID, groupID)
}

func RecordMailGroupDeletionTx(tx *sql.Tx, eventID, ownerID, groupID string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO mail_group_deletions (event_id, owner_id, group_id, deleted_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(event_id)
		 DO UPDATE SET owner_id=excluded.owner_id,
		               group_id=excluded.group_id,
		               deleted_at=excluded.deleted_at`,
		eventID, ownerID, groupID, ts,
	)
	return err
}

func deleteMailGroup(execable sqlLike, ownerID, groupID string) (bool, error) {
	res, err := QExec(execable, `DELETE FROM mail_groups WHERE user_id=? AND id=?`, ownerID, groupID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return true, nil
	}
	return n > 0, nil
}

func InsertDirectMessage(tx *sql.Tx, id, conversationID, fromUserID, toUserID, body string, createdAt, seq int64) error {
	_, err := QExec(tx,
		`INSERT INTO direct_messages (
		    id, conversation_id, from_user_id, to_user_id, body, read_at,
		    sender_deleted, recipient_deleted, created_at, seq
		 ) VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?, ?)`,
		id, conversationID, fromUserID, toUserID, body, createdAt, seq,
	)
	return err
}

func MarkDirectMessageRead(db *sql.DB, userID, messageID string) (bool, error) {
	return markDirectMessageRead(db, userID, messageID, NowMS())
}

func MarkDirectMessageReadTx(tx *sql.Tx, userID, messageID string, readAt int64) (bool, error) {
	return markDirectMessageRead(tx, userID, messageID, readAt)
}

func markDirectMessageRead(execable sqlLike, userID, messageID string, readAt int64) (bool, error) {
	res, err := QExec(execable,
		`UPDATE direct_messages
		    SET read_at=CASE WHEN read_at=0 THEN ? ELSE read_at END
		  WHERE id=? AND to_user_id=? AND recipient_deleted=0`,
		readAt, messageID, userID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return true, nil
	}
	return n > 0, nil
}

func DeleteDirectMessage(db *sql.DB, userID, messageID string) (bool, error) {
	res, err := QExec(db,
		`UPDATE direct_messages
		    SET sender_deleted = CASE WHEN from_user_id=? THEN 1 ELSE sender_deleted END,
		        recipient_deleted = CASE WHEN to_user_id=? THEN 1 ELSE recipient_deleted END
		  WHERE id=? AND (from_user_id=? OR to_user_id=?)`,
		userID, userID, messageID, userID, userID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return true, nil
	}
	return n > 0, nil
}

func DeleteDirectMessageFlagsTx(tx *sql.Tx, messageID string, senderDeleted, recipientDeleted bool) (bool, error) {
	setSender := 0
	if senderDeleted {
		setSender = 1
	}
	setRecipient := 0
	if recipientDeleted {
		setRecipient = 1
	}
	res, err := QExec(tx,
		`UPDATE direct_messages
		    SET sender_deleted = CASE WHEN ?=1 THEN 1 ELSE sender_deleted END,
		        recipient_deleted = CASE WHEN ?=1 THEN 1 ELSE recipient_deleted END
		  WHERE id=?`,
		setSender, setRecipient, messageID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return true, nil
	}
	return n > 0, nil
}

func SetDirectMessageSettings(db *sql.DB, userID, policy string) error {
	return setDirectMessageSettings(db, userID, policy, NowMS())
}

func SetDirectMessageSettingsTx(tx *sql.Tx, userID, policy string, updatedAt int64) error {
	return setDirectMessageSettings(tx, userID, policy, updatedAt)
}

func setDirectMessageSettings(execable sqlLike, userID, policy string, updatedAt int64) error {
	_, err := QExec(execable,
		`INSERT INTO direct_message_settings (user_id, policy, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id)
		 DO UPDATE SET policy=excluded.policy, updated_at=excluded.updated_at`,
		userID, strings.TrimSpace(policy), updatedAt,
	)
	return err
}

func SetUserRelationship(db *sql.DB, userID, targetUserID, kind, note string, active bool) error {
	return setUserRelationship(db, userID, targetUserID, kind, note, active, NowMS())
}

func SetUserRelationshipTx(tx *sql.Tx, userID, targetUserID, kind, note string, active bool, updatedAt int64) error {
	return setUserRelationship(tx, userID, targetUserID, kind, note, active, updatedAt)
}

func setUserRelationship(execable sqlLike, userID, targetUserID, kind, note string, active bool, updatedAt int64) error {
	if !active {
		_, err := QExec(execable,
			`DELETE FROM user_relationships WHERE user_id=? AND target_user_id=? AND kind=?`,
			userID, targetUserID, kind,
		)
		return err
	}
	_, err := QExec(execable,
		`INSERT INTO user_relationships (user_id, target_user_id, kind, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, target_user_id, kind)
		 DO UPDATE SET note=excluded.note, updated_at=excluded.updated_at`,
		userID, targetUserID, kind, strings.TrimSpace(note), updatedAt, updatedAt,
	)
	return err
}

func InsertBlessing(tx *sql.Tx, blessing *Blessing) error {
	_, err := QExec(tx,
		`INSERT INTO blessings (id, from_user_id, to_user_id, message, created_at, seq)
		 VALUES (?,?,?,?,?,?)`,
		blessing.ID, blessing.FromUserID, blessing.ToUserID, blessing.Message, blessing.CreatedAt, blessing.Seq,
	)
	return err
}

func SetUserPresence(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	return setUserPresence(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts, true)
}

func SetUserPresenceWithoutCommunityStatHistory(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	return setUserPresence(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts, false)
}

func setUserPresence(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64, updateCommunityStatHistory bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	status = strings.TrimSpace(status)
	mode = strings.TrimSpace(mode)
	boardID = strings.TrimSpace(boardID)
	threadID = strings.TrimSpace(threadID)
	locationLabel = strings.TrimSpace(locationLabel)
	fromHost = strings.TrimSpace(fromHost)
	var previousStatus string
	var previousMode string
	var previousBoardID string
	var previousThreadID string
	var previousLocationLabel string
	var previousFromHost string
	var previousLastSeen int64
	err := QQueryRow(db,
		`SELECT status, mode, board_id, thread_id, location_label, from_host, last_seen
		   FROM user_presence_sessions
		  WHERE user_id=? AND session_id=?`,
		userID,
		sessionID,
	).Scan(&previousStatus, &previousMode, &previousBoardID, &previousThreadID, &previousLocationLabel, &previousFromHost, &previousLastSeen)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil &&
		presenceStatusCountsOnline(status) &&
		previousStatus == status &&
		previousMode == mode &&
		previousBoardID == boardID &&
		previousThreadID == threadID &&
		previousLocationLabel == locationLabel &&
		previousFromHost == fromHost &&
		presencePingWithinCoalesceWindow(previousLastSeen, ts) {
		return nil
	}
	if seconds := presenceOnlineAccrualSeconds(previousStatus, previousLastSeen, ts); seconds > 0 {
		if err := RecordOnlineSeconds(db, userID, seconds); err != nil {
			return err
		}
	}
	_, err = QExec(db,
		`INSERT INTO user_presence_sessions (user_id, session_id, status, mode, board_id, thread_id, location_label, from_host, last_seen, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, session_id)
		 DO UPDATE SET
		    status=excluded.status,
		    mode=excluded.mode,
		    board_id=excluded.board_id,
		    thread_id=excluded.thread_id,
		    location_label=excluded.location_label,
		    from_host=excluded.from_host,
		    last_seen=excluded.last_seen,
		    updated_at=excluded.updated_at`,
		userID,
		sessionID,
		status,
		mode,
		boardID,
		threadID,
		locationLabel,
		fromHost,
		ts,
		ts,
	)
	if err != nil {
		return err
	}
	if err := refreshUserPresenceSummary(db, userID); err != nil {
		return err
	}
	if !updateCommunityStatHistory {
		return nil
	}
	return UpsertCommunityStatHistoryFromCurrent(db, ts)
}

func InsertChatLine(db *sql.DB, id, roomID, roomName, userID, userName, body string, ts int64) error {
	id = strings.TrimSpace(id)
	roomID = strings.TrimSpace(roomID)
	roomName = strings.TrimSpace(roomName)
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	body = strings.TrimSpace(body)
	if id == "" || roomID == "" || userID == "" || body == "" {
		return fmt.Errorf("chat line id, room, user, and body are required")
	}
	if roomName == "" {
		roomName = roomID
	}
	if ts <= 0 {
		ts = NowMS()
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if _, err := QExec(tx,
		`INSERT INTO chat_rooms (id, name, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id)
		 DO UPDATE SET
		    name=CASE WHEN chat_rooms.name='' THEN excluded.name ELSE chat_rooms.name END,
		    updated_at=excluded.updated_at`,
		roomID,
		roomName,
		userID,
		ts,
		ts,
	); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`INSERT INTO chat_lines (id, room_id, user_id, user_name, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		roomID,
		userID,
		userName,
		body,
		ts,
	); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`DELETE FROM chat_lines
		  WHERE room_id=?
		    AND id NOT IN (
		      SELECT id FROM chat_lines
		       WHERE room_id=?
		       ORDER BY created_at DESC, id DESC
		       LIMIT 200
		    )`,
		roomID,
		roomID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func SetGuestPresence(db *sql.DB, sessionID, status, locationLabel, fromHost string, ts int64) error {
	return setGuestPresence(db, sessionID, status, locationLabel, fromHost, ts, true)
}

func SetGuestPresenceWithoutCommunityStatHistory(db *sql.DB, sessionID, status, locationLabel, fromHost string, ts int64) error {
	return setGuestPresence(db, sessionID, status, locationLabel, fromHost, ts, false)
}

func setGuestPresence(db *sql.DB, sessionID, status, locationLabel, fromHost string, ts int64, updateCommunityStatHistory bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("guest session id required")
	}
	if len(sessionID) > 120 {
		sessionID = sessionID[:120]
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}
	if len(status) > 40 {
		status = status[:40]
	}
	locationLabel = strings.TrimSpace(locationLabel)
	if len(locationLabel) > 120 {
		locationLabel = locationLabel[:120]
	}
	fromHost = strings.TrimSpace(fromHost)
	if len(fromHost) > 120 {
		fromHost = fromHost[:120]
	}
	if ts <= 0 {
		ts = NowMS()
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	var previousStatus string
	var previousLocationLabel string
	var previousFromHost string
	var previousLastSeen int64
	err = QQueryRow(tx,
		`SELECT status, location_label, from_host, last_seen
		   FROM guest_presence_sessions
		  WHERE session_id=?`,
		sessionID,
	).Scan(&previousStatus, &previousLocationLabel, &previousFromHost, &previousLastSeen)
	hadPrevious := err == nil
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	wasOnline := hadPrevious && guestPresenceStatusCountsOnline(previousStatus)
	isOnline := guestPresenceStatusCountsOnline(status)
	if hadPrevious &&
		isOnline &&
		previousStatus == status &&
		previousLocationLabel == locationLabel &&
		previousFromHost == fromHost &&
		presencePingWithinCoalesceWindow(previousLastSeen, ts) {
		return nil
	}

	if !wasOnline && isOnline {
		if err := incrementCommunityCounterTx(tx, "total_guest_logins", ts); err != nil {
			return err
		}
	} else if wasOnline && !isOnline {
		if err := incrementCommunityCounterTx(tx, "total_guest_logouts", ts); err != nil {
			return err
		}
	}

	_, err = QExec(tx,
		`INSERT INTO guest_presence_sessions (session_id, status, location_label, from_host, last_seen, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id)
		 DO UPDATE SET
		    status=excluded.status,
		    location_label=excluded.location_label,
		    from_host=excluded.from_host,
		    last_seen=excluded.last_seen,
		    updated_at=excluded.updated_at`,
		sessionID,
		status,
		locationLabel,
		fromHost,
		ts,
		ts,
	)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !updateCommunityStatHistory {
		return nil
	}
	return UpsertCommunityStatHistoryFromCurrent(db, ts)
}

const (
	maxPresenceAccrualMS                int64 = 5 * 60 * 1000
	presenceUnchangedWriteMinIntervalMS int64 = 30 * 1000
)

func presencePingWithinCoalesceWindow(previousLastSeen, ts int64) bool {
	if previousLastSeen <= 0 {
		return false
	}
	return ts <= previousLastSeen || ts-previousLastSeen < presenceUnchangedWriteMinIntervalMS
}

func presenceOnlineAccrualSeconds(previousStatus string, previousLastSeen, ts int64) int64 {
	if !presenceStatusCountsOnline(previousStatus) || previousLastSeen <= 0 || ts <= previousLastSeen {
		return 0
	}
	elapsed := ts - previousLastSeen
	if elapsed > maxPresenceAccrualMS {
		elapsed = maxPresenceAccrualMS
	}
	return elapsed / 1000
}

func presenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "invisible", "cloak", "cloaked":
		return false
	default:
		return true
	}
}

func guestPresenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "inactive":
		return false
	default:
		return true
	}
}

func UpsertCommunityStatHistoryFromCurrent(db *sql.DB, ts int64) error {
	if ts <= 0 {
		ts = NowMS()
	}
	stats, err := getCommunityStatsCurrent(db)
	if err != nil {
		return err
	}
	day := time.UnixMilli(ts).UTC().Format("2006-01-02")
	maxOnlineAt := int64(0)
	if stats.OnlineUsers > 0 {
		maxOnlineAt = ts
	}
	maxGuestAt := int64(0)
	if stats.OnlineGuests > 0 {
		maxGuestAt = ts
	}
	_, err = QExec(db,
		`INSERT INTO community_stat_history (
		    day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		    total_reactions, total_mail, total_direct_messages, total_logins,
		    total_logouts, total_web_logins, total_web_logouts,
		    total_guest_logins, total_guest_logouts, total_online_seconds,
		    online_users, online_guests, max_online_users, max_online_at,
		    max_online_guests, max_online_guests_at, head_seq
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(day)
		 DO UPDATE SET
		    snapshot_at=excluded.snapshot_at,
		    total_users=excluded.total_users,
		    total_boards=excluded.total_boards,
		    total_threads=excluded.total_threads,
		    total_posts=excluded.total_posts,
		    total_reactions=excluded.total_reactions,
		    total_mail=excluded.total_mail,
		    total_direct_messages=excluded.total_direct_messages,
		    total_logins=excluded.total_logins,
		    total_logouts=excluded.total_logouts,
		    total_web_logins=excluded.total_web_logins,
		    total_web_logouts=excluded.total_web_logouts,
		    total_guest_logins=excluded.total_guest_logins,
		    total_guest_logouts=excluded.total_guest_logouts,
		    total_online_seconds=excluded.total_online_seconds,
		    online_users=excluded.online_users,
		    online_guests=excluded.online_guests,
		    max_online_users=CASE
		      WHEN excluded.online_users > community_stat_history.max_online_users THEN excluded.online_users
		      ELSE community_stat_history.max_online_users
		    END,
		    max_online_at=CASE
		      WHEN excluded.online_users > community_stat_history.max_online_users THEN excluded.max_online_at
		      ELSE community_stat_history.max_online_at
		    END,
		    max_online_guests=CASE
		      WHEN excluded.online_guests > community_stat_history.max_online_guests THEN excluded.online_guests
		      ELSE community_stat_history.max_online_guests
		    END,
		    max_online_guests_at=CASE
		      WHEN excluded.online_guests > community_stat_history.max_online_guests THEN excluded.max_online_guests_at
		      ELSE community_stat_history.max_online_guests_at
		    END,
		    head_seq=excluded.head_seq`,
		day,
		ts,
		stats.TotalUsers,
		stats.TotalBoards,
		stats.TotalThreads,
		stats.TotalPosts,
		stats.TotalReactions,
		stats.TotalMail,
		stats.TotalDirectMessages,
		stats.TotalLogins,
		stats.TotalLogouts,
		stats.TotalWebLogins,
		stats.TotalWebLogouts,
		stats.TotalGuestLogins,
		stats.TotalGuestLogouts,
		stats.TotalOnlineSeconds,
		stats.OnlineUsers,
		stats.OnlineGuests,
		stats.OnlineUsers,
		maxOnlineAt,
		stats.OnlineGuests,
		maxGuestAt,
		stats.HeadSeq,
	)
	return err
}

func UpsertCommunityStatHistoryTx(tx *sql.Tx, history CommunityStatHistory) error {
	if strings.TrimSpace(history.Day) == "" {
		return fmt.Errorf("community stat history day is required")
	}
	_, err := QExec(tx,
		`INSERT INTO community_stat_history (
		    day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		    total_reactions, total_mail, total_direct_messages, total_logins,
		    total_logouts, total_web_logins, total_web_logouts,
		    total_guest_logins, total_guest_logouts, total_online_seconds,
		    online_users, online_guests, max_online_users, max_online_at,
		    max_online_guests, max_online_guests_at, head_seq
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(day)
		 DO UPDATE SET
		    snapshot_at=excluded.snapshot_at,
		    total_users=excluded.total_users,
		    total_boards=excluded.total_boards,
		    total_threads=excluded.total_threads,
		    total_posts=excluded.total_posts,
		    total_reactions=excluded.total_reactions,
		    total_mail=excluded.total_mail,
		    total_direct_messages=excluded.total_direct_messages,
		    total_logins=excluded.total_logins,
		    total_logouts=excluded.total_logouts,
		    total_web_logins=excluded.total_web_logins,
		    total_web_logouts=excluded.total_web_logouts,
		    total_guest_logins=excluded.total_guest_logins,
		    total_guest_logouts=excluded.total_guest_logouts,
		    total_online_seconds=excluded.total_online_seconds,
		    online_users=excluded.online_users,
		    online_guests=excluded.online_guests,
		    max_online_users=CASE
		      WHEN excluded.max_online_users > community_stat_history.max_online_users THEN excluded.max_online_users
		      ELSE community_stat_history.max_online_users
		    END,
		    max_online_at=CASE
		      WHEN excluded.max_online_users > community_stat_history.max_online_users THEN excluded.max_online_at
		      ELSE community_stat_history.max_online_at
		    END,
		    max_online_guests=CASE
		      WHEN excluded.max_online_guests > community_stat_history.max_online_guests THEN excluded.max_online_guests
		      ELSE community_stat_history.max_online_guests
		    END,
		    max_online_guests_at=CASE
		      WHEN excluded.max_online_guests > community_stat_history.max_online_guests THEN excluded.max_online_guests_at
		      ELSE community_stat_history.max_online_guests_at
		    END,
		    head_seq=excluded.head_seq`,
		history.Day,
		history.SnapshotAt,
		history.TotalUsers,
		history.TotalBoards,
		history.TotalThreads,
		history.TotalPosts,
		history.TotalReactions,
		history.TotalMail,
		history.TotalDirectMessages,
		history.TotalLogins,
		history.TotalLogouts,
		history.TotalWebLogins,
		history.TotalWebLogouts,
		history.TotalGuestLogins,
		history.TotalGuestLogouts,
		history.TotalOnlineSeconds,
		history.OnlineUsers,
		history.OnlineGuests,
		history.MaxOnlineUsers,
		history.MaxOnlineAt,
		history.MaxOnlineGuests,
		history.MaxOnlineGuestsAt,
		history.HeadSeq,
	)
	return err
}

func refreshUserPresenceSummary(db *sql.DB, userID string) error {
	var status, mode, boardID, threadID, locationLabel, fromHost string
	var lastSeen, updatedAt int64
	err := QQueryRow(db,
		`SELECT status, mode, board_id, thread_id, location_label, from_host, last_seen, updated_at
		   FROM user_presence_sessions
		  WHERE user_id=?
		  ORDER BY CASE WHEN LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked') THEN 0 ELSE 1 END,
		           last_seen DESC, updated_at DESC, session_id
		  LIMIT 1`,
		userID,
	).Scan(&status, &mode, &boardID, &threadID, &locationLabel, &fromHost, &lastSeen, &updatedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = QExec(db,
		`INSERT INTO user_presence (user_id, status, mode, board_id, thread_id, location_label, from_host, last_seen, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id)
		 DO UPDATE SET
		    status=excluded.status,
		    mode=excluded.mode,
		    board_id=excluded.board_id,
		    thread_id=excluded.thread_id,
		    location_label=excluded.location_label,
		    from_host=excluded.from_host,
		    last_seen=excluded.last_seen,
		    updated_at=excluded.updated_at`,
		userID,
		status,
		mode,
		boardID,
		threadID,
		locationLabel,
		fromHost,
		lastSeen,
		updatedAt,
	)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func favoriteTargetPosition(tx *sql.Tx, userID, folderID string, position *int) (int, error) {
	if position != nil {
		if *position < 0 {
			return 0, nil
		}
		return *position, nil
	}
	var next int
	err := QQueryRow(tx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM board_favorites WHERE user_id=? AND folder_id=?`,
		userID, folderID,
	).Scan(&next)
	return next, err
}

func favoriteFolderTargetPosition(tx *sql.Tx, userID, parentID string, position *int) (int, error) {
	if position != nil {
		if *position < 0 {
			return 0, nil
		}
		return *position, nil
	}
	var next int
	err := QQueryRow(tx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM favorite_folders WHERE user_id=? AND parent_id=?`,
		userID, parentID,
	).Scan(&next)
	return next, err
}

func shiftFavoriteBoards(tx *sql.Tx, userID, folderID, boardID string, position int) error {
	_, err := QExec(tx,
		`UPDATE board_favorites
		    SET position=position + 1
		  WHERE user_id=? AND folder_id=? AND board_id<>? AND position>=?`,
		userID, folderID, boardID, position,
	)
	return err
}

func shiftFavoriteFolders(tx *sql.Tx, userID, parentID, folderID string, position int) error {
	_, err := QExec(tx,
		`UPDATE favorite_folders
		    SET position=position + 1
		  WHERE user_id=? AND parent_id=? AND id<>? AND position>=?`,
		userID, parentID, folderID, position,
	)
	return err
}

func MarkBoardRead(db *sql.DB, userID, boardID string) error {
	var oldSeq int64
	err := QQueryRow(db, `SELECT last_seq FROM board_read_markers WHERE user_id=? AND board_id=?`, userID, boardID).Scan(&oldSeq)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var headSeq int64
	if err := QQueryRow(db, `SELECT COALESCE(MAX(last_seq), 0) FROM threads WHERE board=?`, boardID).Scan(&headSeq); err != nil {
		return err
	}
	_, err = QExec(db,
		`INSERT INTO board_read_markers (user_id, board_id, last_seq, previous_seq, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, board_id)
		 DO UPDATE SET
		    last_seq=excluded.last_seq,
		    previous_seq=excluded.previous_seq,
		    updated_at=excluded.updated_at`,
		userID, boardID, headSeq, oldSeq, NowMS(),
	)
	return err
}

func RestoreBoardRead(db *sql.DB, userID, boardID string) error {
	res, err := QExec(db,
		`UPDATE board_read_markers
		    SET last_seq=previous_seq,
		        previous_seq=last_seq,
		        updated_at=?
		  WHERE user_id=? AND board_id=?`,
		NowMS(), userID, boardID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = QExec(db,
		`INSERT INTO board_read_markers (user_id, board_id, last_seq, previous_seq, updated_at)
		 VALUES (?, ?, 0, 0, ?)`,
		userID, boardID, NowMS(),
	)
	return err
}

func FavoriteBoardIDsInFolder(db *sql.DB, userID, folderID string) ([]string, error) {
	rows, err := QQuery(db,
		`WITH RECURSIVE folder_scope(id) AS (
		    SELECT ? WHERE ? <> ''
		    UNION ALL
		    SELECT child.id
		      FROM favorite_folders child
		      JOIN folder_scope parent ON parent.id = child.parent_id
		     WHERE child.user_id = ?
		)
		SELECT board_id
		  FROM board_favorites
		 WHERE user_id=?
		   AND (?='' OR folder_id=? OR folder_id IN (SELECT id FROM folder_scope))
		 ORDER BY folder_id, position, board_id`,
		folderID, folderID, userID, userID, folderID, folderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boardIDs []string
	for rows.Next() {
		var boardID string
		if err := rows.Scan(&boardID); err != nil {
			return nil, err
		}
		boardIDs = append(boardIDs, boardID)
	}
	return boardIDs, rows.Err()
}

func MarkFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	boardIDs, err := FavoriteBoardIDsInFolder(db, userID, folderID)
	if err != nil {
		return err
	}
	for _, boardID := range boardIDs {
		if err := MarkBoardRead(db, userID, boardID); err != nil {
			return err
		}
	}
	return nil
}

func RestoreFavoriteFolderRead(db *sql.DB, userID, folderID string) error {
	boardIDs, err := FavoriteBoardIDsInFolder(db, userID, folderID)
	if err != nil {
		return err
	}
	for _, boardID := range boardIDs {
		if err := RestoreBoardRead(db, userID, boardID); err != nil {
			return err
		}
	}
	return nil
}

func MarkThreadRead(db *sql.DB, userID, threadID string) error {
	var oldSeq int64
	err := QQueryRow(db, `SELECT last_seq FROM thread_read_markers WHERE user_id=? AND thread_id=?`, userID, threadID).Scan(&oldSeq)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var headSeq int64
	if err := QQueryRow(db, `SELECT last_seq FROM threads WHERE id=?`, threadID).Scan(&headSeq); err != nil {
		return err
	}
	_, err = QExec(db,
		`INSERT INTO thread_read_markers (user_id, thread_id, last_seq, previous_seq, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, thread_id)
		 DO UPDATE SET
		    last_seq=excluded.last_seq,
		    previous_seq=excluded.previous_seq,
		    updated_at=excluded.updated_at`,
		userID, threadID, headSeq, oldSeq, NowMS(),
	)
	return err
}

func RestoreThreadRead(db *sql.DB, userID, threadID string) error {
	res, err := QExec(db,
		`UPDATE thread_read_markers
		    SET last_seq=previous_seq,
		        previous_seq=last_seq,
		        updated_at=?
		  WHERE user_id=? AND thread_id=?`,
		NowMS(), userID, threadID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = QExec(db,
		`INSERT INTO thread_read_markers (user_id, thread_id, last_seq, previous_seq, updated_at)
		 VALUES (?, ?, 0, 0, ?)`,
		userID, threadID, NowMS(),
	)
	return err
}

func MarkPostRead(db *sql.DB, userID, postID string) error {
	var threadID string
	var postSeq int64
	if err := QQueryRow(db, `SELECT thread, created_seq FROM posts WHERE id=?`, postID).Scan(&threadID, &postSeq); err != nil {
		return err
	}
	var oldSeq int64
	err := QQueryRow(db, `SELECT last_seq FROM thread_read_markers WHERE user_id=? AND thread_id=?`, userID, threadID).Scan(&oldSeq)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	_, err = QExec(db,
		`INSERT INTO thread_read_markers (user_id, thread_id, last_seq, previous_seq, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, thread_id)
		 DO UPDATE SET
		    last_seq=excluded.last_seq,
		    previous_seq=excluded.previous_seq,
		    updated_at=excluded.updated_at`,
		userID, threadID, postSeq, oldSeq, NowMS(),
	)
	return err
}

func InsertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO moderation_reviews (id, kind, status, target_id, target_kind, reporter, reason, created_at, updated_at)
		 VALUES (?, ?, 'open', ?, ?, ?, ?, ?, ?)`,
		id, kind, targetID, targetKind, reporter, reason, ts, ts,
	)
	return err
}

func UpsertContentFilter(tx *sql.Tx, id, pattern, scope string, active bool, createdBy string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO content_filters (id, pattern, scope, active, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id)
		 DO UPDATE SET pattern=excluded.pattern, scope=excluded.scope, active=excluded.active, updated_at=excluded.updated_at`,
		id, pattern, scope, boolInt(active), createdBy, ts, ts,
	)
	return err
}

// UpsertBoardAutomodRule inserts or updates a board automod rule. created_by /
// created_at are preserved on update (only set on first insert).
func UpsertBoardAutomodRule(tx *sql.Tx, r BoardAutomodRule) error {
	_, err := QExec(tx,
		`INSERT INTO board_automod_rules
		   (id, board_id, enabled, priority, match_type, pattern, threshold, window_sec, action, duration_sec, reason, note, created_by, created_at, updated_by, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   enabled=excluded.enabled, priority=excluded.priority, match_type=excluded.match_type,
		   pattern=excluded.pattern, threshold=excluded.threshold, window_sec=excluded.window_sec,
		   action=excluded.action, duration_sec=excluded.duration_sec, reason=excluded.reason,
		   note=excluded.note, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		r.ID, r.Board, boolInt(r.Enabled), r.Priority, r.MatchType, r.Pattern, r.Threshold, r.WindowSec,
		r.Action, r.DurationSec, r.Reason, r.Note, r.CreatedBy, r.CreatedAt, r.UpdatedBy, r.UpdatedAt,
	)
	return err
}

// DeleteBoardAutomodRule removes a board automod rule scoped to its board.
func DeleteBoardAutomodRule(tx *sql.Tx, board, id string) error {
	_, err := QExec(tx, `DELETE FROM board_automod_rules WHERE id=? AND board_id=?`, id, board)
	return err
}

// InsertAutomodAuditLog records one fired automod rule.
func InsertAutomodAuditLog(tx *sql.Tx, a BoardAutomodActivity) error {
	_, err := QExec(tx,
		`INSERT INTO automod_audit_log (id, board_id, rule_id, match_type, action, target_user_id, post_id, thread_id, reason, ts)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO NOTHING`,
		a.ID, a.Board, a.RuleID, a.MatchType, a.Action, a.TargetUserID, a.PostID, a.ThreadID, a.Reason, a.TS,
	)
	return err
}

func ResolveModerationReview(tx *sql.Tx, id, actor, resolution string, ts int64) error {
	res, err := QExec(tx,
		`UPDATE moderation_reviews SET status='resolved', actor=?, resolution=?, updated_at=? WHERE id=? AND status='open'`,
		actor, resolution, ts, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func FtsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	if err := FtsDeletePost(tx, postID); err != nil {
		return err
	}
	_, err := QExec(tx,
		`INSERT INTO posts_fts (post_id, thread_id, board_id, author, body) VALUES (?,?,?,?,?)`,
		postID, threadID, boardID, author, body,
	)
	return err
}

func FtsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	_, err := QExec(tx, `UPDATE posts_fts SET body=? WHERE post_id=?`, newBody, postID)
	return err
}

func FtsDeletePost(tx *sql.Tx, postID string) error {
	_, err := QExec(tx, `DELETE FROM posts_fts WHERE post_id=?`, postID)
	return err
}

func InsertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	_, err := QExec(tx,
		`INSERT INTO user_sanctions (id, user_id, kind, scope, expires_at, by, reason, seq)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT (id) DO UPDATE SET
		   user_id=EXCLUDED.user_id, kind=EXCLUDED.kind, scope=EXCLUDED.scope,
		   expires_at=EXCLUDED.expires_at, by=EXCLUDED.by, reason=EXCLUDED.reason, seq=EXCLUDED.seq`,
		id, userID, kind, scope, expiresAt, by, reason, seq,
	)
	return err
}

func ClearUserSanctions(tx *sql.Tx, userID, kind, scope string) (int64, error) {
	var res sql.Result
	var err error
	if strings.TrimSpace(kind) == "" {
		res, err = QExec(tx,
			`DELETE FROM user_sanctions WHERE user_id=? AND scope=?`,
			userID, scope,
		)
	} else {
		res, err = QExec(tx,
			`DELETE FROM user_sanctions WHERE user_id=? AND kind=? AND scope=?`,
			userID, kind, scope,
		)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func CheckProcessed(db *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
	partitionKind, partitionKey = normalizeCommandPartition(partitionKind, partitionKey)
	var result, storedHash string
	err := QQueryRow(db,
		`SELECT result_json, command_hash
		   FROM processed_commands_v2
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=?`,
		partitionKind, partitionKey, actorID, cid,
	).Scan(&result, &storedHash)
	if err == sql.ErrNoRows {
		return "", false, false
	}
	if err != nil {
		return "", false, false
	}
	if storedHash != "" && storedHash != commandHash {
		return "", false, true
	}
	return result, true, false
}

func RecordProcessed(tx *sql.Tx, partitionKind, partitionKey, actorID, cid, commandHash, resultJSON string) error {
	partitionKind, partitionKey = normalizeCommandPartition(partitionKind, partitionKey)
	// Prune entries older than 10 minutes while we're here.
	cutoff := time.Now().Add(-10 * time.Minute).UnixMilli()
	if _, err := QExec(tx, `DELETE FROM processed_commands_v2 WHERE processed_at<?`, cutoff); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`DELETE FROM command_log_receipts
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=? AND status='retrying'`,
		partitionKind, partitionKey, actorID, cid,
	); err != nil {
		return err
	}
	_, err := QExec(tx,
		`INSERT INTO processed_commands_v2 (
		    partition_kind, partition_key, actor_id, cid, command_hash, result_json, processed_at
		) VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT (partition_kind, partition_key, actor_id, cid) DO UPDATE SET
		   command_hash=EXCLUDED.command_hash, result_json=EXCLUDED.result_json, processed_at=EXCLUDED.processed_at`,
		partitionKind, partitionKey, actorID, cid, commandHash, resultJSON, NowMS(),
	)
	return err
}

func normalizeCommandPartition(kind, key string) (string, string) {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" {
		kind = "global"
	}
	if key == "" {
		key = "global"
	}
	return kind, key
}

func UpsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	exists, err := reactionIdentityExists(tx, postID, userID)
	if err != nil {
		return err
	}
	if _, err := QExec(tx,
		`INSERT INTO post_reactions (post_id, user_id, emoji, ts) VALUES (?,?,?,?)
		 ON CONFLICT (post_id, user_id) DO UPDATE SET emoji=EXCLUDED.emoji, ts=EXCLUDED.ts`,
		postID, userID, emoji, ts,
	); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return incrementPostReactionCountShard(tx, postID, userID, ts)
}

func DeleteReaction(tx *sql.Tx, postID, userID string) error {
	result, err := QExec(tx, `DELETE FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil
	}
	return decrementPostReactionCountShard(tx, postID, userID)
}

func InsertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO polls (id, post_id, question, expires_at, ts) VALUES (?,?,?,?,?)`,
		id, postID, question, expiresAt, ts,
	)
	return err
}

func InsertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	_, err := QExec(tx,
		`INSERT INTO poll_options (id, poll_id, text, position) VALUES (?,?,?,?)`,
		id, pollID, text, position,
	)
	return err
}

func CastVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	previousOption, existed, err := pollVoteIdentity(tx, pollID, userID)
	if err != nil {
		return err
	}
	if _, err := QExec(tx,
		`INSERT INTO poll_votes (poll_id, option_id, user_id, ts) VALUES (?,?,?,?)
		 ON CONFLICT (poll_id, user_id) DO UPDATE SET option_id=EXCLUDED.option_id, ts=EXCLUDED.ts`,
		pollID, optionID, userID, ts,
	); err != nil {
		return err
	}
	if existed && previousOption == optionID {
		return nil
	}
	if existed {
		if err := decrementPollVoteCountShard(tx, pollID, previousOption, userID); err != nil {
			return err
		}
	}
	return incrementPollVoteCountShard(tx, pollID, optionID, userID, ts)
}

func DeletePollVote(tx *sql.Tx, pollID, userID string) error {
	previousOption, existed, err := pollVoteIdentity(tx, pollID, userID)
	if err != nil || !existed {
		return err
	}
	if _, err := QExec(tx, `DELETE FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, userID); err != nil {
		return err
	}
	return decrementPollVoteCountShard(tx, pollID, previousOption, userID)
}

func reactionIdentityExists(tx *sql.Tx, postID, userID string) (bool, error) {
	var exists int
	err := QQueryRow(tx, `SELECT 1 FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func pollVoteIdentity(tx *sql.Tx, pollID, userID string) (string, bool, error) {
	var optionID string
	err := QQueryRow(tx, `SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, userID).Scan(&optionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return optionID, err == nil, err
}

func incrementPostReactionCountShard(tx *sql.Tx, postID, userID string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO post_reaction_count_shards (post_id, shard, count_value, updated_at)
		 VALUES (?,?,1,?)
		 ON CONFLICT(post_id, shard)
		 DO UPDATE SET count_value=post_reaction_count_shards.count_value+1,
		               updated_at=EXCLUDED.updated_at`,
		postID, CounterShardForIdentity(userID), ts,
	)
	return err
}

func decrementPostReactionCountShard(tx *sql.Tx, postID, userID string) error {
	_, err := QExec(tx,
		`UPDATE post_reaction_count_shards
		    SET count_value=CASE WHEN count_value > 0 THEN count_value-1 ELSE 0 END
		  WHERE post_id=? AND shard=?`,
		postID, CounterShardForIdentity(userID),
	)
	return err
}

func incrementPollVoteCountShard(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO poll_vote_count_shards (poll_id, option_id, shard, count_value, updated_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(option_id, shard)
		 DO UPDATE SET count_value=poll_vote_count_shards.count_value+1,
		               updated_at=EXCLUDED.updated_at`,
		pollID, optionID, CounterShardForIdentity(userID), 1, ts,
	)
	return err
}

func decrementPollVoteCountShard(tx *sql.Tx, pollID, optionID, userID string) error {
	_, err := QExec(tx,
		`UPDATE poll_vote_count_shards
		    SET count_value=CASE WHEN count_value > 0 THEN count_value-1 ELSE 0 END
		  WHERE poll_id=? AND option_id=? AND shard=?`,
		pollID, optionID, CounterShardForIdentity(userID),
	)
	return err
}

// CounterShardForIdentity maps an identity key to a stable unordered-counter shard.
func CounterShardForIdentity(identity string) int {
	if identity == "" {
		return 0
	}
	var sum int
	for i := 0; i < len(identity); i++ {
		sum += int(identity[i])
	}
	return sum % 64
}

func InsertNotification(db sqlLike, id, userID, kind, threadID, postID, actor string, ts int64) error {
	_, err := QExec(db,
		`INSERT OR IGNORE INTO notifications (id, user_id, kind, thread_id, post_id, actor, read, ts)
		 VALUES (?,?,?,?,?,?,0,?)`,
		id, userID, kind, threadID, postID, actor, ts,
	)
	return err
}

func MarkNotificationRead(db *sql.DB, id, userID string) error {
	_, err := QExec(db,
		`UPDATE notifications SET read=1 WHERE id=? AND user_id=?`, id, userID,
	)
	return err
}

func MarkAllNotificationsRead(db *sql.DB, userID string) error {
	_, err := QExec(db, `UPDATE notifications SET read=1 WHERE user_id=?`, userID)
	return err
}

func DeleteNotification(db *sql.DB, id, userID string) error {
	_, err := QExec(db, `DELETE FROM notifications WHERE id=? AND user_id=?`, id, userID)
	return err
}

func DeleteReadNotifications(db *sql.DB, userID string) error {
	_, err := QExec(db, `DELETE FROM notifications WHERE user_id=? AND read=1`, userID)
	return err
}

func DeleteAllNotifications(db *sql.DB, userID string) error {
	_, err := QExec(db, `DELETE FROM notifications WHERE user_id=?`, userID)
	return err
}

func SetThreadPref(db *sql.DB, userID, threadID, level string) error {
	if level == "normal" {
		// "normal" = remove the row (default).
		_, err := QExec(db, `DELETE FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID)
		return err
	}
	_, err := QExec(db,
		`INSERT INTO thread_prefs (user_id, thread_id, level) VALUES (?,?,?)
		 ON CONFLICT (user_id, thread_id) DO UPDATE SET level=EXCLUDED.level`,
		userID, threadID, level,
	)
	return err
}

func EnsureActivity(db *sql.DB, userID string) error {
	_, err := QExec(db,
		`INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID,
	)
	return err
}

func RecordLogin(db *sql.DB, userID string) error {
	return RecordLoginAt(db, userID, NowMS())
}

func RecordLoginAt(db *sql.DB, userID string, ts int64) error {
	if ts <= 0 {
		ts = NowMS()
	}
	day := time.UnixMilli(ts).UTC()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if _, err := QExec(tx,
		`INSERT INTO user_activity (user_id, login_count)
		 VALUES (?, 1)
		 ON CONFLICT(user_id)
		 DO UPDATE SET login_count=user_activity.login_count + 1`,
		userID,
	); err != nil {
		return err
	}
	if _, err := QExec(tx,
		`INSERT INTO login_hourly_stats (day, hour, login_count, updated_at)
		 VALUES (?, ?, 1, ?)
		 ON CONFLICT(day, hour)
		 DO UPDATE SET
		    login_count=login_hourly_stats.login_count + excluded.login_count,
		    updated_at=excluded.updated_at`,
		day.Format("2006-01-02"),
		day.Hour(),
		ts,
	); err != nil {
		return err
	}
	if err := incrementCommunityCounterTx(tx, "total_web_logins", ts); err != nil {
		return err
	}
	return tx.Commit()
}

func RecordLogout(db *sql.DB) error {
	return RecordLogoutAt(db, NowMS())
}

func RecordLogoutAt(db *sql.DB, ts int64) error {
	return recordLogoutAt(db, ts, true)
}

func RecordLogoutAtWithoutCommunityStatHistory(db *sql.DB, ts int64) error {
	return recordLogoutAt(db, ts, false)
}

func recordLogoutAt(db *sql.DB, ts int64, updateCommunityStatHistory bool) error {
	if ts <= 0 {
		ts = NowMS()
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if err := incrementCommunityCounterTx(tx, "total_logouts", ts); err != nil {
		return err
	}
	if err := incrementCommunityCounterTx(tx, "total_web_logouts", ts); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !updateCommunityStatHistory {
		return nil
	}
	return UpsertCommunityStatHistoryFromCurrent(db, ts)
}

func incrementCommunityCounterTx(tx *sql.Tx, column string, ts int64) error {
	switch column {
	case "total_logouts", "total_web_logins", "total_web_logouts", "total_guest_logins", "total_guest_logouts":
	default:
		return fmt.Errorf("unknown community counter %q", column)
	}
	query := `INSERT INTO community_counter_totals (` + column + `, updated_at)
		 VALUES (1, ?)
		 ON CONFLICT(id)
		 DO UPDATE SET ` + column + `=community_counter_totals.` + column + ` + 1,
		               updated_at=excluded.updated_at`
	_, err := QExec(tx, query, ts)
	return err
}

func RecordOnlineSeconds(db *sql.DB, userID string, seconds int64) error {
	if seconds <= 0 {
		return nil
	}
	_, err := QExec(db,
		`INSERT INTO user_activity (user_id, total_online_seconds)
		 VALUES (?, ?)
		 ON CONFLICT(user_id)
		 DO UPDATE SET total_online_seconds=user_activity.total_online_seconds + excluded.total_online_seconds`,
		userID,
		seconds,
	)
	return err
}

func RecordPostCreated(db *sql.DB, userID string) (int, int, error) {
	today := NowDay()
	_, err := QExec(db, `INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID)
	if err != nil {
		return 0, 0, err
	}
	// Bump posts_created; conditionally bump days_visited.
	_, err = QExec(db, `
		UPDATE user_activity SET
		    posts_created = posts_created + 1,
		    days_visited  = days_visited + CASE WHEN last_visit_day != ? THEN 1 ELSE 0 END,
		    last_visit_day = ?
		 WHERE user_id = ?`, today, today, userID)
	if err != nil {
		return 0, 0, err
	}
	return RecomputeTrust(db, userID)
}

func RecordReactionReceived(db *sql.DB, postAuthorID string) error {
	_, err := QExec(db, `
		INSERT INTO user_activity (user_id, reactions_recv) VALUES (?,1)
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = user_activity.reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func RecordReactionRemoved(db *sql.DB, postAuthorID string) error {
	_, err := QExec(db, `
		UPDATE user_activity SET reactions_recv = CASE WHEN reactions_recv > 0 THEN reactions_recv - 1 ELSE 0 END WHERE user_id=?`,
		postAuthorID,
	)
	return err
}

func RecomputeTrust(db *sql.DB, userID string) (int, int, error) {
	var posts, days, oldLevel int
	err := QQueryRow(db,
		`SELECT posts_created, days_visited, trust_level FROM user_activity WHERE user_id=?`, userID,
	).Scan(&posts, &days, &oldLevel)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	newLevel := ComputeTrustLevel(posts, days, oldLevel)
	if newLevel != oldLevel {
		_, err = QExec(db,
			`UPDATE user_activity SET trust_level=? WHERE user_id=?`, newLevel, userID,
		)
	}
	return oldLevel, newLevel, err
}

func ComputeTrustLevel(postsCreated, daysVisited, currentLevel int) int {
	// Never downgrade below TL4 (manually granted).
	if currentLevel >= 4 {
		return currentLevel
	}
	switch {
	case daysVisited >= 100 && postsCreated >= 50:
		return 3
	case daysVisited >= 30 && postsCreated >= 15:
		return 2
	case postsCreated >= 1:
		return 1
	default:
		return 0
	}
}

func UpdateUserProfile(db *sql.DB, userID, displayName, title, bio, avatar, signature, plan, homepage string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	ts := NowMS()
	title = strings.TrimSpace(title)
	_, err = QExec(tx,
		`INSERT INTO user_profiles (user_id, display_name, title, bio, avatar, signature, plan, homepage, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    display_name=excluded.display_name,
		    title=excluded.title,
		    bio=excluded.bio,
		    avatar=excluded.avatar,
		    signature=excluded.signature,
		    plan=excluded.plan,
		    homepage=excluded.homepage,
		    updated_at=excluded.updated_at`,
		userID, displayName, title, bio, avatar, signature, plan, homepage, ts,
	)
	if err != nil {
		return err
	}
	if err := upsertProfileSignatureTx(tx, userID, signature, ts); err != nil {
		return err
	}
	if err := refreshCurrentProfileSignatureTx(tx, userID, ts); err != nil {
		return err
	}
	return tx.Commit()
}

func UpdateUserPrivateProfile(db *sql.DB, p *UserPrivateProfile) error {
	if p == nil {
		return fmt.Errorf("private profile required")
	}
	userID := strings.TrimSpace(p.UserID)
	if userID == "" {
		return fmt.Errorf("user required")
	}
	ts := NowMS()
	_, err := QExec(db,
		`INSERT INTO user_private_profiles (
		    user_id, real_name, real_email, registration_email, address, phone, mobile,
		    birthday, school, contact_note, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    real_name=excluded.real_name,
		    real_email=excluded.real_email,
		    registration_email=excluded.registration_email,
		    address=excluded.address,
		    phone=excluded.phone,
		    mobile=excluded.mobile,
		    birthday=excluded.birthday,
		    school=excluded.school,
		    contact_note=excluded.contact_note,
		    updated_at=excluded.updated_at`,
		userID,
		strings.TrimSpace(p.RealName),
		strings.TrimSpace(p.RealEmail),
		strings.TrimSpace(p.RegistrationEmail),
		strings.TrimSpace(p.Address),
		strings.TrimSpace(p.Phone),
		strings.TrimSpace(p.Mobile),
		strings.TrimSpace(p.Birthday),
		strings.TrimSpace(p.School),
		strings.TrimSpace(p.ContactNote),
		ts,
	)
	return err
}

func SetAccountRegistrationSettings(db *sql.DB, requireApproval bool) (*AccountRegistrationSettings, error) {
	ts := NowMS()
	_, err := QExec(db,
		`INSERT INTO account_registration_settings (id, require_approval, updated_at)
		 VALUES ('default', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		    require_approval=excluded.require_approval,
		    updated_at=excluded.updated_at`,
		boolInt(requireApproval), ts,
	)
	if err != nil {
		return nil, err
	}
	return GetAccountRegistrationSettings(db)
}

func ReviewAccountRegistration(db *sql.DB, userID, reviewerID, decision, reason string) (*AccountRegistration, error) {
	userID = strings.TrimSpace(userID)
	reviewerID = strings.TrimSpace(reviewerID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if userID == "" || reviewerID == "" {
		return nil, fmt.Errorf("user and reviewer required")
	}
	if decision != "approved" && decision != "rejected" {
		return nil, fmt.Errorf(`decision must be "approved" or "rejected"`)
	}
	ts := NowMS()
	res, err := QExec(db,
		`UPDATE users
		    SET registration_status=?, reviewed_at=?, reviewed_by=?, review_reason=?
		  WHERE id=? AND COALESCE(NULLIF(registration_status,''), 'approved')='pending'`,
		decision, ts, reviewerID, reason, userID,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return GetAccountRegistrationByID(db, userID)
}

func CreatePasswordRecoveryRequest(db *sql.DB, id, userID, submittedName, submittedEmail, note string) (*PasswordRecoveryRequest, error) {
	id = strings.TrimSpace(id)
	userID = strings.TrimSpace(userID)
	if id == "" || userID == "" {
		return nil, fmt.Errorf("request and user required")
	}
	submittedName = strings.TrimSpace(submittedName)
	submittedEmail = strings.TrimSpace(submittedEmail)
	note = strings.TrimSpace(note)
	if len(submittedName) > 120 {
		submittedName = submittedName[:120]
	}
	if len(submittedEmail) > 160 {
		submittedEmail = submittedEmail[:160]
	}
	if len(note) > 1000 {
		note = note[:1000]
	}
	ts := NowMS()
	_, err := QExec(db,
		`INSERT INTO password_recovery_requests (
		    id, user_id, status, submitted_name, submitted_email, note,
		    reviewer_id, review_note, created_at, updated_at
		 )
		 VALUES (?, ?, 'pending', ?, ?, ?, '', '', ?, ?)`,
		id, userID, submittedName, submittedEmail, note, ts, ts,
	)
	if err != nil {
		return nil, err
	}
	return GetPasswordRecoveryRequest(db, id)
}

func ReviewPasswordRecoveryRequest(db *sql.DB, id, reviewerID, decision, passwordHash, note string) (*PasswordRecoveryRequest, error) {
	id = strings.TrimSpace(id)
	reviewerID = strings.TrimSpace(reviewerID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	note = strings.TrimSpace(note)
	if len(note) > 500 {
		note = note[:500]
	}
	if id == "" || reviewerID == "" {
		return nil, fmt.Errorf("request and reviewer required")
	}
	if decision != "reset" && decision != "rejected" {
		return nil, fmt.Errorf(`decision must be "reset" or "rejected"`)
	}
	if decision == "reset" && strings.TrimSpace(passwordHash) == "" {
		return nil, fmt.Errorf("new password required")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var userID string
	if err := QQueryRow(tx,
		`SELECT user_id FROM password_recovery_requests WHERE id=? AND status='pending'`,
		id,
	).Scan(&userID); err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	} else if err != nil {
		return nil, err
	}
	ts := NowMS()
	status := "rejected"
	if decision == "reset" {
		status = "resolved"
		// Bump password_changed_at (unix seconds) so prior session tokens are
		// revoked when an admin resets the password.
		if _, err := QExec(tx, `UPDATE users SET password=?, password_changed_at=? WHERE id=?`, passwordHash, ts/1000, userID); err != nil {
			return nil, err
		}
	}
	res, err := QExec(tx,
		`UPDATE password_recovery_requests
		    SET status=?, reviewer_id=?, review_note=?, updated_at=?
		  WHERE id=? AND status='pending'`,
		status, reviewerID, note, ts, id,
	)
	if err != nil {
		return nil, err
	}
	// If a concurrent reviewer already resolved this request, the guarded update
	// affects 0 rows — return before commit so the (possibly different) password
	// reset above rolls back rather than double-applying.
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetPasswordRecoveryRequest(db, id)
}

func SaveUserPersonalFile(db *sql.DB, userID, name, body string, public bool) (*UserPersonalFile, error) {
	userID = strings.TrimSpace(userID)
	name, err := normalizePersonalFileName(name)
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, fmt.Errorf("user required")
	}
	body = strings.TrimSpace(body)
	if len(body) > 8000 {
		body = body[:8000]
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var exists int
	err = QQueryRow(tx, `SELECT 1 FROM user_personal_files WHERE user_id=? AND name=?`, userID, name).Scan(&exists)
	isNew := err == sql.ErrNoRows
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if isNew {
		var count int
		if err := QQueryRow(tx, `SELECT COUNT(*) FROM user_personal_files WHERE user_id=?`, userID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxUserPersonalFiles {
			return nil, fmt.Errorf("personal file limit reached")
		}
	}
	ts := NowMS()
	if _, err := QExec(tx,
		`INSERT INTO user_personal_files (user_id, name, body, public, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, name) DO UPDATE SET
		    body=excluded.body,
		    public=excluded.public,
		    updated_at=excluded.updated_at`,
		userID, name, body, boolInt(public), ts,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetUserPersonalFile(db, userID, name, true)
}

func DeleteUserPersonalFile(db *sql.DB, userID, name string) error {
	userID = strings.TrimSpace(userID)
	name, err := normalizePersonalFileName(name)
	if err != nil {
		return err
	}
	res, err := QExec(db, `DELETE FROM user_personal_files WHERE user_id=? AND name=?`, userID, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func normalizePersonalFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("file name required")
	}
	if len(name) > 64 {
		return "", fmt.Errorf("file name too long")
	}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return "", fmt.Errorf("file name may contain only letters, numbers, dot, underscore, and dash")
	}
	return name, nil
}

func UpsertUserSignature(db *sql.DB, id, userID, label, body string, position int, active bool) (*UserSignature, error) {
	id = strings.TrimSpace(id)
	userID = strings.TrimSpace(userID)
	if userID == "" || id == "" {
		return nil, fmt.Errorf("user and signature id required")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Signature"
	}
	if len(label) > 80 {
		label = label[:80]
	}
	body = strings.TrimSpace(body)
	if len(body) > 500 {
		body = body[:500]
	}
	if body == "" {
		active = false
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var existingOwner string
	err = QQueryRow(tx, `SELECT user_id FROM user_signatures WHERE id=?`, id).Scan(&existingOwner)
	isNew := err == sql.ErrNoRows
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if existingOwner != "" && existingOwner != userID {
		return nil, sql.ErrNoRows
	}
	if isNew {
		var count int
		if err := QQueryRow(tx, `SELECT COUNT(*) FROM user_signatures WHERE user_id=?`, userID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxUserSignatures {
			return nil, fmt.Errorf("signature limit reached")
		}
		if position < 0 {
			if err := QQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM user_signatures WHERE user_id=?`, userID).Scan(&position); err != nil {
				return nil, err
			}
		}
	} else if position < 0 {
		if err := QQueryRow(tx, `SELECT position FROM user_signatures WHERE id=? AND user_id=?`, id, userID).Scan(&position); err != nil {
			return nil, err
		}
	}
	ts := NowMS()
	if isNew {
		_, err = QExec(tx,
			`INSERT INTO user_signatures (id, user_id, label, body, position, active, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, userID, label, body, position, boolInt(active), ts, ts,
		)
	} else {
		_, err = QExec(tx,
			`UPDATE user_signatures
			    SET label=?, body=?, position=?, active=?, updated_at=?
			  WHERE id=? AND user_id=?`,
			label, body, position, boolInt(active), ts, id, userID,
		)
	}
	if err != nil {
		return nil, err
	}
	if err := ensureUserSignatureSettingsTx(tx, userID, ts); err != nil {
		return nil, err
	}
	if active {
		var selected string
		if err := QQueryRow(tx, `SELECT selected_signature_id FROM user_signature_settings WHERE user_id=?`, userID).Scan(&selected); err != nil {
			return nil, err
		}
		if selected == "" {
			if _, err := QExec(tx,
				`UPDATE user_signature_settings SET selected_signature_id=?, updated_at=? WHERE user_id=?`,
				id, ts, userID,
			); err != nil {
				return nil, err
			}
		}
	}
	if err := refreshCurrentProfileSignatureTx(tx, userID, ts); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetUserSignature(db, userID, id)
}

func DeleteUserSignature(db *sql.DB, userID, id string) error {
	userID = strings.TrimSpace(userID)
	id = strings.TrimSpace(id)
	if userID == "" || id == "" {
		return fmt.Errorf("user and signature id required")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	res, err := QExec(tx, `DELETE FROM user_signatures WHERE user_id=? AND id=?`, userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	ts := NowMS()
	if _, err := QExec(tx,
		`UPDATE user_signature_settings
		    SET selected_signature_id='',
		        updated_at=?
		  WHERE user_id=? AND selected_signature_id=?`,
		ts, userID, id,
	); err != nil {
		return err
	}
	if err := refreshCurrentProfileSignatureTx(tx, userID, ts); err != nil {
		return err
	}
	return tx.Commit()
}

func SetUserSignatureSettings(db *sql.DB, userID, selectedSignatureID string, randomEnabled bool) error {
	userID = strings.TrimSpace(userID)
	selectedSignatureID = strings.TrimSpace(selectedSignatureID)
	if userID == "" {
		return fmt.Errorf("user required")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	if selectedSignatureID != "" {
		var active int
		if err := QQueryRow(tx,
			`SELECT active FROM user_signatures WHERE user_id=? AND id=?`,
			userID, selectedSignatureID,
		).Scan(&active); err == sql.ErrNoRows {
			return sql.ErrNoRows
		} else if err != nil {
			return err
		} else if active == 0 {
			return fmt.Errorf("selected signature is inactive")
		}
	}
	ts := NowMS()
	if _, err := QExec(tx,
		`INSERT INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    selected_signature_id=excluded.selected_signature_id,
		    random_enabled=excluded.random_enabled,
		    updated_at=excluded.updated_at`,
		userID, selectedSignatureID, boolInt(randomEnabled), ts,
	); err != nil {
		return err
	}
	if err := refreshCurrentProfileSignatureTx(tx, userID, ts); err != nil {
		return err
	}
	return tx.Commit()
}

func RecountUserSignatures(db *sql.DB, userID string) (*UserSignatureRecount, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user required")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	ts := NowMS()
	if err := ensureUserSignatureSettingsTx(tx, userID, ts); err != nil {
		return nil, err
	}
	out := &UserSignatureRecount{UserID: userID, UpdatedAt: ts}
	var randomEnabled int
	if err := QQueryRow(tx,
		`SELECT selected_signature_id, random_enabled
		   FROM user_signature_settings
		  WHERE user_id=?`,
		userID,
	).Scan(&out.SelectedSignatureID, &randomEnabled); err != nil {
		return nil, err
	}
	out.RandomEnabled = randomEnabled != 0
	if out.SelectedSignatureID != "" {
		var active int
		err := QQueryRow(tx,
			`SELECT active FROM user_signatures
			  WHERE user_id=? AND id=? AND TRIM(COALESCE(body,'')) <> ''`,
			userID, out.SelectedSignatureID,
		).Scan(&active)
		if err == sql.ErrNoRows || active == 0 {
			out.SelectedSignatureID = ""
			if _, err := QExec(tx,
				`UPDATE user_signature_settings
				    SET selected_signature_id='', updated_at=?
				  WHERE user_id=?`,
				ts, userID,
			); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}
	if err := QQueryRow(tx, `SELECT COUNT(*) FROM user_signatures WHERE user_id=?`, userID).Scan(&out.Count); err != nil {
		return nil, err
	}
	if err := QQueryRow(tx,
		`SELECT COUNT(*) FROM user_signatures
		  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''`,
		userID,
	).Scan(&out.ActiveCount); err != nil {
		return nil, err
	}
	if err := refreshCurrentProfileSignatureTx(tx, userID, ts); err != nil {
		return nil, err
	}
	if err := QQueryRow(tx, `SELECT COALESCE(signature,'') FROM user_profiles WHERE user_id=?`, userID).Scan(&out.CurrentSignature); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func UpsertUserLoginACLRule(db *sql.DB, id, userID, pattern, note string, position int, active bool) (*UserLoginACLRule, error) {
	id = strings.TrimSpace(id)
	userID = strings.TrimSpace(userID)
	pattern = strings.TrimSpace(pattern)
	note = strings.TrimSpace(note)
	if userID == "" || id == "" || pattern == "" {
		return nil, fmt.Errorf("user, rule id, and pattern required")
	}
	if len(pattern) > 120 {
		pattern = pattern[:120]
	}
	if len(note) > 160 {
		note = note[:160]
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	var existingOwner string
	err = QQueryRow(tx, `SELECT user_id FROM user_login_acl_rules WHERE id=?`, id).Scan(&existingOwner)
	isNew := err == sql.ErrNoRows
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if existingOwner != "" && existingOwner != userID {
		return nil, sql.ErrNoRows
	}
	if position < 0 {
		if isNew {
			if err := QQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM user_login_acl_rules WHERE user_id=?`, userID).Scan(&position); err != nil {
				return nil, err
			}
		} else if err := QQueryRow(tx, `SELECT position FROM user_login_acl_rules WHERE user_id=? AND id=?`, userID, id).Scan(&position); err != nil {
			return nil, err
		}
	}
	ts := NowMS()
	if isNew {
		_, err = QExec(tx,
			`INSERT INTO user_login_acl_rules (id, user_id, pattern, note, position, active, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, userID, pattern, note, position, boolInt(active), ts, ts,
		)
	} else {
		_, err = QExec(tx,
			`UPDATE user_login_acl_rules
			    SET pattern=?, note=?, position=?, active=?, updated_at=?
			  WHERE id=? AND user_id=?`,
			pattern, note, position, boolInt(active), ts, id, userID,
		)
	}
	if err != nil {
		return nil, err
	}
	if _, err := QExec(tx,
		`INSERT INTO user_login_acl_settings (user_id, enabled, updated_at)
		 VALUES (?, 0, ?)
		 ON CONFLICT(user_id) DO NOTHING`,
		userID, ts,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetUserLoginACLRule(db, userID, id)
}

func DeleteUserLoginACLRule(db *sql.DB, userID, id string) error {
	userID = strings.TrimSpace(userID)
	id = strings.TrimSpace(id)
	if userID == "" || id == "" {
		return fmt.Errorf("user and rule id required")
	}
	res, err := QExec(db, `DELETE FROM user_login_acl_rules WHERE user_id=? AND id=?`, userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func SetUserLoginACLSettings(db *sql.DB, userID string, enabled bool) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user required")
	}
	if enabled {
		var activeRules int
		if err := QQueryRow(db,
			`SELECT COUNT(*) FROM user_login_acl_rules
			  WHERE user_id=? AND active=1 AND TRIM(COALESCE(pattern,'')) <> ''`,
			userID,
		).Scan(&activeRules); err != nil {
			return err
		}
		if activeRules == 0 {
			return fmt.Errorf("at least one active login ACL rule required")
		}
	}
	_, err := QExec(db,
		`INSERT INTO user_login_acl_settings (user_id, enabled, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    enabled=excluded.enabled,
		    updated_at=excluded.updated_at`,
		userID, boolInt(enabled), NowMS(),
	)
	return err
}

func upsertProfileSignatureTx(tx *sql.Tx, userID, signature string, ts int64) error {
	id := "sig_profile_" + userID
	active := strings.TrimSpace(signature) != ""
	_, err := QExec(tx,
		`INSERT INTO user_signatures (id, user_id, label, body, position, active, created_at, updated_at)
		 VALUES (?, ?, 'Profile signature', ?, 0, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		    body=excluded.body,
		    active=excluded.active,
		    updated_at=excluded.updated_at`,
		id, userID, strings.TrimSpace(signature), boolInt(active), ts, ts,
	)
	if err != nil {
		return err
	}
	if err := ensureUserSignatureSettingsTx(tx, userID, ts); err != nil {
		return err
	}
	if !active {
		return nil
	}
	var selected string
	if err := QQueryRow(tx, `SELECT selected_signature_id FROM user_signature_settings WHERE user_id=?`, userID).Scan(&selected); err != nil {
		return err
	}
	if selected == "" {
		_, err = QExec(tx,
			`UPDATE user_signature_settings SET selected_signature_id=?, updated_at=? WHERE user_id=?`,
			id, ts, userID,
		)
	}
	return err
}

func ensureUserSignatureSettingsTx(tx *sql.Tx, userID string, ts int64) error {
	_, err := QExec(tx,
		`INSERT INTO user_signature_settings (user_id, selected_signature_id, random_enabled, updated_at)
		 VALUES (?, '', 0, ?)
		 ON CONFLICT(user_id) DO NOTHING`,
		userID, ts,
	)
	return err
}

func refreshCurrentProfileSignatureTx(tx *sql.Tx, userID string, ts int64) error {
	var selected string
	err := QQueryRow(tx, `SELECT selected_signature_id FROM user_signature_settings WHERE user_id=?`, userID).Scan(&selected)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var signature string
	if selected != "" {
		err = QQueryRow(tx,
			`SELECT COALESCE(body,'') FROM user_signatures WHERE user_id=? AND id=? AND active=1`,
			userID, selected,
		).Scan(&signature)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	if signature == "" {
		err = QQueryRow(tx,
			`SELECT COALESCE(body,'') FROM user_signatures
			  WHERE user_id=? AND active=1 AND TRIM(COALESCE(body,'')) <> ''
			  ORDER BY position, updated_at, id LIMIT 1`,
			userID,
		).Scan(&signature)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	signature = strings.TrimSpace(signature)
	if len(signature) > 500 {
		signature = signature[:500]
	}
	_, err = QExec(tx,
		`INSERT INTO user_profiles (user_id, signature, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    signature=excluded.signature,
		    updated_at=excluded.updated_at`,
		userID, signature, ts,
	)
	return err
}

func InsertRelayDelivery(tx *sql.Tx, id, boardID, threadID, postID, authorID, authorName, title, body string, createdAt, seq int64) error {
	_, err := QExec(tx,
		`INSERT INTO relay_deliveries (
		    id, board_id, thread_id, post_id, author_id, author_name, title, body,
		    status, created_at, updated_at, seq
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)
		 ON CONFLICT(post_id) DO NOTHING`,
		id,
		boardID,
		threadID,
		postID,
		authorID,
		authorName,
		title,
		body,
		createdAt,
		createdAt,
		seq,
	)
	return err
}

func NowDay() string {
	return time.Now().UTC().Format("2006-01-02")
}

func NullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
