package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type PostSearchProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Indexed    int
}

type PostSearchProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewPostSearchProcessor(c *Core, interval time.Duration, batchSize int) (*PostSearchProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("post search processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &PostSearchProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *PostSearchProcessor) ProcessOnce() (PostSearchProcessResult, error) {
	if p == nil || p.Core == nil {
		return PostSearchProcessResult{}, fmt.Errorf("post search processor: nil core")
	}
	return p.Core.ProcessPostSearchOnce(p.BatchSize)
}

func (p *PostSearchProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("post search processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.AppliedSeq < result.HeadSeq {
				slog.Debug("post search processor advanced",
					"fromSeq", result.FromSeq,
					"appliedSeq", result.AppliedSeq,
					"headSeq", result.HeadSeq,
					"events", result.Events,
					"indexed", result.Indexed)
			}
			if result.Events < p.BatchSize || result.AppliedSeq >= result.HeadSeq {
				return
			}
		}
	}
	drain()
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drain()
		}
	}
}

func (c *Core) StartPostSearchProcessor(ctx context.Context, interval time.Duration, batchSize int) (*PostSearchProcessor, error) {
	processor, err := NewPostSearchProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessPostSearchOnce(batchSize int) (PostSearchProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	if c.postSearchIndex != nil {
		return c.processExternalPostSearchOnce(batchSize)
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewPostSearch)
	if err != nil {
		return PostSearchProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return PostSearchProcessResult{}, err
	}
	result := PostSearchProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		if !found {
			if err := c.RecordDerivedViewApplied(DerivedViewPostSearch, fromSeq); err != nil {
				return result, err
			}
		}
		return result, nil
	}

	tx, err := c.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint

	for _, evt := range events {
		if evt == nil {
			continue
		}
		indexed, err := applyPostSearchEvent(tx, evt)
		if err != nil {
			return result, fmt.Errorf("post search event %d (%s): %w", evt.Seq, evt.Kind, err)
		}
		result.Events++
		if indexed {
			result.Indexed++
		}
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, DerivedViewPostSearch, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Core) processExternalPostSearchOnce(batchSize int) (PostSearchProcessResult, error) {
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewPostSearch)
	if err != nil {
		return PostSearchProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return PostSearchProcessResult{}, err
	}
	result := PostSearchProcessResult{
		FromSeq:    fromSeq,
		AppliedSeq: fromSeq,
		HeadSeq:    head,
	}
	events, err := c.Replay(fromSeq, nil, batchSize)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		if !found {
			if err := c.RecordDerivedViewApplied(DerivedViewPostSearch, fromSeq); err != nil {
				return result, err
			}
		}
		return result, nil
	}

	for _, evt := range events {
		if evt == nil {
			continue
		}
		indexed, err := c.applyExternalPostSearchEvent(context.Background(), evt)
		if err != nil {
			return result, fmt.Errorf("external post search event %d (%s): %w", evt.Seq, evt.Kind, err)
		}
		result.Events++
		if indexed {
			result.Indexed++
		}
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := c.RecordDerivedViewApplied(DerivedViewPostSearch, result.AppliedSeq); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Core) applyExternalPostSearchEvent(ctx context.Context, evt *proto.Event) (bool, error) {
	if c.postSearchIndex == nil || evt == nil {
		return false, nil
	}
	switch payload := evt.Payload.(type) {
	case *proto.PostAppendedPayload:
		return c.upsertExternalPostSearchDocument(ctx, payload.ID)
	case *proto.PostEditedPayload:
		return c.upsertExternalPostSearchDocument(ctx, payload.ID)
	case *proto.PostRedactedPayload:
		if err := c.postSearchIndex.DeletePost(ctx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRestoredPayload:
		return c.upsertExternalPostSearchDocument(ctx, payload.ID)
	case *proto.PostPurgedPayload:
		if err := c.postSearchIndex.DeletePost(ctx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.ThreadMovedPayload:
		docs, err := listPostSearchDocuments(c.DB, payload.Thread, 0)
		if err != nil {
			return false, err
		}
		for _, doc := range docs {
			if err := c.postSearchIndex.UpsertPost(ctx, doc); err != nil {
				return false, err
			}
		}
		return len(docs) > 0, nil
	default:
		return false, nil
	}
}

func (c *Core) upsertExternalPostSearchDocument(ctx context.Context, postID string) (bool, error) {
	doc, ok, err := postSearchDocumentByID(c.DB, postID)
	if err != nil {
		return false, err
	}
	if !ok {
		if err := c.postSearchIndex.DeletePost(ctx, postID); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := c.postSearchIndex.UpsertPost(ctx, doc); err != nil {
		return false, err
	}
	return true, nil
}

func applyPostSearchEvent(tx *sql.Tx, evt *proto.Event) (bool, error) {
	switch payload := evt.Payload.(type) {
	case *proto.PostAppendedPayload:
		body := payload.Body
		sourceBody := payload.Body
		if strings.TrimSpace(payload.RawBody) != "" {
			sourceBody = payload.RawBody
		}
		if pollBlock, cleanBody := extractPoll(sourceBody); pollBlock != nil && cleanBody != sourceBody {
			body = cleanBody
		}
		boardID := ""
		if thread, err := getThreadTx(tx, payload.Thread); err != nil {
			return false, err
		} else if thread != nil {
			boardID = thread.Board
		}
		if err := ftsInsertPost(tx, payload.ID, payload.Thread, boardID, payload.Author, body); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostEditedPayload:
		if err := ftsUpdatePost(tx, payload.ID, payload.NewBody); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRedactedPayload:
		if err := ftsDeletePost(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRestoredPayload:
		if err := reindexPostSearchFromProjection(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostPurgedPayload:
		if err := ftsDeletePost(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.ThreadMovedPayload:
		if _, err := qExec(tx, `UPDATE posts_fts SET board_id=? WHERE thread_id=?`, payload.ToBoard, payload.Thread); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func reindexPostSearchFromProjection(tx *sql.Tx, postID string) error {
	post, err := getPostTx(tx, postID)
	if err != nil {
		return err
	}
	if post == nil || post.Redacted {
		return ftsDeletePost(tx, postID)
	}
	thread, err := getThreadTx(tx, post.Thread)
	if err != nil {
		return err
	}
	if thread == nil {
		return ftsDeletePost(tx, postID)
	}
	return ftsInsertPost(tx, post.ID, post.Thread, thread.Board, post.Author, post.Body)
}

func recordDerivedViewAppliedTx(tx *sql.Tx, view string, appliedSeq int64) error {
	view = normalizeDerivedView(view)
	if view == "" {
		return fmt.Errorf("derived view name required")
	}
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	_, err := qExec(tx,
		`INSERT INTO derived_view_watermarks (view_name, applied_seq, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(view_name) DO UPDATE
		       SET applied_seq=excluded.applied_seq,
		           updated_at=excluded.updated_at`,
		view, appliedSeq, nowMS(),
	)
	return err
}
