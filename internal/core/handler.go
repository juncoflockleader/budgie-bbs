package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// editWindowDur is how long an author may edit their own post without mod role.
const editWindowDur = 24 * time.Hour

// Reply is the result returned by the command handler.
type Reply struct {
	Result *proto.AckResult
	Err    *proto.ErrorDetail
}

// cmdEnvelope is the internal queue message for the single-writer goroutine.
type cmdEnvelope struct {
	actor   *User
	name    proto.CommandName
	payload json.RawMessage
	cid     string
	replyCh chan Reply
}

// Handler is the single-writer command handler.
// All state mutation flows through the Run goroutine.
type Handler struct {
	db    *sql.DB
	bus   Bus
	queue chan cmdEnvelope
}

func newHandler(db *sql.DB, bus Bus) *Handler {
	return &Handler{
		db:    db,
		bus:   bus,
		queue: make(chan cmdEnvelope, 256),
	}
}

// Run processes commands sequentially. Call in a dedicated goroutine.
func (h *Handler) Run(ctx context.Context) {
	for {
		select {
		case env := <-h.queue:
			reply := h.dispatch(env.actor, env.name, env.payload, env.cid)
			env.replyCh <- reply
		case <-ctx.Done():
			return
		}
	}
}

// Execute submits a command and blocks until it is processed.
func (h *Handler) Execute(ctx context.Context, actor *User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	replyCh := make(chan Reply, 1)
	env := cmdEnvelope{
		actor:   actor,
		name:    name,
		payload: payload,
		cid:     cid,
		replyCh: replyCh,
	}
	select {
	case h.queue <- env:
	case <-ctx.Done():
		return Reply{Err: errDetail(proto.ErrForbidden, "request cancelled", false)}
	}
	select {
	case reply := <-replyCh:
		return reply
	case <-ctx.Done():
		return Reply{Err: errDetail(proto.ErrForbidden, "request cancelled", false)}
	}
}

// --- Idempotency wrapper ---

func (h *Handler) dispatch(actor *User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	if cid != "" {
		if cached, ok := checkProcessed(h.db, cid); ok {
			var r proto.AckResult
			_ = json.Unmarshal([]byte(cached), &r)
			return Reply{Result: &r}
		}
	}

	reply := h.route(actor, name, payload)

	if cid != "" && reply.Err == nil && reply.Result != nil {
		raw, _ := json.Marshal(reply.Result)
		// Record inside its own tiny tx; non-fatal if it fails.
		tx, err := h.db.Begin()
		if err == nil {
			_ = recordProcessed(tx, cid, string(raw))
			_ = tx.Commit()
		}
	}
	return reply
}

func (h *Handler) route(actor *User, name proto.CommandName, payload json.RawMessage) Reply {
	switch name {
	case proto.CmdCreateThread:
		var p proto.CreateThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createThread(actor, p)

	case proto.CmdAppendPost:
		var p proto.AppendPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.appendPost(actor, p)

	case proto.CmdEditPost:
		var p proto.EditPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.editPost(actor, p)

	case proto.CmdRedactPost:
		var p proto.RedactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.redactPost(actor, p)

	case proto.CmdRestorePost:
		var p proto.RestorePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restorePost(actor, p)

	case proto.CmdLockThread:
		var p proto.LockThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.lockThread(actor, p)

	case proto.CmdMoveThread:
		var p proto.MoveThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.moveThread(actor, p)

	case proto.CmdGrantRole:
		var p proto.GrantRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.grantRole(actor, p)

	case proto.CmdRevokeRole:
		var p proto.RevokeRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.revokeRole(actor, p)

	case proto.CmdSendChatLine:
		var p proto.SendChatLinePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendChatLine(actor, p)

	case proto.CmdSetPresence:
		var p proto.SetPresencePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setPresence(actor, p)

	case proto.CmdSanctionUser:
		var p proto.SanctionUserPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sanctionUser(actor, p)

	case proto.CmdCreateBoard:
		var p proto.CreateBoardPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createBoard(actor, p)

	default:
		return Reply{Err: errDetail(proto.ErrValidationFailed, fmt.Sprintf("unknown command: %s", name), false)}
	}
}

// --- Command implementations ---

