package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type DigestSearchProcessResult struct {
	FromSeq       int64
	AppliedSeq    int64
	HeadSeq       int64
	Events        int
	DigestChanges int
}

type DigestSearchProcessor struct {
	Core      *Core
	BatchSize int
	Interval  time.Duration
}

func NewDigestSearchProcessor(c *Core, interval time.Duration, batchSize int) (*DigestSearchProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("digest search processor: nil core")
	}
	if interval <= 0 {
		interval = time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &DigestSearchProcessor{
		Core:      c,
		BatchSize: batchSize,
		Interval:  interval,
	}, nil
}

func (p *DigestSearchProcessor) ProcessOnce() (DigestSearchProcessResult, error) {
	if p == nil || p.Core == nil {
		return DigestSearchProcessResult{}, fmt.Errorf("digest search processor: nil core")
	}
	return p.Core.ProcessDigestSearchOnce(p.BatchSize)
}

func (p *DigestSearchProcessor) Run(ctx context.Context) {
	if p == nil || p.Core == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := p.ProcessOnce()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("digest search processor failed", "err", err)
				}
				return
			}
			if result.Events > 0 || result.AppliedSeq < result.HeadSeq {
				slog.Debug("digest search processor advanced",
					"fromSeq", result.FromSeq,
					"appliedSeq", result.AppliedSeq,
					"headSeq", result.HeadSeq,
					"events", result.Events,
					"digestChanges", result.DigestChanges)
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

func (c *Core) StartDigestSearchProcessor(ctx context.Context, interval time.Duration, batchSize int) (*DigestSearchProcessor, error) {
	processor, err := NewDigestSearchProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go processor.Run(ctx)
	return processor, nil
}

func (c *Core) ProcessDigestSearchOnce(batchSize int) (DigestSearchProcessResult, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	fromSeq, found, err := lookupDerivedViewAppliedSeq(c.DB, DerivedViewDigestSearch)
	if err != nil {
		return DigestSearchProcessResult{}, err
	}
	if !found {
		fromSeq = 0
	}
	head, err := c.Head()
	if err != nil {
		return DigestSearchProcessResult{}, err
	}
	result := DigestSearchProcessResult{
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
			if err := c.RecordDerivedViewApplied(DerivedViewDigestSearch, fromSeq); err != nil {
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
		result.Events++
		if isDigestSearchEvent(evt) {
			result.DigestChanges++
		}
		if evt.Seq > result.AppliedSeq {
			result.AppliedSeq = evt.Seq
		}
	}
	if err := recordDerivedViewAppliedTx(tx, DerivedViewDigestSearch, result.AppliedSeq); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func isDigestSearchEvent(evt *proto.Event) bool {
	if evt == nil {
		return false
	}
	switch evt.Kind {
	case proto.EvtDigestEntryUpserted,
		proto.EvtDigestEntryUpdated,
		proto.EvtDigestEntryBodySet,
		proto.EvtDigestEntryRemoved,
		proto.EvtDigestDirectorySet,
		proto.EvtDigestPathMoved,
		proto.EvtDigestPathCopied,
		proto.EvtDigestPathDeleted:
		return true
	default:
		return false
	}
}
