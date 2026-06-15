// Package policy provides the bundled default privacy policy that BudgieBBS
// presents at signup. The policy text is embedded into the binary so it is
// served without depending on a deployment file path; operators who want a
// custom policy can replace it via the -privacy-policy flag (see cmd/budgied).
package policy

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"strings"
)

//go:embed privacy.md
var defaultPrivacy string

// DefaultPrivacyPolicy returns the bundled policy markdown and a short version
// identifier derived from its content. The version changes whenever the text
// changes, so a recorded acceptance can be tied to the exact policy a user saw.
func DefaultPrivacyPolicy() (markdown, version string) {
	return defaultPrivacy, Version(defaultPrivacy)
}

// Version returns a stable short identifier for a policy body (FNV-1a of the
// trimmed content). Empty content yields "0".
func Version(markdown string) string {
	trimmed := strings.TrimSpace(markdown)
	if trimmed == "" {
		return "0"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(trimmed))
	return fmt.Sprintf("v%x", h.Sum32())
}
