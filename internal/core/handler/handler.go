package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/chatstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/presencestore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type Runtime struct {
	CheckProcessed func(db *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool)
	QQueryRow      func(queryable interface {
		QueryRow(query string, args ...any) *sql.Row
	}, query string, args ...any) *sql.Row
	ActiveSanction               func(db *sql.DB, userID, scope string) (string, bool)
	MatchContentFilter           func(db *sql.DB, boardID, text string) (*projections.ContentFilter, error)
	NowMS                        func() int64
	NewID                        func(prefix string) string
	AppendEvent                  func(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error)
	GetThread                    func(db *sql.DB, id string) (*projections.Thread, error)
	GetPost                      func(db *sql.DB, id string) (*projections.Post, error)
	GetMail                      func(db *sql.DB, userID, messageID string) (*projections.MailItem, error)
	GetUserTx                    func(tx *sql.Tx, id string) (*projections.User, error)
	GetThreadTx                  func(tx *sql.Tx, id string) (*projections.Thread, error)
	GetPostTx                    func(tx *sql.Tx, id string) (*projections.Post, error)
	GetPollWithVotes             func(db *sql.DB, pollID, viewerUserID string) (*projections.Poll, error)
	InsertThread                 func(tx *sql.Tx, t *projections.Thread) error
	InsertPost                   func(tx *sql.Tx, p *projections.Post) error
	BumpThread                   func(tx *sql.Tx, threadID string, seq int64) error
	InsertPoll                   func(tx *sql.Tx, id, postID, question string, expiresAt, ts int64) error
	InsertPollOption             func(tx *sql.Tx, id, pollID, text string, position int) error
	InsertPostAttachment         func(tx *sql.Tx, id, postID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error
	InsertMailAttachment         func(tx *sql.Tx, id, mailID, filename, contentType string, sizeBytes int64, url, createdBy string, createdAt int64) error
	PromoteStagedPostBlob        func(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error
	PromoteStagedMailBlob        func(tx *sql.Tx, stagingID, attachmentID string, expectedSize int64, contentType string) error
	InsertRelayDelivery          func(tx *sql.Tx, id, boardID, threadID, postID, authorID, authorName, title, body string, createdAt, seq int64) error
	CounterStore                 counterstore.Store
	PresenceStore                presencestore.Store
	ChatStore                    chatstore.Store
	MarkPostRedacted             func(tx *sql.Tx, postID string, seq int64) error
	MarkPostRestored             func(tx *sql.Tx, postID string, seq int64) error
	RecordPostDeletion           func(tx *sql.Tx, postID, threadID, boardID, deletedByID, deletedByName, reason, kind string, deletedAt, seq int64) error
	ClearPostDeletion            func(tx *sql.Tx, postID string) error
	MarkPostPurged               func(tx *sql.Tx, postID string, seq int64) error
	SetPostFlags                 func(tx *sql.Tx, postID string, marked, recommended, noReply, tex, mailBack bool, seq int64) error
	SetThreadLocked              func(tx *sql.Tx, threadID string, locked bool) error
	SetThreadTitle               func(tx *sql.Tx, threadID, title string, ts int64) error
	MoveThreadBoard              func(tx *sql.Tx, threadID, toBoard string) error
	SetUserRole                  func(tx *sql.Tx, userID, role string) error
	InsertBoard                  func(tx *sql.Tx, id, name, description, parentID string, position int) error
	GetDigestExport              func(db *sql.DB, entryID string) (*projections.DigestExport, error)
	InsertMailMessage            func(tx *sql.Tx, id, fromUserID, subject, body, parentID string, createdAt, seq int64) error
	InsertMailCopy               func(tx *sql.Tx, messageID, userID, role, mailbox string, read, kept bool, updatedAt int64) error
	InsertNotification           func(db *sql.DB, id, userID, kind, threadID, postID, actor string, ts int64) error
	InsertNotificationTx         func(tx *sql.Tx, id, userID, kind, threadID, postID, actor string, ts int64) error
	UpdateMailCopy               func(db *sql.DB, userID, messageID string, mailbox *string, read, kept *bool) (bool, error)
	TrashMailCopy                func(db *sql.DB, userID, messageID string) (bool, error)
	SetMailGroup                 func(db *sql.DB, ownerID, groupID, name string, memberIDs []string) error
	DeleteMailGroup              func(db *sql.DB, ownerID, groupID string) (bool, error)
	GetMailGroupID               func(db *sql.DB, ownerID, groupRef string) (string, error)
	ListMailGroupMembers         func(db *sql.DB, ownerID, groupRef string) ([]projections.MailGroupMember, error)
	ListFriendUserIDs            func(db *sql.DB, ownerID string) ([]string, error)
	ListLoginWatchers            func(db *sql.DB, targetUserID string) ([]string, error)
	InsertDirectMessage          func(tx *sql.Tx, id, conversationID, fromUserID, toUserID, body string, createdAt, seq int64) error
	InsertBlessing               func(tx *sql.Tx, blessing *projections.Blessing) error
	MarkDirectMessageRead        func(db *sql.DB, userID, messageID string) (bool, error)
	DeleteDirectMessage          func(db *sql.DB, userID, messageID string) (bool, error)
	GetDirectMessageSettings     func(db *sql.DB, userID string) (*projections.DirectMessageSettings, error)
	SetDirectMessageSettings     func(db *sql.DB, userID, policy string) error
	SetUserRelationship          func(db *sql.DB, userID, targetUserID, kind, note string, active bool) error
	SetUserPresence              func(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error
	UserIgnores                  func(db *sql.DB, userID, targetUserID string) (bool, error)
	InsertModerationReview       func(tx *sql.Tx, id, kind, targetID, targetKind, reporter, reason string, ts int64) error
	UpsertContentFilter          func(tx *sql.Tx, id, pattern, scope string, active bool, createdBy string, ts int64) error
	UpsertBoardAutomodRule       func(tx *sql.Tx, p *proto.BoardAutomodRuleSetPayload) error
	DeleteBoardAutomodRule       func(tx *sql.Tx, board, id string) error
	EvaluateBoardAutomod         func(db *sql.DB, boardID, text, authorID string) (matched bool, ruleID, matchType, action, reason string, durationSec int64, err error)
	InsertAutomodAuditLog        func(tx *sql.Tx, p *proto.BoardAutomodTriggeredPayload) error
	ResolveModerationReview      func(tx *sql.Tx, id, actor, resolution string, ts int64) error
	SetThreadPref                func(db *sql.DB, userID, threadID, level string) error
	WatchersOfThreadTx           func(tx *sql.Tx, threadID, excludeUserID string) ([]string, error)
	SetBoardFavorite             func(db *sql.DB, userID, boardID, folderID string, position *int, favorite bool) error
	SetBoardZap                  func(db *sql.DB, userID, boardID string, zapped bool) error
	CreateFavoriteFolder         func(db *sql.DB, userID, folderID, parentID, name string, position *int) error
	UpdateFavoriteFolder         func(db *sql.DB, userID, folderID, name string, parentID *string, position *int) error
	DeleteFavoriteFolder         func(db *sql.DB, userID, folderID string) error
	MoveBoardFavorite            func(db *sql.DB, userID, boardID, folderID string, position *int) error
	ImportFavoriteTree           func(db *sql.DB, userID string, tree *projections.FavoriteTree, replace bool) error
	GetBoardSettings             func(db *sql.DB, boardID string) (*projections.BoardSettings, error)
	BoardAllowsPublicSystemPost  func(db *sql.DB, boardID string) (bool, error)
	SetBoardSettings             func(db *sql.DB, boardID string, patch projections.BoardSettingsPatch) error
	SetRecommendedBoard          func(db *sql.DB, boardID, note, curatedBy string, position *int, recommended bool) error
	GetBoardMemberRequirements   func(db *sql.DB, boardID string) (*projections.BoardMemberRequirements, error)
	SetBoardMemberRequirements   func(db *sql.DB, boardID string, patch projections.BoardMemberRequirementsPatch) error
	SetBoardModerator            func(db *sql.DB, boardID, userID, actorID string, moderator bool, position *int) error
	SetBoardMember               func(db *sql.DB, boardID, userID string, member bool, patch projections.BoardMemberPatch) error
	GetBoardMemberApplication    func(db *sql.DB, applicationID string) (*projections.BoardMemberApplication, error)
	InsertBoardMemberApplication func(db *sql.DB, id, boardID, userID, note string) error
	ReviewBoardMemberApplication func(db *sql.DB, applicationID, reviewerID, status, title, reviewNote string) error
	UpsertDigestEntry            func(db *sql.DB, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string) (string, error)
	UpsertDigestEntryTx          func(tx *sql.Tx, id, boardID, targetKind, targetID, kind, title, path, note, createdBy string, ts int64) (string, error)
	RemoveDigestEntry            func(db *sql.DB, id string) error
	RemoveDigestEntryTx          func(tx *sql.Tx, id string) error
	RemoveDigestEntryFinalTx     func(tx *sql.Tx, id, boardID, kind, removedBy string, ts int64) error
	UpdateDigestEntry            func(db *sql.DB, id, title, path, note string) error
	UpdateDigestEntryTx          func(tx *sql.Tx, id, title, path, note string, ts int64) error
	SetDigestEntryBody           func(db *sql.DB, id, body string, edited bool) error
	SetDigestEntryBodyTx         func(tx *sql.Tx, id, body string, edited bool, ts int64) error
	UpsertDigestDirectory        func(db *sql.DB, id, boardID, kind, path, createdBy string) (string, error)
	UpsertDigestDirectoryTx      func(tx *sql.Tx, id, boardID, kind, path, createdBy string, ts int64) (string, error)
	CountDigestPathEntries       func(db *sql.DB, boardID, kind, path string) (int, error)
	CountDigestPathDirectories   func(db *sql.DB, boardID, kind, path string) (int, error)
	MoveDigestPath               func(db *sql.DB, boardID, kind, fromPath, toPath string) (int, error)
	MoveDigestPathTx             func(tx *sql.Tx, boardID, kind, fromPath, toPath string, ts int64) (int, error)
	MoveDigestPathFinalTx        func(tx *sql.Tx, eventID, boardID, kind, fromPath, toPath, actorID string, ts int64) (int, error)
	CopyDigestPath               func(db *sql.DB, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string) (int, error)
	CopyDigestPathTx             func(tx *sql.Tx, boardID, kind, fromPath, toPath, createdBy string, entryIDs, directoryIDs []string, ts int64) (int, error)
	DeleteDigestPath             func(db *sql.DB, boardID, kind, path string) (int, error)
	DeleteDigestPathTx           func(tx *sql.Tx, boardID, kind, path string) (int, error)
	DeleteDigestPathFinalTx      func(tx *sql.Tx, eventID, boardID, kind, path, actorID string, ts int64) (int, error)
	MarkBoardRead                func(db *sql.DB, userID, boardID string) error
	RestoreBoardRead             func(db *sql.DB, userID, boardID string) error
	MarkFavoriteFolderRead       func(db *sql.DB, userID, folderID string) error
	RestoreFavoriteFolderRead    func(db *sql.DB, userID, folderID string) error
	MarkThreadRead               func(db *sql.DB, userID, threadID string) error
	RestoreThreadRead            func(db *sql.DB, userID, threadID string) error
	MarkPostRead                 func(db *sql.DB, userID, postID string) error
	FtsInsertPost                func(tx *sql.Tx, postID, threadID, boardID, author, body string) error
	FtsUpdatePost                func(tx *sql.Tx, postID, newBody string) error
	FtsDeletePost                func(tx *sql.Tx, postID string) error
	RecordProcessed              func(tx *sql.Tx, partitionKind, partitionKey, actorID, cid, commandHash, resultJSON string) error
	RecordReactionReceived       func(db *sql.DB, postAuthorID string) error
	RecordReactionRemoved        func(db *sql.DB, postAuthorID string) error
	UserTrustLevel               func(db *sql.DB, userID string) (int, error)
	UpdatePostBody               func(tx *sql.Tx, postID string, body string, seq int64) error
	InsertSanction               func(tx *sql.Tx, id, userID, kind, scope string, expiresAt int64, by, reason string, seq int64) error
	ClearUserSanctions           func(tx *sql.Tx, userID, kind, scope string) (int64, error)
	EnqueueOutboxJob             func(tx *sql.Tx, kind string, payload any, ts int64) error
	RecordCommunityStatSnapshot  func(db *sql.DB, ts int64) error
	// PGNotifyEphemeral emits a pg_notify for an ephemeral (non-durable) event so
	// sibling nodes can fetch and re-publish it. nil when not in Postgres mode.
	PGNotifyEphemeral func(db *sql.DB, event, eid, scopes string)
}

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

type Reply = commandexec.Reply

// cmdEnvelope is the internal queue message for the command dispatcher.
type cmdEnvelope struct {
	ctx       context.Context // caller context; used for lock timeout and cancellation
	actor     *projections.User
	name      proto.CommandName
	payload   json.RawMessage
	cid       string
	partition commandexec.Partition
	replyCh   chan Reply
}

// Handler dispatches commands through one or more ordered partition lanes.
type Handler struct {
	db               *sql.DB
	bus              commandexec.EventPublisher
	queue            chan cmdEnvelope
	partitionWorkers int
	lockCmd          func(ctx context.Context, partition commandexec.Partition) (unlock func(), err error)
}

func New(db *sql.DB, bus commandexec.EventPublisher) *Handler {
	return &Handler{
		db:    db,
		bus:   bus,
		queue: make(chan cmdEnvelope, 256),
	}
}

// SetPartitionWorkers enables partition-routed command execution. Commands in
// the same partition are routed to the same lane and remain ordered; commands in
// different lanes can make progress independently once the storage lock allows it.
func (h *Handler) SetPartitionWorkers(n int) {
	if n < 1 {
		n = 1
	}
	h.partitionWorkers = n
}

// SetCommandLock installs an optional function that wraps each command dispatch.
// Postgres mode uses this to hold a pg_advisory_lock for the duration of every
// mutating command, serializing writes across nodes.
func (h *Handler) SetCommandLock(fn func(ctx context.Context, partition commandexec.Partition) (unlock func(), err error)) {
	h.lockCmd = fn
}

// Run processes commands. Call in a dedicated goroutine.
func (h *Handler) Run(ctx context.Context) {
	if h.partitionWorkers > 1 {
		h.runPartitioned(ctx)
		return
	}
	for {
		select {
		case env := <-h.queue:
			h.processEnvelope(env)
		case <-ctx.Done():
			return
		}
	}
}

func (h *Handler) runPartitioned(ctx context.Context) {
	lanes := make([]chan cmdEnvelope, h.partitionWorkers)
	for i := range lanes {
		lanes[i] = make(chan cmdEnvelope, 64)
		go func(ch <-chan cmdEnvelope) {
			for {
				select {
				case env := <-ch:
					h.processEnvelope(env)
				case <-ctx.Done():
					return
				}
			}
		}(lanes[i])
	}
	for {
		select {
		case env := <-h.queue:
			idx := commandexec.PartitionLaneIndex(env.partition, len(lanes))
			select {
			case lanes[idx] <- env:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (h *Handler) processEnvelope(env cmdEnvelope) {
	if env.ctx.Err() != nil {
		env.replyCh <- Reply{Err: errDetail(proto.ErrForbidden, "request cancelled", false)}
		return
	}
	start := time.Now()
	reply := h.dispatchWithLock(env)
	metrics.CommandLatency.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	env.replyCh <- reply
}

// dispatchWithLock acquires the command lock (if configured) before dispatching.
func (h *Handler) dispatchWithLock(env cmdEnvelope) Reply {
	if h.lockCmd == nil {
		return h.dispatch(env.actor, env.name, env.payload, env.cid, env.partition)
	}
	lockStart := time.Now()
	unlock, err := h.lockCmd(env.ctx, env.partition)
	waitMS := float64(time.Since(lockStart).Microseconds()) / 1000.0
	metrics.WriterLockWait.Observe(waitMS)
	observePartitionLockWait(env.partition, waitMS)
	if err != nil {
		return Reply{Err: errDetail("lock_unavailable", "write lock unavailable: "+err.Error(), true)}
	}
	defer unlock()
	return h.dispatch(env.actor, env.name, env.payload, env.cid, env.partition)
}

// Execute submits a command and blocks until it is processed.
func (h *Handler) Execute(ctx context.Context, actor *projections.User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	return h.ExecutePartition(ctx, actor, name, payload, cid, commandexec.Partition{})
}

func (h *Handler) ExecutePartition(ctx context.Context, actor *projections.User, name proto.CommandName, payload json.RawMessage, cid string, partition commandexec.Partition) Reply {
	replyCh := make(chan Reply, 1)
	env := cmdEnvelope{
		ctx:       ctx,
		actor:     actor,
		name:      name,
		payload:   payload,
		cid:       cid,
		partition: partition,
		replyCh:   replyCh,
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
