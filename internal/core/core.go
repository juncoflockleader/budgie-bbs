// Package core implements the append-only event log, command handler, and
// pub/sub bus that form the server's single source of truth.
//
// Design invariant: all state mutation flows through the Handler's command
// lanes. Transports (HTTP, WebSocket, SSH) are read-heavy and stateless; they
// submit commands and read projections but never touch the log directly.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/assetstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/chatstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/handler"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/presencestore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/readmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrAccountDeactivated     = errors.New("account deactivated")
	ErrAccountPending         = errors.New("account pending approval")
	ErrAccountRejected        = errors.New("account registration rejected")
	ErrLoginIPDenied          = errors.New("login host not allowed")
	ErrAccountAlreadyClosed   = errors.New("account already deactivated")
	ErrDeactivationIncomplete = errors.New("password required to deactivate account")
	ErrAccountDeleteForbidden = errors.New("account deletion forbidden")
	ErrLastAdminDeletion      = errors.New("cannot delete the last admin")
)

// Core is the central server object. Transports embed or reference it.
type Core struct {
	DB      *sql.DB
	Bus     Bus
	handler *handler.Handler
	// Nodes is the in-memory registry of active SSH sessions (M14).
	Nodes *NodeRegistry
	// assetStore, when non-nil, holds site-asset bytes (logo/banner) in an
	// external object store (S3/R2) so a CDN can serve them; nil keeps bytes in
	// the DB and the app serves them.
	assetStore assetstore.Store
	// pgDSN and nodeID are non-empty in Postgres mode only.
	// pgDSN is used to start the cross-node LISTEN goroutine.
	// nodeID identifies this process in pg_notify payloads.
	pgDSN  string
	nodeID string
	// crossNodeViaBus is true when the injected Bus handles sibling-node event
	// delivery, so the Postgres LISTEN/NOTIFY wakeup path should stay disabled.
	crossNodeViaBus bool
	// isLeader is true while this node holds the background-worker leader lock.
	isLeader atomic.Bool
	// captcha holds signup-captcha configuration; nil means disabled.
	captcha *captchaRuntime
	// emailVerifyEnabled is true when a mailer is configured and email
	// verification is enforced for new accounts.
	emailVerifyEnabled bool
	// emailVerifyBaseURL is the public site URL used to build verification links.
	emailVerifyBaseURL string
	// emailDevInboxURL, when set, points at a local SMTP catcher's web inbox
	// (e.g. mailpit on :8025). Surfaced in the auth policy so the signup UI can
	// link to captured mail during local testing. Empty in production.
	emailDevInboxURL string
	// privacyPolicyRequired gates whether signup must record explicit acceptance
	// of the privacy policy before creating an account.
	privacyPolicyRequired bool

	eventLogShadow          EventStore
	eventLogShadowReporter  EventParityReporter
	eventLogShadowInterval  time.Duration
	eventLogShadowReplay    int
	eventLogShadowPartition int
	eventLogShadowStartHead bool
	commandLogShadow        CommandLog
	commandLogAuthoritative CommandLog
	counterStore            counterstore.Store
	counterStoreOverride    bool
	presenceStore           presencestore.Store
	presenceStoreOverride   bool
	chatStore               chatstore.Store
	chatStoreOverride       bool
	postSearchIndex         PostSearchIndex
	readCache               readmodel.LatestFeedCache
	hotThreadSplitMu        sync.RWMutex
	hotThreadSplits         map[string]int
	hotThreadSplitOverrides map[string]int
}

type coreOptions struct {
	busFactory                func(nodeID string) Bus
	crossNodeViaBus           bool
	eventLogShadow            EventStore
	eventLogShadowReporter    EventParityReporter
	eventLogShadowInterval    time.Duration
	eventLogShadowReplay      int
	eventLogShadowPartition   int
	eventLogShadowStartHead   bool
	commandLogShadow          CommandLog
	commandLogAuthoritative   CommandLog
	counterStore              counterstore.Store
	presenceStore             presencestore.Store
	chatStore                 chatstore.Store
	postSearchIndex           PostSearchIndex
	readCache                 readmodel.LatestFeedCache
	asyncPostSearch           bool
	asyncCommunityStatHistory bool
	hotThreadSplits           map[string]int
}

const hotThreadSplitConfigPollInterval = 5 * time.Second

// Option customizes Core construction.
type Option func(*coreOptions)

// WithBus injects a prebuilt Bus. Use WithBusFactory when the Bus needs the
// generated Postgres node ID.
func WithBus(bus Bus, crossNodeViaBus bool) Option {
	return func(opts *coreOptions) {
		opts.busFactory = func(string) Bus { return bus }
		opts.crossNodeViaBus = crossNodeViaBus
	}
}

// WithBusFactory injects a Bus that is built after the Postgres node ID exists.
func WithBusFactory(factory func(nodeID string) Bus, crossNodeViaBus bool) Option {
	return func(opts *coreOptions) {
		opts.busFactory = factory
		opts.crossNodeViaBus = crossNodeViaBus
	}
}

type EventLogShadowOptions struct {
	Shadow         EventStore
	Reporter       EventParityReporter
	Interval       time.Duration
	ReplayLimit    int
	PartitionLimit int
	StartAtHead    bool
}

func WithEventLogShadow(config EventLogShadowOptions) Option {
	return func(opts *coreOptions) {
		opts.eventLogShadow = config.Shadow
		opts.eventLogShadowReporter = config.Reporter
		opts.eventLogShadowInterval = config.Interval
		opts.eventLogShadowReplay = config.ReplayLimit
		opts.eventLogShadowPartition = config.PartitionLimit
		opts.eventLogShadowStartHead = config.StartAtHead
	}
}

func WithCommandLogShadow(log CommandLog) Option {
	return func(opts *coreOptions) {
		opts.commandLogShadow = log
	}
}

func WithAuthoritativeCommandLog(log CommandLog) Option {
	return func(opts *coreOptions) {
		opts.commandLogAuthoritative = log
	}
}

func WithCounterStore(store counterstore.Store) Option {
	return func(opts *coreOptions) {
		opts.counterStore = store
	}
}

func WithPresenceStore(store presencestore.Store) Option {
	return func(opts *coreOptions) {
		opts.presenceStore = store
	}
}

func WithChatStore(store chatstore.Store) Option {
	return func(opts *coreOptions) {
		opts.chatStore = store
	}
}

func WithPostSearchIndex(index PostSearchIndex) Option {
	return func(opts *coreOptions) {
		opts.postSearchIndex = index
		if index != nil {
			opts.asyncPostSearch = true
		}
	}
}

func WithReadCache(cache readmodel.LatestFeedCache) Option {
	return func(opts *coreOptions) {
		opts.readCache = cache
	}
}

func (c *Core) AuthoritativeCommandLogEnabled() bool {
	return c != nil && c.commandLogAuthoritative != nil
}

func WithAsyncPostSearch() Option {
	return func(opts *coreOptions) {
		opts.asyncPostSearch = true
	}
}

func WithAsyncCommunityStatHistory() Option {
	return func(opts *coreOptions) {
		opts.asyncCommunityStatHistory = true
	}
}

