package core

import (
	"context"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type BoardRankingsProcessResult = derivedRebuildProcessResult

type BoardRankingsProcessor struct {
	derivedRebuildProcessor
}

func NewBoardRankingsProcessor(c *Core, interval time.Duration, batchSize int) (*BoardRankingsProcessor, error) {
	processor, err := newDerivedRebuildProcessor(c, "board rankings", interval, batchSize, (*Core).ProcessBoardRankingsOnce)
	if err != nil {
		return nil, err
	}
	return &BoardRankingsProcessor{derivedRebuildProcessor: processor}, nil
}

func (p *BoardRankingsProcessor) ProcessOnce() (BoardRankingsProcessResult, error) {
	if p == nil {
		return nilDerivedRebuildProcessResult("board rankings")
	}
	return p.derivedRebuildProcessor.ProcessOnce()
}

func (p *BoardRankingsProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.derivedRebuildProcessor.Run(ctx)
}

func (c *Core) StartBoardRankingsProcessor(ctx context.Context, interval time.Duration, batchSize int) (*BoardRankingsProcessor, error) {
	processor, err := NewBoardRankingsProcessor(c, interval, batchSize)
	return startPeriodicProcessor(ctx, processor, err)
}

func (c *Core) ProcessBoardRankingsOnce(batchSize int) (BoardRankingsProcessResult, error) {
	return c.processDerivedRebuildOnce(batchSize, derivedRebuildSpec{
		view:     projections.DerivedViewBoardRankings,
		rebuild:  projections.RebuildBoardRankingStats,
		rowCount: projections.BoardRankingStatsRowCount,
	})
}
