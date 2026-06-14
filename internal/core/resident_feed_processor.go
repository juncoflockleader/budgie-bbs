package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type ResidentFeedProcessResult struct {
	FromSeq     int64
	AppliedSeq  int64
	HeadSeq     int64
	Events      int
	FeedChanges int
}

type ResidentFeedProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewResidentFeedProcessor(c *Core, interval time.Duration, batchSize int) (*ResidentFeedProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("resident feed processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ResidentFeedProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *ResidentFeedProcessor) ProcessOnce() (ResidentFeedProcessResult, error) {
	if p == nil || p.Core == nil {
		return ResidentFeedProcessResult{}, fmt.Errorf("resident feed processor: nil core")
	}
	return p.Core.ProcessResidentFeedOnce(p.BatchSize)
}

func (p *ResidentFeedProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("resident feed processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.AppliedSeq < result.HeadSeq {
				slog.Debug("resident feed processor advanced",
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

func (c *Core) StartResidentFeedProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ResidentFeedProcessor, error) {
	processor, err := NewResidentFeedProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessResidentFeedOnce(batchSize int) (ResidentFeedProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewResidentFeed)
	if err != nil {
		return ResidentFeedProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return ResidentFeedProcessResult{}, err
	}
	result := ResidentFeedProcessResult{
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
			if err := c.RecordDerivedViewApplied(DerivedViewResidentFeed, fromSeq); err != nil {
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
		changed, err := applyResidentFeedEvent(tx, evt)
		if err != nil {
			return result, fmt.Errorf("resident feed event %d (%s): %w", evt.Seq, evt.Kind, err)
		}
		result.Events++
		if changed {
			result.FeedChanges++
		}
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, DerivedViewResidentFeed, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func applyResidentFeedEvent(tx *sql.Tx, evt *proto.Event) (bool, error) {
	switch payload := evt.Payload.(type) {
	case *proto.PostAppendedPayload:
		return upsertResidentFeedPost(tx, payload.ID)
	case *proto.PostRedactedPayload:
		return deleteResidentFeedPost(tx, payload.ID)
	case *proto.PostRestoredPayload:
		return upsertResidentFeedPost(tx, payload.ID)
	case *proto.PostPurgedPayload:
		return deleteResidentFeedPost(tx, payload.ID)
	case *proto.ThreadMovedPayload:
		return moveResidentFeedThread(tx, payload.Thread, payload.ToBoard)
	default:
		return false, nil
	}
}
