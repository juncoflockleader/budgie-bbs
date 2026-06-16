package handler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9_\-]{1,64})`)

var pollTagOpenRe = regexp.MustCompile(`(?i)^\[poll(?:\s+expires\s*=\s*([^\]]+))?\s*\]$`)

// pollBlock represents a parsed [poll] block from post markup.
type pollBlock struct {
	question  string
	options   []string
	expiresAt int64
}

// parseMentions extracts unique @usernames from post body text.
func parseMentions(body string) []string {
	matches := mentionRe.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			out = append(out, m[1]) // preserve original casing for lookup
		}
	}
	return out
}

// ParseMentions extracts mentions without mutating handler internals.
func ParseMentions(body string) []string {
	return parseMentions(body)
}

// extractPoll looks for [poll]...[/poll] in body. Returns the parsed block
// (or nil if absent) and the body with the poll block stripped.
func extractPoll(body string) (*pollBlock, string) {
	const close = "[/poll]"
	start := strings.Index(strings.ToLower(body), "[poll")
	if start < 0 {
		return nil, body
	}
	openClose := strings.Index(body[start:], "]")
	if openClose < 0 {
		return nil, body
	}
	openClose += start
	openTag := body[start : openClose+1]
	openMatch := pollTagOpenRe.FindStringSubmatch(openTag)
	if len(openMatch) == 0 {
		return nil, body
	}

	expiresAt := int64(0)
	if openMatch[1] != "" {
		parsed, err := parsePollExpires(openMatch[1])
		if err != nil {
			return nil, body
		}
		expiresAt = parsed
	}

	end := strings.Index(body[openClose+1:], close)
	if end < 0 {
		end = strings.Index(strings.ToLower(body[openClose+1:]), close)
	}
	if end < 0 {
		return nil, body
	}
	end += openClose + 1

	inner := strings.TrimSpace(body[openClose+1 : end])
	if inner == "" {
		return nil, body
	}
	lines := strings.Split(inner, "\n")
	var question string
	var options []string
	// Cap poll options so a single post can't create an unbounded number of
	// option rows; excess lines beyond the cap are ignored.
	const maxPollOptions = 20
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if question == "" && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") {
			question = line
		} else if len(options) < maxPollOptions {
			opt := strings.TrimLeft(line, "-* ")
			if opt != "" {
				options = append(options, opt)
			}
		}
	}
	if question == "" || len(options) < 2 {
		return nil, body // not a valid poll
	}
	cleanBody := strings.TrimSpace(body[:start])
	after := strings.TrimSpace(body[end+len(close):])
	if cleanBody != "" && after != "" {
		cleanBody = cleanBody + "\n" + after
	} else {
		cleanBody = cleanBody + after
	}
	return &pollBlock{question: question, options: options, expiresAt: expiresAt}, cleanBody
}

// PollBlock is the parsed poll representation for compatibility callers.
type PollBlock struct {
	Question  string
	Options   []string
	ExpiresAt int64
}

// ParsePoll converts the internal parser output into a stable public shape.
func ParsePoll(body string) (*PollBlock, string) {
	pb, cleanBody := extractPoll(body)
	if pb == nil {
		return nil, cleanBody
	}
	return &PollBlock{Question: pb.question, Options: pb.options, ExpiresAt: pb.expiresAt}, cleanBody
}

func parsePollExpires(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	rawLower := strings.ToLower(raw)

	if rawInt, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if rawInt <= 0 {
			return 0, nil
		}
		if rawInt < 1_000_000_000_000 {
			// seconds -> milliseconds
			rawInt *= 1000
		}
		return rawInt, nil
	}

	if dur, err := time.ParseDuration(rawLower); err == nil {
		if dur <= 0 {
			return 0, nil
		}
		return time.Now().Add(dur).UnixMilli(), nil
	}

	if strings.HasSuffix(rawLower, "d") {
		daysRaw := strings.TrimSuffix(rawLower, "d")
		days, err := strconv.ParseFloat(daysRaw, 64)
		if err == nil && days > 0 {
			return time.Now().Add(time.Duration(days*24) * time.Hour).UnixMilli(), nil
		}
	}

	if strings.HasSuffix(rawLower, "w") {
		weeksRaw := strings.TrimSuffix(rawLower, "w")
		weeks, err := strconv.ParseFloat(weeksRaw, 64)
		if err == nil && weeks > 0 {
			return time.Now().Add(time.Duration(weeks*24*7) * time.Hour).UnixMilli(), nil
		}
	}

	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if layout == "2006-01-02T15:04:05" || layout == "2006-01-02T15:04" || layout == "2006-01-02" {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return t.UnixMilli(), nil
			}
		}
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UnixMilli(), nil
		}
	}

	return 0, fmt.Errorf("invalid poll expiry: %q", raw)
}

// ParsePollExpires is exported for compatibility with bridge parsing.
func ParsePollExpires(raw string) (int64, error) {
	return parsePollExpires(raw)
}
