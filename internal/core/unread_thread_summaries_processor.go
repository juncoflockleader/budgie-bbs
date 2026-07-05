package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type UnreadThreadSummariesProcessResult = derivedRebuildProcessResult

type UnreadThreadSummariesProcessor struct {
	derivedRebuildProcessor
}

func NewUnreadThreadSummariesProcessor(c *Core, interval time.Duration, batchSize int) (*UnreadThreadSummariesProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "unread thread summaries", interval, batchSize, (*Core).ProcessUnreadThreadSummariesOnce)
	if err != nil {
		return nil, err
	}
	return &UnreadThreadSummariesProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *UnreadThreadSummariesProcessor) ProcessOnce() (UnreadThreadSummariesProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("unread thread summaries")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *UnreadThreadSummariesProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartUnreadThreadSummariesProcessor(ctx context.Context, interval time.Duration, batchSize int) (*UnreadThreadSummariesProcessor, error) {
	processor, err := NewUnreadThreadSummariesProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessUnreadThreadSummariesOnce(batchSize int) (UnreadThreadSummariesProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     projections.DerivedViewUnreadThreads,
		rebuild:  projections.RebuildUnreadThreadSummaryStats,
		rowCount: projections.UnreadThreadSummaryStatsRowCount,
	})
}
