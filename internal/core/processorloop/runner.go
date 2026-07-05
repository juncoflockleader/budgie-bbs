package processorloop

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Progress struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Log        bool
	Extra      []any
}

type ProcessFunc func(context.Context, int) (Progress, error)

type Runner struct {
	Name      string
	BatchSize int
	Interval  time.Duration
	Process   ProcessFunc
}

func (r *Runner) ProcessOnce(ctx context.Context) (Progress, error) {
	if r == nil || r.Process == nil {
		name := "periodic"
		if r != nil && r.Name != "" {
			name = r.Name
		}
		return Progress{}, fmt.Errorf("%s processor: missing process function", name)
	}
	return r.Process(ctx, r.BatchSize)
}

func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.Process == nil {
		return
	}
	drain := func() {
		for ctx.Err() == nil {
			result, err := r.ProcessOnce(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn(r.Name+" processor failed", "err", err)
				}
				return
			}
			if result.Log {
				attrs := []any{
					"fromSeq", result.FromSeq,
					"appliedSeq", result.AppliedSeq,
					"headSeq", result.HeadSeq,
					"events", result.Events,
				}
				attrs = append(attrs, result.Extra...)
				slog.Debug(r.Name+" processor advanced", attrs...)
			}
			if result.Events < r.BatchSize || result.AppliedSeq >= result.HeadSeq {
				return
			}
		}
	}
	drain()
	ticker := time.NewTicker(r.Interval)
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