func WithHotThreadSplits(splits map[string]int) Option {
	return func(opts *coreOptions) {
		opts.hotThreadSplits = logmodel.NormalizeHotThreadSplits(splits)
	}
}

// IsBackgroundLeader reports whether this node currently holds the
// background-worker leader lock (and is running the outbox worker + stats).
func (c *Core) IsBackgroundLeader() bool { return c.isLeader.Load() }

// New opens the SQLite database, runs migrations, and returns a ready Core.
func New(dbPath string, options ...Option) (*Core, error) {
	setSQLFlavor(sqliteFlavor)
	projections.SetSQLFlavor(sqliteFlavor)
	setNodeID("")
	setCrossNodeViaBus(false)
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite WAL: one writer is plenty
	if err := applySQLiteMigrations(db); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	var bus Bus = NewMemBus()
	opts := coreOptions{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	if opts.busFactory != nil {
		bus = opts.busFactory("")
	}
	if opts.eventLogShadow != nil {
		bus = newEventLogShadowBus(bus, NewSQLEventStore(db), opts.eventLogShadow, opts.eventLogShadowReporter)
	}
	setCrossNodeViaBus(opts.crossNodeViaBus)
	setAsyncPostSearchCommands(opts.asyncPostSearch)
	setAsyncCommunityStatHistorySnapshots(opts.asyncCommunityStatHistory)
	persistedHotThreadSplits, err := projections.LoadHotThreadSplits(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load hot thread splits: %w", err)
	}
	hotThreadSplitOverrides := logmodel.NormalizeHotThreadSplits(opts.hotThreadSplits)
	counterStore := opts.counterStore
	counterStoreOverride := counterStore != nil
	if counterStore == nil {
		counterStore = sqlCounterStore{db: db}
	}
	presenceStore := opts.presenceStore
	presenceStoreOverride := presenceStore != nil
	if presenceStore == nil {
		presenceStore = sqlPresenceStore{db: db}
	}
	bindPresenceStoreDB(presenceStore, db)
	chatStore := opts.chatStore
	chatStoreOverride := chatStore != nil
	if chatStore == nil {
		chatStore = sqlChatStore{db: db}
	}
	c := &Core{
		DB:                      db,
		Bus:                     bus,
		handler:                 newHandler(db, bus, counterStore, presenceStore, chatStore),
		Nodes:                   newNodeRegistry(bus),
		crossNodeViaBus:         opts.crossNodeViaBus,
		eventLogShadow:          opts.eventLogShadow,
		eventLogShadowReporter:  opts.eventLogShadowReporter,
		eventLogShadowInterval:  opts.eventLogShadowInterval,
		eventLogShadowReplay:    opts.eventLogShadowReplay,
		eventLogShadowPartition: opts.eventLogShadowPartition,
		eventLogShadowStartHead: opts.eventLogShadowStartHead,
		commandLogShadow:        opts.commandLogShadow,
		commandLogAuthoritative: opts.commandLogAuthoritative,
		counterStore:            counterStore,
		counterStoreOverride:    counterStoreOverride,
		presenceStore:           presenceStore,
		presenceStoreOverride:   presenceStoreOverride,
		chatStore:               chatStore,
		chatStoreOverride:       chatStoreOverride,
		postSearchIndex:         opts.postSearchIndex,
		readCache:               opts.readCache,
		hotThreadSplits:         logmodel.MergeHotThreadSplits(persistedHotThreadSplits, hotThreadSplitOverrides),
		hotThreadSplitOverrides: hotThreadSplitOverrides,
	}
	if opts.asyncCommunityStatHistory {
		if err := c.RecordDerivedViewApplied(projections.DerivedViewCommunityStatHistory, 0); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize async community stat history watermark: %w", err)
		}
	}
	return c, nil
}

// pgScalarSeqAppendLockKey is a transaction-scoped Postgres advisory lock used
// by appendEvent to preserve scalar seq commit visibility while partition
// writers are introduced.
const pgScalarSeqAppendLockKey = int64(1654893721)

// pgUserBootstrapLockKey serializes RegisterUser transactions on Postgres so the
// first-user-becomes-admin bootstrap and approval gating are race-free.
const pgUserBootstrapLockKey = int64(1654893724)

// acquireUserBootstrapGate takes a transaction-scoped advisory lock on Postgres
// so concurrent registrations cannot both observe an empty users table. It is a
// no-op on SQLite, which already serializes writes via a single connection.
func acquireUserBootstrapGate(tx *sql.Tx) error {
	if SQLFlavor() != postgresFlavor {
		return nil
	}
	_, err := qExec(tx, `SELECT pg_advisory_xact_lock(?)`, pgUserBootstrapLockKey)
	return err
}

const pgPartitionWorkers = 16

