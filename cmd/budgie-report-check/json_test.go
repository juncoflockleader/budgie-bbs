package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

func writeReportCheckJSON(t *testing.T, name string, value any) string {
	t.Helper()
	return writeReportCheckJSONInDir(t, t.TempDir(), name, value, true)
}

func writeReportCheckJSONInDir(t *testing.T, dir, name string, value any, pretty bool) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeReportCheckJSONFile(t, path, value, pretty)
	return path
}

func writeReportCheckJSONFile(t *testing.T, path string, value any, pretty bool) {
	t.Helper()
	if err := runreport.WriteJSONFile(path, value, pretty); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeReportCheckRaw(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

type reportCheckRunner func([]string, io.Reader, io.Writer, io.Writer) int

type reportCheckRunResult struct {
	Code   int
	Stdout string
	Stderr string
}

func runReportCheckForTest(t *testing.T, runner reportCheckRunner, args []string, stdin io.Reader) reportCheckRunResult {
	t.Helper()
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	var stdout, stderr bytes.Buffer
	code := runner(args, stdin, &stdout, &stderr)
	return reportCheckRunResult{
		Code:   code,
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
}

func runBudgetReportCheckForTest(t *testing.T, runner reportCheckRunner, reportPath, budgetPath string) reportCheckRunResult {
	t.Helper()
	return runReportCheckForTest(t, runner, []string{
		"-report-file", reportPath,
		"-budget-file", budgetPath,
	}, nil)
}

func runBundleForTest(t *testing.T, args []string) reportCheckRunResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runBundle(args, &stdout, &stderr)
	return reportCheckRunResult{
		Code:   code,
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
}

func requireReportCheckExit(t *testing.T, result reportCheckRunResult, want int) {
	t.Helper()
	if result.Code != want {
		t.Fatalf("run exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", result.Code, want, result.Stdout, result.Stderr)
	}
}

func requireReportCheckOutputContains(t *testing.T, name, output string, tokens ...string) {
	t.Helper()
	for _, token := range tokens {
		if !strings.Contains(output, token) {
			t.Fatalf("%s missing %q:\n%s", name, token, output)
		}
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	hash, err := runevidence.ReadFileSHA256(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return hash
}
