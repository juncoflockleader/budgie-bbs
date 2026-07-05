package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
		"./internal/core/socialmodel",
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

func TestProductionCodeDoesNotUseCoreProjectionCompatibilityAliases(t *testing.T) {
	forbidden := map[string]bool{"Thread": true, "Post": true, "User": true}
	repoRoot := coreDependencyGuardRepoRoot(t)
	matches := []string{}

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !forbidden[selector.Sel.Name] {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != "core" {
				return true
			}
			pos := fset.Position(selector.Pos())
			matches = append(matches, rel+":"+strconv.Itoa(pos.Line)+": core."+selector.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go files: %v", err)
	}
	sort.Strings(matches)
	if len(matches) > 0 {
		t.Fatalf("production code must import projection DTOs from internal/core/projections, not core compatibility aliases:\n%s", strings.Join(matches, "\n"))
	}
}

func TestCoreProductionCodeDoesNotReexportProjectionDTOAliases(t *testing.T) {
	repoRoot := coreDependencyGuardRepoRoot(t)
	coreRoot := filepath.Join(repoRoot, "internal", "core")
	matches := []string{}

	err := filepath.Walk(coreRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path == filepath.Join(coreRoot, "projections") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !typeSpec.Assign.IsValid() {
					continue
				}
				selector, ok := typeSpec.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || ident.Name != "projections" {
					continue
				}
				pos := fset.Position(typeSpec.Pos())
				matches = append(matches, rel+":"+strconv.Itoa(pos.Line)+": "+typeSpec.Name.Name+" = projections."+selector.Sel.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan core production Go files: %v", err)
	}
	sort.Strings(matches)
	if len(matches) > 0 {
		t.Fatalf("core production code must import projection DTOs directly, not re-export aliases:\n%s", strings.Join(matches, "\n"))
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