// NewPostgres opens a Postgres database and applies the production schema.
//
// It is intentionally explicit and minimal: command serialization still lives
// in Core, but SQL execution is normalized to Postgres placeholder style.
// In Postgres mode commands are routed through partition lanes. A per-partition
// advisory lock is acquired for each command; durable event appends use a short
// transaction-scoped scalar seq gate so compatibility replay cannot observe
// out-of-order commits.
func NewPostgres(dsn string, options ...Option) (*Core, error) {
	setSQLFlavor(postgresFlavor)
	projections.SetSQLFlavor(postgresFlavor)
	db, err := OpenPostgres(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres db: %w", err)
	}

	if err := ApplyPostgresMigrations(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	nodeID := newID("node_")
	setNodeID(nodeID)

	opts := coreOptions{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	bus := Bus(NewMemBus())
	if opts.busFactory != nil {
		bus = opts.busFactory(nodeID)
	}
	if opts.eventLogShadow != nil {
		bus = newEventLogShadowBus(bus, NewSQLEventStore(db), opts.eventLogShadow, opts.eventLogShadowReporter)
	}
	setCrossNodeViaBus(opts.crossNodeViaBus)
	setAsyncPostSearchCommands(opts.asyncPostSearch)
	setAsyncCommunityStatHistorySnapshots(opts.asyncCommunityStatHistory)
	counterStore := opts.counterStore
	counterStoreOverride := counterStore != nil
	if counterStore == nil {
		counterStore = sqlCounterStore{db: db}
	}
	presenceStore := opts.presenceStore
	presenceStoreOverride := presenceStore != nil
	if presenceStore == nil {
		presenceStore = sqlPresenceStore{db: db}
	}
	bindPresenceStoreDB(presenceStore, db)
	chatStore := opts.chatStore
	chatStoreOverride := chatStore != nil
	if chatStore == nil {
		chatStore = sqlChatStore{db: db}
	}
	h := newHandler(db, bus, counterStore, presenceStore, chatStore)
	h.SetPartitionWorkers(pgPartitionWorkers)
	h.SetCommandLock(pgPartitionLockFn(db))
	persistedHotThreadSplits, err := projections.LoadHotThreadSplits(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load hot thread splits: %w", err)
	}
	hotThreadSplitOverrides := logmodel.NormalizeHotThreadSplits(opts.hotThreadSplits)
	c := &Core{
		DB:                      db,
		Bus:                     bus,
		handler:                 h,
		Nodes:                   newNodeRegistry(bus),
		pgDSN:                   dsn,
		nodeID:                  nodeID,
		crossNodeViaBus:         opts.crossNodeViaBus,
		eventLogShadow:          opts.eventLogShadow,
		eventLogShadowReporter:  opts.eventLogShadowReporter,
		eventLogShadowInterval:  opts.eventLogShadowInterval,
		eventLogShadowReplay:    opts.eventLogShadowReplay,
		eventLogShadowPartition: opts.eventLogShadowPartition,
		eventLogShadowStartHead: opts.eventLogShadowStartHead,
		commandLogShadow:        opts.commandLogShadow,
		commandLogAuthoritative: opts.commandLogAuthoritative,
		counterStore:            counterStore,
		counterStoreOverride:    counterStoreOverride,
		presenceStore:           presenceStore,
		presenceStoreOverride:   presenceStoreOverride,
		chatStore:               chatStore,
		chatStoreOverride:       chatStoreOverride,
		postSearchIndex:         opts.postSearchIndex,
		readCache:               opts.readCache,
		hotThreadSplits:         logmodel.MergeHotThreadSplits(persistedHotThreadSplits, hotThreadSplitOverrides),
		hotThreadSplitOverrides: hotThreadSplitOverrides,
	}
	if opts.asyncCommunityStatHistory {
		if err := c.RecordDerivedViewApplied(projections.DerivedViewCommunityStatHistory, 0); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize async community stat history watermark: %w", err)
		}
	}
	return c, nil
}

// pgPartitionLockFn returns a command lock function that acquires a Postgres
// partition advisory lock for the duration of each command execution.
// A dedicated connection is borrowed from the pool per command so the lock
// is pinned to a single connection and released reliably on unlock.
func pgPartitionLockFn(db *sql.DB) func(ctx context.Context, partition commandexec.Partition) (func(), error) {
	return func(ctx context.Context, partition commandexec.Partition) (func(), error) {
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, fmt.Errorf("advisory lock: get conn: %w", err)
		}
		lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		partitionKey := commandexec.PartitionAdvisoryLockKey(partition)
		if _, err := conn.ExecContext(lockCtx, `SELECT pg_advisory_lock($1)`, partitionKey); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("advisory lock: partition pg_advisory_lock: %w", err)
		}
		return func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, partitionKey)
			_ = conn.Close()
		}, nil
	}
}

// Run starts the command dispatcher. Returns when ctx is cancelled.
func (c *Core) Run(ctx context.Context) {
	crossNodeViaBus := c.crossNodeViaBus
	if starter, ok := c.Bus.(interface{ Start(context.Context) error }); ok {
		if err := starter.Start(ctx); err != nil {
			slog.Error("cluster bus: start failed; falling back to Postgres wakeups", "err", err)
			crossNodeViaBus = false
			setCrossNodeViaBus(false)
		} else if crossNodeViaBus {
			setCrossNodeViaBus(true)
			slog.Info("cluster bus: handling cross-node event delivery")
		}
	}
	// W3: in Postgres mode, start the cross-node event wakeup listener.
	if c.pgDSN != "" && !crossNodeViaBus {
		startPGListener(ctx, c.pgDSN, c.nodeID, c.DB, c.Bus)
	}
	// The outbox worker is no longer started here: it is owned by the worker
	// role and runs behind leader election. Callers that want background jobs
	// invoke StartBackgroundWorker (production) or StartOutboxWorker (single
	// process / tests).
	c.startHotThreadSplitConfigPoller(ctx)
	c.handler.Run(ctx)
}

// ExecCmd submits a command for the actor and returns the result.
// payload is the raw JSON of the command-specific payload object.
func (c *Core) ExecCmd(ctx context.Context, actor *projections.User, name proto.CommandName, payload json.RawMessage, cid string) commandexec.Reply {
	partition, ok := c.classifyCommandPartition(actor, name, payload)
	if !ok {
		partition = defaultPartition()
	}
	logPartition := logPartitionFromEventPartition(partition)
	bypassCommandLog := logmodel.CommandBypassesCommandLog(name)
	if c != nil && c.commandLogAuthoritative != nil && !bypassCommandLog {
		return c.enqueueAuthoritativeCommand(ctx, actor, name, payload, cid, logPartition)
	}
	if !bypassCommandLog {
		cid = c.shadowCommand(ctx, actor, name, payload, cid, logPartition)
	}
	return c.handler.ExecutePartition(ctx, actor, name, payload, cid, commandexec.Partition{
		Kind: partition.Kind,
		Key:  partition.Key,
	})
}

func (c *Core) enqueueAuthoritativeCommand(ctx context.Context, actor *projections.User, name proto.CommandName, payload json.RawMessage, cid string, partition LogPartition) commandexec.Reply {
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	enqueuedAt := nowMS()
	if strings.TrimSpace(cid) != "" {
		enqueuedAt = logmodel.DeterministicCommandReceiptEnqueuedAt(partition, actorID, cid, name, payload)
	}
	record, err := c.commandLogAuthoritative.Produce(ctx, CommandLogRecord{
		Partition:  partition.Normalize(),
		ActorID:    actorID,
		CID:        cid,
		Command:    name,
		Payload:    append([]byte(nil), payload...),
		EnqueuedAt: enqueuedAt,
	})
	if err != nil {
		return commandexec.Reply{Err: &proto.ErrorDetail{
			Code:      proto.ErrCommandLogUnavailable,
			Message:   err.Error(),
			Retryable: true,
		}}
	}
	commandID, err := logmodel.EffectiveCommandLogCID(record)
	if err != nil {
		return commandexec.Reply{Err: &proto.ErrorDetail{
			Code:      proto.ErrCommandLogUnavailable,
			Message:   err.Error(),
			Retryable: true,
		}}
	}
	partition = record.Partition.Normalize()
	return commandexec.Reply{Result: &proto.AckResult{
		ID:                   commandID,
		Status:               proto.AckStatusPending,
		CommandID:            commandID,
		CommandPartitionKind: partition.Kind,
		CommandPartitionKey:  partition.Key,
		CommandOffset:        record.Offset,
	}}
}

func (c *Core) shadowCommand(ctx context.Context, actor *projections.User, name proto.CommandName, payload json.RawMessage, cid string, partition LogPartition) string {
	if c == nil || c.commandLogShadow == nil {
		return cid
	}
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	enqueuedAt := nowMS()
	if strings.TrimSpace(cid) != "" {
		enqueuedAt = logmodel.DeterministicCommandReceiptEnqueuedAt(partition, actorID, cid, name, payload)
	}
	record, err := c.commandLogShadow.Produce(ctx, CommandLogRecord{
		Partition:  partition.Normalize(),
		ActorID:    actorID,
		CID:        cid,
		Command:    name,
		Payload:    append([]byte(nil), payload...),
		EnqueuedAt: enqueuedAt,
	})
	if err != nil {
		slog.Warn("command log shadow append failed", "command", name, "partitionKind", partition.Kind, "partitionKey", partition.Key, "err", err)
		return cid
	}
	if cid == "" && record.CID != "" {
		return record.CID
	}
	return cid
}

