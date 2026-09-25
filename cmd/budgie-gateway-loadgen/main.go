package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/loadtest"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := loadtest.DefaultGatewayFanoutLoadConfig()
	var (
		hotSubscribers  = flag.Int("hot-subscribers", defaults.HotSubscribers, "Subscribers attached to the hot scope")
		idleSubscribers = flag.Int("idle-subscribers", defaults.IdleSubscribers, "Subscribers attached to unrelated idle scopes")
		bufferSize      = flag.Int("buffer-size", defaults.BufferSize, "Per-connection gateway queue capacity")
		events          = flag.Int("events", defaults.Events, "Hot-scope events to publish without draining subscriber queues")
		hotScope        = flag.String("hot-scope", defaults.HotScope, "Hot event scope to publish")
		idleScopePrefix = flag.String("idle-scope-prefix", defaults.IdleScopePrefix, "Prefix for generated idle scopes")
		targetConns     = flag.Int("target-connections", defaults.TargetConnections, "Projected total gateway connections to size for; 0 uses gatewayFanout.targetConnections from -budget-file when present")
		timeout         = flag.Duration("timeout", 30*time.Second, "Maximum duration for the synthetic load run")
		budgetFile      = flag.String("budget-file", "", "Path to JSON internet-scale budget file; gatewayFanout section enforces additional thresholds")
		maxPublishMS    = flag.Float64("max-publish-ms", 0, "Fail if hot-scope publish duration exceeds this many milliseconds; 0 reports only")
		minDrops        = flag.Int("min-drops", 0, "Fail if estimated slow-client drops are below this count; useful with -events > -buffer-size")
		pretty          = flag.Bool("pretty", true, "Pretty-print JSON output")
	)
	flag.Parse()
	budgets, err := scalebudget.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}
	targetConnections := *targetConns
	if targetConnections <= 0 && budgets.GatewayFanout != nil {
		targetConnections = budgets.GatewayFanout.TargetConnections
	}

	ctx, cancel := runconfig.InterruptTimeoutContext(context.Background(), *timeout)
	defer cancel()

	report, err := loadtest.RunGatewayFanoutLoad(ctx, loadtest.GatewayFanoutLoadConfig{
		HotSubscribers:    *hotSubscribers,
		IdleSubscribers:   *idleSubscribers,
		BufferSize:        *bufferSize,
		Events:            *events,
		HotScope:          *hotScope,
		IdleScopePrefix:   *idleScopePrefix,
		TargetConnections: targetConnections,
	})
	report.Evidence = runevidence.CollectForTool("budgie-gateway-loadgen", *budgetFile)
	if printErr := runreport.WriteJSON(os.Stdout, report, *pretty); printErr != nil {
		log.Printf("print report: %v", printErr)
		return 1
	}
	if err != nil {
		log.Printf("gateway fanout load failed: %v", err)
		return 1
	}
	if *maxPublishMS > 0 && report.PublishDurationMS > *maxPublishMS {
		log.Printf("publish duration %.3fms above threshold %.3fms", report.PublishDurationMS, *maxPublishMS)
		return 3
	}
	if *minDrops > 0 && report.EstimatedDrops < *minDrops {
		log.Printf("estimated drops %d below threshold %d", report.EstimatedDrops, *minDrops)
		return 3
	}
	if violations := loadtest.EvaluateGatewayFanoutBudget(report, budgets.GatewayFanout); len(violations) > 0 {
		log.Printf("scale budget violations: %s", scalebudget.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}
