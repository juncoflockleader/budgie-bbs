package handler

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (h *Handler) grantRole(actor *User, p proto.GrantRolePayload) Reply {
	return h.changeRole(actor, p.User, p.Role, proto.EvtRoleGranted, "granted", p.Role)
}

func (h *Handler) revokeRole(actor *User, p proto.RevokeRolePayload) Reply {
	return h.changeRole(actor, p.User, p.Role, proto.EvtRoleRevoked, "revoked", "user")
}

func (h *Handler) changeRole(actor *User, userID, role string, kind proto.EventKind, action, storedRole string) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := currentRuntime().GetUserTx(tx, userID)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}

	scopes := []string{"account:" + target.ID}
	seq, err := appendEvent(tx, newID("evt_"), kind, scopes, proto.RoleChangePayload(kind, target.ID, role, actor.ID, ts))
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().SetUserRole(tx, target.ID, storedRole); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if err := h.ensureSyssecuritySystemPost(actor, "Role "+action+": "+target.Name, []string{
		"Action: role " + action,
		"User: " + target.Name,
		"Role: " + role,
		"Actor: " + actor.Name,
	}, ""); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: kind, Seq: seq, Scopes: scopes,
		Payload: proto.RoleChangePayload(kind, target.Name, role, actor.Name, ts), TS: ts})

	return Reply{Result: &proto.AckResult{ID: target.ID, Seq: seq}}
}

func (h *Handler) sendChatLine(actor *User, p proto.SendChatLinePayload) Reply {
	line, errDetail := commandrules.NormalizeChatLine(p.Room, p.Text)
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	ts := nowMS()
	id := newID("chat_")
	if err := chatStore().InsertChatLine(id, line.RoomID, line.RoomName, actor.ID, actor.Name, line.Text, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"chat:" + line.RoomID}

	h.bus.Publish(&proto.Event{
		Kind:    proto.EvtChatLine,
		Scopes:  scopes,
		Payload: &proto.ChatLinePayload{ID: id, Room: line.RoomID, User: actor.Name, Text: line.Text, TS: ts},
		TS:      ts,
	})
	// Notify sibling nodes in Postgres multi-node deployments.
	pgNotifyEphemeral(h.db, string(proto.EvtChatLine), id, "chat:"+line.RoomID)

	return Reply{Result: &proto.AckResult{ID: id}}
}

func (h *Handler) setPresence(actor *User, p proto.SetPresencePayload) Reply {
	status := strings.TrimSpace(p.Status)
	if status == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "status is required", false)}
	}
	if proto.CloakedPresenceStatus(status) {
		if !actor.IsMod() {
			return Reply{Err: errDetail(proto.ErrForbidden, "cloak presence requires moderator privileges", false)}
		}
		status = "cloak"
	}
	mode := strings.TrimSpace(p.Mode)
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	boardID := strings.TrimSpace(p.Board)
	threadID := strings.TrimSpace(p.Thread)
	location := strings.TrimSpace(p.Location)
	fromHost := strings.TrimSpace(p.FromHost)
	explicitBoard := boardID != ""
	explicitThread := threadID != ""

	if mode == "" || (boardID == "" && threadID == "") {
		derivedMode, derivedBoard, derivedThread := commandrules.DerivePresenceHints(status)
		if mode == "" {
			mode = derivedMode
		}
		if boardID == "" {
			boardID = derivedBoard
		}
		if threadID == "" {
			threadID = derivedThread
		}
	}
	if proto.HiddenPresenceStatus(status) {
		mode = ""
		boardID = ""
		threadID = ""
		location = ""
	}
	if errDetail := commandrules.ValidatePresenceText(status, sessionID, mode, boardID, threadID, location, fromHost); errDetail != nil {
		return Reply{Err: errDetail}
	}

	if threadID != "" {
		thread, err := currentRuntime().GetThread(h.db, threadID)
		if err != nil {
			return internalErr(err)
		}
		if thread == nil {
			if explicitThread {
				return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
			}
			threadID = ""
		} else {
			if boardID != "" && boardID != thread.Board {
				return Reply{Err: errDetail(proto.ErrValidationFailed, "thread does not belong to board", false)}
			}
			boardID = thread.Board
		}
	}
	if boardID != "" {
		if errReply := h.requireBoard(boardID); errReply.Err != nil {
			if explicitBoard || explicitThread {
				return errReply
			}
			boardID = ""
		}
	}
	if boardID != "" {
		settings, err := currentRuntime().GetBoardSettings(h.db, boardID)
		if err != nil {
			return internalErr(err)
		}
		if settings != nil && settings.MemberReadMode && !h.actorCanUseMemberBoard(actor, boardID) {
			if explicitBoard || explicitThread {
				return Reply{Err: errDetail(proto.ErrForbidden, "board members only", false)}
			}
			boardID = ""
			threadID = ""
		}
	}

	ts := nowMS()
	scopes := []string{"presence:global"}
	if boardID != "" {
		scopes = append(scopes, "presence:"+boardID)
	}
	persistPresence := !proto.TypingPresenceStatus(status)
	if persistPresence {
		if err := setUserPresence(h.db, actor.ID, sessionID, status, mode, boardID, threadID, location, fromHost, ts); err != nil {
			return internalErr(err)
		}
		if proto.VisiblePresenceStatus(status) {
			if err := h.notifyLoginWatchers(actor, ts); err != nil {
				return internalErr(err)
			}
		}
	}

	h.bus.Publish(&proto.Event{
		Kind:   proto.EvtPresenceUpdate,
		Scopes: scopes,
		Payload: &proto.PresenceUpdatePayload{
			User:      actor.Name,
			UserID:    actor.ID,
			SessionID: sessionID,
			Status:    status,
			Mode:      mode,
			Board:     boardID,
			Thread:    threadID,
			Location:  location,
			FromHost:  fromHost,
			TS:        ts,
		},
		TS: ts,
	})

	return Reply{Result: &proto.AckResult{}}
}

