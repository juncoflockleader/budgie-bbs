package core

import (
	"database/sql"
	"strings"
	"time"
)

// Board is the projection of a board.
type Board struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Thread is the projection of a thread.
type Thread struct {
	ID        string `json:"id"`
	Board     string `json:"board"`
	Author    string `json:"author"`
	Title     string `json:"title"`
	Locked    bool   `json:"locked"`
	PostCount int    `json:"postCount"`
	LastSeq   int64  `json:"lastSeq"`
	CreatedTS int64  `json:"createdTs"`
}

// Post is the projection of a post.
type Post struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Author      string `json:"author"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	ReplyTo     string `json:"replyTo,omitempty"`
	Version     int    `json:"version"`
	Redacted    bool   `json:"redacted"`
	CreatedSeq  int64  `json:"createdSeq"`
	UpdatedSeq  int64  `json:"updatedSeq"`
}

// User is the projection of an account.
type User struct {
	ID       string
	Name     string
	Role     string // "user" | "trusted" | "moderator" | "admin"
	Password string // bcrypt hash, never sent to clients
	Created  int64
}

// Role helpers.
func (u *User) IsMod() bool   { return u.Role == "moderator" || u.Role == "admin" }
func (u *User) IsAdmin() bool { return u.Role == "admin" }

// --- Readers (safe to call from any goroutine) ---

func getBoard(db *sql.DB, id string) (*Board, error) {
	b := &Board{}
	err := db.QueryRow(`SELECT id, name, description FROM boards WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func listBoards(db *sql.DB) ([]Board, error) {
	rows, err := db.Query(`SELECT id, name, description FROM boards ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.Name, &b.Description); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func listThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	rows, err := db.Query(
		`SELECT id, board, author, title, locked, post_count, last_seq, created_ts
		 FROM threads WHERE board=? ORDER BY last_seq DESC LIMIT ? OFFSET ?`,
		boardID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []Thread
	for rows.Next() {
		var t Thread
		var locked int
		if err := rows.Scan(&t.ID, &t.Board, &t.Author, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS); err != nil {
			return nil, err
		}
		t.Locked = locked != 0
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func getThread(db *sql.DB, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := db.QueryRow(
		`SELECT id, board, author, title, locked, post_count, last_seq, created_ts FROM threads WHERE id=?`, id,
	).Scan(&t.ID, &t.Board, &t.Author, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Locked = locked != 0
	return t, nil
}

func listPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	rows, err := db.Query(
		`SELECT id, thread, author, body, content_type, COALESCE(reply_to,''), version, redacted, created_seq, updated_seq
		 FROM posts WHERE thread=? ORDER BY created_seq LIMIT ? OFFSET ?`,
		threadID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq); err != nil {
			return nil, err
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

func getPost(db *sql.DB, id string) (*Post, error) {
	p := &Post{}
	var redacted int
	err := db.QueryRow(
		`SELECT id, thread, author, body, content_type, COALESCE(reply_to,''), version, redacted, created_seq, updated_seq FROM posts WHERE id=?`, id,
	).Scan(&p.ID, &p.Thread, &p.Author, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Redacted = redacted != 0
	return p, nil
}

func getUserByID(db *sql.DB, id string) (*User, error) {
	u := &User{}
	err := db.QueryRow(`SELECT id, name, role, password, created FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func getUserByName(db *sql.DB, name string) (*User, error) {
	u := &User{}
	err := db.QueryRow(`SELECT id, name, role, password, created FROM users WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func getUserByPubkey(db *sql.DB, pubkey string) (*User, error) {
	var userID string
	err := db.QueryRow(`SELECT user_id FROM auth_pubkeys WHERE pubkey=?`, pubkey).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return getUserByID(db, userID)
}

func countUsers(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// --- Writers — called only from the single-writer goroutine inside a tx ---

func insertThread(tx *sql.Tx, t *Thread) error {
	_, err := tx.Exec(
		`INSERT INTO threads (id, board, author, title, locked, post_count, last_seq, created_ts)
		 VALUES (?,?,?,?,0,0,?,?)`,
		t.ID, t.Board, t.Author, t.Title, t.LastSeq, t.CreatedTS,
	)
	return err
}

func insertPost(tx *sql.Tx, p *Post) error {
	_, err := tx.Exec(
		`INSERT INTO posts (id, thread, author, body, content_type, reply_to, version, redacted, created_seq, updated_seq)
		 VALUES (?,?,?,?,?,?,1,0,?,?)`,
		p.ID, p.Thread, p.Author, p.Body, p.ContentType, nullStr(p.ReplyTo), p.CreatedSeq, p.CreatedSeq,
	)
	return err
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	_, err := tx.Exec(
		`UPDATE threads SET post_count=post_count+1, last_seq=? WHERE id=?`,
		seq, threadID,
	)
	return err
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	_, err := tx.Exec(
		`UPDATE posts SET body=?, version=version+1, updated_seq=? WHERE id=?`,
		body, seq, postID,
	)
	return err
}

func markPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	_, err := tx.Exec(
		`UPDATE posts SET redacted=1, updated_seq=? WHERE id=?`,
		seq, postID,
	)
	return err
}

func markPostRestored(tx *sql.Tx, postID string, seq int64) error {
	_, err := tx.Exec(
		`UPDATE posts SET redacted=0, updated_seq=? WHERE id=?`,
		seq, postID,
	)
	return err
}

// markPostPurged irreversibly clears the post body from the projection (GDPR
// hard-delete escape hatch). The body is replaced with an empty string and the
// post is kept redacted. The event log still contains the original content —
// true GDPR compliance would require crypto-shredding or log scrubbing.
func markPostPurged(tx *sql.Tx, postID string, seq int64) error {
	_, err := tx.Exec(
		`UPDATE posts SET body='', redacted=1, updated_seq=? WHERE id=?`,
		seq, postID,
	)
	return err
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := tx.Exec(`UPDATE threads SET locked=? WHERE id=?`, v, threadID)
	return err
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	_, err := tx.Exec(`UPDATE threads SET board=? WHERE id=?`, toBoard, threadID)
	return err
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	_, err := tx.Exec(`UPDATE users SET role=? WHERE id=?`, role, userID)
	return err
}

func insertBoard(tx *sql.Tx, id, name, description string) error {
	_, err := tx.Exec(
		`INSERT INTO boards (id, name, description) VALUES (?,?,?)`,
		id, name, description,
	)
	return err
}

// --- FTS helpers ---

func ftsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	_, err := tx.Exec(
		`INSERT INTO posts_fts (post_id, thread_id, board_id, author, body) VALUES (?,?,?,?,?)`,
		postID, threadID, boardID, author, body,
	)
	return err
}

func ftsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	_, err := tx.Exec(`UPDATE posts_fts SET body=? WHERE post_id=?`, newBody, postID)
	return err
}

func ftsDeletePost(tx *sql.Tx, postID string) error {
	_, err := tx.Exec(`DELETE FROM posts_fts WHERE post_id=?`, postID)
	return err
}

func searchPosts(db *sql.DB, query, boardID string, limit int) ([]Post, error) {
	var rows *sql.Rows
	var err error
	if boardID != "" {
		rows, err = db.Query(
			`SELECT p.id, p.thread, p.author, p.body, p.content_type,
			        COALESCE(p.reply_to,''), p.version, p.redacted, p.created_seq, p.updated_seq
			 FROM posts_fts f
			 JOIN posts p ON p.id = f.post_id
			 WHERE f.board_id=? AND posts_fts MATCH ? AND p.redacted=0
			 ORDER BY rank LIMIT ?`,
			boardID, query, limit,
		)
	} else {
		rows, err = db.Query(
			`SELECT p.id, p.thread, p.author, p.body, p.content_type,
			        COALESCE(p.reply_to,''), p.version, p.redacted, p.created_seq, p.updated_seq
			 FROM posts_fts f
			 JOIN posts p ON p.id = f.post_id
			 WHERE posts_fts MATCH ? AND p.redacted=0
			 ORDER BY rank LIMIT ?`,
			query, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.Body, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq); err != nil {
			return nil, err
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Sanction helpers ---

// insertSanction records an active sanction.
func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO user_sanctions (id, user_id, kind, scope, expires_at, by, reason, seq)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, userID, kind, scope, expiresAt, by, reason, seq,
	)
	return err
}

// activeSanction returns ("mute"|"ban", true) if user has an active sanction
// in the given scope (or globally). scope="" checks global only.
func activeSanction(db *sql.DB, userID, scope string) (string, bool) {
	now := nowMS()
	var kind string
	var err error
	if scope != "" {
		err = db.QueryRow(
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND (scope=? OR scope='global')
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, scope, now,
		).Scan(&kind)
	} else {
		err = db.QueryRow(
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND scope='global'
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, now,
		).Scan(&kind)
	}
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return kind, true
}

// --- Idempotency helpers ---

func checkProcessed(db *sql.DB, cid string) (string, bool) {
	var result string
	err := db.QueryRow(`SELECT result_json FROM processed_commands WHERE cid=?`, cid).Scan(&result)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return result, true
}

func recordProcessed(tx *sql.Tx, cid, resultJSON string) error {
	// Prune entries older than 10 minutes while we're here.
	cutoff := time.Now().Add(-10 * time.Minute).UnixMilli()
	if _, err := tx.Exec(`DELETE FROM processed_commands WHERE processed_at<?`, cutoff); err != nil {
		return err
	}
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO processed_commands (cid, result_json, processed_at) VALUES (?,?,?)`,
		cid, resultJSON, nowMS(),
	)
	return err
}

// ── M10: Reactions ──────────────────────────────────────────────────────────

// upsertReaction inserts or replaces a reaction (one per user per post).
func upsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO post_reactions (post_id, user_id, emoji, ts) VALUES (?,?,?,?)`,
		postID, userID, emoji, ts,
	)
	return err
}

func deleteReaction(tx *sql.Tx, postID, userID string) error {
	_, err := tx.Exec(`DELETE FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID)
	return err
}

