package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type Runtime struct {
	CheckProcessed func(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool)
	QQueryRow      func(queryable interface {
		QueryRow(query string, args ...any) *sql.Row
	}, query string, args ...any) *sql.Row
	ActiveSanction          func(db *sql.DB, userID, scope string) (string, bool)
	NowMS                   func() int64
	NewID                   func(prefix string) string
	AppendEvent             func(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error)
	GetThread               func(db *sql.DB, id string) (*Thread, error)
	GetPost                 func(db *sql.DB, id string) (*Post, error)
	GetUserTx               func(tx *sql.Tx, id string) (*User, error)
	GetThreadTx             func(tx *sql.Tx, id string) (*Thread, error)
	GetPostTx               func(tx *sql.Tx, id string) (*Post, error)
	GetPollWithVotes        func(db *sql.DB, pollID, viewerUserID string) (*Poll, error)
	InsertThread            func(tx *sql.Tx, t *Thread) error
	InsertPost              func(tx *sql.Tx, p *Post) error
	BumpThread              func(tx *sql.Tx, threadID string, seq int64) error
	InsertPoll              func(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error
	InsertPollOption        func(tx *sql.Tx, id, pollID, text string, position int) error
	UpsertReaction          func(tx *sql.Tx, postID, userID, emoji string, ts int64) error
	ReactionCountTx         func(tx *sql.Tx, postID string) (int, error)
	DeleteReaction          func(tx *sql.Tx, postID, userID string) error
	UserReacted             func(db *sql.DB, postID, userID string) (bool, error)
	MarkPostRedacted        func(tx *sql.Tx, postID string, seq int64) error
	MarkPostRestored        func(tx *sql.Tx, postID string, seq int64) error
	MarkPostPurged          func(tx *sql.Tx, postID string, seq int64) error
	SetThreadLocked         func(tx *sql.Tx, threadID string, locked bool) error
	MoveThreadBoard         func(tx *sql.Tx, threadID, toBoard string) error
	SetUserRole             func(tx *sql.Tx, userID, role string) error
	InsertBoard             func(tx *sql.Tx, id, name, description string) error
	InsertModerationReview  func(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error
	ResolveModerationReview func(tx *sql.Tx, id, actor, resolution string, ts int64) error
	CastVote                func(tx *sql.Tx, pollID, optionID, userID string, ts int64) error
	SetThreadPref           func(db *sql.DB, userID, threadID, level string) error
	FtsInsertPost           func(tx *sql.Tx, postID, threadID, boardID, author, body string) error
	FtsUpdatePost           func(tx *sql.Tx, postID, newBody string) error
	FtsDeletePost           func(tx *sql.Tx, postID string) error
	RecordProcessed         func(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error
	RecordReactionReceived  func(db *sql.DB, postAuthorID string) error
	RecordReactionRemoved   func(db *sql.DB, postAuthorID string) error
	UserTrustLevel          func(db *sql.DB, userID string) (int, error)
	UpdatePostBody          func(tx *sql.Tx, postID string, body string, seq int64) error
	InsertSanction          func(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error
	EnqueueOutboxJob        func(tx *sql.Tx, kind string, payload any, ts int64) error
}

type Bus interface {
	Publish(evt *proto.Event)
}

type User = projections.User
type Thread = projections.Thread
type Post = projections.Post
type Poll = projections.Poll

const outboxPostCommitted = "post.committed"

var (
	configuredRuntime Runtime
)

func SetRuntime(rt Runtime) {
	configuredRuntime = rt
}

// Ensure callers do not panic on accidental use before runtime initialization.
func currentRuntime() Runtime {
	if configuredRuntime.CheckProcessed == nil {
		panic("handler runtime not configured")
	}
	return configuredRuntime
}

type postCommittedJob struct {
	ActorID   string `json:"actorId"`
	ActorName string `json:"actorName"`
	PostID    string `json:"postId"`
	ThreadID  string `json:"threadId"`
	BoardID   string `json:"boardId"`
	Body      string `json:"body"`
	ReplyTo   string `json:"replyTo,omitempty"`
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
}

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

func New(db *sql.DB, bus Bus) *Handler {
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
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	commandHash := hashCommand(name, payload)
	if cid != "" {
		if cached, ok, conflict := checkProcessed(h.db, actorID, cid, commandHash); conflict {
			return Reply{Err: errDetail(proto.ErrConflict, "command id was already used with a different payload", false)}
		} else if ok {
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
			_ = recordProcessed(tx, actorID, cid, commandHash, string(raw))
			_ = tx.Commit()
		}
	}
	return reply
}

func hashCommand(name proto.CommandName, payload json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(name+"\x00"), payload...))
	return hex.EncodeToString(sum[:])
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

	case proto.CmdPurgePost:
		var p proto.PurgePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.purgePost(actor, p)

	case proto.CmdReactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.reactPost(actor, p)

	case proto.CmdUnreactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.unreactPost(actor, p)

	case proto.CmdVotePoll:
		var p proto.VotePollPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.votePoll(actor, p)

	case proto.CmdSetThreadPref:
		var p proto.SetThreadPrefPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setThreadPref(actor, p)

	case proto.CmdFlagPost:
		var p proto.FlagPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.flagPost(actor, p)

	case proto.CmdResolveReview:
		var p proto.ResolveReviewPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.resolveReview(actor, p)

	default:
		return Reply{Err: errDetail(proto.ErrValidationFailed, fmt.Sprintf("unknown command: %s", name), false)}
	}
}

// --- Command implementations ---

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

// ── M10: Reactions ──────────────────────────────────────────────────────────

func (h *Handler) reactPost(actor *User, p proto.ReactPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	emoji := p.Emoji
	if emoji == "" {
		emoji = "heart"
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
	if post.Redacted {
		return Reply{Err: errDetail(proto.ErrConflict, "cannot react to a redacted post", false)}
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

	if err := upsertReaction(tx, post.ID, actor.ID, emoji, ts); err != nil {
		return internalErr(err)
	}
	count, err := reactionCountTx(tx, post.ID)
	if err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostReacted, scopes, &proto.PostReactedPayload{
		PostID: post.ID, Thread: post.Thread, User: actor.ID, Emoji: emoji, ReactionCount: count, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	// Update activity for post author (best-effort).
	if postAuthorID != actor.ID {
		go recordReactionReceived(h.db, postAuthorID) //nolint
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostReacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostReactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

func (h *Handler) unreactPost(actor *User, p proto.ReactPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

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

	// Check user actually reacted.
	reacted, err := userReacted(h.db, post.ID, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if !reacted {
		return Reply{Err: errDetail(proto.ErrConflict, "you have not reacted to this post", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if err := deleteReaction(tx, post.ID, actor.ID); err != nil {
		return internalErr(err)
	}
	count, err := reactionCountTx(tx, post.ID)
	if err != nil {
		return internalErr(err)
	}
	emoji := p.Emoji
	if emoji == "" {
		emoji = "heart"
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostUnreacted, scopes, &proto.PostUnreactedPayload{
		PostID: post.ID, Thread: post.Thread, User: actor.ID, Emoji: emoji, ReactionCount: count, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	postAuthorID := post.AuthorID
	if postAuthorID == "" {
		postAuthorID = post.Author
	}
	if postAuthorID != actor.ID {
		go recordReactionRemoved(h.db, postAuthorID) //nolint
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPostUnreacted, Seq: seq, Scopes: scopes,
		Payload: &proto.PostUnreactedPayload{PostID: post.ID, Thread: post.Thread, User: actor.Name, Emoji: emoji, ReactionCount: count, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: post.ID, Seq: seq}}
}

// ── M11: Polls ──────────────────────────────────────────────────────────────

func (h *Handler) votePoll(actor *User, p proto.VotePollPayload) Reply {
	if p.Poll == "" || p.Option == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "poll and option are required", false)}
	}
	ts := nowMS()

	// Verify poll + option exist (reads before TX).
	poll, err := getPollWithVotes(h.db, p.Poll, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if poll == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "poll not found", false)}
	}
	optionValid := false
	for _, opt := range poll.Options {
		if opt.ID == p.Option {
			optionValid = true
			break
		}
	}
	if !optionValid {
		return Reply{Err: errDetail(proto.ErrNotFound, "option not found", false)}
	}
	if poll.ExpiresAt > 0 && ts > poll.ExpiresAt {
		return Reply{Err: errDetail(proto.ErrConflict, "poll has expired", false)}
	}

	// Look up the post for scoping.
	post, err := getPost(h.db, poll.PostID)
	if err != nil || post == nil {
		return internalErr(err)
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

	if err := castVote(tx, p.Poll, p.Option, actor.ID, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPollVoted, scopes, &proto.PollVotedPayload{
		Poll: p.Poll, Option: p.Option, User: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtPollVoted, Seq: seq, Scopes: scopes,
		Payload: &proto.PollVotedPayload{Poll: p.Poll, Option: p.Option, User: actor.Name, TS: ts}, TS: ts})

	return Reply{Result: &proto.AckResult{ID: p.Poll, Seq: seq}}
}

// ── M8: Thread prefs ────────────────────────────────────────────────────────

func (h *Handler) setThreadPref(actor *User, p proto.SetThreadPrefPayload) Reply {
	if p.Thread == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "thread is required", false)}
	}
	if p.Level != "watch" && p.Level != "normal" && p.Level != "mute" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, `level must be "watch", "normal", or "mute"`, false)}
	}
	// Verify thread exists.
	thread, err := getThread(h.db, p.Thread)
	if err != nil {
		return internalErr(err)
	}
	if thread == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "thread not found", false)}
	}
	if err := setThreadPref(h.db, actor.ID, p.Thread, p.Level); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: p.Thread}}
}

