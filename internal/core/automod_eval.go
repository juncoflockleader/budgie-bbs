package core

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
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
	accountAgeHours := -1.0 // computed lazily, once
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		matched := false
		if contentMatch, handled := commandrules.AutomodContentRuleMatches(r.MatchType, r.Pattern, r.Threshold, text); handled {
			matched = contentMatch
		} else if r.MatchType == "account_age" {
			if accountAgeHours < 0 {
				accountAgeHours = automodAccountAgeHours(db, authorID)
			}
			matched = accountAgeHours >= 0 && accountAgeHours < float64(r.Threshold)
		} else if r.MatchType == "rate_threshold" {
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
	reason := commandrules.AutomodReason(rule.Reason, rule.ID)
	by := automodActorID
	var events []EventAppend
	idx := startIndex
	for _, action := range proto.ParseAutomodActions(rule.Action) {
		evtID := stableCommandLogDecisionID("evt_", record, idx)
		var actionEvent *EventAppend
		switch action {
		case "manual_review":
			reviewID := stableCommandLogDecisionID("rev_", record, idx)
			scopes, payload := commandevents.PostFlagged(reviewID, "automod", postID, threadID, by, reason, ts)
			actionEvent = &EventAppend{ID: evtID, Kind: proto.EvtPostFlagged, Scopes: scopes, Payload: payload, TS: ts}
		case "redact":
			scopes, payload := commandevents.PostRedacted(postID, threadID, boardID, by, reason, "", ts)
			actionEvent = &EventAppend{ID: evtID, Kind: proto.EvtPostRedacted, Scopes: scopes, Payload: payload, TS: ts}
		case "lock_thread":
			scopes, payload := commandevents.ThreadLocked(threadID, boardID, true, by, ts)
			actionEvent = &EventAppend{ID: evtID, Kind: proto.EvtThreadLocked, Scopes: scopes, Payload: payload, TS: ts}
		case "board_mute", "board_ban", "global_mute":
			kind := "mute"
			if action == "board_ban" {
				kind = "ban"
			}
			scope := boardID
			if action == "global_mute" {
				scope = "global"
			}
			scopes, payload := commandevents.UserSanctioned(actorID, actorID, kind, scope, rule.DurationSec, by, reason, ts)
			actionEvent = &EventAppend{ID: evtID, Kind: proto.EvtUserSanctioned, Scopes: scopes, Payload: payload, TS: ts}
		default:
			continue
		}
		events = append(events, *actionEvent)
		idx++
		auditScopes, auditPayload := commandevents.BoardAutomodTriggered(
			stableCommandLogDecisionID("amlog_", record, idx),
			boardID, rule.ID, rule.MatchType, action, actorID, postID, threadID, reason, ts,
		)
		events = append(events, EventAppend{
			ID: stableCommandLogDecisionID("evt_", record, idx), Kind: proto.EvtBoardAutomodTriggered,
			Scopes: auditScopes, Payload: auditPayload, TS: ts,
		})
		idx++
	}
	return events, nil
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

func (c *Core) ListModerationReviews(status string, limit, offset int) ([]projections.ModerationReview, error) {
	return projections.ListModerationReviews(c.DB, status, limit, offset)
}

func (c *Core) ListContentFilters(scope string, includeInactive bool, limit, offset int) ([]projections.ContentFilter, error) {
	return projections.ListContentFilters(c.DB, scope, includeInactive, limit, offset)
}

func (c *Core) ListBoardAutomodRules(boardID string) ([]projections.BoardAutomodRule, error) {
	return projections.ListBoardAutomodRules(c.DB, boardID)
}

func (c *Core) ListBoardAutomodActivity(boardID string, limit, offset int) ([]projections.BoardAutomodActivity, error) {
	return projections.ListBoardAutomodActivity(c.DB, boardID, limit, offset)
}

func (c *Core) ListUserSanctions(userID string, limit, offset int) ([]projections.UserSanction, error) {
	return projections.ListUserSanctions(c.DB, userID, limit, offset)
}