// ExecuteCommandLogRecord executes a command-log record through the current
// SQL-backed handler without mirroring it back into the command log. It is the
// bridge used while broker-owned writers are introduced behind the existing
// command implementation.
func (c *Core) ExecuteCommandLogRecord(ctx context.Context, record CommandLogRecord) commandexec.Reply {
	if c == nil || c.handler == nil {
		return commandexec.Reply{Err: &proto.ErrorDetail{Code: "internal_error", Message: "core is not initialized", Retryable: true}}
	}
	var actor *projections.User
	if record.ActorID != "" {
		u, err := projections.GetUserByID(c.DB, record.ActorID)
		if err != nil {
			return commandexec.Reply{Err: &proto.ErrorDetail{Code: "internal_error", Message: err.Error(), Retryable: true}}
		}
		if u == nil {
			return commandexec.Reply{Err: &proto.ErrorDetail{Code: proto.ErrUnauthenticated, Message: "command actor not found", Retryable: false}}
		}
		actor = u
	}
	cid, errDetail := commandLogExecutionCID(record)
	if errDetail != nil {
		return commandexec.Reply{Err: errDetail}
	}
	partition := record.Partition.Normalize()
	if errDetail := c.validateCommandLogRecordPartition(actor, record, partition); errDetail != nil {
		return commandexec.Reply{Err: errDetail}
	}
	return c.handler.ExecutePartition(ctx, actor, record.Command, record.Payload, cid, commandexec.Partition{
		Kind: partition.Kind,
		Key:  partition.Key,
	})
}

func (c *Core) validateCommandLogRecordPartition(actor *projections.User, record CommandLogRecord, actual LogPartition) *proto.ErrorDetail {
	expected, ok := c.classifyCommandPartition(actor, record.Command, record.Payload)
	if !ok {
		expected = defaultPartition()
	}
	want := logPartitionFromEventPartition(expected)
	if actual.Normalize() == want {
		return nil
	}
	if logmodel.CommandPartitionMatchesAppendPostTarget(record.Command, record.Payload, actual) {
		return nil
	}
	return &proto.ErrorDetail{
		Code: proto.ErrValidationFailed,
		Message: fmt.Sprintf("command log partition mismatch: record=%s/%s expected=%s/%s",
			actual.Kind, actual.Key, want.Kind, want.Key),
		Retryable: false,
	}
}

func (c *Core) classifyCommandPartition(actor *projections.User, name proto.CommandName, payload json.RawMessage) (eventPartition, bool) {
	partition, ok := classifyCommandPartition(actor, name, payload)
	if !ok || name != proto.CmdAppendPost {
		return partition, ok
	}
	threadID := partition.Key
	shards := c.hotThreadSplitShards(threadID)
	if shards <= 1 {
		return partition, ok
	}
	shard := logmodel.HotThreadReplyShard(actorID(actor), payload, shards)
	return eventPartition{Kind: partitionThread, Key: logmodel.HotThreadSplitPartitionKey(threadID, shard)}, true
}

func (c *Core) SetHotThreadSplit(threadID string, shards int) {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" {
		return
	}
	c.hotThreadSplitMu.Lock()
	defer c.hotThreadSplitMu.Unlock()
	if c.hotThreadSplits == nil {
		c.hotThreadSplits = map[string]int{}
	}
	if c.hotThreadSplitOverrides == nil {
		c.hotThreadSplitOverrides = map[string]int{}
	}
	if shards <= 1 {
		delete(c.hotThreadSplits, threadID)
		delete(c.hotThreadSplitOverrides, threadID)
		return
	}
	c.hotThreadSplits[threadID] = shards
	c.hotThreadSplitOverrides[threadID] = shards
}

func (c *Core) HotThreadSplits() map[string]int {
	if c == nil {
		return nil
	}
	c.hotThreadSplitMu.RLock()
	defer c.hotThreadSplitMu.RUnlock()
	return logmodel.NormalizeHotThreadSplits(c.hotThreadSplits)
}

func (c *Core) hotThreadSplitShards(threadID string) int {
	if c == nil {
		return 0
	}
	c.hotThreadSplitMu.RLock()
	defer c.hotThreadSplitMu.RUnlock()
	return c.hotThreadSplits[threadID]
}

func (c *Core) PersistHotThreadSplit(threadID string, shards int) error {
	if c == nil || c.DB == nil {
		return errors.New("core is not initialized")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("thread id is required")
	}
	if err := projections.PersistHotThreadSplit(c.DB, threadID, shards); err != nil {
		return err
	}
	return c.ReloadHotThreadSplits()
}

func (c *Core) HotThreadSplitBlockingLag(ctx context.Context, threadID string, nextShards int) ([]logmodel.CommandPartitionOffset, error) {
	if c == nil || c.commandLogAuthoritative == nil {
		return nil, nil
	}
	lister, ok := c.commandLogAuthoritative.(CommandPartitionOffsetLister)
	if !ok {
		return nil, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}
	offsets, _, err := listCommandPartitionOffsetsWithLimit(ctx, lister, 0)
	if err != nil {
		return nil, err
	}
	affected := logmodel.HotThreadSplitPartitionSet(threadID, c.hotThreadSplitShards(threadID), nextShards)
	blocking := make([]logmodel.CommandPartitionOffset, 0)
	for _, offset := range offsets {
		if _, ok := affected[offset.Partition]; !ok {
			continue
		}
		if offset.Lag() > 0 {
			blocking = append(blocking, offset)
		}
	}
	return blocking, nil
}

func (c *Core) ReloadHotThreadSplits() error {
	if c == nil || c.DB == nil {
		return nil
	}
	persisted, err := projections.LoadHotThreadSplits(c.DB)
	if err != nil {
		return err
	}
	c.applyPersistedHotThreadSplits(persisted)
	return nil
}

func (c *Core) applyPersistedHotThreadSplits(persisted map[string]int) {
	c.hotThreadSplitMu.Lock()
	defer c.hotThreadSplitMu.Unlock()
	c.hotThreadSplits = logmodel.MergeHotThreadSplits(persisted, c.hotThreadSplitOverrides)
}

func (c *Core) startHotThreadSplitConfigPoller(ctx context.Context) {
	if c == nil || c.DB == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(hotThreadSplitConfigPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.ReloadHotThreadSplits(); err != nil {
					slog.Warn("hot thread split config reload failed", "err", err)
				}
			}
		}
	}()
}

func actorID(actor *projections.User) string {
	if actor == nil {
		return ""
	}
	return actor.ID
}

func commandLogExecutionCID(record CommandLogRecord) (string, *proto.ErrorDetail) {
	cid, err := logmodel.EffectiveCommandLogCID(record)
	if err != nil {
		return "", &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "command log record missing offset for idempotent execution", Retryable: false}
	}
	return cid, nil
}

// Head returns the current highest seq in the event log.
func (c *Core) Head() (int64, error) {
	return headSeq(c.DB)
}

