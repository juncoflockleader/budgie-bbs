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

// BrokerCommandRecord is the broker-native representation of one gateway
// command. Offset is logical Budgie command-log state for one partition; broker
// stream offsets remain implementation details.
type BrokerCommandRecord struct {
	Version       int               `json:"v"`
	ActorID       string            `json:"actorId,omitempty"`
	CID           string            `json:"cid,omitempty"`
	Command       proto.CommandName `json:"command"`
	Payload       json.RawMessage   `json:"payload"`
	EnqueuedAt    int64             `json:"enqueuedAt"`
	PartitionKind string            `json:"partitionKind"`
	PartitionKey  string            `json:"partitionKey"`
	Offset        int64             `json:"offset"`
}

func SameCommandLogRecordIdentity(existing, requested CommandLogRecord) bool {
	return existing.Partition.Normalize() == requested.Partition.Normalize() &&
		existing.ActorID == requested.ActorID &&
		existing.CID == requested.CID &&
		existing.Command == requested.Command &&
		existing.EnqueuedAt == requested.EnqueuedAt &&
		string(existing.Payload) == string(requested.Payload)
}

func SameBrokerCommandRecordIdentity(existing, requested BrokerCommandRecord) bool {
	return existing.ActorID == requested.ActorID &&
		existing.CID == requested.CID &&
		existing.Command == requested.Command &&
		existing.EnqueuedAt == requested.EnqueuedAt &&
		existing.PartitionKind == requested.PartitionKind &&
		existing.PartitionKey == requested.PartitionKey &&
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

// BrokerEventRecord is the broker-native representation of one durable event.
// PartitionOffset is logical Budgie state, not necessarily the broker's stream
// sequence. This lets shadow/parity compare against SQL partition offsets while
// each broker remains free to expose its own physical offsets.
type BrokerEventRecord struct {
	Version          int             `json:"v"`
	ID               string          `json:"id,omitempty"`
	Kind             proto.EventKind `json:"event"`
	CompatibilitySeq int64           `json:"seq,omitempty"`
	Scopes           []string        `json:"scopes,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	TS               int64           `json:"ts"`
	PartitionKind    string          `json:"partitionKind"`
	PartitionKey     string          `json:"partitionKey"`
	PartitionOffset  int64           `json:"partitionOffset"`
}

func SameBrokerEventRecordIdentity(existing, requested BrokerEventRecord) bool {
	if existing.ID != requested.ID ||
		existing.Kind != requested.Kind ||
		existing.CompatibilitySeq != requested.CompatibilitySeq ||
		existing.TS != requested.TS ||
		existing.PartitionKind != requested.PartitionKind ||
		existing.PartitionKey != requested.PartitionKey ||
		string(existing.Payload) != string(requested.Payload) {
		return false
	}
	if len(existing.Scopes) != len(requested.Scopes) {
		return false
	}
	for i := range existing.Scopes {
		if existing.Scopes[i] != requested.Scopes[i] {
			return false
		}
	}
	return true
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
