package core

import (
	"database/sql"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// automodActorID is the synthetic actor recorded for automod-driven events.
const automodActorID = "automod"

// evaluateBoardAutomod returns the first enabled rule (by priority) that matches
// the given text for the board, or nil if none match. Content match types
// evaluate against text; account_age uses the author's account age. The
// rate_threshold type is not evaluated here (it requires durable counters,
// landing in Phase 9).
func evaluateBoardAutomod(db *sql.DB, boardID, text, authorID string) (*projections.BoardAutomodRule, error) {
	rules, err := projections.ListBoardAutomodRules(db, boardID)
	if err != nil || len(rules) == 0 {
		return nil, err
	}
	lower := strings.ToLower(text)
	accountAgeHours := -1.0 // computed lazily, once
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		matched := false
		switch r.MatchType {
		case "keyword":
			needle := strings.ToLower(strings.TrimSpace(r.Pattern))
			matched = needle != "" && strings.Contains(lower, needle)
		case "regex":
			if re, err := regexp.Compile(r.Pattern); err == nil {
				matched = re.MatchString(text)
			}
		case "repeated_text":
			matched = maxConsecutiveRun(text) >= r.Threshold
		case "link_count":
			matched = countLinks(text) >= r.Threshold
		case "account_age":
			if accountAgeHours < 0 {
				accountAgeHours = automodAccountAgeHours(db, authorID)
			}
			matched = accountAgeHours >= 0 && accountAgeHours < float64(r.Threshold)
		case "rate_threshold":
			continue // deferred to Phase 9 (needs durable counters)
		}
		if matched {
			return r, nil
		}
	}
	return nil, nil
}

// evaluateBoardAutomodForHandler is the runtime bridge used by the command
// handler. It returns the matched rule's actionable fields as primitives so the
// handler package need not import projection types.
func evaluateBoardAutomodForHandler(db *sql.DB, boardID, text, authorID string) (matched bool, ruleID, action, reason string, durationSec int64, err error) {
	rule, err := evaluateBoardAutomod(db, boardID, text, authorID)
	if err != nil || rule == nil {
		return false, "", "", "", 0, err
	}
	return true, rule.ID, rule.Action, rule.Reason, rule.DurationSec, nil
}

// automodReason builds the audit reason recorded for an automod action.
func automodReason(reason, ruleID string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return "Automod: " + reason
	}
	return "Automod rule " + ruleID
}

// nativeAutomodEvents evaluates automod rules for a just-decided post/thread and
// returns the action's events for the native command-log path. Projection
// side-effects are applied downstream when these events are committed.
func nativeAutomodEvents(db *sql.DB, record CommandLogRecord, actorID, postID, threadID, boardID, text string, ts int64, startIndex int) ([]EventAppend, *proto.ErrorDetail) {
	rule, err := evaluateBoardAutomod(db, boardID, text, actorID)
	if err != nil {
		return nil, nativeDecisionErr("internal_error", err.Error(), true)
	}
	if rule == nil {
		return nil, nil
	}
	reason := automodReason(rule.Reason, rule.ID)
	by := automodActorID
	evtID := stableCommandLogDecisionID("evt_", record, startIndex)
	switch rule.Action {
	case "manual_review":
		reviewID := stableCommandLogDecisionID("rev_", record, startIndex)
		return []EventAppend{{
			ID: evtID, Kind: proto.EvtPostFlagged, Scopes: []string{"thread:" + threadID, "board:" + boardID, "moderation:global"},
			Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, Kind: "automod", PostID: postID, Thread: threadID, Reporter: by, Reason: reason, TS: ts}, TS: ts,
		}}, nil
	case "redact":
		return []EventAppend{{
			ID: evtID, Kind: proto.EvtPostRedacted, Scopes: []string{"thread:" + threadID, "board:" + boardID},
			Payload: &proto.PostRedactedPayload{ID: postID, Thread: threadID, By: by, Reason: reason, TS: ts}, TS: ts,
		}}, nil
	case "lock_thread":
		return []EventAppend{{
			ID: evtID, Kind: proto.EvtThreadLocked, Scopes: []string{"board:" + boardID, "thread:" + threadID},
			Payload: &proto.ThreadLockedPayload{Thread: threadID, Locked: true, By: by, TS: ts}, TS: ts,
		}}, nil
	case "board_mute", "board_ban", "global_mute":
		kind := "mute"
		if rule.Action == "board_ban" {
			kind = "ban"
		}
		scope := boardID
		if rule.Action == "global_mute" {
			scope = "global"
		}
		return []EventAppend{{
			ID: evtID, Kind: proto.EvtUserSanctioned, Scopes: []string{"account:" + actorID},
			Payload: &proto.UserSanctionedPayload{User: actorID, Kind: kind, Scope: scope, DurationSec: rule.DurationSec, By: by, Reason: reason, TS: ts}, TS: ts,
		}}, nil
	}
	return nil, nil
}

// maxConsecutiveRun returns the length of the longest run of the same
// non-whitespace rune (a simple "repeated text" / flooding signal).
func maxConsecutiveRun(text string) int {
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

var automodLinkRe = regexp.MustCompile(`(?i)https?://`)

func countLinks(text string) int {
	return len(automodLinkRe.FindAllStringIndex(text, -1))
}

func automodAccountAgeHours(db *sql.DB, userID string) float64 {
	if strings.TrimSpace(userID) == "" {
		return -1
	}
	var created int64
	if err := qQueryRow(db, `SELECT created FROM users WHERE id=?`, userID).Scan(&created); err != nil || created <= 0 {
		return -1
	}
	age := nowMS() - created
	if age < 0 {
		age = 0
	}
	return float64(age) / 3_600_000.0
}