func (h *Handler) createThread(actor *User, p proto.CreateThreadPayload) Reply {
	if p.Board == "" || p.Title == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "board, title, and body are required", false)}
	}

	// Sanction check.
	if kind, ok := activeSanction(h.db, actor.ID, p.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return Reply{Err: errDetail(code, "you are "+kind+"d in this board", false)}
	}

	ct := contentType(p.ContentType)
	ts := nowMS()
	threadID := newID("thr_")
	postID := newID("pst_")

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	// Verify board exists.
	var boardName string
	if err := tx.QueryRow(`SELECT name FROM boards WHERE id=?`, p.Board).Scan(&boardName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + p.Board}

	// Append thread.new
	tseq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, scopes, &proto.ThreadNewPayload{
		ID: threadID, Board: p.Board, Author: actor.ID, Title: p.Title, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}

	threadScopes := append(scopes, "thread:"+threadID)

	// Append post.appended (the first post in the thread)
	pseq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, threadScopes, &proto.PostAppendedPayload{
		ID: postID, Thread: threadID, Author: actor.ID, Body: p.Body,
		ContentType: ct, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}

	// Update projections.
	if err := insertThread(tx, &Thread{
		ID: threadID, Board: p.Board, Author: actor.ID, Title: p.Title,
		LastSeq: tseq, CreatedTS: ts,
	}); err != nil {
		return internalErr(err)
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: threadID, Author: actor.ID,
		Body: p.Body, ContentType: ct, CreatedSeq: pseq,
	}); err != nil {
		return internalErr(err)
	}
	if err := bumpThread(tx, threadID, pseq); err != nil {
		return internalErr(err)
	}
	if err := ftsInsertPost(tx, postID, threadID, p.Board, actor.Name, p.Body); err != nil {
		return internalErr(err)
	}

	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	// Publish both events.
	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadNew, Seq: tseq, Scopes: scopes,
		Payload: &proto.ThreadNewPayload{ID: threadID, Board: p.Board, Author: actor.Name, Title: p.Title, TS: ts}, TS: ts})
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: pseq, Scopes: threadScopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: threadID, Author: actor.Name, Body: p.Body, ContentType: ct, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: threadID, Seq: pseq}}
}

