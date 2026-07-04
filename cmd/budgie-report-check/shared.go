package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

// readStrictJSONReport reads a JSON report from path (or stdin when path is
// "-") and decodes it rejecting unknown fields and trailing data, so a report
// produced by a newer tool never silently passes an older check.
func readStrictJSONReport[T any](path string, stdin io.Reader) (T, error) {
	var report T
	path = strings.TrimSpace(path)
	if path == "" {
		return report, fmt.Errorf("-report-file is required")
	}
	var err error
	if path == "-" {
		report, err = runreport.DecodeJSON[T](stdin, true)
	} else {
		report, err = runreport.ReadJSONFile[T](path, true)
	}
	if err != nil {
		return report, err
	}
	return report, nil
}

func newReportCheckFlagSet(commandName string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("budgie-report-check "+commandName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

type budgetReportCheckConfig[R any, B any] struct {
	commandName       string
	reportFlagHelp    string
	budgetSectionName string
	successMessage    string
	budgetSection     func(scalebudget.ScaleBudgets) *B
	evaluate          func(R, *B) []scalebudget.ScaleBudgetViolation
	requireBudgetHash func(*B) bool
	reportBudgetHash  func(R) string
	budgetHashPath    string
	budgetHashMessage string
}

func runBudgetReportCheck[R any, B any](args []string, stdin io.Reader, stdout, stderr io.Writer, config budgetReportCheckConfig[R, B]) int {
	flags := newReportCheckFlagSet(config.commandName, stderr)
	reportFile := flags.String("report-file", "", config.reportFlagHelp)
	budgetFile := flags.String("budget-file", "", "Path to JSON internet-scale budget file with a "+config.budgetSectionName+" section")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*budgetFile) == "" {
		fmt.Fprintln(stderr, "-budget-file is required")
		return 2
	}
	budgets, err := scalebudget.LoadScaleBudgets(*budgetFile)
	if err != nil {
		fmt.Fprintf(stderr, "load budget file: %v\n", err)
		return 2
	}
	budget := config.budgetSection(budgets)
	if budget == nil {
		fmt.Fprintf(stderr, "budget file is missing %s\n", config.budgetSectionName)
		return 2
	}
	report, err := readStrictJSONReport[R](*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read %s report: %v\n", config.commandName, err)
		return 2
	}
	violations := config.evaluate(report, budget)
	if config.requireBudgetHash(budget) {
		hashViolations, err := scalebudget.EvaluateReportBudgetHashEvidence(
			config.reportBudgetHash(report),
			*budgetFile,
			config.budgetHashPath,
			config.budgetHashMessage,
		)
		if err != nil {
			fmt.Fprintf(stderr, "check budget hash evidence: %v\n", err)
			return 2
		}
		violations = append(violations, hashViolations...)
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "scale budget violations: %s\n", scalebudget.FormatScaleBudgetViolations(violations))
		return 3
	}
	fmt.Fprintln(stdout, config.successMessage)
	return 0
}
