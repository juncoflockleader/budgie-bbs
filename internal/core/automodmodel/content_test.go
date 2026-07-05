package automodmodel

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReason(t *testing.T) {
	if got := Reason("  spam wave ", "rule_1"); got != "Automod: spam wave" {
		t.Fatalf("Reason with reason = %q", got)
	}
	if got := Reason(" ", "rule_1"); got != "Automod rule rule_1" {
		t.Fatalf("Reason fallback = %q", got)
	}
}

func TestContentRuleMatches(t *testing.T) {
	tests := []struct {
		name      string
		matchType string
		pattern   string
		threshold int
		text      string
		want      bool
		handled   bool
	}{
		{name: "keyword", matchType: "keyword", pattern: "Spam", text: "fresh spam here", want: true, handled: true},
		{name: "blank keyword", matchType: "keyword", pattern: " ", text: "anything", want: false, handled: true},
		{name: "regex", matchType: "regex", pattern: `sp[ae]m`, text: "hello spam", want: true, handled: true},
		{name: "bad regex", matchType: "regex", pattern: "(", text: "hello spam", want: false, handled: true},
		{name: "repeated", matchType: "repeated_text", threshold: 4, text: "heyyyy", want: true, handled: true},
		{name: "links", matchType: "link_count", threshold: 2, text: "http://a.test https://b.test", want: true, handled: true},
		{name: "db backed", matchType: "account_age", threshold: 2, text: "hello", want: false, handled: false},
		{name: "blocked regex", matchType: "regex", pattern: `sp[ae]m`, text: "hello spam", want: false, handled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regexAllowed := func(string) bool { return tt.name != "blocked regex" }
			got, handled := ContentRuleMatches(tt.matchType, tt.pattern, tt.threshold, tt.text, regexAllowed)
			if got != tt.want || handled != tt.handled {
				t.Fatalf("ContentRuleMatches = %v/%v, want %v/%v", got, handled, tt.want, tt.handled)
			}
		})
	}
}

func TestRegexInputPreservesUTF8(t *testing.T) {
	text := strings.Repeat("a", MaxRegexInputBytes-1) + "界"
	got := RegexInput(text)
	if len(got) > MaxRegexInputBytes {
		t.Fatalf("RegexInput length = %d, want <= %d", len(got), MaxRegexInputBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("RegexInput returned invalid UTF-8")
	}
}

func TestLinkCount(t *testing.T) {
	if got := LinkCount("HTTP://a.test and https://b.test and ftp://c.test"); got != 2 {
		t.Fatalf("LinkCount = %d, want 2", got)
	}
}
