package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWebRootUsesExplicitPath(t *testing.T) {
	if got := resolveWebRoot("/tmp/custom-web"); got != "/tmp/custom-web" {
		t.Fatalf("expected explicit web root to win, got %q", got)
	}
}

func TestHasWebIndex(t *testing.T) {
	root := t.TempDir()
	if hasWebIndex(root) {
		t.Fatalf("expected empty dir to have no web index")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if !hasWebIndex(root) {
		t.Fatalf("expected index.html to be detected")
	}
}
