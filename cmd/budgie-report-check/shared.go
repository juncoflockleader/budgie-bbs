package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
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
		return report, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return report, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return report, fmt.Errorf("unexpected trailing JSON")
	}
	return report, nil
}

// budgetHashEvidence checks that the budget hash a load report recorded as
// evidence matches the budget file this check is running against. An empty
// recorded hash is not a violation here; whether evidence is required is the
// caller's budget policy.
func budgetHashEvidence(reportSHA, budgetFile, violationPath, message string) ([]core.ScaleBudgetViolation, error) {
	got := strings.ToLower(strings.TrimSpace(reportSHA))
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
		Path:    violationPath,
		Value:   reportSHA,
		Limit:   want,
		Message: message,
	}}, nil
}