func (h *Handler) sanctionUser(actor *User, p proto.SanctionUserPayload) Reply {
	p, msg := proto.NormalizeSanctionUserPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	scope := p.Scope
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

	target, err := currentRuntime().GetUserTx(tx, p.User)
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

	// Authorize by scope: a global sanction requires the site moderator role;
	// a board sanction is also available to that board's moderators.
	if scope == "global" {
		if !actor.IsMod() {
			return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
		}
	} else {
		if _, found, err := projections.BoardName(tx, scope); err != nil {
			return internalErr(err)
		} else if !found {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		}
		if !h.actorCanModerateBoardPostsTx(tx, actor, scope) {
			return Reply{Err: errDetail(proto.ErrForbidden, "you do not moderate this board", false)}
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
	if err := currentRuntime().InsertSanction(tx, sanctionID, target.ID, p.Kind, scope, expiresAt, actor.ID, p.Reason, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtUserSanctioned, Seq: seq, Scopes: scopes,
		Payload: &proto.UserSanctionedPayload{User: target.Name, Kind: p.Kind, Scope: scope,
			DurationSec: p.DurationSec, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})
	if scope != "global" {
		if err := h.ensureDenyPostSystemPost(actor, target, scope, p.Kind, p.Reason, ts); err != nil {
			return internalErr(err)
		}
	}

	return Reply{Result: &proto.AckResult{ID: sanctionID, Seq: seq}}
}

func (h *Handler) clearUserSanction(actor *User, p proto.ClearUserSanctionPayload) Reply {
	p, msg := proto.NormalizeClearUserSanctionPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	userRef := p.User
	kind := p.Kind
	scope := p.Scope
	reason := p.Reason

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := projections.FindUserRef(tx, userRef)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if target.IsMod() && !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "only admins can clear moderator sanctions", false)}
	}
	// Authorize by scope: a global sanction requires the site moderator role;
	// a board sanction is also clearable by that board's moderators.
	if scope == "global" {
		if !actor.IsMod() {
			return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
		}
	} else {
		if _, found, err := projections.BoardName(tx, scope); err != nil {
			return internalErr(err)
		} else if !found {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		}
		if !h.actorCanModerateBoardPostsTx(tx, actor, scope) {
			return Reply{Err: errDetail(proto.ErrForbidden, "you do not moderate this board", false)}
		}
	}

	ts := nowMS()
	scopes := []string{"account:" + target.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtUserSanctionCleared, scopes, &proto.UserSanctionClearedPayload{
		User: target.ID, Kind: kind, Scope: scope, By: actor.ID, Reason: reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	removed, err := currentRuntime().ClearUserSanctions(tx, target.ID, kind, scope)
	if err != nil {
		return internalErr(err)
	}
	if removed == 0 {
		return Reply{Err: errDetail(proto.ErrNotFound, "sanction not found", false)}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtUserSanctionCleared, Seq: seq, Scopes: scopes,
		Payload: &proto.UserSanctionClearedPayload{User: target.Name, Kind: kind, Scope: scope, By: actor.Name, Reason: reason, TS: ts}, TS: ts})
	if scope != "global" {
		if err := h.ensureUndenyPostSystemPost(actor, target, scope, kind, reason, ts); err != nil {
			return internalErr(err)
		}
	}
	return Reply{Result: &proto.AckResult{ID: target.ID, Seq: seq}}
}

