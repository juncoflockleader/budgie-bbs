package handler

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

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
	action := strings.TrimSpace(p.Action)
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

	if reply := h.authorizeAutomodAction(tx, actor, board, action); reply.Err != nil {
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

func (h *Handler) authorizeAutomodAction(tx *sql.Tx, actor *User, board, action string) Reply {
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