// ── Modern moderation review queue ──────────────────────────────────────────

func (h *Handler) flagPost(actor *User, p proto.FlagPostPayload) Reply {
	if p.Post == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "post is required", false)}
	}
	ts := nowMS()

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

	reviewID := newID("rev_")
	if err := insertModerationReview(tx, reviewID, "post_flag", post.ID, "post", actor.ID, p.Reason, ts); err != nil {
		return internalErr(err)
	}
	scopes := []string{"thread:" + post.Thread, "board:" + thread.Board, "moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtPostFlagged, scopes, &proto.PostFlaggedPayload{
		ReviewID: reviewID, PostID: post.ID, Thread: post.Thread, Reporter: actor.ID, Reason: p.Reason, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtPostFlagged, Seq: seq, Scopes: scopes,
		Payload: &proto.PostFlaggedPayload{ReviewID: reviewID, PostID: post.ID, Thread: post.Thread, Reporter: actor.Name, Reason: p.Reason, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: reviewID, Seq: seq}}
}

func (h *Handler) resolveReview(actor *User, p proto.ResolveReviewPayload) Reply {
	if !actor.IsMod() {
		return Reply{Err: errDetail(proto.ErrForbidden, "moderator role required", false)}
	}
	if p.Review == "" || p.Resolution == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "review and resolution are required", false)}
	}
	ts := nowMS()

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback() //nolint

	if err := resolveModerationReview(tx, p.Review, actor.ID, p.Resolution, ts); err != nil {
		if err == sql.ErrNoRows {
			return Reply{Err: errDetail(proto.ErrNotFound, "review not found", false)}
		}
		return internalErr(err)
	}
	scopes := []string{"moderation:global"}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtReviewResolved, scopes, &proto.ReviewResolvedPayload{
		ReviewID: p.Review, Resolution: p.Resolution, By: actor.ID, TS: ts,
	})
	if err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}
	h.bus.Publish(&proto.Event{Kind: proto.EvtReviewResolved, Seq: seq, Scopes: scopes,
		Payload: &proto.ReviewResolvedPayload{ReviewID: p.Review, Resolution: p.Resolution, By: actor.Name, TS: ts}, TS: ts})
	return Reply{Result: &proto.AckResult{ID: p.Review, Seq: seq}}
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

