package proto

import (
	"strings"
	"testing"
)

func TestValidateAutomodRuleRejectsComplexRegex(t *testing.T) {
	base := SetBoardAutomodRulePayload{Board: "b", MatchType: "regex", Action: "redact"}

	// A normal regex is accepted.
	ok := base
	ok.Pattern = `(?i)\bspam(my)?\b`
	if msg := ValidateAutomodRule(ok); msg != "" {
		t.Fatalf("expected a simple regex to validate, got %q", msg)
	}

	// A large-bounded-repetition pattern (the ReDoS-style CPU bomb) is rejected
	// even though it is under the 500-char cap and compiles under RE2.
	bomb := base
	bomb.Pattern = strings.Repeat("a{1000}", 60) // ~420 chars, ~60k NFA instructions
	if len(bomb.Pattern) > 500 {
		t.Fatalf("test pattern unexpectedly exceeds the length cap (%d)", len(bomb.Pattern))
	}
	if msg := ValidateAutomodRule(bomb); msg == "" {
		t.Fatal("expected an over-complex regex to be rejected")
	}
}

func TestAutomodRegexWithinComplexityLimit(t *testing.T) {
	if !AutomodRegexWithinComplexityLimit(`https?://\S+`) {
		t.Fatal("a normal pattern should be within the limit")
	}
	if AutomodRegexWithinComplexityLimit(strings.Repeat("a{1000}", 60)) {
		t.Fatal("a large bounded-repetition pattern should exceed the limit")
	}
	if AutomodRegexWithinComplexityLimit("(") {
		t.Fatal("an invalid pattern should not be reported as within the limit")
	}
}
