package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const automodSystemActor = "automod"

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
		// Moderation-only: the audit payload carries rule/target/action metadata,
		// so it must not be delivered/replayed on the board scope (that exposes
		// moderation internals to every board member — M8 sibling).
		auditScopes := []string{"moderation:global"}
		auditSeq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodTriggered, auditScopes, auditPayload)
		if err != nil {
			return nil, err
		}
		if err := currentRuntime().InsertAutomodAuditLog(tx, auditPayload); err != nil {
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
		if err := currentRuntime().InsertModerationReview(tx, reviewID, "automod", postID, "post", by, reason, ts); err != nil {
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
		if err := currentRuntime().MarkPostRedacted(tx, postID, seq); err != nil {
			return nil, err
		}
		if err := currentRuntime().RecordPostDeletion(tx, postID, threadID, boardID, by, automodSystemActor, reason, "recycle", ts, seq); err != nil {
			return nil, err
		}
		if err := currentRuntime().FtsDeletePost(tx, postID); err != nil {
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
		if err := currentRuntime().SetThreadLocked(tx, threadID, true); err != nil {
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
		if err := currentRuntime().InsertSanction(tx, sanctionID, targetUserID, kind, scope, expiresAt, by, reason, seq); err != nil {
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
	p, actions, msg := proto.NormalizeSetBoardAutomodRulePayload(p)
	board := p.Board
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	if msg := proto.ValidateAutomodRule(p); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	matchType := p.MatchType
	action := p.Action
	pattern := p.Pattern

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if errDetail := commandrules.RequireBoard(tx, board); errDetail != nil {
		return Reply{Err: errDetail}
	}

	req := proto.AutomodActionPermissionRequirements(actions)
	canModerateThreads := false
	if req.ThreadModeration {
		canModerateThreads = commandrules.ActorCanModerateBoardThreads(tx, actor, board)
	}
	canModeratePosts := false
	if req.PostModeration {
		canModeratePosts = commandrules.ActorCanModerateBoardPosts(tx, actor, board)
	}
	if failure := proto.CheckAutomodActionPermissions(req, actor.IsAdmin(), canModerateThreads, canModeratePosts); failure != nil {
		return Reply{Err: errDetail(failure.Code, failure.Message, false)}
	}

	ruleID := p.ID
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
		Action: action, DurationSec: p.DurationSec, Reason: p.Reason,
		Note: p.Note, By: actor.ID, TS: ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodRuleSet, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().UpsertBoardAutomodRule(tx, payload); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtBoardAutomodRuleSet, seq, scopes, payload, ts)
	return Reply{Result: &proto.AckResult{ID: ruleID, Seq: seq}}
}

// deleteBoardAutomodRule removes a board automod rule. Any moderator of the
// board (post or thread) may delete a rule.
func (h *Handler) deleteBoardAutomodRule(actor *User, p proto.DeleteBoardAutomodRulePayload) Reply {
	var msg string
	p, msg = proto.NormalizeDeleteBoardAutomodRulePayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	board := p.Board
	id := p.ID
	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint
	canModerateBoard := commandrules.ActorCanModerateBoardPosts(tx, actor, board) || commandrules.ActorCanModerateBoardThreads(tx, actor, board)
	if errDetail := commandrules.RequireBoardModerationPermission(canModerateBoard); errDetail != nil {
		return Reply{Err: errDetail}
	}
	ts := nowMS()
	scopes := []string{"board:" + board}
	payload := &proto.BoardAutomodRuleDeletedPayload{ID: id, Board: board, By: actor.ID, TS: ts}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardAutomodRuleDeleted, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().DeleteBoardAutomodRule(tx, board, id); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.publishEvent(proto.EvtBoardAutomodRuleDeleted, seq, scopes, payload, ts)
	return Reply{Result: &proto.AckResult{ID: id, Seq: seq}}
}