// requireMinTrustForPoll blocks poll creation for actors below the requested
// trust level. Mod/admin actors bypass this gate.
func (h *Handler) requireMinTrustForPoll(actor *User, minLevel int, action string) Reply {
	if actor.IsMod() {
		return Reply{}
	}
	trustLevel, err := userTrustLevel(h.db, actor.ID)
	if err != nil {
		return internalErr(err)
	}
	if trustLevel < minLevel {
		return Reply{Err: errDetail(proto.ErrForbidden, action+" with poll requires trust level "+strconv.Itoa(minLevel), false)}
	}
	return Reply{}
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

func nowMS() int64 {
	return currentRuntime().NowMS()
}

func newID(prefix string) string {
	return currentRuntime().NewID(prefix)
}

func checkProcessed(db *sql.DB, actorID, cid, commandHash string) (string, bool, bool) {
	return currentRuntime().CheckProcessed(db, actorID, cid, commandHash)
}

func qQueryRow(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, query string, args ...any) *sql.Row {
	return currentRuntime().QQueryRow(queryable, query, args...)
}

func activeSanction(db *sql.DB, userID, scope string) (string, bool) {
	return currentRuntime().ActiveSanction(db, userID, scope)
}

func appendEvent(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error) {
	return currentRuntime().AppendEvent(tx, id, kind, scopes, payload)
}

func getThread(db *sql.DB, id string) (*Thread, error) {
	return currentRuntime().GetThread(db, id)
}

func getPost(db *sql.DB, id string) (*Post, error) {
	return currentRuntime().GetPost(db, id)
}

func getUserTx(tx *sql.Tx, id string) (*User, error) {
	return currentRuntime().GetUserTx(tx, id)
}

// GetUserTx exposes user lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetUserTx(tx *sql.Tx, id string) (*User, error) {
	return getUserTx(tx, id)
}

func getThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	return currentRuntime().GetThreadTx(tx, id)
}

// GetThreadTx exposes thread lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	return getThreadTx(tx, id)
}

func getPostTx(tx *sql.Tx, id string) (*Post, error) {
	return currentRuntime().GetPostTx(tx, id)
}

