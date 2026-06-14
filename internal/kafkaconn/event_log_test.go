package kafkaconn

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestEventLogReplayPartitionReadsKafkaEventRecords(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partitionCount := int32(16)
	target := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	other := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physical := kafkaPhysicalPartitionForTest(t, target, partitionCount)
	client := &fakeEventLogClient{
		fetchRecords: []*kgo.Record{
			testKafkaEventLogRecord(t, topic, target, physical, 10, 1, 101, "evt_before"),
			testKafkaEventLogRecord(t, topic, other, physical, 11, 1, 102, "evt_other"),
			testKafkaEventLogRecord(t, topic, target, physical, 12, 3, 104, "evt_target_3"),
			testKafkaEventLogRecord(t, topic, target, physical, 13, 2, 103, "evt_target_2"),
		},
	}
	store := NewEventStore(client, EventLogOptions{
		EventTopic:     topic,
		PartitionCount: partitionCount,
		Partitions:     eventPartitionListerFunc(func(context.Context, int) ([]core.LogPartition, error) { return []core.LogPartition{target}, nil }),
		Head:           eventLogHeadReaderFunc(func(context.Context) (int64, error) { return 104, nil }),
	})

	events, err := store.ReplayPartition(ctx, target.Kind, target.Key, 1, 10)
	if err != nil {
		t.Fatalf("ReplayPartition: %v", err)
	}
	if client.fetchRequest.Topic != topic ||
		client.fetchRequest.Key != LogicalPartitionKey(target) ||
		client.fetchRequest.PhysicalPartition != physical ||
		client.fetchRequest.AfterLogicalOffset != 1 ||
		client.fetchRequest.Limit != 10 {
		t.Fatalf("fetch request = %+v, want target topic/key/physical/after/limit", client.fetchRequest)
	}
	gotOffsets := []int64{}
	gotSeqs := []int64{}
	for _, event := range events {
		gotOffsets = append(gotOffsets, event.PartitionOffset)
		gotSeqs = append(gotSeqs, event.Seq)
		if event.PartitionKind != target.Kind || event.PartitionKey != target.Key {
			t.Fatalf("event partition = %s/%s, want %s/%s", event.PartitionKind, event.PartitionKey, target.Kind, target.Key)
		}
	}
	if !reflect.DeepEqual(gotOffsets, []int64{2, 3}) {
		t.Fatalf("partition offsets = %v, want [2 3]", gotOffsets)
	}
	if !reflect.DeepEqual(gotSeqs, []int64{103, 104}) {
		t.Fatalf("seqs = %v, want compatibility seqs [103 104]", gotSeqs)
	}
}

func TestEventLogReplayPartitionCanKeepPartitionOnlyEventsScalarless(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partitionCount := int32(16)
	target := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := kafkaPhysicalPartitionForTest(t, target, partitionCount)
	client := &fakeEventLogClient{
		fetchRecords: []*kgo.Record{
			testKafkaEventLogRecord(t, topic, target, physical, 12, 1, 0, "evt_partition_only"),
		},
	}
	store := NewEventStore(client, EventLogOptions{
		EventTopic:                  topic,
		PartitionCount:              partitionCount,
		Partitions:                  eventPartitionListerFunc(func(context.Context, int) ([]core.LogPartition, error) { return []core.LogPartition{target}, nil }),
		DisableKafkaOffsetStreamSeq: true,
	})

	events, err := store.ReplayPartition(ctx, target.Kind, target.Key, 0, 10)
	if err != nil {
		t.Fatalf("ReplayPartition: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want one partition-only event", len(events))
	}
	if events[0].Seq != 0 {
		t.Fatalf("event seq = %d, want no synthesized scalar sequence", events[0].Seq)
	}
}

func TestEventLogListPartitionsAndHeadDelegateDurablePositionReaders(t *testing.T) {
	ctx := context.Background()
	partitions := []core.LogPartition{{Kind: "board", Key: "general"}}
	lister := eventPartitionListerFunc(func(ctx context.Context, limit int) ([]core.LogPartition, error) {
		if limit != 5 {
			t.Fatalf("limit = %d, want 5", limit)
		}
		return partitions, nil
	})
	head := eventLogHeadReaderFunc(func(context.Context) (int64, error) { return 42, nil })
	log := NewEventLog(&fakeEventLogClient{}, EventLogOptions{
		Partitions: lister,
		Head:       head,
	})

	got, err := log.ListEventPartitions(ctx, 5)
	if err != nil {
		t.Fatalf("ListEventPartitions: %v", err)
	}
	if !reflect.DeepEqual(got, partitions) {
		t.Fatalf("partitions = %+v, want %+v", got, partitions)
	}
	gotHead, err := log.Head(ctx)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if gotHead != 42 {
		t.Fatalf("head = %d, want 42", gotHead)
	}

	_, err = NewEventLog(&fakeEventLogClient{}, EventLogOptions{}).ListEventPartitions(ctx, 5)
	if err == nil || !strings.Contains(err.Error(), "partition listing requires event position reader") {
		t.Fatalf("ListEventPartitions without lister err = %v, want requirement", err)
	}
	_, err = log.AppendEvent(ctx, partitions[0], core.BrokerEventRecord{})
	if err == nil || !strings.Contains(err.Error(), "append requires command/event transaction") {
		t.Fatalf("AppendEvent err = %v, want transaction-only append guard", err)
	}
}

