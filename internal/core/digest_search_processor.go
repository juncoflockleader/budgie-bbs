package core

import (
	"context"
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
	periodicProcessor
}

func NewDigestSearchProcessor(c *Core, interval time.Duration, batchSize int) (*DigestSearchProcessor, error) {
	processor, err := newPeriodicProcessor(c, "digest search", interval, batchSize, func(_ context.Context, c *Core, batchSize int) (processorRunProgress, error) {
		result, err := c.ProcessDigestSearchOnce(batchSize)
		return digestSearchRunProgress(result), err
	})
	if err != nil {
		return nil, err
	}
	return &DigestSearchProcessor{periodicProcessor: processor}, nil
}

func (p *DigestSearchProcessor) ProcessOnce() (DigestSearchProcessResult, error) {
	if p == nil || p.Core == nil {
		return DigestSearchProcessResult{}, nilProcessorError("digest search")
	}
	return p.Core.ProcessDigestSearchOnce(p.BatchSize)
}

func (p *DigestSearchProcessor) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.periodicProcessor.Run(ctx)
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
	batch, err := c.replayDerivedViewEventBatch(DerivedViewDigestSearch, batchSize)
	result := DigestSearchProcessResult{
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

	events, changes, appliedSeq, err := c.applyDerivedViewEventBatchTx(batch, "digest search", derivedViewEventPredicate(isDigestSearchEvent))
	if err != nil {
		return result, err
	}
	result.Events = events
	result.DigestChanges = changes
	result.AppliedSeq = appliedSeq
	return result, nil
}

func digestSearchRunProgress(result DigestSearchProcessResult) processorRunProgress {
	return processorRunProgress{
		FromSeq:    result.FromSeq,
		AppliedSeq: result.AppliedSeq,
		HeadSeq:    result.HeadSeq,
		Events:     result.Events,
		Log:        result.Events > 0 || result.AppliedSeq < result.HeadSeq,
		Extra:      []any{"digestChanges", result.DigestChanges},
	}
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
