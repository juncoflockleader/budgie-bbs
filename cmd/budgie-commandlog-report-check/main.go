package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budgie-commandlog-report-check", flag.ContinueOnError)
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
	report, err := readCommandLogDrainReport(*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read command-log report: %v\n", err)
		return 2
	}
	violations := core.EvaluateCommandLogDrainBudget(report, budgets.CommandLogDrain)
	if budgets.CommandLogDrain.RequireReportEvidence {
		hashViolations, err := evaluateReportBudgetHashEvidence(report, *budgetFile)
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

func readCommandLogDrainReport(path string, stdin io.Reader) (core.CommandLogDrainLoadReport, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return core.CommandLogDrainLoadReport{}, fmt.Errorf("-report-file is required")
	}
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return core.CommandLogDrainLoadReport{}, err
	}
	var report core.CommandLogDrainLoadReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return core.CommandLogDrainLoadReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return core.CommandLogDrainLoadReport{}, fmt.Errorf("unexpected trailing JSON")
	}
	return report, nil
}

func evaluateReportBudgetHashEvidence(report core.CommandLogDrainLoadReport, budgetFile string) ([]core.ScaleBudgetViolation, error) {
	got := strings.ToLower(strings.TrimSpace(report.Evidence.BudgetSHA256))
	if got == "" {
		return nil, nil
	}
	data, err := os.ReadFile(budgetFile)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	want := fmt.Sprintf("%x", sum)
	if got == want {
		return nil, nil
	}
	return []core.ScaleBudgetViolation{{
		Path:    "commandLogDrain.evidence.budgetSha256",
		Value:   report.Evidence.BudgetSHA256,
		Limit:   want,
		Message: "command-log drain report budget hash does not match the checked budget file",
	}}, nil
}