// HeadCursor returns the scalar compatibility head plus current per-partition
// heads so partition-aware clients can resume without relying only on seq.
func (c *Core) HeadCursor() (proto.Cursor, error) {
	head, err := c.Head()
	if err != nil {
		return proto.Cursor{}, err
	}
	cursor := proto.CursorFromHead(head)
	offsets, _, err := listEventPartitionOffsets(context.Background(), NewSQLEventStore(c.DB), 0)
	if err != nil {
		return proto.Cursor{}, err
	}
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i].Partition.Less(offsets[j].Partition)
	})
	for _, offset := range offsets {
		if offset.LastOffset <= 0 {
			continue
		}
		partition := offset.Partition.Normalize()
		cursor.Partitions = append(cursor.Partitions, proto.PartitionCursor{
			Kind:   partition.Kind,
			Key:    partition.Key,
			Offset: offset.LastOffset,
		})
	}
	return cursor, nil
}

// Replay returns events with seq > after, filtered to the given scopes.
func (c *Core) Replay(after int64, scopes []string, limit int) ([]*proto.Event, error) {
	return replayEvents(c.DB, after, scopes, limit)
}

// ReplayCursor replays from a durable cursor. Scalar cursors preserve legacy
// seq replay; partition-only cursors use per-partition offsets as the replay
// boundary while keeping a compatibility seq sort for the returned batch.
func (c *Core) ReplayCursor(cursor proto.Cursor, scopes []string, limit int) ([]*proto.Event, error) {
	if cursor.Seq > 0 || len(cursor.Partitions) == 0 {
		return c.Replay(cursor.AfterSeq(0), scopes, limit)
	}
	if limit <= 0 {
		limit = 100
	}
	afterByPartition := map[LogPartition]int64{}
	for _, part := range cursor.Partitions {
		partition := LogPartition{Kind: part.Kind, Key: part.Key}.Normalize()
		if part.Offset > afterByPartition[partition] {
			afterByPartition[partition] = part.Offset
		}
	}
	offsets, _, err := listEventPartitionOffsets(context.Background(), NewSQLEventStore(c.DB), 0)
	if err != nil {
		return nil, err
	}
	seen := map[LogPartition]bool{}
	partitions := make([]LogPartition, 0, len(offsets)+len(afterByPartition))
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		partitions = append(partitions, partition)
	}
	for partition := range afterByPartition {
		if seen[partition] {
			continue
		}
		seen[partition] = true
		partitions = append(partitions, partition)
	}
	SortLogPartitions(partitions)

	events := make([]*proto.Event, 0, limit)
	for _, partition := range partitions {
		batch, err := replayPartitionEventsFiltered(c.DB, partition.Kind, partition.Key, afterByPartition[partition], scopes, limit)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}
	proto.SortEventsByReplayOrder(events)
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

// ReplayPartition returns events in one write-ordering partition with
// partition_offset > afterOffset.
func (c *Core) ReplayPartition(partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	return replayPartitionEvents(c.DB, partitionKind, partitionKey, afterOffset, limit)
}

// ListNodes returns a snapshot of active SSH sessions for sysop use (M14).
func (c *Core) ListNodes() []NodeEntry {
	return c.Nodes.List()
}

// KickNode forcibly closes the SSH session identified by nodeID (M14).
func (c *Core) KickNode(nodeID string) error {
	return c.Nodes.KickNode(nodeID)
}

// SendNodeMessage enqueues a sysop message to the given SSH session (M14).
func (c *Core) SendNodeMessage(nodeID, msg string) error {
	return c.Nodes.SendMessage(nodeID, msg)
}

// Subscribe creates a new subscription on the bus.
func (c *Core) Subscribe(scopes []string) *Subscription {
	return c.Bus.Subscribe(scopes)
}

// Unsubscribe removes a subscription.
func (c *Core) Unsubscribe(s *Subscription) {
	c.Bus.Unsubscribe(s)
}

// --- Projection readers (safe for concurrent access) ---

func (c *Core) ListBoards() ([]projections.Board, error) { return projections.ListBoards(c.DB) }
func (c *Core) ListCategories() ([]projections.Category, error) {
	return projections.ListCategories(c.DB)
}
func (c *Core) GetBoard(id string) (*projections.Board, error) { return projections.GetBoard(c.DB, id) }

