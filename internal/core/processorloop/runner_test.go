package processorloop

import (
	"context"
	"testing"
	"time"
)

func TestRunnerRunDrainsUntilCaughtUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	runner := Runner{
		Name:      "test",
		BatchSize: 1,
		Interval:  time.Hour,
		Process: func(context.Context, int) (Progress, error) {
			calls++
			if calls == 3 {
				cancel()
			}
			return Progress{Events: 1, AppliedSeq: int64(calls), HeadSeq: 3}, nil
		},
	}
	runner.Run(ctx)

	if calls != 3 {
		t.Fatalf("calls = %d, want drain until caught up", calls)
	}
}

func TestRunnerRunStopsDrainOnPartialBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	runner := Runner{
		Name:      "test",
		BatchSize: 5,
		Interval:  time.Hour,
		Process: func(context.Context, int) (Progress, error) {
			calls++
			cancel()
			return Progress{Events: 2, AppliedSeq: 1, HeadSeq: 10}, nil
		},
	}
	runner.Run(ctx)

	if calls != 1 {
		t.Fatalf("calls = %d, want partial batch to stop current drain", calls)
	}
}
