package loadtest

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func TestPartitionWriteLoadRunnerReportsSameAndSpreadCases(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "partition-write-load.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := RunPartitionWriteLoad(ctx, c, PartitionWriteLoadConfig{
		Boards:         2,
		WritesPerBoard: 2,
		Concurrency:    2,
		BodyBytes:      32,
		BoardPrefix:    "loadtest",
		UserName:       "load_runner",
	})
	if err != nil {
		t.Fatalf("RunPartitionWriteLoad: %v", err)
	}
	if report.TotalWrites != 4 {
		t.Fatalf("total writes = %d, want 4", report.TotalWrites)
	}
	if report.SamePartition.Succeeded != 4 || report.SpreadPartitions.Succeeded != 4 {
		t.Fatalf("report = %+v, want all writes successful", report)
	}
	if report.SamePartition.WritesPerSec <= 0 || report.SpreadPartitions.WritesPerSec <= 0 {
		t.Fatalf("writes/sec not populated: %+v", report)
	}
	if report.SpreadSpeedup <= 0 {
		t.Fatalf("spread speedup = %.2f, want positive", report.SpreadSpeedup)
	}
}

func TestEvaluatePartitionWriteBudgetPassesAndFails(t *testing.T) {
	report := PartitionWriteLoadReport{
		SamePartition: PartitionWriteLoadCase{
			WritesPerSec: 100,
			LatencyP95MS: 80,
		},
		SpreadPartitions: PartitionWriteLoadCase{
			WritesPerSec: 225,
			LatencyP95MS: 40,
		},
		SpreadSpeedup: 2.25,
	}
	budget := &scalebudget.PostgresWriteBudget{
		MinSpreadSpeedup:        2,
		MinSpreadWritesPerSec:   200,
		MaxSamePartitionP95MS:   100,
		MaxSpreadPartitionP95MS: 60,
	}
	if violations := EvaluatePartitionWriteBudget(report, budget); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	report.SpreadSpeedup = 1.1
	report.SpreadPartitions.WritesPerSec = 90
	report.SamePartition.LatencyP95MS = 125
	report.SpreadPartitions.LatencyP95MS = 75
	report.SpreadPartitions.Failed = 1
	violations := EvaluatePartitionWriteBudget(report, budget)
	requireScaleBudgetPaths(t, violations,
		"postgresWrites.minSpreadSpeedup",
		"postgresWrites.minSpreadWritesPerSec",
		"postgresWrites.maxSamePartitionP95Ms",
		"postgresWrites.maxSpreadPartitionP95Ms",
		"postgresWrites.maxFailedWrites",
	)
	formatted := scalebudget.FormatScaleBudgetViolations(violations)
	if !strings.Contains(formatted, "postgresWrites.minSpreadSpeedup") || !strings.Contains(formatted, "value=1.1") {
		t.Fatalf("formatted violations = %q, want path and value", formatted)
	}
}
