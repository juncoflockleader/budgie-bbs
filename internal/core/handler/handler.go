package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

func hashCommand(name proto.CommandName, payload json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(name+"\x00"), payload...))
	return hex.EncodeToString(sum[:])
}
