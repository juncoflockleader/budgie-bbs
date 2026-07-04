package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/preflightcheck"
	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

func runPreflight(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := newReportCheckFlagSet("preflight", stderr)
	reportFile := flags.String("report-file", "", "Path to a budgie-internet-scale-preflight JSON report; use - for stdin")
	targets := flags.String("targets", "", "Expected preflight targets: postgres,nats,kafka,all; nats/kafka imply postgres")
	remoteStaging := flags.Bool("remote-staging", false, "Require non-local endpoint evidence and remote staging mode")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	report, err := readStrictJSONReport[preflightmodel.Report](*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read preflight report: %v\n", err)
		return 2
	}
	expectedTargets, err := runconfig.NormalizePreflightTargets(*targets, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	violations, err := preflightcheck.EvaluateReport(report, expectedTargets, *remoteStaging)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "preflight report violations: %s\n", strings.Join(violations, "; "))
		return 3
	}
	fmt.Fprintln(stdout, "internet-scale preflight report satisfies staging evidence")
	return 0
}
