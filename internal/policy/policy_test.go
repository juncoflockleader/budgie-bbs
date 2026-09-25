package policy

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedMatchesDoc guards against drift between the embedded policy that
// budgied serves and the human-facing copy under doc/.
func TestEmbeddedMatchesDoc(t *testing.T) {
	docBytes, err := os.ReadFile("../../doc/default-privacy-policy.md")
	if err != nil {
		t.Fatalf("read doc copy: %v", err)
	}
	embedded, _ := DefaultPrivacyPolicy()
	if strings.TrimSpace(string(docBytes)) != strings.TrimSpace(embedded) {
		t.Fatalf("internal/policy/privacy.md and doc/default-privacy-policy.md have drifted; keep them identical")
	}
}

func TestVersionStableAndContentSensitive(t *testing.T) {
	_, v := DefaultPrivacyPolicy()
	if v == "" || v == "0" {
		t.Fatalf("expected a non-empty version, got %q", v)
	}
	if Version("alpha") == Version("beta") {
		t.Fatalf("expected different content to yield different versions")
	}
	if Version("  alpha  ") != Version("alpha") {
		t.Fatalf("expected version to ignore surrounding whitespace")
	}
}
