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

type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    string `json:"parentId,omitempty"`
	Position    int    `json:"position"`
	Visibility  string `json:"visibility"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// Thread is the projection of a thread.
type Thread struct {
	ID        string `json:"id"`
	Board     string `json:"board"`
	Author    string `json:"author"`
	AuthorID  string `json:"authorId,omitempty"`
	Title     string `json:"title"`
	Locked    bool   `json:"locked"`
	PostCount int    `json:"postCount"`
	LastSeq   int64  `json:"lastSeq"`
	CreatedTS int64  `json:"createdTs"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Post is the projection of a post.
type Post struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Author      string `json:"author"`
	AuthorID    string `json:"authorId,omitempty"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	ReplyTo     string `json:"replyTo,omitempty"`
	Version     int    `json:"version"`
	Redacted    bool   `json:"redacted"`
	CreatedSeq  int64  `json:"createdSeq"`
	UpdatedSeq  int64  `json:"updatedSeq"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// User is the projection of an account.
type User struct {
	ID       string
	Name     string
	Role     string // "user" | "trusted" | "moderator" | "admin"
	Password string // bcrypt hash, never sent to clients
	Created  int64
}

type UserProfile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	DisplayName       string `json:"displayName"`
	Bio               string `json:"bio"`
	Avatar            string `json:"avatar"`
	Created           int64  `json:"created"`
	PostsCreated      int    `json:"postsCreated"`
	ReactionsReceived int    `json:"reactionsReceived"`
	TrustLevel        int    `json:"trustLevel"`
}

