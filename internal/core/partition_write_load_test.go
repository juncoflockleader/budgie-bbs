package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPartitionWriteLoadRunnerReportsSameAndSpreadCases(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "partition-write-load.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunPartitionWriteLoad(ctx, PartitionWriteLoadConfig{
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