func reactionCount(db *sql.DB, postID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func reactionCountTx(tx *sql.Tx, postID string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func userReacted(db *sql.DB, postID, userID string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID,
	).Scan(&n)
	return n > 0, err
}

// ── M11: Polls ──────────────────────────────────────────────────────────────

// Poll is the API projection of a poll.
type Poll struct {
	ID        string       `json:"id"`
	PostID    string       `json:"postId"`
	Question  string       `json:"question,omitempty"`
	ExpiresAt int64        `json:"expiresAt,omitempty"`
	TS        int64        `json:"ts"`
	Options   []PollOption `json:"options"`
	Voted     string       `json:"voted,omitempty"` // option_id the current user voted for
}

type PollOption struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	VoteCount int    `json:"voteCount"`
}

func insertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	_, err := tx.Exec(
		`INSERT INTO polls (id, post_id, question, expires_at, ts) VALUES (?,?,?,?,?)`,
		id, postID, question, expiresAt, ts,
	)
	return err
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	_, err := tx.Exec(
		`INSERT INTO poll_options (id, poll_id, text, position) VALUES (?,?,?,?)`,
		id, pollID, text, position,
	)
	return err
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO poll_votes (poll_id, option_id, user_id, ts) VALUES (?,?,?,?)`,
		pollID, optionID, userID, ts,
	)
	return err
}

func getPollByPostID(db *sql.DB, postID string) (*Poll, error) {
	p := &Poll{}
	err := db.QueryRow(
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE post_id=?`, postID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func getPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	p := &Poll{}
	err := db.QueryRow(
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE id=?`, pollID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Load options with counts.
	rows, err := db.Query(
		`SELECT po.id, po.text,
		        (SELECT COUNT(*) FROM poll_votes pv WHERE pv.option_id=po.id) AS cnt
		 FROM poll_options po WHERE po.poll_id=? ORDER BY po.position`, pollID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var opt PollOption
		if err := rows.Scan(&opt.ID, &opt.Text, &opt.VoteCount); err != nil {
			return nil, err
		}
		p.Options = append(p.Options, opt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Check if viewer voted.
	if viewerUserID != "" {
		var votedOptionID string
		err := db.QueryRow(
			`SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, viewerUserID,
		).Scan(&votedOptionID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		p.Voted = votedOptionID
	}
	return p, nil
}

