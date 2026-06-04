package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) grantRole(actor *User, p proto.GrantRolePayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := getUserTx(tx, p.User)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}

	scopes := []string{"account:" + target.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtRoleGranted, scopes, &proto.RoleGrantedPayload{
		User: target.ID, Role: p.Role, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setUserRole(tx, target.ID, p.Role); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtRoleGranted, Seq: seq, Scopes: scopes,
		Payload: &proto.RoleGrantedPayload{User: target.Name, Role: p.Role, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: target.ID, Seq: seq}}
}

func (h *Handler) revokeRole(actor *User, p proto.RevokeRolePayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := getUserTx(tx, p.User)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}

	scopes := []string{"account:" + target.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtRoleRevoked, scopes, &proto.RoleRevokedPayload{
		User: target.ID, Role: p.Role, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setUserRole(tx, target.ID, "user"); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtRoleRevoked, Seq: seq, Scopes: scopes,
		Payload: &proto.RoleRevokedPayload{User: target.Name, Role: p.Role, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: target.ID, Seq: seq}}
}

func (h *Handler) sendChatLine(actor *User, p proto.SendChatLinePayload) Reply {
	if p.Room == "" || p.Text == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "room and text are required", false)}
	}
	ts := nowMS()
	id := newID("chat_")
	scopes := []string{"chat:" + p.Room}

	h.bus.Publish(&proto.Event{
		Kind:    proto.EvtChatLine,
		Scopes:  scopes,
		Payload: &proto.ChatLinePayload{ID: id, Room: p.Room, User: actor.Name, Text: p.Text, TS: ts},
		TS:      ts,
	})

	return Reply{Result: &proto.AckResult{ID: id}}
}

func (h *Handler) setPresence(actor *User, p proto.SetPresencePayload) Reply {
	if p.Status == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "status is required", false)}
	}
	ts := nowMS()
	scopes := []string{"presence:global"}

	h.bus.Publish(&proto.Event{
		Kind:    proto.EvtPresenceUpdate,
		Scopes:  scopes,
		Payload: &proto.PresenceUpdatePayload{User: actor.Name, Status: p.Status, TS: ts},
		TS:      ts,
	})

	return Reply{Result: &proto.AckResult{}}
}

func (h *Handler) sanctionUser(actor *User, p proto.SanctionUserPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	if p.User == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "user is required", false)}
	}
	if p.Kind != "mute" && p.Kind != "ban" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, `kind must be "mute" or "ban"`, false)}
	}
	scope := p.Scope
	if scope == "" {
		scope = "global"
	}
	ts := nowMS()
	var expiresAt int64
	if p.DurationSec > 0 {
		expiresAt = ts + p.DurationSec*1000
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := getUserTx(tx, p.User)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if target.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "cannot sanction an admin", false)}
	}
	if target.IsMod() && !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "only admins can sanction moderators", false)}
	}

	// Validate scope is "global" or an existing board.
	if scope != "global" {
		var boardName string
		if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		} else if err != nil {
			return internalErr(err)
		}
	}

	sanctionID := newID("san_")
	scopes := []string{"account:" + target.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtUserSanctioned, scopes, &proto.UserSanctionedPayload{
		User: target.ID, Kind: p.Kind, Scope: scope, DurationSec: p.DurationSec,
		By: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertSanction(tx, sanctionID, target.ID, p.Kind, scope, expiresAt, actor.ID, p.Reason, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtUserSanctioned, Seq: seq, Scopes: scopes,
		Payload: &proto.UserSanctionedPayload{User: target.Name, Kind: p.Kind, Scope: scope,
			DurationSec: p.DurationSec, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: sanctionID, Seq: seq}}
}

func (h *Handler) createBoard(actor *User, p proto.CreateBoardPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	if p.ID == "" || p.Name == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "id and name are required", false)}
	}
	if !isValidSlug(p.ID) {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "id must be lowercase alphanumeric, hyphens, or underscores (max 64 chars)", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + p.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, scopes, &proto.BoardCreatedPayload{
		ID: p.ID, Name: p.Name, Description: p.Description, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertBoard(tx, p.ID, p.Name, p.Description); err != nil {
		return Reply{Err: errDetail(proto.ErrConflict, "board already exists", false)}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: seq, Scopes: scopes,
		Payload: &proto.BoardCreatedPayload{ID: p.ID, Name: p.Name, Description: p.Description, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: p.ID, Seq: seq}}
}

func (h *Handler) purgePost(actor *User, p proto.PurgePostPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	// Read before TX.
	post, err := getPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}

	thread, err := getThread(h.db, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostPurged, scopes, &proto.PostPurgedPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := markPostPurged(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	// Remove from FTS permanently.
	if err := ftsDeletePost(tx, post.ID); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostPurged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostPurgedPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}
