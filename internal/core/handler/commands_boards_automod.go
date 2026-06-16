package handler

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const automodSystemActor = "automod"

// automodReasonFor builds the audit reason recorded for an automod action.
func automodReasonFor(reason, ruleID string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return "Automod: " + reason
	}
	return "Automod rule " + ruleID
}

// applyAutomodActionTx applies a matched automod rule's action(s) inside the
// post/thread-creation transaction, returning the generated events to publish
// after commit. A rule may carry several comma-separated actions, applied in
// order; each gets its own audit-log entry. targetUserID is the posting author.
func (h *Handler) applyAutomodActionTx(tx *sql.Tx, ruleID, matchType, action, reason string, durationSec int64, targetUserID, postID, threadID, boardID string, ts int64) ([]*proto.Event, error) {
	by := automodSystemActor
	var generated []*proto.Event
	for _, act := range proto.ParseAutomodActions(action) {
		events, err := h.applyAutomodActionEventsTx(tx, act, reason, durationSec, by, targetUserID, postID, threadID, boardID, ts)
		if err != nil {
			return nil, err
		}
		generated = append(generated, events...)
		auditPayload := &proto.BoardAutomodTriggeredPayload{
			ID: newID("amlog_"), Board: boardID, RuleID: ruleID, MatchType: matchType, Action: act,
			TargetUser: targetUserID, PostID: postID, ThreadID: threadID, Reason: reason, TS: ts,
		}
		auditScopes := []string{"board:" + boardID, "moderation:global"}
		auditSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodTriggered, auditScopes, auditPayload)
		if err != nil {
			return nil, err
		}
		if err := insertAutomodAuditLog(tx, auditPayload); err != nil {
			return nil, err
		}
		generated = append(generated, &proto.Event{Kind: proto.EvtBoardAutomodTriggered, Seq: auditSeq, Scopes: auditScopes, Payload: auditPayload, TS: ts})
	}
	return generated, nil
}

func (h *Handler) applyAutomodActionEventsTx(tx *sql.Tx, action, reason string, durationSec int64, by, targetUserID, postID, threadID, boardID string, ts int64) ([]*proto.Event, error) {
	switch action {
	case "manual_review":
		reviewID := newID("rev_")
		if err := insertModerationReview(tx, reviewID, "automod", postID, "post", by, reason, ts); err != nil {
			return nil, err
		}
		// Moderation-only (reporter/reason must not reach board subscribers — M8).
		scopes := []string{"moderation:global"}
		payload := &proto.PostFlaggedPayload{ReviewID: reviewID, Kind: "automod", PostID: postID, Thread: threadID, Reporter: by, Reason: reason, TS: ts}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagged, scopes, payload)
		if err != nil {
			return nil, err
		}
		return []*proto.Event{{Kind: proto.EvtPostFlagged, Seq: seq, Scopes: scopes, Payload: payload, TS: ts}}, nil
	case "redact":
		scopes := []string{"thread:" + threadID, "board:" + boardID}
		payload := &proto.PostRedactedPayload{ID: postID, Thread: threadID, By: by, Reason: reason, TS: ts}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRedacted, scopes, payload)
		if err != nil {
			return nil, err
		}
		if err := markPostRedacted(tx, postID, seq); err != nil {
			return nil, err
		}
		if err := recordPostDeletion(tx, postID, threadID, boardID, by, automodSystemActor, reason, "recycle", ts, seq); err != nil {
			return nil, err
		}
		if err := ftsDeletePost(tx, postID); err != nil {
			return nil, err
		}
		return []*proto.Event{{Kind: proto.EvtPostRedacted, Seq: seq, Scopes: scopes, Payload: payload, TS: ts}}, nil
	case "lock_thread":
		scopes := []string{"board:" + boardID, "thread:" + threadID}
		payload := &proto.ThreadLockedPayload{Thread: threadID, Locked: true, By: by, TS: ts}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadLocked, scopes, payload)
		if err != nil {
			return nil, err
		}
		if err := setThreadLocked(tx, threadID, true); err != nil {
			return nil, err
		}
		return []*proto.Event{{Kind: proto.EvtThreadLocked, Seq: seq, Scopes: scopes, Payload: payload, TS: ts}}, nil
	case "board_mute", "board_ban", "global_mute":
		kind := "mute"
		if action == "board_ban" {
			kind = "ban"
		}
		scope := boardID
		if action == "global_mute" {
			scope = "global"
		}
		var expiresAt int64
		if durationSec > 0 {
			expiresAt = ts + durationSec*1000
		}
		sanctionID := newID("san_")
		scopes := []string{"account:" + targetUserID}
		payload := &proto.UserSanctionedPayload{User: targetUserID, Kind: kind, Scope: scope, DurationSec: durationSec, By: by, Reason: reason, TS: ts}
		seq, err := appendEvent(tx, newID("evt_"), proto.EvtUserSanctioned, scopes, payload)
		if err != nil {
			return nil, err
		}
		if err := insertSanction(tx, sanctionID, targetUserID, kind, scope, expiresAt, by, reason, seq); err != nil {
			return nil, err
		}
		return []*proto.Event{{Kind: proto.EvtUserSanctioned, Seq: seq, Scopes: scopes, Payload: payload, TS: ts}}, nil
	}
	return nil, nil
}

