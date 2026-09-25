package main

import (
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/loadtest"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func runGateway(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runBudgetReportCheck(args, stdin, stdout, stderr, budgetReportCheckConfig[loadtest.GatewayFanoutLoadReport, scalebudget.GatewayFanoutBudget]{
		commandName:       "gateway",
		reportFlagHelp:    "Path to a budgie-gateway-loadgen JSON report; use - for stdin",
		budgetSectionName: "gatewayFanout",
		successMessage:    "gateway fanout report satisfies gatewayFanout budget",
		budgetSection: func(budgets scalebudget.ScaleBudgets) *scalebudget.GatewayFanoutBudget {
			return budgets.GatewayFanout
		},
		evaluate: loadtest.EvaluateGatewayFanoutBudget,
		requireBudgetHash: func(budget *scalebudget.GatewayFanoutBudget) bool {
			return strings.TrimSpace(budget.RequiredReportBudgetFile) != ""
		},
		reportBudgetHash: func(report loadtest.GatewayFanoutLoadReport) string {
			return report.Evidence.BudgetSHA256
		},
		budgetHashPath:    "gatewayFanout.evidence.budgetSha256",
		budgetHashMessage: "gateway fanout report budget hash does not match the checked budget file",
	})
}