func (h *Handler) setContentFilter(actor *User, p proto.SetContentFilterPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	p = proto.NormalizeContentFilterPayload(p)
	filterID := p.ID
	if filterID == "" {
		filterID = newID("filter_")
	} else if msg := proto.ValidateContentFilterID(filterID); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	pattern := p.Pattern
	if msg := proto.ValidateContentFilterPattern(pattern); msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	scope := p.Scope
	active := true
	if p.Active != nil {
		active = *p.Active
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if scope != proto.DefaultContentFilterScope {
		if _, found, err := projections.BoardName(tx, scope); err != nil {
			return internalErr(err)
		} else if !found {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		}
	}

	ts := nowMS()
	scopes := []string{"moderation:global"}
	if scope != proto.DefaultContentFilterScope {
		scopes = append(scopes, "board:"+scope)
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtContentFilterSet, scopes, &proto.ContentFilterSetPayload{
		ID: filterID, Pattern: pattern, Scope: scope, Active: active, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().UpsertContentFilter(tx, filterID, pattern, scope, active, actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtContentFilterSet, Seq: seq, Scopes: scopes,
		Payload: &proto.ContentFilterSetPayload{ID: filterID, Pattern: pattern, Scope: scope, Active: active, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: filterID, Seq: seq}}
}

func (h *Handler) ensureDenyPostSystemPost(actor, target *User, boardID, kind, reason string, ts int64) error {
	return h.ensureSanctionSystemPost(proto.DenyPostSystemBoardID, proto.DenyPostSystemBoardID, proto.DenyPostSystemBoardDescription, actor, target, boardID, kind, reason, "Board posting denied", ts)
}

func (h *Handler) ensureUndenyPostSystemPost(actor, target *User, boardID, kind, reason string, ts int64) error {
	return h.ensureSanctionSystemPost(proto.UndenyPostSystemBoardID, proto.UndenyPostSystemBoardID, proto.UndenyPostSystemBoardDescription, actor, target, boardID, kind, reason, "Board posting restored", ts)
}

func (h *Handler) ensureSanctionSystemPost(systemBoardID, systemBoardName, systemBoardDescription string, actor, target *User, sourceBoardID, kind, reason, action string, ts int64) error {
	emit, err := currentRuntime().BoardAllowsPublicSystemPost(h.db, sourceBoardID)
	if err != nil {
		return err
	}
	if !emit {
		return nil
	}
	sourceBoardName, found, err := projections.BoardName(h.db, sourceBoardID)
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	if ts == 0 {
		ts = nowMS()
	}

	threadID := newID(systemBoardID + "_thr_")
	postID := newID(systemBoardID + "_pst_")
	title := fmt.Sprintf("%s: %s on %s", action, target.Name, sourceBoardID)
	body := proto.FormatSanctionSystemBody(action, target.Name, sourceBoardName, sourceBoardID, kind, actor.Name, reason)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	events, err := h.appendGeneratedSystemPostTx(tx, actor, generatedSystemPostSpec{
		BoardID:     systemBoardID,
		BoardName:   systemBoardName,
		Description: systemBoardDescription,
		ThreadID:    threadID,
		PostID:      postID,
		Title:       title,
		Body:        body,
	}, ts)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.publishGeneratedEvents(events)
	return nil
}

func (h *Handler) createBoard(actor *User, p proto.CreateBoardPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	p, msg := proto.NormalizeCreateBoardPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if p.ParentID != "" {
		found, err := projections.CategoryExists(tx, p.ParentID)
		if err != nil {
			return internalErr(err)
		}
		if !found {
			return Reply{Err: errDetail(proto.ErrNotFound, "parent category not found", false)}
		}
	}
	position, err := projections.CategoryPositionForCreate(tx, p.ID, p.ParentID, p.Position)
	if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + p.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, scopes, &proto.BoardCreatedPayload{
		ID: p.ID, Name: p.Name, Description: p.Description, ParentID: p.ParentID, Position: position, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertBoard(tx, p.ID, p.Name, p.Description, p.ParentID, position); err != nil {
		return Reply{Err: errDetail(proto.ErrConflict, "board already exists", false)}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: seq, Scopes: scopes,
		Payload: &proto.BoardCreatedPayload{ID: p.ID, Name: p.Name, Description: p.Description, ParentID: p.ParentID, Position: position, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: p.ID, Seq: seq}}
}

func (h *Handler) purgePost(actor *User, p proto.PurgePostPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	p, msg := proto.NormalizePurgePostPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}
	ts := nowMS()

	// Read before TX.
	post, err := currentRuntime().GetPost(h.db, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}

	thread, err := currentRuntime().GetThread(h.db, post.Thread)
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
	if err := currentRuntime().MarkPostPurged(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	// Remove from FTS permanently.
	if err := currentRuntime().FtsDeletePost(tx, post.ID); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostPurged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostPurgedPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}