func (c *Core) GetCommunityStats() (*projections.CommunityStats, error) {
	rows, err := projections.CommunityStatsSnapshotRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	var stats *projections.CommunityStats
	if rows > 0 {
		stats, err = projections.GetCommunityStatsSnapshot(c.DB)
	} else {
		stats, err = projections.GetCommunityStats(c.DB)
	}
	if err != nil {
		return nil, err
	}
	if err := c.applyPresenceStoreStats(stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func (c *Core) ListCommunityStatHistory(limit, offset int) ([]projections.CommunityStatHistory, error) {
	return projections.ListCommunityStatHistory(c.DB, limit, offset)
}

func (c *Core) PublishDailyStatsSnapshot(ctx context.Context, at time.Time) (*proto.AckResult, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := recordCommunityStatSnapshot(c.DB, at.UTC().UnixMilli()); err != nil {
		return nil, err
	}
	day := at.UTC().Format("2006-01-02")
	raw, err := json.Marshal(proto.PublishStatsSnapshotPayload{Date: day})
	if err != nil {
		return nil, err
	}
	systemActor := &projections.User{ID: "system", Name: "system", Role: "admin", RegistrationStatus: "approved"}
	reply := c.ExecCmd(ctx, systemActor, proto.CmdPublishStatsSnapshot, raw, "auto-stats-"+day)
	if reply.Err != nil {
		return nil, fmt.Errorf("%s: %s", reply.Err.Code, reply.Err.Message)
	}
	return reply.Result, nil
}

func (c *Core) ListBoardRankings(actor *projections.User, limit, offset int) ([]projections.BoardRanking, error) {
	rows, err := projections.BoardRankingStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListBoardRankingsMaterialized(c.DB, actor.ID, actor.IsMod(), limit, offset)
	}
	return projections.ListBoardRankings(c.DB, actor.ID, actor.IsMod(), limit, offset)
}
func (c *Core) ListRecommendedBoards(limit, offset int) ([]projections.RecommendedBoard, error) {
	return projections.ListRecommendedBoards(c.DB, limit, offset)
}
func (c *Core) ListThreadRankings(actor *projections.User, boardID string, limit, offset int) ([]projections.ThreadRanking, error) {
	rows, err := projections.ThreadRankingStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListThreadRankingsMaterialized(c.DB, actor.ID, actor.IsMod(), boardID, limit, offset)
	}
	return projections.ListThreadRankings(c.DB, actor.ID, actor.IsMod(), boardID, limit, offset)
}
func (c *Core) ListReplyRankings(actor *projections.User, limit, offset int) ([]projections.ReplyRanking, error) {
	rows, err := projections.ReplyRankingPostsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListReplyRankingsMaterialized(c.DB, actor.ID, actor.IsMod(), limit, offset)
	}
	return projections.ListReplyRankings(c.DB, actor.ID, actor.IsMod(), limit, offset)
}
func (c *Core) ListUserRankings(limit, offset int) ([]projections.UserRanking, error) {
	rows, err := projections.UserRankingStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListUserRankingsMaterialized(c.DB, limit, offset)
	}
	return projections.ListUserRankings(c.DB, limit, offset)
}
func (c *Core) ListBlessingRankings(limit, offset int) ([]projections.BlessingRanking, error) {
	rows, err := projections.BlessingRankingStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListBlessingRankingsMaterialized(c.DB, limit, offset)
	}
	return projections.ListBlessingRankings(c.DB, limit, offset)
}
func (c *Core) ListBlessings(limit, offset int) ([]projections.Blessing, error) {
	return projections.ListBlessings(c.DB, limit, offset)
}
func (c *Core) ListBoardModeratorTerms(boardID string, limit, offset int) ([]projections.BoardModeratorTerm, error) {
	return projections.ListBoardModeratorTerms(c.DB, boardID, limit, offset)
}
func (c *Core) ListArchiveRankings(actor *projections.User, kind string, limit, offset int) ([]projections.ArchiveRanking, error) {
	rows, err := projections.ArchiveRankingStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListArchiveRankingsMaterialized(c.DB, actor.ID, actor.IsMod(), kind, limit, offset)
	}
	return projections.ListArchiveRankings(c.DB, actor.ID, actor.IsMod(), kind, limit, offset)
}
func (c *Core) ListBoardSummaries(userID string, unreadOnly bool, opts ...projections.BoardSummaryOptions) ([]projections.BoardSummary, error) {
	rows, err := projections.BoardSummaryStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListBoardSummariesMaterialized(c.DB, userID, unreadOnly, opts...)
	}
	return projections.ListBoardSummaries(c.DB, userID, unreadOnly, opts...)
}
func (c *Core) GetBoardInfo(boardID string) (*projections.BoardInfo, error) {
	return projections.GetBoardInfo(c.DB, boardID)
}
func (c *Core) GetBoardMemberRequirements(boardID string) (*projections.BoardMemberRequirements, error) {
	return projections.GetBoardMemberRequirements(c.DB, boardID)
}
func (c *Core) ListBoardMembers(boardID string) ([]projections.BoardMember, error) {
	return projections.ListBoardMembers(c.DB, boardID)
}
func (c *Core) UserIsBoardMember(boardID, userID string) (bool, error) {
	return projections.BoardMemberExists(c.DB, boardID, userID)
}
func (c *Core) GetBoardMemberApplication(applicationID string) (*projections.BoardMemberApplication, error) {
	return projections.GetBoardMemberApplication(c.DB, applicationID)
}
func (c *Core) ListBoardMemberApplications(boardID, status, userID string, limit, offset int) ([]projections.BoardMemberApplication, error) {
	return projections.ListBoardMemberApplications(c.DB, boardID, status, userID, limit, offset)
}
func (c *Core) ListDigestEntries(boardID, kind, path string, limit, offset int) ([]projections.DigestEntry, error) {
	return projections.ListDigestEntries(c.DB, boardID, kind, path, limit, offset)
}
func (c *Core) ListDigestPathTree(boardID, kind string) ([]projections.DigestPathNode, error) {
	return projections.ListDigestPathTree(c.DB, boardID, kind)
}
func (c *Core) ListSiteDigestEntries(actor *projections.User, kind, path string, limit, offset int) ([]projections.DigestEntry, error) {
	viewer := readmodel.ViewerScopeForUser(actor)
	return projections.ListSiteDigestEntries(c.DB, viewer.UserID, viewer.IncludePrivate, kind, path, limit, offset)
}
func (c *Core) SearchDigestEntries(actor *projections.User, boardID, kind, path, query string, limit, offset int) ([]projections.DigestEntry, error) {
	viewer := readmodel.ViewerScopeForUser(actor)
	return projections.SearchDigestEntries(c.DB, viewer.UserID, viewer.IncludePrivate, boardID, kind, path, query, limit, offset)
}
func (c *Core) GetDigestExport(entryID string) (*projections.DigestExport, error) {
	return projections.GetDigestExport(c.DB, entryID)
}
func FormatDigestExportText(export *projections.DigestExport) string {
	return projections.FormatDigestExportText(export)
}
func (c *Core) ListMail(userID, mailbox string, limit, offset int, unreadOnly bool) ([]projections.MailItem, error) {
	return projections.ListMail(c.DB, userID, mailbox, limit, offset, unreadOnly)
}
func (c *Core) ListMailThread(userID, messageID string, limit, offset int) ([]projections.MailItem, error) {
	return projections.ListMailThread(c.DB, userID, messageID, limit, offset)
}
func (c *Core) ListMailByAuthor(userID, messageID string, limit, offset int) ([]projections.MailItem, error) {
	return projections.ListMailByAuthor(c.DB, userID, messageID, limit, offset)
}
func (c *Core) GetMail(userID, messageID string) (*projections.MailItem, error) {
	return projections.GetMail(c.DB, userID, messageID)
}
func (c *Core) CountUnreadMail(userID string) (int, error) {
	return projections.CountUnreadMail(c.DB, userID)
}
func (c *Core) GetMailUsage(userID string) (*projections.MailUsage, error) {
	return projections.GetMailUsage(c.DB, userID)
}
func (c *Core) ListRelayDeliveries(status string, limit, offset int) ([]projections.RelayDelivery, error) {
	return projections.ListRelayDeliveries(c.DB, status, limit, offset)
}
func (c *Core) ListMailGroups(userID string) ([]projections.MailGroup, error) {
	groups, err := projections.ListMailGroups(c.DB, userID)
	if err != nil {
		return nil, err
	}
	friends, err := projections.ListSocialUsers(c.DB, userID, "friends", false)
	if err != nil {
		return nil, err
	}
	friendGroup := projections.MailGroup{ID: "friends", Name: "Friends", BuiltIn: true}
	for i, friend := range friends {
		friendGroup.Members = append(friendGroup.Members, projections.MailGroupMember{
			UserID:   friend.UserID,
			Name:     friend.Name,
			Position: i,
		})
	}
	return append([]projections.MailGroup{friendGroup}, groups...), nil
}
func (c *Core) GetDirectMessageSettings(userID string) (*projections.DirectMessageSettings, error) {
	return projections.GetDirectMessageSettings(c.DB, userID)
}
func (c *Core) ListDirectMessageConversations(userID string, limit, offset int) ([]projections.DirectMessageConversation, error) {
	return projections.ListDirectMessageConversations(c.DB, userID, limit, offset)
}
func (c *Core) ListDirectMessages(userID, otherUserID string, limit, offset int) ([]projections.DirectMessage, error) {
	return projections.ListDirectMessages(c.DB, userID, otherUserID, limit, offset)
}
func (c *Core) CountUnreadDirectMessages(userID string) (int, error) {
	return projections.CountUnreadDirectMessages(c.DB, userID)
}
func (c *Core) ListSocialUsers(userID, list string, onlineOnly bool) ([]projections.SocialUser, error) {
	return projections.ListSocialUsers(c.DB, userID, list, onlineOnly)
}

