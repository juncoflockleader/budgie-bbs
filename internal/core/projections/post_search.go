package projections

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type PostSearchBodyCleaner func(sourceBody string) (cleanBody string, ok bool)

func PostSearchEventApplier(cleanBody PostSearchBodyCleaner) func(*sql.Tx, *proto.Event) (bool, error) {
	return func(tx *sql.Tx, evt *proto.Event) (bool, error) {
		return ApplyPostSearchEvent(tx, evt, cleanBody)
	}
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
