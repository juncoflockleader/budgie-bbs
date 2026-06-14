package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var pollTagOpenRe = regexp.MustCompile(`(?i)^\[poll(?:\s+expires\s*=\s*([^\]]+))?\s*\]$`)
var pollExpiryNumberRe = regexp.MustCompile(`^\d+$`)

func validatePollMarkup(body string) error {
	openIdx := strings.Index(strings.ToLower(body), "[poll")
	if openIdx < 0 {
		return nil
	}

	closeBracketIdx := strings.Index(body[openIdx:], "]")
	if closeBracketIdx < 0 {
		return fmt.Errorf("poll block has an invalid opening tag.")
	}
	closeBracketIdx += openIdx

	openTag := body[openIdx : closeBracketIdx+1]
	openMatch := pollTagOpenRe.FindStringSubmatch(openTag)
	if openMatch == nil {
		return fmt.Errorf("poll tag is malformed. Use [poll] or [poll expires=<timestamp>].")
	}

	expires := openMatch[1]
	if expires != "" && !looksLikeValidExpires(expires) {
		return fmt.Errorf("poll closing time is invalid. Use like 2026-06-15T14:30, 2h, 3d, or UNIX ms.")
	}

	closeIdx := strings.Index(body[closeBracketIdx+1:], "[/poll]")
	if closeIdx < 0 {
		closeIdx = strings.Index(strings.ToLower(body[closeBracketIdx+1:]), "[/poll]")
		if closeIdx < 0 {
			return fmt.Errorf("poll block is missing a closing [/poll] tag.")
		}
	}
	closeIdx += closeBracketIdx + 1

	inner := body[closeBracketIdx+1 : closeIdx]
	lines := strings.Split(inner, "\n")
	question := ""
	var options []string
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if question == "" && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") {
			question = line
			continue
		}
		option := strings.TrimLeft(line, "-* ")
		if option != "" {
			options = append(options, option)
		}
	}

	if question == "" {
		return fmt.Errorf("poll block is invalid: add a question line before options.")
	}
	if len(options) < 2 {
		return fmt.Errorf("poll block is invalid: include at least two options.")
	}
	return nil
}

func looksLikeValidExpires(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	if pollExpiryNumberRe.MatchString(value) {
		return value != "0"
	}
	if _, err := parsePollExpiresLikeBackend(value); err == nil {
		return true
	}
	return false
}

func parsePollExpiresLikeBackend(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	rawLower := strings.ToLower(raw)

	if rawInt, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if rawInt <= 0 {
			return 0, fmt.Errorf("invalid poll expiry: %q", raw)
		}
		if rawInt < 1_000_000_000_000 {
			rawInt *= 1000
		}
		return rawInt, nil
	}

	if dur, err := time.ParseDuration(rawLower); err == nil {
		if dur <= 0 {
			return 0, fmt.Errorf("invalid poll expiry: %q", raw)
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
		if err == nil {
			return 0, fmt.Errorf("invalid poll expiry: %q", raw)
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
