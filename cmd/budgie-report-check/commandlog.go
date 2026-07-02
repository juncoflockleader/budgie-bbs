package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func runCommandLog(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budgie-report-check commandlog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportFile := flags.String("report-file", "", "Path to a budgie-commandlog-loadgen JSON report; use - for stdin")
	budgetFile := flags.String("budget-file", "", "Path to JSON internet-scale budget file with a commandLogDrain section")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*budgetFile) == "" {
		fmt.Fprintln(stderr, "-budget-file is required")
		return 2
	}
	budgets, err := core.LoadScaleBudgets(*budgetFile)
	if err != nil {
		fmt.Fprintf(stderr, "load budget file: %v\n", err)
		return 2
	}
	if budgets.CommandLogDrain == nil {
		fmt.Fprintln(stderr, "budget file is missing commandLogDrain")
		return 2
	}
	report, err := readStrictJSONReport[core.CommandLogDrainLoadReport](*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read command-log report: %v\n", err)
		return 2
	}
	violations := core.EvaluateCommandLogDrainBudget(report, budgets.CommandLogDrain)
	if budgets.CommandLogDrain.RequireReportEvidence {
		hashViolations, err := budgetHashEvidence(report.Evidence.BudgetSHA256, *budgetFile,
			"commandLogDrain.evidence.budgetSha256",
			"command-log drain report budget hash does not match the checked budget file")
		if err != nil {
			fmt.Fprintf(stderr, "check budget hash evidence: %v\n", err)
			return 2
		}
		violations = append(violations, hashViolations...)
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "scale budget violations: %s\n", core.FormatScaleBudgetViolations(violations))
		return 3
	}
	fmt.Fprintln(stdout, "command-log drain report satisfies commandLogDrain budget")
	return 0
}