// setBoardAutomodRule creates or updates a board automod rule. Authorization is
// per action: global sanctions require admin, thread actions require thread
// moderation, and the rest require post moderation on that board.
func (h *Handler) setBoardAutomodRule(actor *User, p proto.SetBoardAutomodRulePayload) Reply {
	board := strings.TrimSpace(p.Board)
	if board == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board is required", false)}
	}
	if msg := proto.ValidateAutomodRule(p); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	matchType := strings.TrimSpace(p.MatchType)
	actions := proto.ParseAutomodActions(p.Action)
	action := strings.Join(actions, ",") // normalized
	pattern := strings.TrimSpace(p.Pattern)

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	var boardName string
	if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, board).Scan(&boardName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	if reply := h.authorizeAutomodActions(tx, actor, board, actions); reply.Err != nil {
		return reply
	}

	ruleID := strings.TrimSpace(p.ID)
	if ruleID == "" {
		ruleID = newID("rule_")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	ts := nowMS()
	scopes := []string{"board:" + board}
	payload := &proto.BoardAutomodRuleSetPayload{
		ID: ruleID, Board: board, Enabled: enabled, Priority: p.Priority,
		MatchType: matchType, Pattern: pattern, Threshold: p.Threshold, WindowSec: p.WindowSec,
		Action: action, DurationSec: p.DurationSec, Reason: strings.TrimSpace(p.Reason),
		Note: strings.TrimSpace(p.Note), By: actor.ID, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodRuleSet, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := upsertBoardAutomodRule(tx, payload); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtBoardAutomodRuleSet, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
	return Reply{Result: &proto.AckResult{ID: ruleID, Seq: seq}}
}

// authorizeAutomodActions requires the actor to hold the permission for every
// action a rule will take (the union of per-action requirements).
func (h *Handler) authorizeAutomodActions(tx *sql.Tx, actor *User, board string, actions []string) Reply {
	for _, action := range actions {
		switch action {
		case "global_mute":
			if !actor.IsAdmin() {
				return Reply{Err: errDetail(proto.ErrForbidden, "only admins can create global-sanction rules", false)}
			}
		case "lock_thread":
			if !h.actorCanModerateBoardThreadsTx(tx, actor, board) {
				return Reply{Err: errDetail(proto.ErrForbidden, "thread moderation permission required", false)}
			}
		default: // manual_review, redact, board_mute, board_ban
			if !h.actorCanModerateBoardPostsTx(tx, actor, board) {
				return Reply{Err: errDetail(proto.ErrForbidden, "post moderation permission required", false)}
			}
		}
	}
	return Reply{}
}

// deleteBoardAutomodRule removes a board automod rule. Any moderator of the
// board (post or thread) may delete a rule.
func (h *Handler) deleteBoardAutomodRule(actor *User, p proto.DeleteBoardAutomodRulePayload) Reply {
	board := strings.TrimSpace(p.Board)
	id := strings.TrimSpace(p.ID)
	if board == "" || id == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board and id are required", false)}
	}
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	if !h.actorCanModerateBoardPostsTx(tx, actor, board) && !h.actorCanModerateBoardThreadsTx(tx, actor, board) {
		return Reply{Err: errDetail(proto.ErrForbidden, "board moderation permission required", false)}
	}
	ts := nowMS()
	scopes := []string{"board:" + board}
	payload := &proto.BoardAutomodRuleDeletedPayload{ID: id, Board: board, By: actor.ID, TS: ts}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodRuleDeleted, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := deleteBoardAutomodRule(tx, board, id); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtBoardAutomodRuleDeleted, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}
