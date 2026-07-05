package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const coreDependencyGuardModulePath = "github.com/juncoflockleader/budgie-bbs"

func TestCoreDoesNotImportInternetScaleToolingPackages(t *testing.T) {
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

	imports := coreDependencyGuardImports(t, "./internal/core")
	forbiddenPrefixes := make([]string, 0, len(forbiddenImports))
	for _, suffix := range forbiddenImports {
		forbiddenPrefixes = append(forbiddenPrefixes, coreDependencyGuardModulePath+"/"+suffix)
	}
	found := coreDependencyGuardMatchingImports(imports, forbiddenPrefixes)
	if len(found) > 0 {
		t.Fatalf("internal/core must not import internet-scale tooling packages: %s", strings.Join(found, ", "))
	}
}

func TestCoreModelPackagesRemainLeafPackages(t *testing.T) {
	modelPackages := []string{
		"./internal/core/accountmodel",
		"./internal/core/automodmodel",
		"./internal/core/boardmodel",
		"./internal/core/chatmodel",
		"./internal/core/categorymodel",
		"./internal/core/favoritemodel",
		"./internal/core/mailmodel",
		"./internal/core/notificationmodel",
		"./internal/core/postmodel",
		"./internal/core/pollmodel",
		"./internal/core/threadmodel",
	}

	for _, pkg := range modelPackages {
		t.Run(pkg, func(t *testing.T) {
			imports := coreDependencyGuardImports(t, pkg)
			found := coreDependencyGuardMatchingImports(imports, []string{
				coreDependencyGuardModulePath + "/internal/core",
			})
			if len(found) > 0 {
				t.Fatalf("%s must stay below command, projection, and read-model layers; found core imports: %s", pkg, strings.Join(found, ", "))
			}
		})
	}
}

func coreDependencyGuardImports(t *testing.T, pkg string) map[string]bool {
	t.Helper()

	cmd := exec.Command("go", "list", "-f", "{{range .Imports}}{{println .}}{{end}}", pkg)
	cmd.Dir = coreDependencyGuardRepoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s imports: %v\n%s", pkg, err, out)
	}

	imports := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			imports[line] = true
		}
	}
	return imports
}

func coreDependencyGuardMatchingImports(imports map[string]bool, forbiddenPrefixes []string) []string {
	found := []string{}
	for _, prefix := range forbiddenPrefixes {
		for imported := range imports {
			if imported == prefix || strings.HasPrefix(imported, prefix+"/") {
				found = append(found, imported)
			}
		}
	}
	sort.Strings(found)
	return found
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
