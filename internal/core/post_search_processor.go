package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
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
	periodicProcessor
}

func NewPostSearchProcessor(c *Core, interval time.Duration, batchSize int) (*PostSearchProcessor, error) {
	processor, err := newCorePeriodicProcessor(c, "post search", interval, batchSize, (*Core).ProcessPostSearchOnce, postSearchRunProgress)
	if err != nil {
		return nil, err
	}
	return &PostSearchProcessor{periodicProcessor: processor}, nil
}

func (p *PostSearchProcessor) ProcessOnce() (PostSearchProcessResult, error) {
	if p == nil || p.Core == nil {
		return PostSearchProcessResult{}, nilProcessorError("post search")
	}
	return p.Core.ProcessPostSearchOnce(p.BatchSize)
}

func (p *PostSearchProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.periodicProcessor.Run(ctx)
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
	if c.postSearchIndex != nil {
		return c.processExternalPostSearchOnce(batchSize)
	}
	batch, err := c.replayDerivedViewEventBatch(DerivedViewPostSearch, batchSize)
	result := PostSearchProcessResult{
		FromSeq:    batch.FromSeq,
		AppliedSeq: batch.AppliedSeq,
		HeadSeq:    batch.HeadSeq,
	}
	if err != nil {
		return result, err
	}
	if len(batch.Events) == 0 {
		return result, c.finishEmptyDerivedViewEventBatch(batch)
	}

	events, indexed, appliedSeq, err := c.applyDerivedViewEventBatchTx(batch, "post search", applyPostSearchEvent)
	if err != nil {
		return result, err
	}
	result.Events = events
	result.Indexed = indexed
	result.AppliedSeq = appliedSeq
	return result, nil
}

func postSearchRunProgress(result PostSearchProcessResult) processorRunProgress {
	return processorRunProgress{
		FromSeq:    result.FromSeq,
		AppliedSeq: result.AppliedSeq,
		HeadSeq:    result.HeadSeq,
		Events:     result.Events,
		Log:        result.Events > 0 || result.AppliedSeq < result.HeadSeq,
		Extra:      []any{"indexed", result.Indexed},
	}
}

func (c *Core) processExternalPostSearchOnce(batchSize int) (PostSearchProcessResult, error) {
	batch, err := c.replayDerivedViewEventBatch(DerivedViewPostSearch, batchSize)
	result := PostSearchProcessResult{
		FromSeq:    batch.FromSeq,
		AppliedSeq: batch.AppliedSeq,
		HeadSeq:    batch.HeadSeq,
	}
	if err != nil {
		return result, err
	}
	if len(batch.Events) == 0 {
		return result, c.finishEmptyDerivedViewEventBatch(batch)
	}

	for _, evt := range batch.Events {
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
	if err := c.RecordDerivedViewApplied(batch.View, result.AppliedSeq); err != nil {
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
		if err := projections.FtsInsertPost(tx, payload.ID, payload.Thread, boardID, payload.Author, body); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostEditedPayload:
		if err := projections.FtsUpdatePost(tx, payload.ID, payload.NewBody); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRedactedPayload:
		if err := projections.FtsDeletePost(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostRestoredPayload:
		if err := reindexPostSearchFromProjection(tx, payload.ID); err != nil {
			return false, err
		}
		return true, nil
	case *proto.PostPurgedPayload:
		if err := projections.FtsDeletePost(tx, payload.ID); err != nil {
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
		return projections.FtsDeletePost(tx, postID)
	}
	thread, err := getThreadTx(tx, post.Thread)
	if err != nil {
		return err
	}
	if thread == nil {
		return projections.FtsDeletePost(tx, postID)
	}
	return projections.FtsInsertPost(tx, post.ID, post.Thread, thread.Board, post.Author, post.Body)
}

func recordDerivedViewAppliedTx(tx *sql.Tx, view string, appliedSeq int64) error {
	return projections.RecordDerivedViewApplied(tx, view, appliedSeq, nowMS())
}
