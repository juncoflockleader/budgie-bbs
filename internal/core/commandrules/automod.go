package commandrules

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// AutomodReason builds the audit reason recorded for an automod action.
func AutomodReason(reason, ruleID string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return "Automod: " + reason
	}
	return "Automod rule " + ruleID
}

func AutomodContentRuleMatches(matchType, pattern string, threshold int, text string) (matched bool, handled bool) {
	switch matchType {
	case "keyword":
		needle := strings.ToLower(strings.TrimSpace(pattern))
		return needle != "" && strings.Contains(strings.ToLower(text), needle), true
	case "regex":
		if !proto.AutomodRegexWithinComplexityLimit(pattern) {
			return false, true
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, true
		}
		return re.MatchString(AutomodRegexInput(text)), true
	case "repeated_text":
		return MaxAutomodConsecutiveRun(text) >= threshold, true
	case "link_count":
		return AutomodLinkCount(text) >= threshold, true
	default:
		return false, false
	}
}

// MaxAutomodConsecutiveRun returns the length of the longest run of the same
// non-whitespace rune (a simple repeated-text/flooding signal).
func MaxAutomodConsecutiveRun(text string) int {
	best, run := 0, 0
	var prev rune = -1
	for _, c := range text {
		switch {
		case c == prev && c != ' ' && c != '\n' && c != '\t':
			run++
		default:
			run = 1
			prev = c
		}
		if run > best {
			best = run
		}
	}
	if utf8.RuneCountInString(text) == 0 {
		return 0
	}
	return best
}

// maxAutomodRegexInputBytes bounds how much post text a user regex runs against.
// Combined with the program-size cap at validation, this keeps any single
// automod regex evaluation cheap even on very large posts.
const maxAutomodRegexInputBytes = 16 << 10 // 16 KiB

// AutomodRegexInput returns text truncated (on a UTF-8 boundary) to the input
// cap used for regex matching.
func AutomodRegexInput(text string) string {
	if len(text) <= maxAutomodRegexInputBytes {
		return text
	}
	b := text[:maxAutomodRegexInputBytes]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

var automodLinkRe = regexp.MustCompile(`(?i)https?://`)

func AutomodLinkCount(text string) int {
	return len(automodLinkRe.FindAllStringIndex(text, -1))
}
