package logmodel

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestDecodeBrokerCommandMessage(t *testing.T) {
	data, err := EncodeBrokerCommandRecord(BrokerCommandRecord{
		Version:       BrokerCommandRecordVersion,
		ActorID:       "usr_alice",
		CID:           "cid_1",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt:    1000,
		PartitionKind: PartitionBoard,
		PartitionKey:  "general",
		Offset:        7,
	})
	if err != nil {
		t.Fatalf("EncodeBrokerCommandRecord: %v", err)
	}
	record, err := DecodeBrokerCommandMessage(BrokerCommandLogMessage{
		Partition: Partition{Kind: PartitionBoard, Key: "general"},
		Offset:    7,
		Data:      data,
	})
	if err != nil {
		t.Fatalf("DecodeBrokerCommandMessage: %v", err)
	}
	if record.Partition != (Partition{Kind: PartitionBoard, Key: "general"}) || record.Offset != 7 || record.CID != "cid_1" {
		t.Fatalf("decoded record = %+v", record)
	}
	if string(record.Payload) != `{"board":"general","title":"General"}` {
		t.Fatalf("decoded payload = %s", record.Payload)
	}
}

func TestDecodeBrokerCommandMessageRejectsMetadataMismatch(t *testing.T) {
	data, err := EncodeBrokerCommandRecord(BrokerCommandRecord{
		Version:       BrokerCommandRecordVersion,
		ActorID:       "usr_alice",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt:    1000,
		PartitionKind: PartitionBoard,
		PartitionKey:  "general",
		Offset:        7,
	})
	if err != nil {
		t.Fatalf("EncodeBrokerCommandRecord: %v", err)
	}
	if _, err := DecodeBrokerCommandMessage(BrokerCommandLogMessage{
		Partition: Partition{Kind: PartitionBoard, Key: "other"},
		Offset:    7,
		Data:      data,
	}); err == nil {
		t.Fatal("DecodeBrokerCommandMessage accepted wrong partition metadata")
	}
	if _, err := DecodeBrokerCommandMessage(BrokerCommandLogMessage{
		Partition: Partition{Kind: PartitionBoard, Key: "general"},
		Offset:    8,
		Data:      data,
	}); err == nil {
		t.Fatal("DecodeBrokerCommandMessage accepted wrong offset metadata")
	}
}

func TestBrokerEventSequencePrefersCompatibilitySeq(t *testing.T) {
	if got := BrokerEventSequence(BrokerEventRecord{CompatibilitySeq: 42}, BrokerEventLogMessage{StreamSeq: 100}); got != 42 {
		t.Fatalf("BrokerEventSequence with compatibility seq = %d, want 42", got)
	}
	if got := BrokerEventSequence(BrokerEventRecord{}, BrokerEventLogMessage{StreamSeq: 100}); got != 100 {
		t.Fatalf("BrokerEventSequence fallback = %d, want 100", got)
	}
}
