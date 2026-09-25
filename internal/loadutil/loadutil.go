package loadutil

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

func Body(size int) string {
	if size <= 0 {
		return "load"
	}
	const pattern = "partition-write-load "
	var b strings.Builder
	for b.Len() < size {
		b.WriteString(pattern)
	}
	return b.String()[:size]
}

func PerSecond(count int, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(count) / elapsed.Seconds()
}

func SafeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "load"
	}
	return out
}

func RunCommandLogLoadSubmitStage(ctx context.Context, stage loadmodel.CommandLogLoadStage, workers int, failurePrefix string, submit func(workerID, job int) string) (loadmodel.CommandLogLoadStage, error) {
	if stage.Commands <= 0 {
		return stage, nil
	}
	workers = max(workers, 1)
	type submitResult struct {
		errText string
	}
	jobs := make(chan int)
	results := make(chan submitResult, stage.Commands)
	var wg sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range jobs {
				results <- submitResult{errText: submit(workerID, i)}
			}
		}(worker)
	}
	for i := 0; i < stage.Commands; i++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(results)
			stage.DurationMS = time.Since(start).Milliseconds()
			return stage, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	elapsed := time.Since(start)
	stage.DurationMS = elapsed.Milliseconds()
	errSamples := map[string]bool{}
	for result := range results {
		if result.errText != "" {
			stage.Failed++
			loadmodel.AddLoadSample(&stage.SampleErrorText, errSamples, result.errText)
		} else {
			stage.Succeeded++
		}
	}
	stage.CommandsPerSec = PerSecond(stage.Succeeded, elapsed)
	if stage.Failed > 0 {
		return stage, fmt.Errorf("%s failed %d/%d commands", failurePrefix, stage.Failed, stage.Commands)
	}
	return stage, nil
}
