package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// LogPartition is the write-ordering key for command and event logs. IS4 moves
// ownership of these partitions to a broker consumer group; the Postgres path
// still derives the same key before taking a partition advisory lock.
type LogPartition = logmodel.Partition

func SortLogPartitions(partitions []LogPartition) {
	logmodel.SortPartitions(partitions)
}

func logPartitionFromEventPartition(p eventPartition) LogPartition {
	return LogPartition{Kind: p.Kind, Key: p.Key}.Normalize()
}

// CommandLogRecord is the durable command-log entry produced by gateways and
// consumed by the writer tier in the partitioned-log architecture.
type CommandLogRecord = logmodel.CommandLogRecord

// CommandLog is the IS4 command-log boundary. Implementations must preserve
// order within one partition and may process different partitions in parallel.
type CommandLog interface {
	Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error)
	FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error)
	CommitPartition(ctx context.Context, partition LogPartition, offset int64) error
	CommittedOffset(ctx context.Context, partition LogPartition) (int64, error)
}

// CommandLogRebalanceAllower is implemented by command logs whose fetch client
// gates consumer-group rebalances while a worker processes fetched records.
type CommandLogRebalanceAllower interface {
	AllowCommandLogRebalance()
}

// CommandPartitionLister exposes command-log partitions with pending or
// historical records. Writer workers use it to discover owned partitions.
type CommandPartitionLister interface {
	ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error)
}

// CommandPartitionOffsetLister exposes command-log tail and committed offsets
// so operators can see writer lag before promoting a partitioned command log.
type CommandPartitionOffsetLister interface {
	ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error)
}

// CommandPartitionAssigner models broker consumer-group ownership for command
// partitions. A worker may drain a partition only while the assigner says that
// worker owns the current assignment generation.
type CommandPartitionAssigner interface {
	AssignCommandPartition(ctx context.Context, ownerID string, partition LogPartition) (logmodel.CommandPartitionAssignment, bool, error)
}

// StableCommandPartitionAssigner marks deterministic assignment snapshots that
// cannot rebalance while a worker is draining one partition. Workers can skip
// per-record ownership heartbeats for these assigners when no claim store is
// configured.
type StableCommandPartitionAssigner interface {
	StableCommandPartitionAssignment() bool
}

// CommandPartitionAssignmentLister is implemented by broker-native assignment
// adapters that can list the partitions currently owned by one writer. Workers
// use it to avoid scanning every known command partition when a consumer-group
// rebalance has already produced an owned partition set.
type CommandPartitionAssignmentLister interface {
	ListAssignedCommandPartitions(ctx context.Context, ownerID string, limit int) ([]logmodel.CommandPartitionAssignment, error)
}

// CommandPartitionClaimer owns short-lived writer leases for command-log
// partitions. It is the bridge between the current poll/drain worker and the
// eventual broker consumer-group partition assignment model.
type CommandPartitionClaimer interface {
	ClaimCommandPartition(ctx context.Context, ownerID string, partition LogPartition, ttl time.Duration) (logmodel.CommandPartitionClaim, bool, error)
}

// EventAppend is the logical event the writer tier appends after deciding a
// command. The log implementation assigns offsets for normal appends; shadow
// and replay paths can carry existing CompatibilitySeq/PartitionOffset/TS
// metadata when mirroring an already-durable event into a broker log.
type EventAppend = logmodel.EventAppend

// EventStore is the durable event log contract. The current SQL implementation
// preserves global seq compatibility while also exposing partition replay; IS4
// implementations can make partition offsets authoritative behind this shape.
type EventStore interface {
	Append(ctx context.Context, event EventAppend) (*proto.Event, error)
	Head(ctx context.Context) (int64, error)
	Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error)
	ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error)
}

// CommandEventTransactionStore is the final promotion boundary for broker-owned
// writes. Redpanda/Kafka adapters must make CommitCommandEvents atomic or
// provide equivalent idempotent replay semantics for the append/commit gap.
type CommandEventTransactionStore interface {
	CommitCommandEvents(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error)
}

// CommandEventTransactionBatchStore is an optional promotion boundary for
// draining multiple logical command partitions through one broker transaction
// where the backend can provide equivalent idempotent append/commit semantics.
type CommandEventTransactionBatchStore interface {
	CommitCommandEventBatch(ctx context.Context, txs []logmodel.CommandEventTransaction) ([]logmodel.CommandEventTransactionResult, error)
}

// EventPartitionLister exposes the partitions that currently have durable
// events. Shadow/parity runners use it to choose replay windows without knowing
// which storage engine backs the event log.
type EventPartitionLister interface {
	ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error)
}

// EventPartitionOffsetLister exposes durable tail offsets for event
// partitions. Projection catch-up gates use it to distinguish a short broker
// read from a fully drained logical partition.
type EventPartitionOffsetLister interface {
	ListEventPartitionOffsets(ctx context.Context, limit int) ([]logmodel.EventPartitionOffset, error)
}

// EventPartitionOffsetSeeder is implemented by shadow stores that can start in
// tail-only mode by adopting the primary log's current partition offsets.
type EventPartitionOffsetSeeder interface {
	SeedEventPartitionOffset(ctx context.Context, partition LogPartition, offset int64) error
}

// ProjectionStore exposes rebuildable read models. It is intentionally broad:
// projection implementations can split this into smaller concrete stores while
// keeping API and transport code on the interface boundary.
type ProjectionStore interface {
	ListBoards(ctx context.Context) ([]projections.Board, error)
	GetBoard(ctx context.Context, id string) (*projections.Board, error)
	ListThreads(ctx context.Context, boardID string, limit, offset int) ([]projections.Thread, error)
	GetThread(ctx context.Context, id string) (*projections.Thread, error)
	ListPosts(ctx context.Context, threadID string, limit, offset int) ([]projections.Post, error)
}

// CommandReceiptStore owns command idempotency. Receipts are partition-scoped so
// retry dedup does not reintroduce a global coordination point.
type CommandReceiptStore interface {
	Load(ctx context.Context, partition LogPartition, actorID, cid, commandHash string) (result json.RawMessage, ok bool, conflict bool, err error)
	Record(ctx context.Context, partition LogPartition, actorID, cid, commandHash string, result json.RawMessage) error
}

// Migrator applies storage-specific migrations.
type Migrator interface {
	Apply(ctx context.Context) error
}
