package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

var schemaNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := core.DefaultPartitionWriteLoadConfig()
	var (
		dsn                 = flag.String("postgres-dsn", envOr("BUDGIE_POSTGRES_DSN", ""), "PostgreSQL DSN for the load target")
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
	schemaName := strings.TrimSpace(*schema)
	if schemaName == "" {
		schemaName = fmt.Sprintf("budgie_load_%d_%d", os.Getpid(), time.Now().UnixNano())
	}
	if !schemaNamePattern.MatchString(schemaName) {
		log.Printf("invalid schema %q; use letters, digits, and underscores, starting with a letter or underscore", schemaName)
		return 2
	}
	budgets, err := core.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	adminDB, err := core.OpenPostgres(*dsn)
	if err != nil {
		log.Printf("open postgres: %v", err)
		return 1
	}
	defer adminDB.Close()
	if _, err := adminDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
		log.Printf("drop old schema: %v", err)
		return 1
	}
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		log.Printf("create schema: %v", err)
		return 1
	}
	if !*keepSchema {
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			if _, err := adminDB.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
				log.Printf("cleanup schema %s: %v", schemaName, err)
			}
		}()
	}

	c, err := core.NewPostgres(withSearchPath(*dsn, schemaName))
	if err != nil {
		log.Printf("new postgres core: %v", err)
		return 1
	}
	defer c.DB.Close()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go c.Run(runCtx)

	report, err := c.RunPartitionWriteLoad(ctx, core.PartitionWriteLoadConfig{
		Boards:         *boards,
		WritesPerBoard: *writesPerBoard,
		Concurrency:    *concurrency,
		BodyBytes:      *bodyBytes,
		BoardPrefix:    *boardPrefix,
		UserName:       *userName,
	})
	if printErr := printReport(report, *pretty); printErr != nil {
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
	if violations := core.EvaluatePartitionWriteBudget(report, budgets.PostgresWrites); len(violations) > 0 {
		log.Printf("scale budget violations: %s", core.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}

func printReport(report core.PartitionWriteLoadReport, pretty bool) error {
	encoder := json.NewEncoder(os.Stdout)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(report)
}

func withSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
