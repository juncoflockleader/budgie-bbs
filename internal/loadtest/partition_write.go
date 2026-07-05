package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadutil"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

type PartitionWriteLoadConfig struct {
	Boards         int    `json:"boards"`
	WritesPerBoard int    `json:"writesPerBoard"`
	Concurrency    int    `json:"concurrency"`
	BodyBytes      int    `json:"bodyBytes"`
	BoardPrefix    string `json:"boardPrefix"`
	UserName       string `json:"userName"`
}

type PartitionWriteLoadReport struct {
	Config            PartitionWriteLoadConfig `json:"config"`
	StartedAt         int64                    `json:"startedAt"`
	FinishedAt        int64                    `json:"finishedAt"`
	TotalWrites       int                      `json:"totalWrites"`
	SamePartition     PartitionWriteLoadCase   `json:"samePartition"`
	SpreadPartitions  PartitionWriteLoadCase   `json:"spreadPartitions"`
	SpreadSpeedup     float64                  `json:"spreadSpeedup"`
	SpreadThroughputX float64                  `json:"spreadThroughputX"`
}

type PartitionWriteLoadCase struct {
	Name            string   `json:"name"`
	Boards          int      `json:"boards"`
	Writes          int      `json:"writes"`
	Succeeded       int      `json:"succeeded"`
	Failed          int      `json:"failed"`
	DurationMS      int64    `json:"durationMs"`
	WritesPerSec    float64  `json:"writesPerSec"`
	LatencyP50MS    float64  `json:"latencyP50Ms"`
	LatencyP95MS    float64  `json:"latencyP95Ms"`
	LatencyP99MS    float64  `json:"latencyP99Ms"`
	MaxLatencyMS    float64  `json:"maxLatencyMs"`
	SampleErrorText []string `json:"sampleErrorText,omitempty"`
}

func DefaultPartitionWriteLoadConfig() PartitionWriteLoadConfig {
	return PartitionWriteLoadConfig{
		Boards:         8,
		WritesPerBoard: 50,
		Concurrency:    32,
		BodyBytes:      256,
		BoardPrefix:    "load",
		UserName:       "load_admin",
	}
}

func RunPartitionWriteLoad(ctx context.Context, c *core.Core, config PartitionWriteLoadConfig) (PartitionWriteLoadReport, error) {
	if c == nil {
		return PartitionWriteLoadReport{}, fmt.Errorf("partition write load: nil core")
	}
	config = normalizePartitionWriteLoadConfig(config)
	report := PartitionWriteLoadReport{
		Config:           config,
		StartedAt:        time.Now().UnixMilli(),
		TotalWrites:      config.Boards * config.WritesPerBoard,
		SamePartition:    newPartitionWriteLoadCase("same_partition", 1, config.Boards*config.WritesPerBoard),
		SpreadPartitions: newPartitionWriteLoadCase("spread_partitions", config.Boards, config.Boards*config.WritesPerBoard),
	}

	actor, err := c.RegisterUser(config.UserName, fmt.Sprintf("pw_%d", time.Now().UnixNano()))
	if err != nil {
		return report, fmt.Errorf("register load user: %w", err)
	}
	boardIDs, err := createPartitionWriteLoadBoards(ctx, c, actor, config)
	if err != nil {
		return report, err
	}

	sameBoard := []string{boardIDs[0]}
	report.SamePartition, err = runPartitionWriteLoadCase(ctx, c, actor, config, "same_partition", sameBoard, report.TotalWrites)
	if err != nil {
		report.FinishedAt = time.Now().UnixMilli()
		return report, err
	}
	report.SpreadPartitions, err = runPartitionWriteLoadCase(ctx, c, actor, config, "spread_partitions", boardIDs, report.TotalWrites)
	if err != nil {
		report.FinishedAt = time.Now().UnixMilli()
		return report, err
	}
	if report.SamePartition.WritesPerSec > 0 {
		report.SpreadSpeedup = report.SpreadPartitions.WritesPerSec / report.SamePartition.WritesPerSec
		report.SpreadThroughputX = report.SpreadSpeedup
	}
	report.FinishedAt = time.Now().UnixMilli()
	return report, nil
}

func EvaluatePartitionWriteBudget(report PartitionWriteLoadReport, budget *scalebudget.PostgresWriteBudget) []scalebudget.ScaleBudgetViolation {
	if budget == nil {
		return nil
	}
	var out []scalebudget.ScaleBudgetViolation
	out = scalebudget.AddMinViolation(out, "postgresWrites.minSpreadSpeedup", report.SpreadSpeedup, budget.MinSpreadSpeedup,
		"spread-partition throughput speedup is below budget")
	out = scalebudget.AddMinViolation(out, "postgresWrites.minSpreadWritesPerSec", report.SpreadPartitions.WritesPerSec, budget.MinSpreadWritesPerSec,
		"spread-partition writes/sec is below budget")
	out = scalebudget.AddPositiveMaxViolation(out, "postgresWrites.maxSamePartitionP95Ms", report.SamePartition.LatencyP95MS, budget.MaxSamePartitionP95MS,
		"same-partition p95 latency is above budget")
	out = scalebudget.AddPositiveMaxViolation(out, "postgresWrites.maxSpreadPartitionP95Ms", report.SpreadPartitions.LatencyP95MS, budget.MaxSpreadPartitionP95MS,
		"spread-partition p95 latency is above budget")
	failed := report.SamePartition.Failed + report.SpreadPartitions.Failed
	out = scalebudget.AddZeroOrPositiveMaxIntViolation(out, "postgresWrites.maxFailedWrites", failed, budget.MaxFailedWrites,
		"failed writes are above budget", "failed writes are above zero-failure budget")
	return out
}

