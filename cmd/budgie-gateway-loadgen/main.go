package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := core.DefaultGatewayFanoutLoadConfig()
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
	budgets, err := core.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}
	targetConnections := *targetConns
	if targetConnections <= 0 && budgets.GatewayFanout != nil {
		targetConnections = budgets.GatewayFanout.TargetConnections
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	report, err := core.RunGatewayFanoutLoad(ctx, core.GatewayFanoutLoadConfig{
		HotSubscribers:    *hotSubscribers,
		IdleSubscribers:   *idleSubscribers,
		BufferSize:        *bufferSize,
		Events:            *events,
		HotScope:          *hotScope,
		IdleScopePrefix:   *idleScopePrefix,
		TargetConnections: targetConnections,
	})
	report.Evidence = gatewayFanoutEvidence(*budgetFile)
	if printErr := printReport(report, *pretty); printErr != nil {
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
	if violations := core.EvaluateGatewayFanoutBudget(report, budgets.GatewayFanout); len(violations) > 0 {
		log.Printf("scale budget violations: %s", core.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}

func gatewayFanoutEvidence(budgetFile string) core.GatewayFanoutLoadEvidence {
	budgetFile = strings.TrimSpace(budgetFile)
	evidence := core.GatewayFanoutLoadEvidence{
		Tool:       "budgie-gateway-loadgen",
		BudgetFile: budgetFile,
	}
	if budgetFile != "" {
		if data, err := os.ReadFile(budgetFile); err == nil {
			sum := sha256.Sum256(data)
			evidence.BudgetSHA256 = fmt.Sprintf("%x", sum)
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				evidence.GitRevision = setting.Value
			case "vcs.modified":
				evidence.GitModified = setting.Value == "true"
			}
		}
	}
	if strings.TrimSpace(evidence.GitRevision) == "" {
		if revision, ok := gatewayFanoutGitOutput("rev-parse", "HEAD"); ok {
			evidence.GitRevision = revision
		}
	}
	if status, ok := gatewayFanoutGitOutput("status", "--porcelain"); ok && strings.TrimSpace(status) != "" {
		evidence.GitModified = true
	}
	return evidence
}

func gatewayFanoutGitOutput(args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func printReport(report core.GatewayFanoutLoadReport, pretty bool) error {
	encoder := json.NewEncoder(os.Stdout)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(report)
}
