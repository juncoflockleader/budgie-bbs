// budgie-report-check validates gate and staging evidence reports against
// scale budgets. It replaces the former per-report binaries
// (budgie-commandlog-report-check, budgie-gateway-report-check,
// budgie-internet-scale-preflight-report-check,
// budgie-internet-scale-bundle-report-check) with one subcommand per report
// kind; flags and exit codes are unchanged from the standalone binaries.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "commandlog":
		return runCommandLog(rest, stdin, stdout, stderr)
	case "gateway":
		return runGateway(rest, stdin, stdout, stderr)
	case "preflight":
		return runPreflight(rest, stdin, stdout, stderr)
	case "bundle":
		return runBundle(rest, stdout, stderr)
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n", sub)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: budgie-report-check <subcommand> [flags]

Subcommands:
  commandlog  check a budgie-commandlog-loadgen report against the commandLogDrain budget
  gateway     check a budgie-gateway-loadgen report against the gatewayFanout budget
  preflight   check a budgie-internet-scale-preflight report for staging evidence
  bundle      check evidence consistency across a report bundle, or write/verify its manifest

Run "budgie-report-check <subcommand> -h" for subcommand flags.`)
}
