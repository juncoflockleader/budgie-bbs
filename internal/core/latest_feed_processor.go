package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type LatestFeedProcessResult struct {
	FromSeq     int64
	AppliedSeq  int64
	HeadSeq     int64
	Events      int
	FeedChanges int
}

type LatestFeedProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewLatestFeedProcessor(c *Core, interval time.Duration, batchSize int) (*LatestFeedProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("latest feed processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &LatestFeedProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *LatestFeedProcessor) ProcessOnce() (LatestFeedProcessResult, error) {
	if p == nil || p.Core == nil {
		return LatestFeedProcessResult{}, fmt.Errorf("latest feed processor: nil core")
	}
	return p.Core.ProcessLatestFeedOnce(p.BatchSize)
}

func (p *LatestFeedProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("latest feed processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.AppliedSeq < result.HeadSeq {
				slog.Debug("latest feed processor advanced",
					"fromSeq", result.FromSeq,
					"appliedSeq", result.AppliedSeq,
					"headSeq", result.HeadSeq,
					"events", result.Events,
					"feedChanges", result.FeedChanges)
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

func (c *Core) StartLatestFeedProcessor(ctx context.Context, interval time.Duration, batchSize int) (*LatestFeedProcessor, error) {
	processor, err := NewLatestFeedProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessLatestFeedOnce(batchSize int) (LatestFeedProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewLatestFeed)
	if err != nil {
		return LatestFeedProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return LatestFeedProcessResult{}, err
	}
	result := LatestFeedProcessResult{
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
			if err := c.RecordDerivedViewApplied(DerivedViewLatestFeed, fromSeq); err != nil {
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
		changed, err := applyLatestFeedEvent(tx, evt)
		if err != nil {
			return result, fmt.Errorf("latest feed event %d (%s): %w", evt.Seq, evt.Kind, err)
		}
		result.Events++
		if changed {
			result.FeedChanges++
		}
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, DerivedViewLatestFeed, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func applyLatestFeedEvent(tx *sql.Tx, evt *proto.Event) (bool, error) {
	switch payload := evt.Payload.(type) {
	case *proto.PostAppendedPayload:
		return upsertLatestFeedPost(tx, payload.ID)
	case *proto.PostRedactedPayload:
		return deleteLatestFeedPost(tx, payload.ID)
	case *proto.PostRestoredPayload:
		return upsertLatestFeedPost(tx, payload.ID)
	case *proto.PostPurgedPayload:
		return deleteLatestFeedPost(tx, payload.ID)
	case *proto.ThreadMovedPayload:
		return moveLatestFeedThread(tx, payload.Thread, payload.ToBoard)
	default:
		return false, nil
	}
}