func (c *Core) ListFavoriteBoards(userID string) ([]projections.Board, error) {
	return projections.ListFavoriteBoards(c.DB, userID)
}
func (c *Core) ListFavoriteTree(userID string) (*projections.FavoriteTree, error) {
	return projections.ListFavoriteTree(c.DB, userID)
}
func (c *Core) ImportFavoriteTree(userID string, tree *projections.FavoriteTree, replace bool) (*projections.FavoriteTree, error) {
	if err := importFavoriteTree(c.DB, userID, tree, replace); err != nil {
		return nil, err
	}
	return c.ListFavoriteTree(userID)
}
func (c *Core) ListThreads(board string, limit, offset int) ([]projections.Thread, error) {
	return projections.ListThreads(c.DB, board, limit, offset)
}
func (c *Core) ListThreadSummaries(userID, board string, limit, offset int, unreadOnly bool) ([]projections.ThreadSummary, error) {
	return projections.ListThreadSummariesFiltered(c.DB, userID, board, "", "", limit, offset, unreadOnly)
}
func (c *Core) ListThreadSummariesFiltered(userID, board, titleQuery, authorQuery string, limit, offset int, unreadOnly bool) ([]projections.ThreadSummary, error) {
	return projections.ListThreadSummariesFiltered(c.DB, userID, board, titleQuery, authorQuery, limit, offset, unreadOnly)
}
func (c *Core) ListUnreadThreadSummaries(actor *projections.User, favoritesOnly bool, folderID string, limit, offset int) ([]projections.ThreadSummary, error) {
	rows, err := projections.UnreadThreadSummaryStatsRowCount(c.DB)
	if err != nil {
		return nil, err
	}
	if rows > 0 {
		return projections.ListUnreadThreadSummariesMaterialized(c.DB, actor.ID, actor.IsMod(), favoritesOnly, folderID, limit, offset)
	}
	return projections.ListUnreadThreadSummaries(c.DB, actor.ID, actor.IsMod(), favoritesOnly, folderID, limit, offset)
}
func (c *Core) GetThread(id string) (*projections.Thread, error) {
	return projections.GetThread(c.DB, id)
}
func (c *Core) ListPosts(thread string, limit, offset int) ([]projections.Post, error) {
	posts, err := projections.ListPosts(c.DB, thread, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}
func (c *Core) ListReplyTreePosts(rootPostID string, limit, offset int) ([]projections.Post, error) {
	posts, err := projections.ListReplyTreePosts(c.DB, rootPostID, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}
func (c *Core) GetPostAttachment(attachmentID string) (*projections.PostAttachment, error) {
	return projections.GetPostAttachment(c.DB, attachmentID)
}
func (c *Core) GetAttachmentBlob(attachmentID string) ([]byte, string, error) {
	return projections.GetAttachmentBlob(c.DB, attachmentID)
}
func (c *Core) StagePostAttachmentBlob(attachmentID, actorID string, data []byte, contentType string) error {
	return projections.StageAttachmentBlob(c.DB, projections.StagedBlobPostAttachment, attachmentID, actorID, data, contentType, nowMS()+int64(time.Hour.Milliseconds()))
}
func IsStagedAttachmentBlobConflict(err error) bool {
	return errors.Is(err, projections.ErrStagedAttachmentBlobConflict)
}
func (c *Core) DiscardStagedPostAttachmentBlob(attachmentID string) error {
	return projections.DiscardStagedAttachmentBlob(c.DB, projections.StagedBlobPostAttachment, attachmentID)
}
func (c *Core) StoreAttachmentBlob(attachmentID string, data []byte, contentType string) error {
	return projections.StoreAttachmentBlob(c.DB, attachmentID, data, contentType)
}
func (c *Core) GetMailAttachment(attachmentID string) (*projections.MailAttachment, error) {
	return projections.GetMailAttachment(c.DB, attachmentID)
}
func (c *Core) GetMailAttachmentBlob(attachmentID string) ([]byte, string, error) {
	return projections.GetMailAttachmentBlob(c.DB, attachmentID)
}
func (c *Core) StageMailAttachmentBlob(attachmentID, actorID string, data []byte, contentType string) error {
	return projections.StageAttachmentBlob(c.DB, projections.StagedBlobMailAttachment, attachmentID, actorID, data, contentType, nowMS()+int64(time.Hour.Milliseconds()))
}
func (c *Core) DiscardStagedMailAttachmentBlob(attachmentID string) error {
	return projections.DiscardStagedAttachmentBlob(c.DB, projections.StagedBlobMailAttachment, attachmentID)
}
func (c *Core) StoreMailAttachmentBlob(attachmentID string, data []byte, contentType string) error {
	return projections.StoreMailAttachmentBlob(c.DB, attachmentID, data, contentType)
}
func (c *Core) PruneExpiredAttachmentBlobStaging(limit int) (int64, error) {
	return projections.PruneExpiredStagedAttachmentBlobs(c.DB, nowMS(), limit)
}
func (c *Core) GetPost(id string) (*projections.Post, error) {
	post, err := projections.GetPost(c.DB, id)
	if err != nil || post == nil {
		return post, err
	}
	if err := c.applyCounterStorePostCount(post); err != nil {
		return nil, err
	}
	return post, nil
}
func (c *Core) SearchPosts(query, boardID string, limit int) ([]projections.Post, error) {
	if c.postSearchIndex != nil {
		ids, err := c.postSearchIndex.Search(context.Background(), query, boardID, limit)
		if err != nil {
			return nil, err
		}
		posts, err := projections.HydrateSearchPostIDs(c.DB, nil, ids, boardID, limit, false)
		if err != nil {
			return nil, err
		}
		if err := c.applyCounterStorePostCounts(posts); err != nil {
			return nil, err
		}
		return posts, nil
	}
	posts, err := projections.SearchPosts(c.DB, query, boardID, limit)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Core) SearchReadablePosts(actor *projections.User, query, boardID string, limit int) ([]projections.Post, error) {
	if c.postSearchIndex != nil {
		ids, err := c.postSearchIndex.Search(context.Background(), query, boardID, limit)
		if err != nil {
			return nil, err
		}
		posts, err := projections.HydrateSearchPostIDs(c.DB, actor, ids, boardID, limit, true)
		if err != nil {
			return nil, err
		}
		if err := c.applyCounterStorePostCounts(posts); err != nil {
			return nil, err
		}
		return posts, nil
	}
	viewer := readmodel.ViewerScopeForUser(actor)
	posts, err := projections.SearchReadablePosts(c.DB, viewer.UserID, viewer.IncludePrivate, query, boardID, limit)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Core) ListPostsByAuthor(name string, limit, offset int) ([]projections.Post, error) {
	posts, err := projections.ListPostsByAuthor(c.DB, name, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Core) ListReadablePostsByAuthor(actor *projections.User, name string, limit, offset int) ([]projections.Post, error) {
	posts, err := projections.ListReadablePostsByAuthor(c.DB, actor.ID, actor.IsMod(), name, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Core) ListResidentBoardPosts(userID string, limit, offset int) ([]projections.Post, error) {
	var (
		posts []projections.Post
		err   error
	)
	if rows, err := projections.ResidentFeedMaterializedRowCount(c.DB); err == nil && rows > 0 {
		posts, err = projections.ListResidentBoardPostsMaterialized(c.DB, userID, limit, offset)
	} else {
		posts, err = projections.ListResidentBoardPosts(c.DB, userID, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Core) ListLatestFeedPosts(actor *projections.User, limit, offset int) ([]projections.Post, error) {
	viewer := readmodel.ViewerScopeForUser(actor)
	limit, offset = readmodel.NormalizePagination(limit, offset, 30, 100)
	cacheKey, cacheOK := c.latestFeedCacheKey(viewer.UserID, viewer.IncludePrivate, limit, offset)
	if cacheOK {
		if posts, ok, err := c.readCache.GetLatestFeedPosts(context.Background(), cacheKey); err == nil && ok {
			if err := c.applyCounterStorePostCounts(posts); err != nil {
				return nil, err
			}
			return posts, nil
		}
	}
	var (
		posts []projections.Post
		err   error
	)
	if rows, err := projections.LatestFeedMaterializedRowCount(c.DB); err == nil && rows > 0 {
		posts, err = projections.ListLatestFeedPostsMaterialized(c.DB, viewer.UserID, viewer.IncludePrivate, limit, offset)
	} else {
		posts, err = projections.ListLatestFeedPosts(c.DB, viewer.UserID, viewer.IncludePrivate, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	if err := c.applyCounterStorePostCounts(posts); err != nil {
		return nil, err
	}
	if cacheOK {
		_ = c.readCache.PutLatestFeedPosts(context.Background(), cacheKey, posts)
	}
	return posts, nil
}

func (c *Core) latestFeedCacheKey(viewerID string, includePrivate bool, limit, offset int) (readmodel.LatestFeedKey, bool) {
	if c == nil || c.readCache == nil {
		return readmodel.LatestFeedKey{}, false
	}
	appliedSeq, err := c.DerivedViewAppliedSeq(projections.DerivedViewLatestFeed)
	if err != nil {
		return readmodel.LatestFeedKey{}, false
	}
	headSeq, err := c.Head()
	if err != nil {
		return readmodel.LatestFeedKey{}, false
	}
	return readmodel.NewLatestFeedKey(viewerID, includePrivate, limit, offset, appliedSeq, headSeq), true
}

func (c *Core) ListBoardDeletedPosts(boardID, kind string, limit, offset int) ([]projections.PostDeletion, error) {
	return projections.ListBoardDeletedPosts(c.DB, boardID, kind, limit, offset)
}

// AuditLog returns recent durable events (mod/admin use).
func (c *Core) AuditLog(after int64, limit int) ([]*proto.Event, error) {
	return replayEvents(c.DB, after, nil, limit)
}

// ── M8: Notifications ───────────────────────────────────────────────────────

func (c *Core) ListNotifications(userID string, limit, offset int, unreadOnly bool) ([]projections.Notification, error) {
	return projections.ListNotifications(c.DB, userID, limit, offset, unreadOnly)
}
func (c *Core) CountUnreadNotifications(userID string) (int, error) {
	return projections.CountUnreadNotifications(c.DB, userID)
}
func (c *Core) MarkNotificationRead(id, userID string) error {
	return projections.MarkNotificationRead(c.DB, id, userID)
}
func (c *Core) MarkAllNotificationsRead(userID string) error {
	return projections.MarkAllNotificationsRead(c.DB, userID)
}
func (c *Core) DeleteNotification(id, userID string) error {
	return projections.DeleteNotification(c.DB, id, userID)
}
func (c *Core) DeleteReadNotifications(userID string) error {
	return projections.DeleteReadNotifications(c.DB, userID)
}
func (c *Core) DeleteAllNotifications(userID string) error {
	return projections.DeleteAllNotifications(c.DB, userID)
}

// ── M9: Trust levels ────────────────────────────────────────────────────────

func (c *Core) TrustInfo(userID string) (*projections.TrustLevelInfo, error) {
	return projections.TrustInfo(c.DB, userID)
}

// --- Moderation projections ---

func (c *Core) ListModerationReviews(status string, limit, offset int) ([]projections.ModerationReview, error) {
	return projections.ListModerationReviews(c.DB, status, limit, offset)
}

func (c *Core) ListContentFilters(scope string, includeInactive bool, limit, offset int) ([]projections.ContentFilter, error) {
	return projections.ListContentFilters(c.DB, scope, includeInactive, limit, offset)
}

func (c *Core) ListBoardAutomodRules(boardID string) ([]projections.BoardAutomodRule, error) {
	return projections.ListBoardAutomodRules(c.DB, boardID)
}

func (c *Core) ListBoardAutomodActivity(boardID string, limit, offset int) ([]projections.BoardAutomodActivity, error) {
	return projections.ListBoardAutomodActivity(c.DB, boardID, limit, offset)
}

// UserCanModerateBoard reports whether a user may moderate a board at all: site
// moderators/admins always can, as can a board's moderators or members with a
// post or thread moderation capability.
func (c *Core) UserCanModerateBoard(userID, role, boardID string) (bool, error) {
	return projections.ActorCanModerateBoardContent(c.DB, &projections.User{ID: userID, Role: role}, boardID)
}

func (c *Core) ListUserSanctions(userID string, limit, offset int) ([]projections.UserSanction, error) {
	return projections.ListUserSanctions(c.DB, userID, limit, offset)
}

// RebuildProjectionsFromEventLog truncates projection tables and replays all durable
// events from the given sequence onward to rebuild event-derived state.
func (c *Core) RebuildProjectionsFromEventLog(fromSeq int64) error {
	return rebuildProjectionsFromEventLog(c.DB, fromSeq)
}

// RebuildProjectionsFromEventStore truncates projection tables and replays all
// durable events from an EventStore. Shadow broker logs use this to prove they
// can rebuild the same SQL projections before becoming authoritative.
func (c *Core) RebuildProjectionsFromEventStore(ctx context.Context, store EventStore, fromSeq int64) error {
	return rebuildProjectionsFromEventStore(ctx, c.DB, store, fromSeq)
}

// CheckEventLogPromotionReadiness compares the current SQL event log with a
// candidate partitioned event log before the candidate is used as a rebuild or
// promotion source.
func (c *Core) CheckEventLogPromotionReadiness(ctx context.Context, candidate EventStore, replayLimit, partitionLimit int) (EventLogPromotionReadinessReport, error) {
	if c == nil || c.DB == nil {
		return EventLogPromotionReadinessReport{}, fmt.Errorf("event log promotion readiness: nil core db")
	}
	primary := NewSQLEventStore(c.DB)
	return CheckEventLogPromotionReadiness(ctx, EventLogPromotionReadinessConfig{
		Primary:        primary,
		Candidate:      candidate,
		ReplayLimit:    replayLimit,
		PartitionLimit: partitionLimit,
	})
}
