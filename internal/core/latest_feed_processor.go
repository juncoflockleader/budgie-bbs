package core

import (
	"context"
	"database/sql"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type LatestFeedProcessResult = feedMaterializationProcessResult

type feedMaterializationProcessResult struct {
	FromSeq     int64
	AppliedSeq  int64
	HeadSeq     int64
	Events      int
	FeedChanges int
}

type feedMaterializationSpec struct {
	view      string
	errPrefix string
	apply     func(*sql.Tx, *proto.Event) (bool, error)
}

type LatestFeedProcessor struct {
	periodicProcessor
}

func NewLatestFeedProcessor(c *Core, interval time.Duration, batchSize int) (*LatestFeedProcessor, error) {
	processor, err := newCorePeriodicProcessor(c, "latest feed", interval, batchSize, (*Core).ProcessLatestFeedOnce, latestFeedRunProgress)
	if err != nil {
		return nil, err
	}
	return &LatestFeedProcessor{periodicProcessor: processor}, nil
}

func (p *LatestFeedProcessor) ProcessOnce() (LatestFeedProcessResult, error) {
	if p == nil || p.Core == nil {
		return LatestFeedProcessResult{}, nilProcessorError("latest feed")
	}
	return p.Core.ProcessLatestFeedOnce(p.BatchSize)
}

func (p *LatestFeedProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.periodicProcessor.Run(ctx)
}

func (c *Core) StartLatestFeedProcessor(ctx context.Context, interval time.Duration, batchSize int) (*LatestFeedProcessor, error) {
	processor, err := NewLatestFeedProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessLatestFeedOnce(batchSize int) (LatestFeedProcessResult, error) {
	return c.processFeedMaterializationOnce(batchSize, feedMaterializationSpec{
		view:      projections.DerivedViewLatestFeed,
		errPrefix: "latest feed",
		apply:     projections.ApplyLatestFeedEvent,
	})
}

func (c *Core) processFeedMaterializationOnce(batchSize int, spec feedMaterializationSpec) (feedMaterializationProcessResult, error) {
	batch, err := c.replayDerivedViewEventBatch(spec.view, batchSize)
	result := feedMaterializationProcessResult{
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

	events, changes, appliedSeq, err := c.applyDerivedViewEventBatchTx(batch, spec.errPrefix, spec.apply)
	if err != nil {
		return result, err
	}
	result.Events = events
	result.FeedChanges = changes
	result.AppliedSeq = appliedSeq
	return result, nil
}

func latestFeedRunProgress(result LatestFeedProcessResult) processorRunProgress {
	return processorRunProgress{
		FromSeq:    result.FromSeq,
		AppliedSeq: result.AppliedSeq,
		HeadSeq:    result.HeadSeq,
		Events:     result.Events,
		Log:        result.Events > 0 || result.AppliedSeq < result.HeadSeq,
		Extra:      []any{"feedChanges", result.FeedChanges},
	}
}
