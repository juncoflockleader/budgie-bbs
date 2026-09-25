package logmodel

import (
	"encoding/json"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// CommandLogRecord is the durable command-log entry produced by gateways and
// consumed by the writer tier in the partitioned-log architecture.
type CommandLogRecord struct {
	Partition      Partition
	Offset         int64
	SourcePosition CommandLogSourcePosition
	ActorID        string
	CID            string
	Command        proto.CommandName
	Payload        json.RawMessage
	EnqueuedAt     int64
}

func SameCommandLogRecordIdentity(existing, requested CommandLogRecord) bool {
	return existing.Partition.Normalize() == requested.Partition.Normalize() &&
		existing.ActorID == requested.ActorID &&
		existing.CID == requested.CID &&
		existing.Command == requested.Command &&
		existing.EnqueuedAt == requested.EnqueuedAt &&
		string(existing.Payload) == string(requested.Payload)
}

// EventAppend is the logical event the writer tier appends after deciding a
// command. The log implementation assigns offsets for normal appends; shadow
// and replay paths can carry existing CompatibilitySeq/PartitionOffset/TS
// metadata when mirroring an already-durable event into a broker log.
type EventAppend struct {
	ID               string
	Kind             proto.EventKind
	Scopes           []string
	Payload          any
	CompatibilitySeq int64
	PartitionOffset  int64
	TS               int64
}

func EventAppendTimestamp(ts, fallback int64) int64 {
	if ts > 0 {
		return ts
	}
	return fallback
}

// CommandEventTransaction is the broker-native write unit for IS4: a writer
// consumes one command-log record, decides zero or more durable events, appends
// those events, and advances the consumed command offset through one transaction
// boundary.
type CommandEventTransaction struct {
	CommandPartition      Partition
	CommandOffset         int64
	CommandSourcePosition CommandLogSourcePosition
	Events                []EventAppend
}

type CommandEventTransactionResult struct {
	Events             []*proto.Event
	CommittedPartition Partition
	CommittedOffset    int64
}
