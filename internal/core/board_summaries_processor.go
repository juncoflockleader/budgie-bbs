package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type BoardSummariesProcessResult = derivedRebuildProcessResult

type BoardSummariesProcessor struct {
	derivedRebuildProcessor
}

func NewBoardSummariesProcessor(c *Core, interval time.Duration, batchSize int) (*BoardSummariesProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "board summaries", interval, batchSize, (*Core).ProcessBoardSummariesOnce)
	if err != nil {
		return nil, err
	}
	return &BoardSummariesProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *BoardSummariesProcessor) ProcessOnce() (BoardSummariesProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("board summaries")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *BoardSummariesProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartBoardSummariesProcessor(ctx context.Context, interval time.Duration, batchSize int) (*BoardSummariesProcessor, error) {
	processor, err := NewBoardSummariesProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessBoardSummariesOnce(batchSize int) (BoardSummariesProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     projections.DerivedViewBoardSummaries,
		rebuild:  projections.RebuildBoardSummaryStats,
		rowCount: projections.BoardSummaryStatsRowCount,
	})
}