// GetPostTx exposes post lookup via a transaction for callers outside
// this package that still need command-time projection reads.
func GetPostTx(tx *sql.Tx, id string) (*Post, error) {
	return getPostTx(tx, id)
}

func getPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	return currentRuntime().GetPollWithVotes(db, pollID, viewerUserID)
}

func insertThread(tx *sql.Tx, t *Thread) error {
	return currentRuntime().InsertThread(tx, t)
}

func insertPost(tx *sql.Tx, p *Post) error {
	return currentRuntime().InsertPost(tx, p)
}

func bumpThread(tx *sql.Tx, threadID string, seq int64) error {
	return currentRuntime().BumpThread(tx, threadID, seq)
}

func ftsInsertPost(tx *sql.Tx, postID, threadID, boardID, author, body string) error {
	return currentRuntime().FtsInsertPost(tx, postID, threadID, boardID, author, body)
}

func ftsUpdatePost(tx *sql.Tx, postID, newBody string) error {
	return currentRuntime().FtsUpdatePost(tx, postID, newBody)
}

func ftsDeletePost(tx *sql.Tx, postID string) error {
	return currentRuntime().FtsDeletePost(tx, postID)
}

func insertPoll(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error {
	return currentRuntime().InsertPoll(tx, id, postID, question, expiresAt, ts)
}

func insertPollOption(tx *sql.Tx, id, pollID, text string, position int) error {
	return currentRuntime().InsertPollOption(tx, id, pollID, text, position)
}

func enqueueOutboxJob(tx *sql.Tx, kind string, payload any, ts int64) error {
	return currentRuntime().EnqueueOutboxJob(tx, kind, payload, ts)
}

func upsertReaction(tx *sql.Tx, postID, userID, emoji string, ts int64) error {
	return currentRuntime().UpsertReaction(tx, postID, userID, emoji, ts)
}

func reactionCountTx(tx *sql.Tx, postID string) (int, error) {
	return currentRuntime().ReactionCountTx(tx, postID)
}

func userReacted(db *sql.DB, postID, userID string) (bool, error) {
	return currentRuntime().UserReacted(db, postID, userID)
}

func deleteReaction(tx *sql.Tx, postID, userID string) error {
	return currentRuntime().DeleteReaction(tx, postID, userID)
}

func castVote(tx *sql.Tx, pollID, optionID, userID string, ts int64) error {
	return currentRuntime().CastVote(tx, pollID, optionID, userID, ts)
}

func markPostRedacted(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostRedacted(tx, postID, seq)
}

func markPostRestored(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostRestored(tx, postID, seq)
}

func markPostPurged(tx *sql.Tx, postID string, seq int64) error {
	return currentRuntime().MarkPostPurged(tx, postID, seq)
}

func setThreadLocked(tx *sql.Tx, threadID string, locked bool) error {
	return currentRuntime().SetThreadLocked(tx, threadID, locked)
}

func moveThreadBoard(tx *sql.Tx, threadID, toBoard string) error {
	return currentRuntime().MoveThreadBoard(tx, threadID, toBoard)
}

func setUserRole(tx *sql.Tx, userID, role string) error {
	return currentRuntime().SetUserRole(tx, userID, role)
}

func insertBoard(tx *sql.Tx, id, name, description string) error {
	return currentRuntime().InsertBoard(tx, id, name, description)
}

func insertModerationReview(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error {
	return currentRuntime().InsertModerationReview(tx, id, kind, targetID, targetKind, reporter, reason, ts)
}

func resolveModerationReview(tx *sql.Tx, id, actor, resolution string, ts int64) error {
	return currentRuntime().ResolveModerationReview(tx, id, actor, resolution, ts)
}

func setThreadPref(db *sql.DB, userID, threadID, level string) error {
	return currentRuntime().SetThreadPref(db, userID, threadID, level)
}

func recordProcessed(tx *sql.Tx, actorID, cid, commandHash, resultJSON string) error {
	return currentRuntime().RecordProcessed(tx, actorID, cid, commandHash, resultJSON)
}

func recordReactionReceived(db *sql.DB, postAuthorID string) error {
	return currentRuntime().RecordReactionReceived(db, postAuthorID)
}

func recordReactionRemoved(db *sql.DB, postAuthorID string) error {
	return currentRuntime().RecordReactionRemoved(db, postAuthorID)
}

func userTrustLevel(db *sql.DB, userID string) (int, error) {
	return currentRuntime().UserTrustLevel(db, userID)
}

func updatePostBody(tx *sql.Tx, postID string, body string, seq int64) error {
	return currentRuntime().UpdatePostBody(tx, postID, body, seq)
}

func insertSanction(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error {
	return currentRuntime().InsertSanction(tx, id, userID, kind, scope, expiresAt, by, reason, seq)
}