// pollsForPosts returns a map of postID → Poll for any posts that have polls.
// viewerUserID is used to populate the Voted field.
func pollsForPosts(db *sql.DB, postIDs []string, viewerUserID string) (map[string]*Poll, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}
	// Build "?,?,?" placeholder.
	args := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		args[i] = id
	}
	placeholder := strings.Repeat("?,", len(postIDs))
	placeholder = placeholder[:len(placeholder)-1] // trim trailing comma
	rows, err := db.Query(
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE post_id IN (`+placeholder+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	polls := map[string]*Poll{}
	for rows.Next() {
		p := &Poll{}
		if err := rows.Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS); err != nil {
			return nil, err
		}
		polls[p.PostID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Load options and votes for each poll found.
	for _, p := range polls {
		full, err := getPollWithVotes(db, p.ID, viewerUserID)
		if err != nil {
			return nil, err
		}
		if full != nil {
			polls[p.PostID] = full
		}
	}
	return polls, nil
}

// ── M8: Notifications ───────────────────────────────────────────────────────

// Notification is the API projection of a notification.
type Notification struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "mention" | "reply" | "watched"
	ThreadID string `json:"threadId"`
	PostID   string `json:"postId"`
	Actor    string `json:"actor"`
	Read     bool   `json:"read"`
	TS       int64  `json:"ts"`
}

func insertNotification(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO notifications (id, user_id, kind, thread_id, post_id, actor, read, ts)
		 VALUES (?,?,?,?,?,?,0,?)`,
		id, userID, kind, threadID, postID, actor, ts,
	)
	return err
}

