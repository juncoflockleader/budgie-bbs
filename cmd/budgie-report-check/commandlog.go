package main

import (
	"io"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func runCommandLog(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runBudgetReportCheck(args, stdin, stdout, stderr, budgetReportCheckConfig[loadmodel.CommandLogDrainLoadReport, scalebudget.CommandLogDrainBudget]{
		commandName:       "commandlog",
		reportFlagHelp:    "Path to a budgie-commandlog-loadgen JSON report; use - for stdin",
		budgetSectionName: "commandLogDrain",
		successMessage:    "command-log drain report satisfies commandLogDrain budget",
		budgetSection: func(budgets scalebudget.ScaleBudgets) *scalebudget.CommandLogDrainBudget {
			return budgets.CommandLogDrain
		},
		evaluate: scalebudget.EvaluateCommandLogDrainBudget,
		requireBudgetHash: func(budget *scalebudget.CommandLogDrainBudget) bool {
			return budget.RequireReportEvidence
		},
		reportBudgetHash: func(report loadmodel.CommandLogDrainLoadReport) string {
			return report.Evidence.BudgetSHA256
		},
		budgetHashPath:    "commandLogDrain.evidence.budgetSha256",
		budgetHashMessage: "command-log drain report budget hash does not match the checked budget file",
	})
}
