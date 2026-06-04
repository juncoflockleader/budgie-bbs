package projections

import (
	"database/sql"
	"strings"
	"time"
)

// --- Readers ---

func GetBoard(db *sql.DB, id string) (*Board, error) {
	b := &Board{}
	err := QQueryRow(db, `SELECT id, name, description FROM boards WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func ListBoards(db *sql.DB) ([]Board, error) {
	rows, err := QQuery(db, `SELECT id, name, description FROM boards ORDER BY name`)
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

func ListCategories(db *sql.DB) ([]Category, error) {
	rows, err := QQuery(db,
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

func ListThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	rows, err := QQuery(db,
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

func GetThread(db *sql.DB, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := QQueryRow(db,
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

func ListPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	rows, err := QQuery(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, content_type, COALESCE(reply_to,''), version, redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=posts.id), 0),
		        created_seq, updated_seq, created_at, updated_at
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
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

func ListPostsByAuthor(db *sql.DB, name string, limit, offset int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, content_type,
		        COALESCE(reply_to,''), version, redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=posts.id), 0),
		        created_seq, updated_seq, created_at, updated_at
		 FROM posts
		 WHERE author=? AND redacted=0
		 ORDER BY created_seq DESC LIMIT ? OFFSET ?`,
		name, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

func GetPost(db *sql.DB, id string) (*Post, error) {
	p := &Post{}
	var redacted int
	err := QQueryRow(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, content_type, COALESCE(reply_to,''), version, redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=posts.id), 0),
		        created_seq, updated_seq, created_at, updated_at FROM posts WHERE id=?`, id,
	).Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt)
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

func GetUserByID(db *sql.DB, id string) (*User, error) {
	u := &User{}
	err := QQueryRow(db, `SELECT id, name, role, password, created FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetUserByName(db *sql.DB, name string) (*User, error) {
	u := &User{}
	err := QQueryRow(db, `SELECT id, name, role, password, created FROM users WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetUserByPubkey(db *sql.DB, pubkey string) (*User, error) {
	var userID string
	err := QQueryRow(db, `SELECT user_id FROM auth_pubkeys WHERE pubkey=?`, pubkey).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return GetUserByID(db, userID)
}

func CountUsers(db *sql.DB) (int, error) {
	var n int
	err := QQueryRow(db, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func GetUserProfileByName(db *sql.DB, name string) (*UserProfile, error) {
	p := &UserProfile{}
	var lastVisitDay string
	err := QQueryRow(db,
		`SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		        COALESCE(up.bio,''), COALESCE(up.avatar,''), u.created,
		        COALESCE(ua.posts_created,0), COALESCE(ua.reactions_recv,0), COALESCE(ua.trust_level,0),
		        COALESCE(ua.last_visit_day,'')
		 FROM users u
		 LEFT JOIN user_profiles up ON up.user_id = u.id
		 LEFT JOIN user_activity ua ON ua.user_id = u.id
		 WHERE u.name=?`,
		name,
	).Scan(&p.ID, &p.Name, &p.Role, &p.DisplayName, &p.Bio, &p.Avatar, &p.Created,
		&p.PostsCreated, &p.ReactionsReceived, &p.TrustLevel, &lastVisitDay)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastVisitDay != "" {
		if lastSeen, err := time.Parse("2006-01-02", lastVisitDay); err == nil {
			p.LastSeen = lastSeen.UnixMilli()
		}
	}
	pubkeys, err := ListPubkeyTitlesByUserName(db, p.Name)
	if err != nil {
		return nil, err
	}
	p.Pubkeys = pubkeys
	return p, err
}

func ListPubkeyTitlesByUserName(db *sql.DB, username string) ([]string, error) {
	rows, err := QQuery(db,
		`SELECT pubkey FROM auth_pubkeys ap
		 JOIN users u ON u.id = ap.user_id
		 WHERE u.name = ?
		 ORDER BY pubkey`,
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		keys = append(keys, ExtractPubkeyTitle(raw))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func ExtractPubkeyTitle(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) >= 3 {
		title := strings.Join(parts[2:], " ")
		if title != "" {
			return title
		}
	}
	if raw == "" {
		return "SSH key"
	}
	if len(parts) >= 1 {
		return parts[0] + " key"
	}
	return "SSH key"
}

func ListModerationReviews(db *sql.DB, status string, limit, offset int) ([]ModerationReview, error) {
	q := `SELECT id, kind, status, target_id, target_kind, reporter, reason, resolution, actor, created_at, updated_at
	      FROM moderation_reviews`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := QQuery(db, q, args...)
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

func ListUserSanctions(db *sql.DB, userID string, limit, offset int) ([]UserSanction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := QQuery(db,
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

func SearchPosts(db *sql.DB, query, boardID string, limit int) ([]Post, error) {
	var rows *sql.Rows
	var err error
	if boardID != "" {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		 FROM posts_fts f
		 JOIN posts p ON p.id = f.post_id
		 WHERE f.board_id=? AND posts_fts MATCH ? AND p.redacted=0
		 ORDER BY rank LIMIT ?`,
			boardID, query, limit,
		)
	} else {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
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
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

func ReactionCount(db *sql.DB, postID string) (int, error) {
	var n int
	err := QQueryRow(db, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func ReactionCountTx(tx *sql.Tx, postID string) (int, error) {
	var n int
	err := QQueryRow(tx, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func UserReacted(db *sql.DB, postID, userID string) (bool, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID,
	).Scan(&n)
	return n > 0, err
}

func GetPollByPostID(db *sql.DB, postID string) (*Poll, error) {
	p := &Poll{}
	err := QQueryRow(db,
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

func GetPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	p := &Poll{}
	err := QQueryRow(db,
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE id=?`, pollID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Load options with counts.
	rows, err := QQuery(db,
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
		err := QQueryRow(db,
			`SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, viewerUserID,
		).Scan(&votedOptionID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		p.Voted = votedOptionID
	}
	return p, nil
}

func PollsForPosts(db *sql.DB, postIDs []string, viewerUserID string) (map[string]*Poll, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}
	args := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		args[i] = id
	}
	placeholder := strings.Repeat("?,", len(postIDs))
	placeholder = placeholder[:len(placeholder)-1] // trim trailing comma
	rows, err := QQuery(db,
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
		full, err := GetPollWithVotes(db, p.ID, viewerUserID)
		if err != nil {
			return nil, err
		}
		if full != nil {
			polls[p.PostID] = full
		}
	}
	return polls, nil
}

func ActiveSanction(db *sql.DB, userID, scope string) (string, bool) {
	now := NowMS()
	var kind string
	var err error
	if scope != "" {
		err = QQueryRow(db,
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND (scope=? OR scope='global')
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, scope, now,
		).Scan(&kind)
	} else {
		err = QQueryRow(db,
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

func ListNotifications(db *sql.DB, userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	q := `SELECT id, kind, thread_id, post_id, actor, read, ts
	      FROM notifications WHERE user_id=?`
	if unreadOnly {
		q += ` AND read=0`
	}
	q += ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	rows, err := QQuery(db, q, userID, limit, offset)
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

func CountUnreadNotifications(db *sql.DB, userID string) (int, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, userID,
	).Scan(&n)
	return n, err
}

func WatchersOfThread(db *sql.DB, threadID, excludeUserID string) ([]string, error) {
	rows, err := QQuery(db,
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

func TrustInfo(db *sql.DB, userID string) (*TrustLevelInfo, error) {
	_ = EnsureActivity(db, userID)
	t := &TrustLevelInfo{}
	err := QQueryRow(db,
		`SELECT posts_created, days_visited, reactions_recv, trust_level
		 FROM user_activity WHERE user_id=?`, userID,
	).Scan(&t.PostsCreated, &t.DaysVisited, &t.ReactionsRecv, &t.TrustLevel)
	if err == sql.ErrNoRows {
		return t, nil
	}
	return t, err
}

func UserTrustLevel(db *sql.DB, userID string) (int, error) {
	var level int
	err := QQueryRow(db, `SELECT trust_level FROM user_activity WHERE user_id=?`, userID).Scan(&level)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return level, nil
}