func normalizePartitionWriteLoadConfig(config PartitionWriteLoadConfig) PartitionWriteLoadConfig {
	def := DefaultPartitionWriteLoadConfig()
	config.Boards = positiveOrDefault(config.Boards, def.Boards)
	config.WritesPerBoard = positiveOrDefault(config.WritesPerBoard, def.WritesPerBoard)
	config.Concurrency = positiveOrDefault(config.Concurrency, def.Concurrency)
	config.BodyBytes = nonNegativeOrDefault(config.BodyBytes, def.BodyBytes)
	if strings.TrimSpace(config.BoardPrefix) == "" {
		config.BoardPrefix = def.BoardPrefix
	}
	if strings.TrimSpace(config.UserName) == "" {
		config.UserName = def.UserName
	}
	config.Concurrency = min(config.Concurrency, config.Boards*config.WritesPerBoard)
	return config
}

func createPartitionWriteLoadBoards(ctx context.Context, c *core.Core, actor *projections.User, config PartitionWriteLoadConfig) ([]string, error) {
	boardIDs := make([]string, 0, config.Boards)
	for i := 0; i < config.Boards; i++ {
		boardID := fmt.Sprintf("%s_%02d", loadutil.SafeID(config.BoardPrefix), i)
		payload := proto.CreateBoardPayload{
			ID:          boardID,
			Name:        fmt.Sprintf("Load %02d", i),
			Description: "Partition write load fixture",
		}
		if reply := execLoadCommand(ctx, c, actor, proto.CmdCreateBoard, payload, fmt.Sprintf("load-board-%d", i)); reply.Err != nil {
			return nil, fmt.Errorf("create load board %s: %s (%s)", boardID, reply.Err.Message, reply.Err.Code)
		}
		boardIDs = append(boardIDs, boardID)
	}
	return boardIDs, nil
}

func runPartitionWriteLoadCase(ctx context.Context, c *core.Core, actor *projections.User, config PartitionWriteLoadConfig, name string, boardIDs []string, writes int) (PartitionWriteLoadCase, error) {
	result := newPartitionWriteLoadCase(name, len(boardIDs), writes)
	if writes <= 0 {
		return result, nil
	}
	type writeResult struct {
		latency time.Duration
		errText string
	}
	jobs := make(chan int)
	results := make(chan writeResult, writes)
	workers := min(max(config.Concurrency, 1), writes)

	var wg sync.WaitGroup
	start := time.Now()
	body := loadutil.Body(config.BodyBytes)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range jobs {
				boardID := boardIDs[i%len(boardIDs)]
				title := fmt.Sprintf("%s write %06d", name, i)
				payload := proto.CreateThreadPayload{
					Board: boardID,
					Title: title,
					Body:  body,
				}
				opStart := time.Now()
				reply := execLoadCommand(ctx, c, actor, proto.CmdCreateThread, payload, fmt.Sprintf("%s-%d-%d", name, workerID, i))
				latency := time.Since(opStart)
				if reply.Err != nil {
					results <- writeResult{latency: latency, errText: fmt.Sprintf("%s (%s)", reply.Err.Message, reply.Err.Code)}
					continue
				}
				results <- writeResult{latency: latency}
			}
		}(worker)
	}
	for i := 0; i < writes; i++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(results)
			return result, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	result.DurationMS = time.Since(start).Milliseconds()
	latencies := make([]float64, 0, writes)
	errSamples := map[string]bool{}
	for item := range results {
		latencies = append(latencies, float64(item.latency.Microseconds())/1000)
		if item.errText != "" {
			result.Failed++
			loadmodel.AddLoadSample(&result.SampleErrorText, errSamples, item.errText)
		} else {
			result.Succeeded++
		}
	}
	result.WritesPerSec = loadutil.PerSecond(result.Succeeded, time.Duration(result.DurationMS)*time.Millisecond)
	sort.Float64s(latencies)
	result.LatencyP50MS = percentile(latencies, 0.50)
	result.LatencyP95MS = percentile(latencies, 0.95)
	result.LatencyP99MS = percentile(latencies, 0.99)
	if len(latencies) > 0 {
		result.MaxLatencyMS = latencies[len(latencies)-1]
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("%s load case failed %d/%d writes", name, result.Failed, result.Writes)
	}
	return result, nil
}

func newPartitionWriteLoadCase(name string, boards, writes int) PartitionWriteLoadCase {
	return PartitionWriteLoadCase{Name: name, Boards: boards, Writes: writes}
}

func execLoadCommand(ctx context.Context, c *core.Core, actor *projections.User, name proto.CommandName, payload any, cid string) commandexec.Reply {
	raw, err := json.Marshal(payload)
	if err != nil {
		return commandexec.Reply{Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: err.Error()}}
	}
	return c.ExecCmd(ctx, actor, name, raw, cid)
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	rank := p * float64(len(values)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return values[lower]
	}
	weight := rank - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func nonNegativeOrDefault(value, fallback int) int {
	if value < 0 {
		return fallback
	}
	return value
}
