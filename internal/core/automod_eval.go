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
	// Staff are exempt: a user who can moderate the board is never auto-actioned
	// by that board's own rules.
	if userExemptFromAutomod(db, boardID, authorID) {
		return nil, nil
	}
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
			// Defense-in-depth against ReDoS-style CPU exhaustion: skip patterns
			// that compile to an oversized program (validation rejects these on
			// write, but a rule may predate that check), and bound the input fed
			// to the matcher so per-evaluation cost stays small regardless.
			if proto.AutomodRegexWithinComplexityLimit(r.Pattern) {
				if re, err := regexp.Compile(r.Pattern); err == nil {
					matched = re.MatchString(automodRegexInput(text))
				}
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
			if r.Threshold >= 1 && r.WindowSec >= 1 && strings.TrimSpace(authorID) != "" {
				since := nowMS() - int64(r.WindowSec)*1000
				matched = automodRecentPostCount(db, boardID, authorID, since) >= r.Threshold
			}
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
func evaluateBoardAutomodForHandler(db *sql.DB, boardID, text, authorID string) (matched bool, ruleID, matchType, action, reason string, durationSec int64, err error) {
	rule, err := evaluateBoardAutomod(db, boardID, text, authorID)
	if err != nil || rule == nil {
		return false, "", "", "", "", 0, err
	}
	return true, rule.ID, rule.MatchType, rule.Action, rule.Reason, rule.DurationSec, nil
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
	var events []EventAppend
	idx := startIndex
	for _, action := range proto.ParseAutomodActions(rule.Action) {
		evtID := stableCommandLogDecisionID("evt_", record, idx)
		var actionEvent *EventAppend
		switch action {
		case "manual_review":
			reviewID := stableCommandLogDecisionID("rev_", record, idx)
			actionEvent = &EventAppend{
				ID: evtID, Kind: proto.EvtPostFlagged, Scopes: []string{"moderation:global"}, // moderation-only: reporter/reason not broadcast to board (M8)
				Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, Kind: "automod", PostID: postID, Thread: threadID, Reporter: by, Reason: reason, TS: ts}, TS: ts,
			}
		case "redact":
			actionEvent = &EventAppend{
				ID: evtID, Kind: proto.EvtPostRedacted, Scopes: []string{"thread:" + threadID, "board:" + boardID},
				Payload: &proto.PostRedactedPayload{ID: postID, Thread: threadID, By: by, Reason: reason, TS: ts}, TS: ts,
			}
		case "lock_thread":
			actionEvent = &EventAppend{
				ID: evtID, Kind: proto.EvtThreadLocked, Scopes: []string{"board:" + boardID, "thread:" + threadID},
				Payload: &proto.ThreadLockedPayload{Thread: threadID, Locked: true, By: by, TS: ts}, TS: ts,
			}
		case "board_mute", "board_ban", "global_mute":
			kind := "mute"
			if action == "board_ban" {
				kind = "ban"
			}
			scope := boardID
			if action == "global_mute" {
				scope = "global"
			}
			actionEvent = &EventAppend{
				ID: evtID, Kind: proto.EvtUserSanctioned, Scopes: []string{"account:" + actorID},
				Payload: &proto.UserSanctionedPayload{User: actorID, Kind: kind, Scope: scope, DurationSec: rule.DurationSec, By: by, Reason: reason, TS: ts}, TS: ts,
			}
		default:
			continue
		}
		events = append(events, *actionEvent)
		idx++
		events = append(events, EventAppend{
			ID:     stableCommandLogDecisionID("evt_", record, idx),
			Kind:   proto.EvtBoardAutomodTriggered,
			Scopes: []string{"moderation:global"}, // moderation-only: metadata must not reach board subscribers (M8 sibling)
			Payload: &proto.BoardAutomodTriggeredPayload{
				ID: stableCommandLogDecisionID("amlog_", record, idx), Board: boardID, RuleID: rule.ID,
				MatchType: rule.MatchType, Action: action, TargetUser: actorID, PostID: postID, ThreadID: threadID, Reason: reason, TS: ts,
			},
			TS: ts,
		})
		idx++
	}
	return events, nil
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

// maxAutomodRegexInputBytes bounds how much post text a user regex runs against.
// Combined with the program-size cap at validation, this keeps any single
// automod regex evaluation cheap even on very large posts.
const maxAutomodRegexInputBytes = 16 << 10 // 16 KiB

// automodRegexInput returns text truncated (on a UTF-8 boundary) to the input
// cap used for regex matching.
func automodRegexInput(text string) string {
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

func countLinks(text string) int {
	return len(automodLinkRe.FindAllStringIndex(text, -1))
}

// automodRecentPostCount counts a user's posts on a board since the given time
// (unix ms). Derived from the durable posts projection, so it is consistent
// across API nodes without a separate counter store.
func automodRecentPostCount(db *sql.DB, boardID, authorID string, sinceMS int64) int {
	var n int
	err := qQueryRow(db,
		`SELECT COUNT(*) FROM posts p JOIN threads t ON t.id=p.thread
		  WHERE t.board=? AND p.author_id=? AND p.created_at >= ?`,
		boardID, authorID, sinceMS,
	).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}

// userExemptFromAutomod reports whether a user moderates the board (site
// mod/admin, board moderator, or member with a post/thread moderation
// capability) and is therefore exempt from its automod rules.
func userExemptFromAutomod(db *sql.DB, boardID, userID string) bool {
	if strings.TrimSpace(userID) == "" {
		return false
	}
	var role string
	if err := qQueryRow(db, `SELECT role FROM users WHERE id=?`, userID).Scan(&role); err == nil {
		if role == "admin" || role == "moderator" {
			return true
		}
	}
	var x int
	if err := qQueryRow(db, `SELECT 1 FROM board_moderators WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&x); err == nil {
		return true
	}
	var posts, threads int
	if err := qQueryRow(db, `SELECT can_moderate_posts, can_moderate_threads FROM board_members WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&posts, &threads); err == nil {
		return posts != 0 || threads != 0
	}
	return false
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
