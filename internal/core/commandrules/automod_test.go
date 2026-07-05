package commandrules

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAutomodReason(t *testing.T) {
	if got := AutomodReason("  spam wave ", "rule_1"); got != "Automod: spam wave" {
		t.Fatalf("AutomodReason with reason = %q", got)
	}
	if got := AutomodReason(" ", "rule_1"); got != "Automod rule rule_1" {
		t.Fatalf("AutomodReason fallback = %q", got)
	}
}

func TestAutomodContentRuleMatches(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, handled := AutomodContentRuleMatches(tt.matchType, tt.pattern, tt.threshold, tt.text)
			if got != tt.want || handled != tt.handled {
				t.Fatalf("AutomodContentRuleMatches = %v/%v, want %v/%v", got, handled, tt.want, tt.handled)
			}
		})
	}
}

func TestAutomodRegexInputPreservesUTF8(t *testing.T) {
	text := strings.Repeat("a", maxAutomodRegexInputBytes-1) + "界"
	got := AutomodRegexInput(text)
	if len(got) > maxAutomodRegexInputBytes {
		t.Fatalf("AutomodRegexInput length = %d, want <= %d", len(got), maxAutomodRegexInputBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("AutomodRegexInput returned invalid UTF-8")
	}
}

func TestAutomodLinkCount(t *testing.T) {
	if got := AutomodLinkCount("HTTP://a.test and https://b.test and ftp://c.test"); got != 2 {
		t.Fatalf("AutomodLinkCount = %d, want 2", got)
	}
}
