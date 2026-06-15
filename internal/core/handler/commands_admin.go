package handler

import (
	"database/sql"
	"fmt"
	"strings"

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

	if err := h.ensureSyssecuritySystemPost(actor, "Role granted: "+target.Name, []string{
		"Action: role granted",
		"User: " + target.Name,
		"Role: " + p.Role,
		"Actor: " + actor.Name,
	}, ""); err != nil {
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

	if err := h.ensureSyssecuritySystemPost(actor, "Role revoked: "+target.Name, []string{
		"Action: role revoked",
		"User: " + target.Name,
		"Role: " + p.Role,
		"Actor: " + actor.Name,
	}, ""); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtRoleRevoked, Seq: seq, Scopes: scopes,
		Payload: &proto.RoleRevokedPayload{User: target.Name, Role: p.Role, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: target.ID, Seq: seq}}
}

func (h *Handler) sendChatLine(actor *User, p proto.SendChatLinePayload) Reply {
	roomID := normalizeChatRoomID(p.Room)
	text := strings.TrimSpace(p.Text)
	if roomID == "" || text == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "room and text are required", false)}
	}
	if !validChatRoomID(roomID) {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "chat room must use letters, numbers, underscore, or hyphen", false)}
	}
	if len([]rune(text)) > 1000 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "chat text is too long", false)}
	}
	ts := nowMS()
	id := newID("chat_")
	roomName := formatChatRoomName(roomID)
	if err := chatStore().InsertChatLine(id, roomID, roomName, actor.ID, actor.Name, text, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"chat:" + roomID}

	h.bus.Publish(&proto.Event{
		Kind:    proto.EvtChatLine,
		Scopes:  scopes,
		Payload: &proto.ChatLinePayload{ID: id, Room: roomID, User: actor.Name, Text: text, TS: ts},
		TS:      ts,
	})
	// Notify sibling nodes in Postgres multi-node deployments.
	pgNotifyEphemeral(h.db, string(proto.EvtChatLine), id, "chat:"+roomID)

	return Reply{Result: &proto.AckResult{ID: id}}
}

func normalizeChatRoomID(room string) string {
	room = strings.ToLower(strings.TrimSpace(room))
	if room == "" {
		return "lobby"
	}
	return room
}

func validChatRoomID(room string) bool {
	if len(room) > 40 {
		return false
	}
	for _, ch := range room {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func formatChatRoomName(roomID string) string {
	if roomID == "lobby" {
		return "Lobby"
	}
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(roomID))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return roomID
	}
	return strings.Join(parts, " ")
}