func (h *Handler) appendPost(actor *User, p proto.AppendPostPayload) Reply {
	if p.Thread == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread and body are required", false)}
	}
	ct := contentType(p.ContentType)
	ts := nowMS()
	postID := newID("pst_")

	// All reads happen before the TX so we don't exhaust the single DB connection
	// (SetMaxOpenConns(1) means the TX holds the only connection).
	thread, err := getThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if thread.Locked && !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrThreadLocked, "thread is locked", false)}
	}

	// Sanction check.
	if kind, ok := activeSanction(h.db, actor.ID, thread.Board); ok {
		code := proto.ErrMuted
		if kind == "ban" {
			code = proto.ErrBanned
		}
		return Reply{Err: errDetail(code, "you are "+kind+"d in this board", false)}
	}

	// Threading depth cap: max 1 level. If replyTo is itself a reply, flatten.
	if p.ReplyTo != "" {
		parent, err := getPost(h.db, p.ReplyTo)
		if err != nil {
			return internalErr(err)
		}
		if parent == nil {
			return Reply{Err: errDetail(proto.ErrNotFound, "replyTo post not found", false)}
		}
		if parent.ReplyTo != "" {
			// Already depth-1 reply — flatten to grandparent.
			p.ReplyTo = parent.ReplyTo
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostAppended, scopes, &proto.PostAppendedPayload{
		ID: postID, Thread: p.Thread, Author: actor.ID, Body: p.Body,
		ContentType: ct, ReplyTo: p.ReplyTo, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := insertPost(tx, &Post{
		ID: postID, Thread: p.Thread, Author: actor.ID,
		Body: p.Body, ContentType: ct, ReplyTo: p.ReplyTo, CreatedSeq: seq,
	}); err != nil {
		return internalErr(err)
	}
	if err := bumpThread(tx, p.Thread, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsInsertPost(tx, postID, p.Thread, thread.Board, actor.Name, p.Body); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostAppended, Seq: seq, Scopes: scopes,
		Payload: &proto.PostAppendedPayload{ID: postID, Thread: p.Thread, Author: actor.Name, Body: p.Body, ContentType: ct, ReplyTo: p.ReplyTo, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: postID, Seq: seq}}
}

func (h *Handler) editPost(actor *User, p proto.EditPostPayload) Reply {
	if p.Post == "" || p.Body == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post and body are required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot edit a redacted post", false)}
	}

	isAuthor := post.Author == actor.ID
	withinWindow := time.Now().UnixMilli()-post.CreatedSeq < editWindowDur.Milliseconds()
	if !actor.IsMod() && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrEditWindowExpired, "edit window has expired", false)}
	}

	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostEdited, scopes, &proto.PostEditedPayload{
		ID: post.ID, Thread: post.Thread, NewBody: p.Body, Version: post.Version + 1, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := updatePostBody(tx, post.ID, p.Body, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsUpdatePost(tx, post.ID, p.Body); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostEdited, Seq: seq, Scopes: scopes,
		Payload: &proto.PostEditedPayload{ID: post.ID, Thread: post.Thread, NewBody: p.Body, Version: post.Version + 1, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) redactPost(actor *User, p proto.RedactPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is already redacted", false)}
	}

	isAuthor := post.Author == actor.ID
	withinWindow := time.Now().UnixMilli()-post.CreatedSeq < editWindowDur.Milliseconds()
	if !actor.IsMod() && !(isAuthor && withinWindow) {
		return Reply{Err: errDetail(proto.ErrForbidden, "insufficient permissions to redact this post", false)}
	}

	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRedacted, scopes, &proto.PostRedactedPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := markPostRedacted(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := ftsDeletePost(tx, post.ID); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostRedacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostRedactedPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) restorePost(actor *User, p proto.RestorePostPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	post, err := getPostTx(tx, p.Post)
	if err != nil {
		return internalErr(err)
	}
	if post == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "post not found", false)}
	}
	if !post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "post is not redacted", false)}
	}

	thread, err := getThreadTx(tx, post.Thread)
	if err != nil || thread == nil {
		return internalErr(err)
	}

	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostRestored, scopes, &proto.PostRestoredPayload{
		ID: post.ID, Thread: post.Thread, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := markPostRestored(tx, post.ID, seq); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostRestored, Seq: seq, Scopes: scopes,
		Payload: &proto.PostRestoredPayload{ID: post.ID, Thread: post.Thread, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) lockThread(actor *User, p proto.LockThreadPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := getThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}

	scopes := []string{"board:" + thread.Board, "thread:" + thread.ID}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadLocked, scopes, &proto.ThreadLockedPayload{
		Thread: thread.ID, Locked: p.Locked, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := setThreadLocked(tx, thread.ID, p.Locked); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadLocked, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadLockedPayload{Thread: thread.ID, Locked: p.Locked, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

func (h *Handler) moveThread(actor *User, p proto.MoveThreadPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	thread, err := getThreadTx(tx, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}

	var destName string
	if err := tx.QueryRow(`SELECT name FROM boards WHERE id=?`, p.ToBoard).Scan(&destName); err == sql.ErrNoRows {
		return Reply{Err: errDetail(proto.ErrNotFound, "destination board not found", false)}
	} else if err != nil {
		return internalErr(err)
	}

	scopes := []string{"board:" + thread.Board, "board:" + p.ToBoard}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtThreadMoved, scopes, &proto.ThreadMovedPayload{
		Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := moveThreadBoard(tx, thread.ID, p.ToBoard); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtThreadMoved, Seq: seq, Scopes: scopes,
		Payload: &proto.ThreadMovedPayload{Thread: thread.ID, FromBoard: thread.Board, ToBoard: p.ToBoard, By: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: thread.ID, Seq: seq}}
}

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
		Kind:   proto.EvtChatLine,
		Scopes: scopes,
		Payload: &proto.ChatLinePayload{ID: id, Room: p.Room, User: actor.Name, Text: p.Text, TS: ts},
		TS:     ts,
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
		if err := tx.QueryRow(`SELECT name FROM boards WHERE id=?`, scope).Scan(&boardName); err == sql.ErrNoRows {
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

// --- Helpers ---

func getThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := tx.QueryRow(
		`SELECT id, board, author, title, locked, post_count, last_seq, created_ts FROM threads WHERE id=?`, id,
	).Scan(&t.ID, &t.Board, &t.Author, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Locked = locked != 0
	return t, nil
}

func getPostTx(tx *sql.Tx, id string) (*Post, error) {
	p := &Post{}
	var redacted int
	err := tx.QueryRow(
		`SELECT id, thread, author, body, content_type, COALESCE(reply_to,''), version, redacted, created_seq, updated_seq FROM posts WHERE id=?`, id,
	).Scan(&p.ID, &p.Thread, &p.Author, &p.Body, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.CreatedSeq, &p.UpdatedSeq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Redacted = redacted != 0
	return p, nil
}

func getUserTx(tx *sql.Tx, id string) (*User, error) {
	u := &User{}
	err := tx.QueryRow(`SELECT id, name, role, password, created FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func contentType(ct string) string {
	if ct == "ansi-art" {
		return "ansi-art"
	}
	return "markup"
}

func errDetail(code, msg string, retryable bool) *proto.ErrorDetail {
	return &proto.ErrorDetail{Code: code, Message: msg, Retryable: retryable}
}

func badPayload() Reply {
	return Reply{Err: errDetail(proto.ErrValidationFailed, "invalid payload", false)}
}

func internalErr(err error) Reply {
	return Reply{Err: errDetail("internal_error", err.Error(), true)}
}

// isValidSlug returns true if s is a non-empty lowercase alphanumeric / hyphen / underscore
// string of at most 64 characters (suitable as a board ID).
func isValidSlug(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
