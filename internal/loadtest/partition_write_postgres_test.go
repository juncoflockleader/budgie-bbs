package loadtest

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

func TestPostgresPartitionWriteLoadReportsSpreadThroughput(t *testing.T) {
	baseDSN := os.Getenv("BUDGIE_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set BUDGIE_TEST_POSTGRES_DSN to run the Postgres partition write load test")
	}
	if os.Getenv("BUDGIE_TEST_POSTGRES_LOAD") == "" {
		t.Skip("set BUDGIE_TEST_POSTGRES_LOAD=1 to run the Postgres partition write load test")
	}

	c, cleanup, err := OpenPostgresCore(context.Background(), PostgresCoreConfig{
		DSN:          baseDSN,
		SchemaPrefix: "budgie_partition_load_test",
		Logf:         t.Logf,
	})
	if err != nil {
		t.Fatalf("open postgres core: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(func() {
		cancel()
		_ = c.DB.Close()
		cleanup()
	})
	go c.Run(ctx)

	report, err := RunPartitionWriteLoad(ctx, c, PartitionWriteLoadConfig{
		Boards:         runconfig.EnvInt("BUDGIE_TEST_POSTGRES_LOAD_BOARDS", 8),
		WritesPerBoard: runconfig.EnvInt("BUDGIE_TEST_POSTGRES_LOAD_WRITES_PER_BOARD", 25),
		Concurrency:    runconfig.EnvInt("BUDGIE_TEST_POSTGRES_LOAD_CONCURRENCY", 32),
		BodyBytes:      runconfig.EnvInt("BUDGIE_TEST_POSTGRES_LOAD_BODY_BYTES", 256),
		BoardPrefix:    "pgload",
		UserName:       "pg_load_admin",
	})
	var reportJSON bytes.Buffer
	if marshalErr := runreport.WriteJSON(&reportJSON, report, true); marshalErr != nil {
		t.Fatalf("marshal load report: %v", marshalErr)
	}
	t.Logf("postgres partition write load report:\n%s", reportJSON.String())
	if err != nil {
		t.Fatalf("RunPartitionWriteLoad: %v", err)
	}
	if report.SamePartition.Succeeded != report.TotalWrites || report.SpreadPartitions.Succeeded != report.TotalWrites {
		t.Fatalf("report = %+v, want all writes successful", report)
	}
	if report.SpreadSpeedup <= 0 {
		t.Fatalf("spread speedup = %.2f, want positive", report.SpreadSpeedup)
	}
	if minRaw := os.Getenv("BUDGIE_TEST_POSTGRES_MIN_SPREAD_SPEEDUP"); minRaw != "" {
		min, err := strconv.ParseFloat(minRaw, 64)
		if err != nil {
			t.Fatalf("invalid BUDGIE_TEST_POSTGRES_MIN_SPREAD_SPEEDUP=%q: %v", minRaw, err)
		}
		if report.SpreadSpeedup < min {
			t.Fatalf("spread speedup %.2fx below threshold %.2fx", report.SpreadSpeedup, min)
		}
	}
}
