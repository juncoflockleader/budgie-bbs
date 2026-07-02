package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func runGateway(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budgie-report-check gateway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportFile := flags.String("report-file", "", "Path to a budgie-gateway-loadgen JSON report; use - for stdin")
	budgetFile := flags.String("budget-file", "", "Path to JSON internet-scale budget file with a gatewayFanout section")
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
	if budgets.GatewayFanout == nil {
		fmt.Fprintln(stderr, "budget file is missing gatewayFanout")
		return 2
	}
	report, err := readStrictJSONReport[core.GatewayFanoutLoadReport](*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read gateway fanout report: %v\n", err)
		return 2
	}
	violations := core.EvaluateGatewayFanoutBudget(report, budgets.GatewayFanout)
	if strings.TrimSpace(budgets.GatewayFanout.RequiredReportBudgetFile) != "" {
		hashViolations, err := budgetHashEvidence(report.Evidence.BudgetSHA256, *budgetFile,
			"gatewayFanout.evidence.budgetSha256",
			"gateway fanout report budget hash does not match the checked budget file")
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
	fmt.Fprintln(stdout, "gateway fanout report satisfies gatewayFanout budget")
	return 0
}