func TestFranzEventLogShadowClientAppendsPositionedSQLMirrorEvent(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	record := core.BrokerEventRecord{
		Version:          1,
		ID:               "evt_sql_shadow",
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: 42,
		Scopes:           []string{"board:general"},
		Payload:          []byte(`{"id":"thr_shadow","board":"general","author":"alice","authorID":"usr_alice","title":"Kafka Shadow","ts":1234}`),
		TS:               1234,
		PartitionKind:    partition.Kind,
		PartitionKey:     partition.Key,
		PartitionOffset:  7,
	}
	produced, err := NewKafkaEventRecord(topic, record)
	if err != nil {
		t.Fatalf("NewKafkaEventRecord: %v", err)
	}
	produced.Partition = 3
	produced.Offset = 99
	runtime := &fakeFranzCommandLogRuntime{
		produceResults: kgo.ProduceResults{{Record: produced}},
	}
	client := newFranzEventLogShadowClient(runtime, &fakeEventLogClient{}, EventLogOptions{
		EventTopic:     topic,
		PartitionCount: 16,
	})

	msg, err := client.AppendEvent(ctx, partition, record)
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if runtime.produceCalls != 1 || len(runtime.produced) != 1 {
		t.Fatalf("produce calls=%d records=%d, want one produced event", runtime.produceCalls, len(runtime.produced))
	}
	if runtime.produced[0].Topic != topic || string(runtime.produced[0].Key) != LogicalPartitionKey(partition) {
		t.Fatalf("produced record = topic %q key %q, want topic/key mirror", runtime.produced[0].Topic, string(runtime.produced[0].Key))
	}
	if msg.Partition != partition || msg.Offset != 7 || msg.StreamSeq != 42 {
		t.Fatalf("message = %+v, want partition offset 7 compatibility seq 42", msg)
	}
}

func TestFranzEventLogShadowClientRequiresSQLPositionEvidence(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	base := core.BrokerEventRecord{
		Version:          1,
		ID:               "evt_sql_shadow",
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: 42,
		Scopes:           []string{"board:general"},
		Payload:          []byte(`{"id":"thr_shadow","board":"general","author":"alice","authorID":"usr_alice","title":"Kafka Shadow","ts":1234}`),
		TS:               1234,
		PartitionKind:    partition.Kind,
		PartitionKey:     partition.Key,
		PartitionOffset:  7,
	}
	tests := []struct {
		name string
		edit func(*core.BrokerEventRecord)
		want string
	}{
		{name: "id", edit: func(r *core.BrokerEventRecord) { r.ID = "" }, want: "event id"},
		{name: "timestamp", edit: func(r *core.BrokerEventRecord) { r.TS = 0 }, want: "timestamp"},
		{name: "seq", edit: func(r *core.BrokerEventRecord) { r.CompatibilitySeq = 0 }, want: "compatibility seq"},
		{name: "offset", edit: func(r *core.BrokerEventRecord) { r.PartitionOffset = 0 }, want: "partition offset"},
	}
	for _, tt := range tests {
		record := base
		tt.edit(&record)
		client := newFranzEventLogShadowClient(&fakeFranzCommandLogRuntime{}, &fakeEventLogClient{}, EventLogOptions{
			EventTopic:     "budgie.events",
			PartitionCount: 16,
		})
		_, err := client.AppendEvent(ctx, partition, record)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s missing evidence err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestDecodeKafkaEventRecordRejectsWrongLogicalKey(t *testing.T) {
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	record := testKafkaEventLogRecord(t, DefaultEventTopic, partition, 2, 10, 1, 1, "evt_wrong_key")
	record.Key = []byte(LogicalPartitionKey(core.LogPartition{Kind: "board", Key: "other"}))

	_, err := DecodeKafkaEventRecord(record)
	if err == nil || !strings.Contains(err.Error(), "record key") {
		t.Fatalf("DecodeKafkaEventRecord err = %v, want key mismatch", err)
	}
}

type fakeEventLogClient struct {
	fetchRequest EventFetchRequest
	fetchRecords []*kgo.Record
	fetchErr     error
}

func (c *fakeEventLogClient) FetchEventRecords(ctx context.Context, request EventFetchRequest) ([]*kgo.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.fetchRequest = request
	if c.fetchErr != nil {
		return nil, c.fetchErr
	}
	out := make([]*kgo.Record, 0, len(c.fetchRecords))
	for _, record := range c.fetchRecords {
		out = append(out, cloneKafkaRecord(record))
	}
	return out, nil
}

type eventPartitionListerFunc func(context.Context, int) ([]core.LogPartition, error)

func (f eventPartitionListerFunc) ListEventPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	return f(ctx, limit)
}

type eventLogHeadReaderFunc func(context.Context) (int64, error)

func (f eventLogHeadReaderFunc) Head(ctx context.Context) (int64, error) {
	return f(ctx)
}

func testKafkaEventLogRecord(t *testing.T, topic string, partition core.LogPartition, physicalPartition int32, physicalOffset, partitionOffset, compatibilitySeq int64, id string) *kgo.Record {
	t.Helper()
	partition = partition.Normalize()
	record, err := NewKafkaEventRecord(topic, core.BrokerEventRecord{
		Version:          1,
		ID:               id,
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: compatibilitySeq,
		Scopes:           []string{"board:" + partition.Key},
		Payload:          []byte(`{"id":"thr_` + id + `","board":"` + partition.Key + `","author":"alice","authorID":"usr_alice","title":"Kafka Event","ts":1234}`),
		TS:               1234,
		PartitionKind:    partition.Kind,
		PartitionKey:     partition.Key,
		PartitionOffset:  partitionOffset,
	})
	if err != nil {
		t.Fatalf("NewKafkaEventRecord: %v", err)
	}
	record.Partition = physicalPartition
	record.Offset = physicalOffset
	return record
}
