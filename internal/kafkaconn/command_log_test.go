package kafkaconn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestCommandLogProducePreservesKafkaSourcePosition(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := kafkaPhysicalPartitionForTest(t, partition, 16)
	client := &fakeCommandLogClient{appendPhysicalPartition: physical, appendPhysicalOffset: 40}
	log := NewCommandLog(client, CommandLogOptions{
		CommandTopic:   "budgie.commands",
		ConsumerGroup:  "budgie-writers",
		PartitionCount: 16,
	})

	record, err := log.Produce(ctx, core.CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        "cid-kafka-produce",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"Kafka"}`),
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if client.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", client.appendCalls)
	}
	if client.appendRecord.Topic != "budgie.commands" || string(client.appendRecord.Key) != LogicalPartitionKey(partition) {
		t.Fatalf("append record topic/key = %s/%s, want budgie.commands/%s",
			client.appendRecord.Topic, string(client.appendRecord.Key), LogicalPartitionKey(partition))
	}
	if record.Partition != partition || record.Offset != 41 || record.CID != "cid-kafka-produce" {
		t.Fatalf("record = %+v, want partition with logical offset 41 and cid", record)
	}
	wantSource := core.CommandLogSourcePosition{
		Backend:           "kafka",
		Topic:             "budgie.commands",
		PhysicalPartition: physical,
		PhysicalOffset:    40,
		CommitOffset:      41,
		LogicalPartition:  partition,
		LogicalOffset:     41,
	}
	if record.SourcePosition != wantSource {
		t.Fatalf("source position = %+v, want %+v", record.SourcePosition, wantSource)
	}
}

func TestCommandLogFetchFiltersLogicalPartitionAndKeepsSparseOffsets(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	other := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physical := kafkaPhysicalPartitionForTest(t, partition, 16)
	client := &fakeCommandLogClient{
		fetchRecords: []*kgo.Record{
			testKafkaCommandLogRecord(t, "budgie.commands", partition, physical, 3, "cid-before"),
			testKafkaCommandLogRecord(t, "budgie.commands", other, physical, 5, "cid-other"),
			testKafkaCommandLogRecord(t, "budgie.commands", partition, physical, 40, "cid-target-40"),
			testKafkaCommandLogRecord(t, "budgie.commands", partition, physical, 8, "cid-target-8"),
		},
	}
	log := NewCommandLog(client, CommandLogOptions{
		CommandTopic:   "budgie.commands",
		ConsumerGroup:  "budgie-writers",
		PartitionCount: 16,
	})

	records, err := log.FetchPartition(ctx, partition, 7, 10)
	if err != nil {
		t.Fatalf("FetchPartition: %v", err)
	}
	if client.fetchRequest.Topic != "budgie.commands" ||
		client.fetchRequest.Key != LogicalPartitionKey(partition) ||
		client.fetchRequest.PhysicalPartition != physical ||
		client.fetchRequest.AfterLogicalOffset != 7 ||
		client.fetchRequest.Limit != 10 {
		t.Fatalf("fetch request = %+v, want topic/key/physical/after/limit", client.fetchRequest)
	}
	gotOffsets := []int64{}
	for _, record := range records {
		gotOffsets = append(gotOffsets, record.Offset)
		if record.Partition != partition {
			t.Fatalf("fetched record partition = %+v, want %+v", record.Partition, partition)
		}
		if record.SourcePosition.IsZero() {
			t.Fatalf("fetched record missing source position: %+v", record)
		}
	}
	if !reflect.DeepEqual(gotOffsets, []int64{9, 41}) {
		t.Fatalf("fetched offsets = %v, want sparse logical offsets [9 41]", gotOffsets)
	}
}

func TestCommandLogCommitsPhysicalKafkaOffset(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := kafkaPhysicalPartitionForTest(t, partition, 16)
	client := &fakeCommandLogClient{committedOffset: 41}
	log := NewCommandLog(client, CommandLogOptions{
		CommandTopic:   "budgie.commands",
		ConsumerGroup:  "budgie-writers",
		PartitionCount: 16,
	})

	if err := log.CommitPartition(ctx, partition, 41); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}
	if client.commit.ConsumerGroup != "budgie-writers" ||
		client.commit.Topic != "budgie.commands" ||
		client.commit.PhysicalPartition != physical ||
		client.commit.Key != LogicalPartitionKey(partition) ||
		client.commit.Offset != 41 ||
		client.commit.LogicalPartition != partition ||
		client.commit.LogicalOffset != 41 {
		t.Fatalf("commit = %+v, want physical Kafka offset with logical evidence", client.commit)
	}

	offset, err := log.CommittedOffset(ctx, partition)
	if err != nil {
		t.Fatalf("CommittedOffset: %v", err)
	}
	if offset != 41 {
		t.Fatalf("committed offset = %d, want 41", offset)
	}
	if client.committedPartition.Topic != "budgie.commands" ||
		client.committedPartition.Key != LogicalPartitionKey(partition) ||
		client.committedPartition.PhysicalPartition != physical ||
		client.committedPartition.Partition != partition {
		t.Fatalf("committed partition request = %+v, want physical Kafka partition", client.committedPartition)
	}
}

func TestCommandLogRequiresPartitionCountForPhysicalOperations(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}
	log := NewCommandLog(&fakeCommandLogClient{}, CommandLogOptions{CommandTopic: "budgie.commands"})

	_, err := log.FetchPartition(ctx, partition, 0, 10)
	requireErrorContains(t, err, "partition count is required")
	requireErrorContains(t, log.CommitPartition(ctx, partition, 1), "partition count is required")
	_, err = log.CommittedOffset(ctx, partition)
	requireErrorContains(t, err, "partition count is required")
}

func TestCommandLogListPartitionsDelegatesCandidates(t *testing.T) {
	ctx := context.Background()
	partitions := []core.LogPartition{{Kind: "board", Key: "general"}}
	lister := commandPartitionListerFunc(func(ctx context.Context, limit int) ([]core.LogPartition, error) {
		if limit != 5 {
			t.Fatalf("limit = %d, want 5", limit)
		}
		return partitions, nil
	})
	log := NewCommandLog(&fakeCommandLogClient{}, CommandLogOptions{Candidates: lister})

	got, err := log.ListCommandPartitions(ctx, 5)
	if err != nil {
		t.Fatalf("ListCommandPartitions: %v", err)
	}
	if !reflect.DeepEqual(got, partitions) {
		t.Fatalf("partitions = %+v, want %+v", got, partitions)
	}

	_, err = NewCommandLog(&fakeCommandLogClient{}, CommandLogOptions{}).ListCommandPartitions(ctx, 5)
	requireErrorContains(t, err, "partition listing requires candidates")
}

type fakeCommandLogClient struct {
	appendCalls             int
	appendRecord            kgo.Record
	appendErr               error
	appendResult            *kgo.Record
	appendPhysicalPartition int32
	appendPhysicalOffset    int64
	fetchRequest            CommandFetchRequest
	fetchRecords            []*kgo.Record
	fetchErr                error
	commit                  CommandOffsetCommit
	commitErr               error
	committedPartition      CommandTopicPartition
	committedOffset         int64
	committedErr            error
}

func (c *fakeCommandLogClient) AppendCommandRecord(ctx context.Context, record *kgo.Record) (*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.appendCalls++
	if c.appendErr != nil {
		return nil, c.appendErr
	}
	if record == nil {
		return nil, errors.New("nil append record")
	}
	c.appendRecord = cloneKgoRecord(record)
	if c.appendResult != nil {
		result := cloneKgoRecord(c.appendResult)
		return &result, nil
	}
	result := cloneKgoRecord(record)
	result.Partition = c.appendPhysicalPartition
	result.Offset = c.appendPhysicalOffset
	return &result, nil
}

func (c *fakeCommandLogClient) FetchCommandRecords(ctx context.Context, request CommandFetchRequest) ([]*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.fetchRequest = request
	if c.fetchErr != nil {
		return nil, c.fetchErr
	}
	out := make([]*kgo.Record, 0, len(c.fetchRecords))
	for _, record := range c.fetchRecords {
		cloned := cloneKgoRecord(record)
		out = append(out, &cloned)
	}
	return out, nil
}

func (c *fakeCommandLogClient) CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.commit = commit
	return c.commitErr
}

func (c *fakeCommandLogClient) CommittedCommandOffset(ctx context.Context, partition CommandTopicPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.committedPartition = partition
	if c.committedErr != nil {
		return 0, c.committedErr
	}
	return c.committedOffset, nil
}

type commandPartitionListerFunc func(context.Context, int) ([]core.LogPartition, error)

func (f commandPartitionListerFunc) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	return f(ctx, limit)
}

func testKafkaCommandLogRecord(t *testing.T, topic string, partition core.LogPartition, physicalPartition int32, physicalOffset int64, cid string) *kgo.Record {
	t.Helper()
	record, err := NewKafkaCommandRecord(topic, core.BrokerCommandRecord{
		Version:       1,
		ActorID:       "usr_alice",
		CID:           cid,
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"Kafka"}`),
		EnqueuedAt:    1000,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		t.Fatalf("NewKafkaCommandRecord: %v", err)
	}
	record.Partition = physicalPartition
	record.Offset = physicalOffset
	return record
}

func kafkaPhysicalPartitionForTest(t *testing.T, partition core.LogPartition, partitionCount int32) int32 {
	t.Helper()
	physical, err := KafkaPartitionForLogicalPartition(partition, partitionCount)
	if err != nil {
		t.Fatalf("KafkaPartitionForLogicalPartition: %v", err)
	}
	return physical
}

func cloneKgoRecord(record *kgo.Record) kgo.Record {
	if record == nil {
		return kgo.Record{}
	}
	cloned := *record
	cloned.Key = append([]byte(nil), record.Key...)
	cloned.Value = append([]byte(nil), record.Value...)
	cloned.Headers = append([]kgo.RecordHeader(nil), record.Headers...)
	return cloned
}