func (h *Handler) setPresence(actor *User, p proto.SetPresencePayload) Reply {
	status := strings.TrimSpace(p.Status)
	if status == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "status is required", false)}
	}
	if cloakedPresenceStatus(status) {
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
		derivedMode, derivedBoard, derivedThread := derivePresenceHints(status)
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
	if hiddenPresenceStatus(status) {
		mode = ""
		boardID = ""
		threadID = ""
		location = ""
	}
	if errReply := validatePresenceText(status, sessionID, mode, boardID, threadID, location, fromHost); errReply.Err != nil {
		return errReply
	}

	if threadID != "" {
		thread, err := getThread(h.db, threadID)
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
		settings, err := getBoardSettings(h.db, boardID)
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
	persistPresence := !typingPresenceStatus(status)
	if persistPresence {
		if err := setUserPresence(h.db, actor.ID, sessionID, status, mode, boardID, threadID, location, fromHost, ts); err != nil {
			return internalErr(err)
		}
		if visiblePresenceStatus(status) {
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

func derivePresenceHints(status string) (mode, boardID, threadID string) {
	parts := strings.Split(status, ":")
	if len(parts) < 2 {
		return "", "", ""
	}
	mode = strings.TrimSpace(parts[0])
	switch strings.TrimSpace(parts[1]) {
	case "board":
		if len(parts) >= 3 {
			boardID = strings.TrimSpace(parts[2])
		}
	case "thread":
		if len(parts) >= 3 {
			threadID = strings.TrimSpace(parts[2])
		}
	default:
		boardID = strings.TrimSpace(parts[1])
	}
	return mode, boardID, threadID
}

func validatePresenceText(status, sessionID, mode, boardID, threadID, location, fromHost string) Reply {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"status", status, 120},
		{"sessionId", sessionID, 80},
		{"mode", mode, 40},
		{"board", boardID, 80},
		{"thread", threadID, 120},
		{"location", location, 160},
		{"fromHost", fromHost, 160},
	} {
		if len(field.value) > field.limit {
			return Reply{Err: errDetail(proto.ErrValidationFailed, field.name+" is too long", false)}
		}
	}
	return Reply{}
}

func (h *Handler) sanctionUser(actor *User, p proto.SanctionUserPayload) Reply {
	p.Reason = strings.TrimSpace(p.Reason)
	if len(p.Reason) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "reason must be 500 characters or less", false)}
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

	// Authorize by scope: a global sanction requires the site moderator role;
	// a board sanction is also available to that board's moderators.
	if scope == "global" {
		if !actor.IsMod() {
			return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
		}
	} else {
		var boardName string
		if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		} else if err != nil {
			return internalErr(err)
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
	if err := insertSanction(tx, sanctionID, target.ID, p.Kind, scope, expiresAt, actor.ID, p.Reason, seq); err != nil {
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
	userRef := strings.TrimSpace(p.User)
	if userRef == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "user is required", false)}
	}
	kind := strings.TrimSpace(p.Kind)
	if kind != "" && kind != "mute" && kind != "ban" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, `kind must be "mute", "ban", or empty`, false)}
	}
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = "global"
	}
	reason := strings.TrimSpace(p.Reason)
	if len(reason) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "reason must be 500 characters or less", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	target, err := findUserRefTx(tx, userRef)
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
		var boardName string
		if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		} else if err != nil {
			return internalErr(err)
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
	removed, err := clearUserSanctions(tx, target.ID, kind, scope)
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
	filterID := strings.TrimSpace(p.ID)
	if filterID == "" {
		filterID = newID("filter_")
	} else if !isValidSlug(filterID) {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "filter id must be lowercase alphanumeric, hyphens, or underscores", false)}
	}
	pattern := strings.TrimSpace(p.Pattern)
	if pattern == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "pattern is required", false)}
	}
	if len(pattern) > 120 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "pattern must be 120 characters or less", false)}
	}
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = "global"
	}
	active := true
	if p.Active != nil {
		active = *p.Active
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if scope != "global" {
		var boardName string
		if err := qQueryRow(tx, `SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "board not found for scope", false)}
		} else if err != nil {
			return internalErr(err)
		}
	}

	ts := nowMS()
	scopes := []string{"moderation:global"}
	if scope != "global" {
		scopes = append(scopes, "board:"+scope)
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtContentFilterSet, scopes, &proto.ContentFilterSetPayload{
		ID: filterID, Pattern: pattern, Scope: scope, Active: active, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := upsertContentFilter(tx, filterID, pattern, scope, active, actor.ID, ts); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtContentFilterSet, Seq: seq, Scopes: scopes,
		Payload: &proto.ContentFilterSetPayload{ID: filterID, Pattern: pattern, Scope: scope, Active: active, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: filterID, Seq: seq}}
}

const denyPostSystemBoardID = "denypost"
const undenyPostSystemBoardID = "undenypost"

func (h *Handler) ensureDenyPostSystemPost(actor, target *User, boardID, kind, reason string, ts int64) error {
	return h.ensureSanctionSystemPost(denyPostSystemBoardID, "denypost", "Generated board posting deny records", actor, target, boardID, kind, reason, "Board posting denied", ts)
}

func (h *Handler) ensureUndenyPostSystemPost(actor, target *User, boardID, kind, reason string, ts int64) error {
	return h.ensureSanctionSystemPost(undenyPostSystemBoardID, "undenypost", "Generated board posting restore records", actor, target, boardID, kind, reason, "Board posting restored", ts)
}

func (h *Handler) ensureSanctionSystemPost(systemBoardID, systemBoardName, systemBoardDescription string, actor, target *User, sourceBoardID, kind, reason, action string, ts int64) error {
	settings, err := getBoardSettings(h.db, sourceBoardID)
	if err != nil {
		return err
	}
	if settings != nil && settings.MemberReadMode {
		return nil
	}
	var sourceBoardName string
	if err := qQueryRow(h.db, `SELECT name FROM boards WHERE id=?`, sourceBoardID).Scan(&sourceBoardName); err != nil {
		return err
	}
	if ts == 0 {
		ts = nowMS()
	}

	threadID := newID(systemBoardID + "_thr_")
	postID := newID(systemBoardID + "_pst_")
	title := fmt.Sprintf("%s: %s on %s", action, target.Name, sourceBoardID)
	body := formatSanctionSystemBody(action, target.Name, sourceBoardName, sourceBoardID, kind, actor.Name, reason)

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	boardCreated := false
	var boardSeq int64
	var exists int
	err = qQueryRow(tx, `SELECT 1 FROM boards WHERE id=?`, systemBoardID).Scan(&exists)
	if err == sql.ErrNoRows {
		position, err := boardCategoryPosition(tx, "", nil)
		if err != nil {
			return err
		}
		boardScopes := []string{"board:" + systemBoardID}
		boardSeq, err = appendEvent(tx, newID("evt_"), proto.EvtBoardCreated, boardScopes, &proto.BoardCreatedPayload{
			ID:          systemBoardID,
			Name:        systemBoardName,
			Description: systemBoardDescription,
			Position:    position,
			By:          actor.ID,
			TS:          ts,
		})
		if err != nil {
			return err
		}
		if err := insertBoard(tx, systemBoardID, systemBoardName, systemBoardDescription, "", position); err != nil {
			return err
		}
		boardCreated = true
	} else if err != nil {
		return err
	}

	scopes := []string{"board:" + systemBoardID}
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: systemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts,
	})
	if err != nil {
		return err
	}
	threadScopes := append(scopes, "thread:"+threadID)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts,
	})
	if err != nil {
		return err
	}
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: systemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title,
		LastSeq: tseq, CreatedTS: ts, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID,
		Body: body, ContentType: "markup", CreatedSeq: pseq, CreatedAt: ts, UpdatedAt: ts,
	}); err != nil {
		return err
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return err
	}
	if err := ftsInsertPost(tx, postID, threadID, systemBoardID, actor.Name, body); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if boardCreated {
		h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: boardSeq, Scopes: []string{"board:" + systemBoardID},
			Payload: &proto.BoardCreatedPayload{ID: systemBoardID, Name: systemBoardName, Description: systemBoardDescription, By: actor.Name, TS: ts}, TS: ts})
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: systemBoardID, Author: actor.Name, AuthorID: actor.ID, Title: title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, AuthorID: actor.ID, Body: body, RawBody: body, ContentType: "markup", TS: ts}, TS: ts})
	return nil
}

func formatSanctionSystemBody(action, targetName, sourceBoardName, sourceBoardID, kind, actorName, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", action)
	fmt.Fprintf(&b, "- Action: %s\n", strings.ToLower(action))
	fmt.Fprintf(&b, "- User: %s\n", targetName)
	fmt.Fprintf(&b, "- Board: %s (%s)\n", sourceBoardName, sourceBoardID)
	if strings.TrimSpace(kind) == "" {
		fmt.Fprintf(&b, "- Kind: all\n")
	} else {
		fmt.Fprintf(&b, "- Kind: %s\n", strings.TrimSpace(kind))
	}
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&b, "- Reason: %s\n", strings.TrimSpace(reason))
	}
	b.WriteString("\nGenerated public board-posting sanction record. Private moderation notes and article bodies are not mirrored.\n")
	return b.String()
}

func (h *Handler) createBoard(actor *User, p proto.CreateBoardPayload) Reply {
	if !actor.IsAdmin() {
		return Reply{Err: errDetail(proto.ErrForbidden, "admin role required", false)}
	}
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.ParentID = strings.TrimSpace(p.ParentID)
	if p.ID == "" || p.Name == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "id and name are required", false)}
	}
	if !isValidSlug(p.ID) {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "id must be lowercase alphanumeric, hyphens, or underscores (max 64 chars)", false)}
	}
	if p.ParentID == p.ID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board cannot be its own parent", false)}
	}
	if p.Position != nil && *p.Position < 0 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "position cannot be negative", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if p.ParentID != "" {
		found, err := categoryExistsTx(tx, p.ParentID)
		if err != nil {
			return internalErr(err)
		}
		if !found {
			return Reply{Err: errDetail(proto.ErrNotFound, "parent category not found", false)}
		}
	}
	position, err := boardCategoryPosition(tx, p.ParentID, p.Position)
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
	if err := insertBoard(tx, p.ID, p.Name, p.Description, p.ParentID, position); err != nil {
		return Reply{Err: errDetail(proto.ErrConflict, "board already exists", false)}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtBoardCreated, Seq: seq, Scopes: scopes,
		Payload: &proto.BoardCreatedPayload{ID: p.ID, Name: p.Name, Description: p.Description, ParentID: p.ParentID, Position: position, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: p.ID, Seq: seq}}
}

func boardCategoryPosition(tx *sql.Tx, parentID string, requested *int) (int, error) {
	if requested != nil {
		return *requested, nil
	}
	var next int
	err := qQueryRow(tx, `SELECT COALESCE(MAX(position) + 1, 0) FROM categories WHERE parent_id=?`, parentID).Scan(&next)
	return next, err
}

func categoryExistsTx(tx *sql.Tx, categoryID string) (bool, error) {
	var found int
	err := qQueryRow(tx, `SELECT 1 FROM categories WHERE id=?`, categoryID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
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
