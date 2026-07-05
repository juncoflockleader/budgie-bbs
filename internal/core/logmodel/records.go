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
