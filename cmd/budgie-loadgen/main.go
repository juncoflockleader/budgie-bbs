package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/loadtest"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := loadtest.DefaultPartitionWriteLoadConfig()
	var (
		dsn                 = flag.String("postgres-dsn", runconfig.EnvOr("BUDGIE_POSTGRES_DSN", ""), "PostgreSQL DSN for the load target")
		schema              = flag.String("schema", "", "Postgres schema to create for the load run; defaults to a unique temporary schema")
		keepSchema          = flag.Bool("keep-schema", false, "Keep the load schema after the run")
		boards              = flag.Int("boards", defaults.Boards, "Number of board partitions in the spread workload")
		writesPerBoard      = flag.Int("writes-per-board", defaults.WritesPerBoard, "Number of createThread writes per spread board")
		concurrency         = flag.Int("concurrency", defaults.Concurrency, "Maximum concurrent command submissions")
		bodyBytes           = flag.Int("body-bytes", defaults.BodyBytes, "Post body size in bytes")
		boardPrefix         = flag.String("board-prefix", defaults.BoardPrefix, "Board id prefix for generated load boards")
		userName            = flag.String("user", defaults.UserName, "Load user name")
		timeout             = flag.Duration("timeout", 2*time.Minute, "Maximum duration for the load run")
		budgetFile          = flag.String("budget-file", "", "Path to JSON internet-scale budget file; postgresWrites section enforces additional thresholds")
		minSpreadSpeedup    = flag.Float64("min-spread-speedup", 0, "Fail if spread-partition writes/sec is below this multiple of same-partition writes/sec; 0 reports only")
		minSpreadWritesPerS = flag.Float64("min-spread-writes-per-sec", 0, "Fail if spread-partition writes/sec is below this value; 0 reports only")
		pretty              = flag.Bool("pretty", true, "Pretty-print JSON output")
	)
	flag.Parse()

	if strings.TrimSpace(*dsn) == "" {
		log.Print("postgres DSN required via -postgres-dsn or BUDGIE_POSTGRES_DSN")
		return 2
	}
	if requestedSchema := strings.TrimSpace(*schema); requestedSchema != "" && !runconfig.ValidSchemaName(requestedSchema) {
		log.Printf("invalid schema %q; use letters, digits, and underscores, starting with a letter or underscore", requestedSchema)
		return 2
	}
	budgets, err := scalebudget.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}

	ctx, cancel := runconfig.InterruptTimeoutContext(context.Background(), *timeout)
	defer cancel()

	c, cleanup, err := loadtest.OpenPostgresCore(ctx, loadtest.PostgresCoreConfig{
		DSN:          *dsn,
		Schema:       *schema,
		SchemaPrefix: "budgie_load",
		KeepSchema:   *keepSchema,
		Logf:         log.Printf,
	})
	if err != nil {
		log.Printf("open postgres core: %v", err)
		return 1
	}
	defer cleanup()
	defer c.DB.Close()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go c.Run(runCtx)

	report, err := loadtest.RunPartitionWriteLoad(ctx, c, loadtest.PartitionWriteLoadConfig{
		Boards:         *boards,
		WritesPerBoard: *writesPerBoard,
		Concurrency:    *concurrency,
		BodyBytes:      *bodyBytes,
		BoardPrefix:    *boardPrefix,
		UserName:       *userName,
	})
	if printErr := runreport.WriteJSON(os.Stdout, report, *pretty); printErr != nil {
		log.Printf("print report: %v", printErr)
		return 1
	}
	if err != nil {
		log.Printf("load run failed: %v", err)
		return 1
	}
	if *minSpreadSpeedup > 0 && report.SpreadSpeedup < *minSpreadSpeedup {
		log.Printf("spread speedup %.2fx below threshold %.2fx", report.SpreadSpeedup, *minSpreadSpeedup)
		return 3
	}
	if *minSpreadWritesPerS > 0 && report.SpreadPartitions.WritesPerSec < *minSpreadWritesPerS {
		log.Printf("spread writes/sec %.2f below threshold %.2f", report.SpreadPartitions.WritesPerSec, *minSpreadWritesPerS)
		return 3
	}
	if violations := loadtest.EvaluatePartitionWriteBudget(report, budgets.PostgresWrites); len(violations) > 0 {
		log.Printf("scale budget violations: %s", scalebudget.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}