func listNotifications(db *sql.DB, userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	q := `SELECT id, kind, thread_id, post_id, actor, read, ts
	      FROM notifications WHERE user_id=?`
	if unreadOnly {
		q += ` AND read=0`
	}
	q += ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	rows, err := db.Query(q, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var read int
		if err := rows.Scan(&n.ID, &n.Kind, &n.ThreadID, &n.PostID, &n.Actor, &read, &n.TS); err != nil {
			return nil, err
		}
		n.Read = read != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

func countUnreadNotifications(db *sql.DB, userID string) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, userID,
	).Scan(&n)
	return n, err
}

func markNotificationRead(db *sql.DB, id, userID string) error {
	_, err := db.Exec(
		`UPDATE notifications SET read=1 WHERE id=? AND user_id=?`, id, userID,
	)
	return err
}

func markAllNotificationsRead(db *sql.DB, userID string) error {
	_, err := db.Exec(`UPDATE notifications SET read=1 WHERE user_id=?`, userID)
	return err
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	if level == "normal" {
		// "normal" = remove the row (default).
		_, err := db.Exec(`DELETE FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID)
		return err
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO thread_prefs (user_id, thread_id, level) VALUES (?,?,?)`,
		userID, threadID, level,
	)
	return err
}

// watchersOfThread returns user IDs with level='watch' for the given thread,
// excluding excludeUserID (usually the post author).
func watchersOfThread(db *sql.DB, threadID, excludeUserID string) ([]string, error) {
	rows, err := db.Query(
		`SELECT user_id FROM thread_prefs WHERE thread_id=? AND level='watch' AND user_id!=?`,
		threadID, excludeUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── M9: Trust levels ────────────────────────────────────────────────────────

// TrustLevelInfo holds computed activity stats and trust level for a user.
type TrustLevelInfo struct {
	PostsCreated  int `json:"postsCreated"`
	DaysVisited   int `json:"daysVisited"`
	ReactionsRecv int `json:"reactionsReceived"`
	TrustLevel    int `json:"trustLevel"`
}

// ensureActivity creates or returns the activity row for a user (idempotent).
func ensureActivity(db *sql.DB, userID string) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID,
	)
	return err
}

// recordPostCreated bumps the post counter and visit day, then recomputes trust.
// Returns (oldLevel, newLevel, error).
func recordPostCreated(db *sql.DB, userID string) (int, int, error) {
	today := nowDay()
	_, err := db.Exec(`INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID)
	if err != nil {
		return 0, 0, err
	}
	// Bump posts_created; conditionally bump days_visited.
	_, err = db.Exec(`
		UPDATE user_activity SET
		    posts_created = posts_created + 1,
		    days_visited  = days_visited + CASE WHEN last_visit_day != ? THEN 1 ELSE 0 END,
		    last_visit_day = ?
		WHERE user_id = ?`, today, today, userID)
	if err != nil {
		return 0, 0, err
	}
	return recomputeTrust(db, userID)
}

// recordReactionReceived increments the reactions_recv counter.
func recordReactionReceived(db *sql.DB, postAuthorID string) error {
	_, err := db.Exec(`
		INSERT INTO user_activity (user_id, reactions_recv) VALUES (?,1)
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func recordReactionRemoved(db *sql.DB, postAuthorID string) error {
	_, err := db.Exec(`
		UPDATE user_activity SET reactions_recv = MAX(0, reactions_recv - 1) WHERE user_id=?`,
		postAuthorID,
	)
	return err
}

// recomputeTrust recalculates trust level from activity data and updates it.
// Returns (oldLevel, newLevel, error).
func recomputeTrust(db *sql.DB, userID string) (int, int, error) {
	var posts, days, oldLevel int
	err := db.QueryRow(
		`SELECT posts_created, days_visited, trust_level FROM user_activity WHERE user_id=?`, userID,
	).Scan(&posts, &days, &oldLevel)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	newLevel := computeTrustLevel(posts, days, oldLevel)
	if newLevel != oldLevel {
		_, err = db.Exec(
			`UPDATE user_activity SET trust_level=? WHERE user_id=?`, newLevel, userID,
		)
	}
	return oldLevel, newLevel, err
}

// computeTrustLevel returns TL0–TL3 (TL4 = manual admin grant only).
func computeTrustLevel(postsCreated, daysVisited, currentLevel int) int {
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

// trustInfo returns trust level info for a user.
func trustInfo(db *sql.DB, userID string) (*TrustLevelInfo, error) {
	_ = ensureActivity(db, userID)
	t := &TrustLevelInfo{}
	err := db.QueryRow(
		`SELECT posts_created, days_visited, reactions_recv, trust_level
		 FROM user_activity WHERE user_id=?`, userID,
	).Scan(&t.PostsCreated, &t.DaysVisited, &t.ReactionsRecv, &t.TrustLevel)
	if err == sql.ErrNoRows {
		return t, nil
	}
	return t, err
}

// nowDay returns today's date string 'YYYY-MM-DD'.
func nowDay() string {
	return time.Now().UTC().Format("2006-01-02")
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
