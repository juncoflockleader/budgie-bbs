package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type ResidentFeedProcessResult = feedMaterializationProcessResult

type ResidentFeedProcessor struct {
	periodicProcessor
}

func NewResidentFeedProcessor(c *Core, interval time.Duration, batchSize int) (*ResidentFeedProcessor, error) {
	processor, err := newCorePeriodicProcessor(c, "resident feed", interval, batchSize, (*Core).ProcessResidentFeedOnce, residentFeedRunProgress)
	if err != nil {
		return nil, err
	}
	return &ResidentFeedProcessor{periodicProcessor: processor}, nil
}

func (p *ResidentFeedProcessor) ProcessOnce() (ResidentFeedProcessResult, error) {
	if p == nil || p.Core == nil {
		return ResidentFeedProcessResult{}, nilProcessorError("resident feed")
	}
	return p.Core.ProcessResidentFeedOnce(p.BatchSize)
}

func (p *ResidentFeedProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.periodicProcessor.Run(ctx)
}

func (c *Core) StartResidentFeedProcessor(ctx context.Context, interval time.Duration, batchSize int) (*ResidentFeedProcessor, error) {
	processor, err := NewResidentFeedProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessResidentFeedOnce(batchSize int) (ResidentFeedProcessResult, error) {
	return c.processFeedMaterializationOnce(batchSize, feedMaterializationSpec{
		view:      projections.DerivedViewResidentFeed,
		errPrefix: "resident feed",
		apply:     projections.ApplyResidentFeedEvent,
	})
}

func residentFeedRunProgress(result ResidentFeedProcessResult) processorRunProgress {
	return processorRunProgress{
		FromSeq:    result.FromSeq,
		AppliedSeq: result.AppliedSeq,
		HeadSeq:    result.HeadSeq,
		Events:     result.Events,
		Log:        result.Events > 0 || result.AppliedSeq < result.HeadSeq,
		Extra:      []any{"feedChanges", result.FeedChanges},
	}
}
