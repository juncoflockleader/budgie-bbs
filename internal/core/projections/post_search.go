package projections

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
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

type PostSearchBodyCleaner func(sourceBody string) (cleanBody string, ok bool)

func PostSearchEventApplier(cleanBody PostSearchBodyCleaner) func(*sql.Tx, *proto.Event) (bool, error) {
	return func(tx *sql.Tx, evt *proto.Event) (bool, error) {
		return ApplyPostSearchEvent(tx, evt, cleanBody)
	}
}

func ListPostSearchDocuments(db *sql.DB, threadID string, limit int) ([]PostSearchDocument, error) {
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
	rows, err := QQuery(db,
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
		normalizePostSearchDocument(&doc)
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func PostSearchDocumentByID(db *sql.DB, postID string) (PostSearchDocument, bool, error) {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return PostSearchDocument{}, false, nil
	}
	rows, err := QQuery(db,
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
	normalizePostSearchDocument(&doc)
	if rows.Next() {
		return PostSearchDocument{}, false, fmt.Errorf("duplicate post search document %s", postID)
	}
	return doc, true, rows.Err()
}

func ApplyPostSearchEvent(tx *sql.Tx, evt *proto.Event, cleanBody PostSearchBodyCleaner) (bool, error) {
	if evt == nil {
		return false, nil
	}
	switch payload := evt.Payload.(type) {
	case *proto.PostAppendedPayload:
		boardID, err := postSearchThreadBoard(tx, payload.Thread)
		if err != nil {
			return false, err
		}
		if err := FtsInsertPost(tx, payload.ID, payload.Thread, boardID, payload.Author, postSearchAppendBody(payload, cleanBody)); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostEditedPayload:
		if err := FtsUpdatePost(tx, payload.ID, payload.NewBody); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRedactedPayload:
		if err := FtsDeletePost(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRestoredPayload:
		if err := reindexPostSearchFromProjection(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostPurgedPayload:
		if err := FtsDeletePost(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.ThreadMovedPayload:
		if _, err := QExec(tx, `UPDATE posts_fts SET board_id=? WHERE thread_id=?`, payload.ToBoard, payload.Thread); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func normalizePostSearchDocument(doc *PostSearchDocument) {
	doc.ID = doc.PostID
	if doc.CreatedAt == 0 {
		doc.CreatedAt = doc.CreatedSeq
	}
	if doc.UpdatedAt == 0 {
		doc.UpdatedAt = doc.CreatedAt
	}
}

func postSearchAppendBody(payload *proto.PostAppendedPayload, cleanBody PostSearchBodyCleaner) string {
	body := payload.Body
	sourceBody := payload.Body
	if strings.TrimSpace(payload.RawBody) != "" {
		sourceBody = payload.RawBody
	}
	if cleanBody == nil {
		return body
	}
	if cleaned, ok := cleanBody(sourceBody); ok && cleaned != sourceBody {
		body = cleaned
	}
	return body
}

func postSearchThreadBoard(tx *sql.Tx, threadID string) (string, error) {
	var boardID string
	err := QQueryRow(tx, `SELECT board FROM threads WHERE id=?`, threadID).Scan(&boardID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return boardID, err
}

func reindexPostSearchFromProjection(tx *sql.Tx, postID string) error {
	var id, threadID, boardID, author, body string
	err := QQueryRow(tx,
		`SELECT p.id, p.thread, t.board, p.author, p.body
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		  WHERE p.id=? AND p.redacted=0
		  LIMIT 1`,
		postID,
	).Scan(&id, &threadID, &boardID, &author, &body)
	if err == sql.ErrNoRows {
		return FtsDeletePost(tx, postID)
	}
	if err != nil {
		return err
	}
	return FtsInsertPost(tx, id, threadID, boardID, author, body)
}
