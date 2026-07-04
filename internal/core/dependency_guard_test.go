package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreDoesNotImportInternetScaleToolingPackages(t *testing.T) {
	const modulePath = "github.com/juncoflockleader/budgie-bbs"
	forbiddenImports := []string{
		"internal/kafkaconn",
		"internal/loadtest",
		"internal/natsconn",
		"internal/preflightcheck",
		"internal/preflightmodel",
		"internal/redisconn",
		"internal/runbundle",
		"internal/runconfig",
		"internal/runevidence",
		"internal/runreport",
		"internal/scalebudget",
	}

	cmd := exec.Command("go", "list", "-f", "{{range .Imports}}{{println .}}{{end}}", "./internal/core")
	cmd.Dir = coreDependencyGuardRepoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list core imports: %v\n%s", err, out)
	}

	imports := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			imports[line] = true
		}
	}
	found := []string{}
	for _, suffix := range forbiddenImports {
		prefix := modulePath + "/" + suffix
		for imported := range imports {
			if imported == prefix || strings.HasPrefix(imported, prefix+"/") {
				found = append(found, imported)
			}
		}
	}
	if len(found) > 0 {
		t.Fatalf("internal/core must not import internet-scale tooling packages: %s", strings.Join(found, ", "))
	}
}

func coreDependencyGuardRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod above %s", dir)
		}
		dir = parent
	}
}
