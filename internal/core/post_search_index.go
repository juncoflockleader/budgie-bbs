package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type PostSearchDocument struct {
	ID         string `json:"id"`
	PostID     string `json:"post_id"`
	ThreadID   string `json:"thread_id"`
	BoardID    string `json:"board_id"`
	Author     string `json:"author"`
	Body       string `json:"body"`
	CreatedSeq int64  `json:"created_seq"`
	UpdatedSeq int64  `json:"updated_seq"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type PostSearchIndex interface {
	UpsertPost(context.Context, PostSearchDocument) error
	DeletePost(context.Context, string) error
	Clear(context.Context) error
	Search(context.Context, string, string, int) ([]string, error)
}

func (c *Core) rebuildExternalPostSearchIndex(ctx context.Context) (int, error) {
	if c == nil || c.postSearchIndex == nil {
		return 0, nil
	}
	if err := c.postSearchIndex.Clear(ctx); err != nil {
		return 0, fmt.Errorf("clear post search index: %w", err)
	}
	docs, err := listPostSearchDocuments(c.DB, "", 0)
	if err != nil {
		return 0, err
	}
	for _, doc := range docs {
		if err := c.postSearchIndex.UpsertPost(ctx, doc); err != nil {
			return 0, fmt.Errorf("upsert post search document %s: %w", doc.PostID, err)
		}
	}
	return len(docs), nil
}

func listPostSearchDocuments(db *sql.DB, threadID string, limit int) ([]PostSearchDocument, error) {
	args := []any{}
	where := "p.redacted=0"
	if strings.TrimSpace(threadID) != "" {
		where += " AND p.thread=?"
		args = append(args, strings.TrimSpace(threadID))
	}
	limitSQL := ""
	if limit > 0 {
		limitSQL = " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := qQuery(db,
		fmt.Sprintf(
			`SELECT p.id, p.thread, t.board, p.author, p.body,
			        p.created_seq, p.updated_seq, p.created_at, p.updated_at
			   FROM posts p
			   JOIN threads t ON t.id=p.thread
			  WHERE %s
			  ORDER BY p.created_seq DESC%s`,
			where, limitSQL,
		),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []PostSearchDocument
	for rows.Next() {
		var doc PostSearchDocument
		if err := rows.Scan(&doc.PostID, &doc.ThreadID, &doc.BoardID, &doc.Author, &doc.Body, &doc.CreatedSeq, &doc.UpdatedSeq, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		doc.ID = doc.PostID
		if doc.CreatedAt == 0 {
			doc.CreatedAt = doc.CreatedSeq
		}
		if doc.UpdatedAt == 0 {
			doc.UpdatedAt = doc.CreatedAt
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func postSearchDocumentByID(db *sql.DB, postID string) (PostSearchDocument, bool, error) {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return PostSearchDocument{}, false, nil
	}
	rows, err := qQuery(db,
		`SELECT p.id, p.thread, t.board, p.author, p.body,
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.id=? AND p.redacted=0
		  LIMIT 1`,
		postID,
	)
	if err != nil {
		return PostSearchDocument{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return PostSearchDocument{}, false, rows.Err()
	}
	var doc PostSearchDocument
	if err := rows.Scan(&doc.PostID, &doc.ThreadID, &doc.BoardID, &doc.Author, &doc.Body, &doc.CreatedSeq, &doc.UpdatedSeq, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return PostSearchDocument{}, false, err
	}
	doc.ID = doc.PostID
	if doc.CreatedAt == 0 {
		doc.CreatedAt = doc.CreatedSeq
	}
	if doc.UpdatedAt == 0 {
		doc.UpdatedAt = doc.CreatedAt
	}
	if rows.Next() {
		return PostSearchDocument{}, false, fmt.Errorf("duplicate post search document %s", postID)
	}
	return doc, true, rows.Err()
}

func hydrateSearchPostIDs(db *sql.DB, actor *User, ids []string, boardID string, limit int, enforceReadable bool) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	boardID = strings.TrimSpace(boardID)
	seen := map[string]bool{}
	capacity := len(ids)
	if capacity > limit {
		capacity = limit
	}
	posts := make([]Post, 0, capacity)
	for _, rawID := range ids {
		if len(posts) >= limit {
			break
		}
		postID := strings.TrimSpace(rawID)
		if postID == "" || seen[postID] {
			continue
		}
		seen[postID] = true
		post, postBoardID, memberRead, ok, err := getSearchHydrationPost(db, postID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if boardID != "" && postBoardID != boardID {
			continue
		}
		if enforceReadable {
			readable, err := actorCanReadBoardForSearch(db, actor, postBoardID, memberRead)
			if err != nil {
				return nil, err
			}
			if !readable {
				continue
			}
		}
		posts = append(posts, post)
	}
	for i := range posts {
		attachments, err := listPostAttachments(db, posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Attachments = attachments
	}
	return posts, nil
}

func getSearchHydrationPost(db *sql.DB, postID string) (Post, string, bool, bool, error) {
	rows, err := qQuery(db,
		`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT SUM(count_value) FROM post_reaction_count_shards WHERE post_id=p.id), (SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at,
		        t.board, COALESCE(s.member_read_mode, 0)
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   LEFT JOIN board_settings s ON s.board_id=t.board
		  WHERE p.id=?
		  LIMIT 1`,
		postID,
	)
	if err != nil {
		return Post{}, "", false, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Post{}, "", false, false, rows.Err()
	}
	var (
		post       Post
		redacted   int
		board      string
		memberRead int
	)
	if err := rows.Scan(&post.ID, &post.Thread, &post.Author, &post.AuthorID, &post.Body, &post.Signature, &post.ContentType,
		&post.ReplyTo, &post.Version, &redacted, &post.ReactionCount, &post.CreatedSeq, &post.UpdatedSeq, &post.CreatedAt, &post.UpdatedAt,
		&board, &memberRead); err != nil {
		return Post{}, "", false, false, err
	}
	if post.CreatedAt == 0 {
		post.CreatedAt = post.CreatedSeq
	}
	if post.UpdatedAt == 0 {
		post.UpdatedAt = post.CreatedAt
	}
	post.Redacted = redacted != 0
	if rows.Next() {
		return Post{}, "", false, false, fmt.Errorf("duplicate post row %s", postID)
	}
	if post.Redacted {
		return Post{}, board, memberRead != 0, false, rows.Err()
	}
	return post, board, memberRead != 0, true, rows.Err()
}

func actorCanReadBoardForSearch(db *sql.DB, actor *User, boardID string, memberRead bool) (bool, error) {
	if !memberRead {
		return true, nil
	}
	if actor == nil {
		return false, nil
	}
	if actor.IsMod() {
		return true, nil
	}
	var found int
	err := qQueryRow(db,
		`SELECT 1
		   FROM board_members
		  WHERE board_id=? AND user_id=?
		  UNION
		 SELECT 1
		   FROM board_moderators
		  WHERE board_id=? AND user_id=?
		  LIMIT 1`,
		boardID, actor.ID, boardID, actor.ID,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
