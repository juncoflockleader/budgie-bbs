package kafkaconn

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaCommandRecordHydratesBudgieOffsetFromKafkaOffset(t *testing.T) {
	partition := core.LogPartition{Kind: "board", Key: "general"}
	produce, err := NewKafkaCommandRecord(DefaultCommandTopic, core.BrokerCommandRecord{
		ActorID:       "usr_alice",
		CID:           "cid-kafka-command",
		Command:       proto.CmdCreateThread,
		Payload:       json.RawMessage(`{"board":"general","title":"Kafka command"}`),
		EnqueuedAt:    1234,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		t.Fatalf("NewKafkaCommandRecord: %v", err)
	}
	if produce.Topic != DefaultCommandTopic {
		t.Fatalf("topic = %q, want %q", produce.Topic, DefaultCommandTopic)
	}
	if string(produce.Key) != LogicalPartitionKey(partition) {
		t.Fatalf("key = %q, want logical partition key", string(produce.Key))
	}
	if strings.Contains(string(produce.Value), `"offset"`) {
		t.Fatalf("encoded kafka command should not contain preassigned offset: %s", produce.Value)
	}

	produced := *produce
	produced.Partition = 7
	produced.Offset = 41
	message, position, err := DecodeKafkaCommandRecord(&produced)
	if err != nil {
		t.Fatalf("DecodeKafkaCommandRecord: %v", err)
	}
	if position.Backend != "kafka" || position.Topic != DefaultCommandTopic || position.PhysicalPartition != 7 || position.PhysicalOffset != 41 || position.CommitOffset != 42 {
		t.Fatalf("position = %+v, want kafka topic partition 7 offset 41 commit 42", position)
	}
	if position.LogicalPartition != partition.Normalize() || position.LogicalOffset != 42 {
		t.Fatalf("logical position = %+v/%d, want board/general offset 42", position.LogicalPartition, position.LogicalOffset)
	}
	command, commandPosition, err := DecodeKafkaCommandLogRecord(&produced)
	if err != nil {
		t.Fatalf("DecodeKafkaCommandLogRecord: %v", err)
	}
	if command.SourcePosition != position || commandPosition != position {
		t.Fatalf("command source position = %+v / %+v, want %+v", command.SourcePosition, commandPosition, position)
	}
	if err := command.SourcePosition.ValidateForRecord(command); err != nil {
		t.Fatalf("source position ValidateForRecord: %v", err)
	}
	decoded, err := core.DecodeBrokerCommandMessage(message)
	if err != nil {
		t.Fatalf("DecodeBrokerCommandMessage: %v", err)
	}
	if decoded.Offset != 42 || decoded.Partition != partition.Normalize() || decoded.CID != "cid-kafka-command" {
		t.Fatalf("decoded command = %+v, want offset 42 partition board/general cid", decoded)
	}
}

func TestKafkaCommandRecordRejectsMismatchedKey(t *testing.T) {
	partition := core.LogPartition{Kind: "board", Key: "general"}
	produce, err := NewKafkaCommandRecord(DefaultCommandTopic, core.BrokerCommandRecord{
		ActorID:       "usr_alice",
		Command:       proto.CmdCreateThread,
		Payload:       json.RawMessage(`{"board":"general","title":"Kafka command"}`),
		EnqueuedAt:    1234,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		t.Fatalf("NewKafkaCommandRecord: %v", err)
	}
	produce.Key = []byte(LogicalPartitionKey(core.LogPartition{Kind: "board", Key: "other"}))
	_, _, err = DecodeKafkaCommandRecord(produce)
	requireErrorContains(t, err, "does not match partition board/general")
}

func TestKafkaCommandRecordRejectsInvalidProduceRecord(t *testing.T) {
	_, err := NewKafkaCommandRecord(DefaultCommandTopic, core.BrokerCommandRecord{
		Command:       proto.CmdCreateThread,
		Payload:       json.RawMessage(`not-json`),
		PartitionKind: "board",
		PartitionKey:  "general",
	})
	requireErrorContains(t, err, "invalid payload")
	_, _, err = DecodeKafkaCommandRecord(&kgo.Record{
		Topic:     DefaultCommandTopic,
		Partition: 1,
		Offset:    -1,
		Value:     []byte(`{}`),
	})
	requireErrorContains(t, err, "negative record offset")
}