type ModerationReview struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	TargetID   string `json:"targetId"`
	TargetKind string `json:"targetKind"`
	Reporter   string `json:"reporter"`
	Reason     string `json:"reason"`
	Resolution string `json:"resolution,omitempty"`
	Actor      string `json:"actor,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type UserSanction struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	ExpiresAt int64  `json:"expiresAt"`
	By        string `json:"by"`
	Reason    string `json:"reason"`
	Seq       int64  `json:"seq"`
}

// Role helpers.
func (u *User) IsMod() bool   { return u.Role == "moderator" || u.Role == "admin" }
func (u *User) IsAdmin() bool { return u.Role == "admin" }

// --- Readers (safe to call from any goroutine) ---

func getBoard(db *sql.DB, id string) (*Board, error) {
	b := &Board{}
	err := qQueryRow(db, `SELECT id, name, description FROM boards WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func listBoards(db *sql.DB) ([]Board, error) {
	rows, err := qQuery(db, `SELECT id, name, description FROM boards ORDER BY name`)
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

func listCategories(db *sql.DB) ([]Category, error) {
	rows, err := qQuery(db,
		`SELECT id, name, description, parent_id, position, visibility, created_at, updated_at
		 FROM categories ORDER BY position, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Position, &c.Visibility, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

func listThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	rows, err := qQuery(db,
		`SELECT id, board, author, COALESCE(author_id,''), title, locked, post_count, last_seq, created_ts, created_at, updated_at
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
		if err := rows.Scan(&t.ID, &t.Board, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if t.CreatedAt == 0 {
			t.CreatedAt = t.CreatedTS
		}
		if t.UpdatedAt == 0 {
			t.UpdatedAt = t.CreatedAt
		}
		t.Locked = locked != 0
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func getThread(db *sql.DB, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := qQueryRow(db,
		`SELECT id, board, author, COALESCE(author_id,''), title, locked, post_count, last_seq, created_ts, created_at, updated_at FROM threads WHERE id=?`, id,
	).Scan(&t.ID, &t.Board, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = t.CreatedTS
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = t.CreatedAt
	}
	t.Locked = locked != 0
	return t, nil
}

func listPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	rows, err := qQuery(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, content_type, COALESCE(reply_to,''), version, redacted, created_seq, updated_seq, created_at, updated_at
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
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

func getPost(db *sql.DB, id string) (*Post, error) {
	p := &Post{}
	var redacted int
	err := qQueryRow(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, content_type, COALESCE(reply_to,''), version, redacted, created_seq, updated_seq, created_at, updated_at FROM posts WHERE id=?`, id,
	).Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = p.CreatedSeq
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = p.CreatedAt
	}
	p.Redacted = redacted != 0
	return p, nil
}

func getUserByID(db *sql.DB, id string) (*User, error) {
	u := &User{}
	err := qQueryRow(db, `SELECT id, name, role, password, created FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func getUserByName(db *sql.DB, name string) (*User, error) {
	u := &User{}
	err := qQueryRow(db, `SELECT id, name, role, password, created FROM users WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func getUserByPubkey(db *sql.DB, pubkey string) (*User, error) {
	var userID string
	err := qQueryRow(db, `SELECT user_id FROM auth_pubkeys WHERE pubkey=?`, pubkey).Scan(&userID)
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
	err := qQueryRow(db, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func getUserProfileByName(db *sql.DB, name string) (*UserProfile, error) {
	p := &UserProfile{}
	err := qQueryRow(db,
		`SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		        COALESCE(up.bio,''), COALESCE(up.avatar,''), u.created,
		        COALESCE(ua.posts_created,0), COALESCE(ua.reactions_recv,0), COALESCE(ua.trust_level,0)
		 FROM users u
		 LEFT JOIN user_profiles up ON up.user_id = u.id
		 LEFT JOIN user_activity ua ON ua.user_id = u.id
		 WHERE u.name=?`,
		name,
	).Scan(&p.ID, &p.Name, &p.Role, &p.DisplayName, &p.Bio, &p.Avatar, &p.Created,
		&p.PostsCreated, &p.ReactionsReceived, &p.TrustLevel)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func updateUserProfile(db *sql.DB, userID, displayName, bio, avatar string) error {
	_, err := qExec(db,
		`INSERT INTO user_profiles (user_id, display_name, bio, avatar, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		    display_name=excluded.display_name,
		    bio=excluded.bio,
		    avatar=excluded.avatar,
		    updated_at=excluded.updated_at`,
		userID, displayName, bio, avatar, nowMS(),
	)
	return err
}

func listModerationReviews(db *sql.DB, status string, limit, offset int) ([]ModerationReview, error) {
	q := `SELECT id, kind, status, target_id, target_kind, reporter, reason, resolution, actor, created_at, updated_at
	      FROM moderation_reviews`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := qQuery(db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModerationReview
	for rows.Next() {
		var r ModerationReview
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.TargetID, &r.TargetKind, &r.Reporter, &r.Reason, &r.Resolution, &r.Actor, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func listUserSanctions(db *sql.DB, userID string, limit, offset int) ([]UserSanction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := qQuery(db,
		`SELECT id, user_id, kind, scope, expires_at, by, reason, seq
		   FROM user_sanctions
		  WHERE user_id = ?
		  ORDER BY seq DESC LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserSanction
	for rows.Next() {
		var s UserSanction
		if err := rows.Scan(&s.ID, &s.UserID, &s.Kind, &s.Scope, &s.ExpiresAt, &s.By, &s.Reason, &s.Seq); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// --- Writers — called only from the single-writer goroutine inside a tx ---

func insertThread(tx *sql.Tx, t *Thread) error {
	_, err := qExec(tx,
		`INSERT INTO threads (id, board, author, author_id, title, locked, post_count, last_seq, created_ts, created_at, updated_at)
		 VALUES (?,?,?,?,?,0,0,?,?,?,?)`,
		t.ID, t.Board, t.Author, t.AuthorID, t.Title, t.LastSeq, t.CreatedTS, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func insertPost(tx *sql.Tx, p *Post) error {
	_, err := qExec(tx,
		`INSERT INTO posts (id, thread, author, author_id, body, content_type, reply_to, version, redacted, created_seq, updated_seq, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,1,0,?,?,?,?)`,
		p.ID, p.Thread, p.Author, p.AuthorID, p.Body, p.ContentType, nullStr(p.ReplyTo), p.CreatedSeq, p.CreatedSeq, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	_, err := qExec(tx,
		`UPDATE threads SET post_count=post_count+1, last_seq=?, updated_at=? WHERE id=?`,
		seq, nowMS(), threadID,
	)
	return err
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	_, err := qExec(tx,
		`UPDATE posts SET body=?, version=version+1, updated_seq=?, updated_at=? WHERE id=?`,
		body, seq, nowMS(), postID,
	)
	return err
}

func markPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	_, err := qExec(tx,
		`UPDATE posts SET redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
		seq, nowMS(), postID,
	)
	return err
}

func markPostRestored(tx *sql.Tx, postID string, seq int64) error {
	_, err := qExec(tx,
		`UPDATE posts SET redacted=0, updated_seq=?, updated_at=? WHERE id=?`,
		seq, nowMS(), postID,
	)
	return err
}

// markPostPurged irreversibly clears the post body from the projection (GDPR
// hard-delete escape hatch). The body is replaced with an empty string and the
// post is kept redacted. The event log still contains the original content —
// true GDPR compliance would require crypto-shredding or log scrubbing.
func markPostPurged(tx *sql.Tx, postID string, seq int64) error {
	_, err := qExec(tx,
		`UPDATE posts SET body='', redacted=1, updated_seq=?, updated_at=? WHERE id=?`,
		seq, nowMS(), postID,
	)
	return err
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := qExec(tx, `UPDATE threads SET locked=? WHERE id=?`, v, threadID)
	return err
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	_, err := qExec(tx, `UPDATE threads SET board=? WHERE id=?`, toBoard, threadID)
	return err
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	_, err := qExec(tx, `UPDATE users SET role=? WHERE id=?`, role, userID)
	return err
}

func insertBoard(tx *sql.Tx, id, name, description string) error {
	if _, err := qExec(tx,
		`INSERT INTO boards (id, name, description) VALUES (?,?,?)`,
		id, name, description,
	); err != nil {
		return err
	}
	_, err := qExec(tx,
		`INSERT OR IGNORE INTO categories (id, name, description, created_at, updated_at) VALUES (?,?,?,?,?)`,
		id, name, description, nowMS(), nowMS(),
	)
	return err
}

func insertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	_, err := qExec(tx,
		`INSERT INTO moderation_reviews (id, kind, status, target_id, target_kind, reporter, reason, created_at, updated_at)
		 VALUES (?, ?, 'open', ?, ?, ?, ?, ?, ?)`,
		id, kind, targetID, targetKind, reporter, reason, ts, ts,
	)
	return err
}

func resolveModerationReview(tx *sql.Tx, id, actor, resolution string, ts int64) error {
	res, err := qExec(tx,
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

// --- FTS helpers ---

func ftsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	_, err := qExec(tx,
		`INSERT INTO posts_fts (post_id, thread_id, board_id, author, body) VALUES (?,?,?,?,?)`,
		postID, threadID, boardID, author, body,
	)
	return err
}

func ftsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	_, err := qExec(tx, `UPDATE posts_fts SET body=? WHERE post_id=?`, newBody, postID)
	return err
}

func ftsDeletePost(tx *sql.Tx, postID string) error {
	_, err := qExec(tx, `DELETE FROM posts_fts WHERE post_id=?`, postID)
	return err
}

func searchPosts(db *sql.DB, query, boardID string, limit int) ([]Post, error) {
	var rows *sql.Rows
	var err error
	if boardID != "" {
		rows, err = qQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, p.content_type,
			        COALESCE(p.reply_to,''), p.version, p.redacted, p.created_seq, p.updated_seq, p.created_at, p.updated_at
			 FROM posts_fts f
			 JOIN posts p ON p.id = f.post_id
			 WHERE f.board_id=? AND posts_fts MATCH ? AND p.redacted=0
			 ORDER BY rank LIMIT ?`,
			boardID, query, limit,
		)
	} else {
		rows, err = qQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, p.content_type,
			        COALESCE(p.reply_to,''), p.version, p.redacted, p.created_seq, p.updated_seq, p.created_at, p.updated_at
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
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Sanction helpers ---

// insertSanction records an active sanction.
func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	_, err := qExec(tx,
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
		err = qQueryRow(db,
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND (scope=? OR scope='global')
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, scope, now,
		).Scan(&kind)
	} else {
		err = qQueryRow(db,
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

func checkProcessed(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool) {
	var result, storedHash string
	err := qQueryRow(db,
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

func recordProcessed(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error {
	// Prune entries older than 10 minutes while we're here.
	cutoff := time.Now().Add(-10 * time.Minute).UnixMilli()
	if _, err := qExec(tx, `DELETE FROM processed_commands WHERE processed_at<?`, cutoff); err != nil {
		return err
	}
	_, err := qExec(tx,
		`INSERT OR REPLACE INTO processed_commands (actor_id, cid, command_hash, result_json, processed_at) VALUES (?,?,?,?,?)`,
		actorID, cid, commandHash, resultJSON, nowMS(),
	)
	return err
}

// ── M10: Reactions ──────────────────────────────────────────────────────────

// upsertReaction inserts or replaces a reaction (one per user per post).
func upsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	_, err := qExec(tx,
		`INSERT OR REPLACE INTO post_reactions (post_id, user_id, emoji, ts) VALUES (?,?,?,?)`,
		postID, userID, emoji, ts,
	)
	return err
}

func deleteReaction(tx *sql.Tx, postID, userID string) error {
	_, err := qExec(tx, `DELETE FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID)
	return err
}

func reactionCount(db *sql.DB, postID string) (int, error) {
	var n int
	err := qQueryRow(db, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func reactionCountTx(tx *sql.Tx, postID string) (int, error) {
	var n int
	err := qQueryRow(tx, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func userReacted(db *sql.DB, postID, userID string) (bool, error) {
	var n int
	err := qQueryRow(db,
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
	_, err := qExec(tx,
		`INSERT INTO polls (id, post_id, question, expires_at, ts) VALUES (?,?,?,?,?)`,
		id, postID, question, expiresAt, ts,
	)
	return err
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	_, err := qExec(tx,
		`INSERT INTO poll_options (id, poll_id, text, position) VALUES (?,?,?,?)`,
		id, pollID, text, position,
	)
	return err
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	_, err := qExec(tx,
		`INSERT OR REPLACE INTO poll_votes (poll_id, option_id, user_id, ts) VALUES (?,?,?,?)`,
		pollID, optionID, userID, ts,
	)
	return err
}

func getPollByPostID(db *sql.DB, postID string) (*Poll, error) {
	p := &Poll{}
	err := qQueryRow(db,
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
	err := qQueryRow(db,
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE id=?`, pollID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Load options with counts.
	rows, err := qQuery(db,
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
		err := qQueryRow(db,
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
	rows, err := qQuery(db,
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
	_, err := qExec(db,
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
	rows, err := qQuery(db, q, userID, limit, offset)
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
	err := qQueryRow(db,
		`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, userID,
	).Scan(&n)
	return n, err
}

func markNotificationRead(db *sql.DB, id, userID string) error {
	_, err := qExec(db,
		`UPDATE notifications SET read=1 WHERE id=? AND user_id=?`, id, userID,
	)
	return err
}

func markAllNotificationsRead(db *sql.DB, userID string) error {
	_, err := qExec(db, `UPDATE notifications SET read=1 WHERE user_id=?`, userID)
	return err
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	if level == "normal" {
		// "normal" = remove the row (default).
		_, err := qExec(db, `DELETE FROM thread_prefs WHERE user_id=? AND thread_id=?`, userID, threadID)
		return err
	}
	_, err := qExec(db,
		`INSERT OR REPLACE INTO thread_prefs (user_id, thread_id, level) VALUES (?,?,?)`,
		userID, threadID, level,
	)
	return err
}

// watchersOfThread returns user IDs with level='watch' for the given thread,
// excluding excludeUserID (usually the post author).
func watchersOfThread(db *sql.DB, threadID, excludeUserID string) ([]string, error) {
	rows, err := qQuery(db,
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
	_, err := qExec(db,
		`INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID,
	)
	return err
}

// recordPostCreated bumps the post counter and visit day, then recomputes trust.
// Returns (oldLevel, newLevel, error).
func recordPostCreated(db *sql.DB, userID string) (int, int, error) {
	today := nowDay()
	_, err := qExec(db, `INSERT OR IGNORE INTO user_activity (user_id) VALUES (?)`, userID)
	if err != nil {
		return 0, 0, err
	}
	// Bump posts_created; conditionally bump days_visited.
	_, err = qExec(db, `
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
	_, err := qExec(db, `
		INSERT INTO user_activity (user_id, reactions_recv) VALUES (?,1)
		ON CONFLICT(user_id) DO UPDATE SET reactions_recv = reactions_recv + 1`,
		postAuthorID,
	)
	return err
}

func recordReactionRemoved(db *sql.DB, postAuthorID string) error {
	_, err := qExec(db, `
		UPDATE user_activity SET reactions_recv = MAX(0, reactions_recv - 1) WHERE user_id=?`,
		postAuthorID,
	)
	return err
}

// recomputeTrust recalculates trust level from activity data and updates it.
// Returns (oldLevel, newLevel, error).
func recomputeTrust(db *sql.DB, userID string) (int, int, error) {
	var posts, days, oldLevel int
	err := qQueryRow(db,
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
		_, err = qExec(db,
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
	err := qQueryRow(db,
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
