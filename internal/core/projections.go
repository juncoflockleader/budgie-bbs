package core

import (
	"database/sql"
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

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
