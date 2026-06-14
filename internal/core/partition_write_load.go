package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
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

func (c *Core) RunPartitionWriteLoad(ctx context.Context, config PartitionWriteLoadConfig) (PartitionWriteLoadReport, error) {
	if c == nil {
		return PartitionWriteLoadReport{}, fmt.Errorf("partition write load: nil core")
	}
	config = normalizePartitionWriteLoadConfig(config)
	report := PartitionWriteLoadReport{
		Config:      config,
		StartedAt:   nowMS(),
		TotalWrites: config.Boards * config.WritesPerBoard,
		SamePartition: PartitionWriteLoadCase{Name: "same_partition",
			Boards: 1,
			Writes: config.Boards * config.WritesPerBoard,
		},
		SpreadPartitions: PartitionWriteLoadCase{Name: "spread_partitions",
			Boards: config.Boards,
			Writes: config.Boards * config.WritesPerBoard,
		},
	}

	actor, err := c.RegisterUser(config.UserName, newID("pw_"))
	if err != nil {
		return report, fmt.Errorf("register load user: %w", err)
	}
	boardIDs, err := c.createPartitionWriteLoadBoards(ctx, actor, config)
	if err != nil {
		return report, err
	}

	sameBoard := []string{boardIDs[0]}
	report.SamePartition, err = c.runPartitionWriteLoadCase(ctx, actor, config, "same_partition", sameBoard, report.TotalWrites)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.SpreadPartitions, err = c.runPartitionWriteLoadCase(ctx, actor, config, "spread_partitions", boardIDs, report.TotalWrites)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	if report.SamePartition.WritesPerSec > 0 {
		report.SpreadSpeedup = report.SpreadPartitions.WritesPerSec / report.SamePartition.WritesPerSec
		report.SpreadThroughputX = report.SpreadSpeedup
	}
	report.FinishedAt = nowMS()
	return report, nil
}

func normalizePartitionWriteLoadConfig(config PartitionWriteLoadConfig) PartitionWriteLoadConfig {
	def := DefaultPartitionWriteLoadConfig()
	if config.Boards <= 0 {
		config.Boards = def.Boards
	}
	if config.WritesPerBoard <= 0 {
		config.WritesPerBoard = def.WritesPerBoard
	}
	if config.Concurrency <= 0 {
		config.Concurrency = def.Concurrency
	}
	if config.BodyBytes < 0 {
		config.BodyBytes = def.BodyBytes
	}
	if strings.TrimSpace(config.BoardPrefix) == "" {
		config.BoardPrefix = def.BoardPrefix
	}
	if strings.TrimSpace(config.UserName) == "" {
		config.UserName = def.UserName
	}
	if config.Concurrency > config.Boards*config.WritesPerBoard {
		config.Concurrency = config.Boards * config.WritesPerBoard
	}
	return config
}

func (c *Core) createPartitionWriteLoadBoards(ctx context.Context, actor *User, config PartitionWriteLoadConfig) ([]string, error) {
	boardIDs := make([]string, 0, config.Boards)
	for i := 0; i < config.Boards; i++ {
		boardID := fmt.Sprintf("%s_%02d", sanitizeLoadID(config.BoardPrefix), i)
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

func (c *Core) runPartitionWriteLoadCase(ctx context.Context, actor *User, config PartitionWriteLoadConfig, name string, boardIDs []string, writes int) (PartitionWriteLoadCase, error) {
	result := PartitionWriteLoadCase{
		Name:   name,
		Boards: len(boardIDs),
		Writes: writes,
	}
	if writes <= 0 {
		return result, nil
	}
	type writeResult struct {
		latency time.Duration
		errText string
	}
	jobs := make(chan int)
	results := make(chan writeResult, writes)
	workers := config.Concurrency
	if workers <= 0 {
		workers = 1
	}
	if workers > writes {
		workers = writes
	}

	var wg sync.WaitGroup
	start := time.Now()
	body := loadBody(config.BodyBytes)
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
			if len(result.SampleErrorText) < 5 && !errSamples[item.errText] {
				result.SampleErrorText = append(result.SampleErrorText, item.errText)
				errSamples[item.errText] = true
			}
		} else {
			result.Succeeded++
		}
	}
	if result.DurationMS > 0 {
		result.WritesPerSec = float64(result.Succeeded) / (float64(result.DurationMS) / 1000)
	}
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

func execLoadCommand(ctx context.Context, c *Core, actor *User, name proto.CommandName, payload any, cid string) Reply {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Reply{Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: err.Error()}}
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

func loadBody(size int) string {
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

func sanitizeLoadID(raw string) string {
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
