package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type PostSearchIndex interface {
	UpsertPost(context.Context, projections.PostSearchDocument) error
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
	docs, err := projections.ListPostSearchDocuments(c.DB, "", 0)
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

func hydrateSearchPostIDs(db *sql.DB, actor *projections.User, ids []string, boardID string, limit int, enforceReadable bool) ([]projections.Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	boardID = strings.TrimSpace(boardID)
	seen := map[string]bool{}
	capacity := len(ids)
	if capacity > limit {
		capacity = limit
	}
	posts := make([]projections.Post, 0, capacity)
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
		attachments, err := projections.ListPostAttachments(db, posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Attachments = attachments
	}
	return posts, nil
}

func getSearchHydrationPost(db *sql.DB, postID string) (projections.Post, string, bool, bool, error) {
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
		return projections.Post{}, "", false, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return projections.Post{}, "", false, false, rows.Err()
	}
	var (
		post       projections.Post
		redacted   int
		board      string
		memberRead int
	)
	if err := rows.Scan(&post.ID, &post.Thread, &post.Author, &post.AuthorID, &post.Body, &post.Signature, &post.ContentType,
		&post.ReplyTo, &post.Version, &redacted, &post.ReactionCount, &post.CreatedSeq, &post.UpdatedSeq, &post.CreatedAt, &post.UpdatedAt,
		&board, &memberRead); err != nil {
		return projections.Post{}, "", false, false, err
	}
	if post.CreatedAt == 0 {
		post.CreatedAt = post.CreatedSeq
	}
	if post.UpdatedAt == 0 {
		post.UpdatedAt = post.CreatedAt
	}
	post.Redacted = redacted != 0
	if rows.Next() {
		return projections.Post{}, "", false, false, fmt.Errorf("duplicate post row %s", postID)
	}
	if post.Redacted {
		return projections.Post{}, board, memberRead != 0, false, rows.Err()
	}
	return post, board, memberRead != 0, true, rows.Err()
}

func actorCanReadBoardForSearch(db *sql.DB, actor *projections.User, boardID string, memberRead bool) (bool, error) {
	if !memberRead {
		return true, nil
	}
	return projections.ActorCanUseMemberBoard(db, actor, boardID)
}
