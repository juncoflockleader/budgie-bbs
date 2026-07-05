package logmodel

import "fmt"

// BrokerCommandLogMessage is a physical broker command-log message plus its
// logical command offset. StreamSeq is optional broker metadata used only for
// scalar diagnostics.
type BrokerCommandLogMessage struct {
	Partition Partition
	Offset    int64
	StreamSeq int64
	Data      []byte
}

func CloneBrokerCommandLogMessage(msg BrokerCommandLogMessage) BrokerCommandLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}

func DecodeBrokerCommandMessage(msg BrokerCommandLogMessage) (CommandLogRecord, error) {
	record, err := DecodeBrokerCommandRecord(msg.Data)
	if err != nil {
		return CommandLogRecord{}, err
	}
	partition := Partition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	if msg.Partition != (Partition{}) && msg.Partition.Normalize() != partition {
		return CommandLogRecord{}, fmt.Errorf("broker command message: partition metadata mismatch")
	}
	if msg.Offset > 0 && msg.Offset != record.Offset {
		return CommandLogRecord{}, fmt.Errorf("broker command message: offset metadata mismatch")
	}
	return CommandLogRecord{
		Partition:  partition,
		Offset:     record.Offset,
		ActorID:    record.ActorID,
		CID:        record.CID,
		Command:    record.Command,
		Payload:    append([]byte(nil), record.Payload...),
		EnqueuedAt: record.EnqueuedAt,
	}, nil
}

// BrokerEventLogMessage is a physical broker event-log message plus its logical
// event offset. StreamSeq is optional broker metadata used only for scalar
// diagnostics.
type BrokerEventLogMessage struct {
	Partition Partition
	Offset    int64
	StreamSeq int64
	Data      []byte
}

func CloneBrokerEventLogMessage(msg BrokerEventLogMessage) BrokerEventLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}
