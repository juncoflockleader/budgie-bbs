package projections

import (
	"database/sql"
	"time"
)

// --- Writers / mutators ---

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
		`INSERT INTO posts (id, thread, author, author_id, body, content_type, reply_to, version, redacted, created_seq, updated_seq, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,1,0,?,?,?,?)`,
		p.ID, p.Thread, p.Author, p.AuthorID, p.Body, p.ContentType, NullStr(p.ReplyTo), p.CreatedSeq, p.CreatedSeq, p.CreatedAt, p.UpdatedAt,
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

func SetThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := QExec(tx, `UPDATE threads SET locked=? WHERE id=?`, v, threadID)
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

func InsertBoard(tx *sql.Tx, id, name, description string) error {
	if _, err := QExec(tx,
		`INSERT INTO boards (id, name, description) VALUES (?,?,?)`,
		id, name, description,
	); err != nil {
		return err
	}
	_, err := QExec(tx,
		`INSERT OR IGNORE INTO categories (id, name, description, created_at, updated_at) VALUES (?,?,?,?,?)`,
		id, name, description, NowMS(), NowMS(),
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
		`INSERT OR REPLACE INTO user_sanctions (id, user_id, kind, scope, expires_at, by, reason, seq)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, userID, kind, scope, expiresAt, by, reason, seq,
	)
	return err
}

func CheckProcessed(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool) {
	var result, storedHash string
	err := QQueryRow(db,
		`SELECT result_json, command_hash FROM processed_commands WHERE actor_id=? AND cid=?`,
		actorID, cid,
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

func RecordProcessed(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error {
	// Prune entries older than 10 minutes while we're here.
	cutoff := time.Now().Add(-10 * time.Minute).UnixMilli()
	if _, err := QExec(tx, `DELETE FROM processed_commands WHERE processed_at<?`, cutoff); err != nil {
		return err
	}
	_, err := QExec(tx,
		`INSERT OR REPLACE INTO processed_commands (actor_id, cid, command_hash, result_json, processed_at) VALUES (?,?,?,?,?)`,
		actorID, cid, commandHash, resultJSON, NowMS(),
	)
	return err
}

func UpsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	_, err := QExec(tx,
		`INSERT OR REPLACE INTO post_reactions (post_id, user_id, emoji, ts) VALUES (?,?,?,?)`,
		postID, userID, emoji, ts,
	)
	return err
}

func DeleteReaction(tx *sql.Tx, postID, userID string) error {
	_, err := QExec(tx, `DELETE FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID)
	return err
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
	_, err := QExec(tx,
		`INSERT OR REPLACE INTO poll_votes (poll_id, option_id, user_id, ts) VALUES (?,?,?,?)`,
		pollID, optionID, userID, ts,
	)
	return err
}

func InsertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
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

func SetThreadPref(db *sql.DB, userID, threadID, level string) error {
	if level == "normal" {
		// "normal" = remove the row (default).
		_, err := QExec(db, `DELETE FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID)
		return err
	}
	_, err := QExec(db,
		`INSERT OR REPLACE INTO thread_prefs (user_id, thread_id, level) VALUES (?,?,?)`,
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
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func RecordReactionRemoved(db *sql.DB, postAuthorID string) error {
	_, err := QExec(db, `
		UPDATE user_activity SET reactions_recv = MAX(0, reactions_recv - 1) WHERE user_id=?`,
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

func UpdateUserProfile(db *sql.DB, userID, displayName, bio, avatar string) error {
	_, err := QExec(db,
		`INSERT INTO user_profiles (user_id, display_name, bio, avatar, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    display_name=excluded.display_name,
		    bio=excluded.bio,
		    avatar=excluded.avatar,
		    updated_at=excluded.updated_at`,
		userID, displayName, bio, avatar, NowMS(),
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
