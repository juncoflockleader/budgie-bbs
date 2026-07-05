package automodmodel

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Reason builds the audit reason recorded for an automod action.
func Reason(reason, ruleID string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return "Automod: " + reason
	}
	return "Automod rule " + ruleID
}

func ContentRuleMatches(matchType, pattern string, threshold int, text string, regexAllowed func(string) bool) (matched bool, handled bool) {
	switch matchType {
	case "keyword":
		needle := strings.ToLower(strings.TrimSpace(pattern))
		return needle != "" && strings.Contains(strings.ToLower(text), needle), true
	case "regex":
		if regexAllowed != nil && !regexAllowed(pattern) {
			return false, true
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, true
		}
		return re.MatchString(RegexInput(text)), true
	case "repeated_text":
		return MaxConsecutiveRun(text) >= threshold, true
	case "link_count":
		return LinkCount(text) >= threshold, true
	default:
		return false, false
	}
}

// MaxConsecutiveRun returns the length of the longest run of the same
// non-whitespace rune, a simple repeated-text/flooding signal.
func MaxConsecutiveRun(text string) int {
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

// MaxRegexInputBytes bounds how much post text a user regex runs against.
// Combined with the program-size cap at validation, this keeps any single
// automod regex evaluation cheap even on very large posts.
const MaxRegexInputBytes = 16 << 10 // 16 KiB

// RegexInput returns text truncated (on a UTF-8 boundary) to the input cap used
// for regex matching.
func RegexInput(text string) string {
	if len(text) <= MaxRegexInputBytes {
		return text
	}
	b := text[:MaxRegexInputBytes]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

var linkRe = regexp.MustCompile(`(?i)https?://`)

func LinkCount(text string) int {
	return len(linkRe.FindAllStringIndex(text, -1))
}
