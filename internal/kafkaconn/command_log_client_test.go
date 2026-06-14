package kafkaconn

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestFranzCommandLogClientAppendReturnsAssignedRecord(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	produced := testKafkaCommandLogRecord(t, "budgie.commands", partition, 2, 40, "cid-franz-produce")
	runtime := &fakeFranzCommandLogRuntime{
		produceResults: kgo.ProduceResults{{Record: produced}},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{})
	input, err := NewKafkaCommandRecord("budgie.commands", core.BrokerCommandRecord{
		Version:       1,
		ActorID:       "usr_alice",
		CID:           "cid-franz-produce",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"Kafka"}`),
		EnqueuedAt:    1000,
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
	})
	if err != nil {
		t.Fatalf("NewKafkaCommandRecord: %v", err)
	}

	got, err := client.AppendCommandRecord(ctx, input)
	if err != nil {
		t.Fatalf("AppendCommandRecord: %v", err)
	}
	if runtime.produceCalls != 1 || len(runtime.produced) != 1 {
		t.Fatalf("produce calls=%d records=%d, want one produce", runtime.produceCalls, len(runtime.produced))
	}
	if runtime.produced[0] == input {
		t.Fatalf("produced record reused caller pointer, want defensive clone")
	}
	if got == produced {
		t.Fatalf("returned record reused runtime pointer, want defensive clone")
	}
	if got.Topic != "budgie.commands" || got.Partition != 2 || got.Offset != 40 {
		t.Fatalf("returned record = topic %s partition %d offset %d, want budgie.commands/2/40", got.Topic, got.Partition, got.Offset)
	}
}

func TestFranzCommandLogClientBuffersNeighborLogicalPartitionsInPhysicalOrder(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partitionA := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	partitionB := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physical := int32(2)
	recordB := testKafkaCommandLogRecord(t, topic, partitionB, physical, 4, "cid-buffered-other")
	recordA := testKafkaCommandLogRecord(t, topic, partitionA, physical, 8, "cid-target")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical, recordB, recordA)},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	gotA, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords partition A: %v", err)
	}
	if len(gotA) != 0 {
		t.Fatalf("partition A records = %+v, want none while earlier physical offset belongs to partition B", gotA)
	}

	gotB, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionB,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords partition B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Offset != 4 {
		t.Fatalf("partition B records = %+v, want buffered physical offset 4", gotB)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls = %d, want partition B served from buffer without another poll", runtime.pollCalls)
	}

	if err := client.CommitCommandOffset(ctx, CommandOffsetCommit{
		Topic:             topic,
		PhysicalPartition: physical,
		Offset:            5,
	}); err != nil {
		t.Fatalf("CommitCommandOffset partition B: %v", err)
	}
	gotA, err = client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 5,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords partition A after B commit: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Offset != 8 {
		t.Fatalf("partition A records after B commit = %+v, want physical offset 8", gotA)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls after partition A fetch = %d, want buffered record without another poll", runtime.pollCalls)
	}
}

func TestFranzCommandLogClientBuffersOtherPhysicalPartitions(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partitionA := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	partitionB := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physicalA := int32(2)
	physicalB := int32(7)
	recordA := testKafkaCommandLogRecord(t, topic, partitionA, physicalA, 8, "cid-target")
	recordB := testKafkaCommandLogRecord(t, topic, partitionB, physicalB, 4, "cid-buffered-physical")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{{{
			Topics: []kgo.FetchTopic{{
				Topic: topic,
				Partitions: []kgo.FetchPartition{{
					Partition: physicalB,
					Records:   []*kgo.Record{recordB},
				}, {
					Partition: physicalA,
					Records:   []*kgo.Record{recordA},
				}},
			}},
		}}},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	gotA, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physicalA,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords partition A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Offset != 8 {
		t.Fatalf("partition A records = %+v, want physical offset 8", gotA)
	}

	gotB, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionB,
		PhysicalPartition:  physicalB,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords partition B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Offset != 4 {
		t.Fatalf("partition B records = %+v, want buffered physical offset 4", gotB)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls = %d, want buffered second physical partition without another poll", runtime.pollCalls)
	}
}

func TestFranzCommandLogClientRecordedCommitSkipsRefetchedPhysicalRecords(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partitionA := core.LogPartition{Kind: "thread", Key: "thr_reply"}.Normalize()
	partitionB := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	stale := testKafkaCommandLogRecord(t, topic, partitionB, physical, 4, "cid-stale-create")
	target := testKafkaCommandLogRecord(t, topic, partitionA, physical, 8, "cid-target-reply")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical, stale, target)},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})
	if err := client.RecordCommandOffsetCommit(ctx, CommandOffsetCommit{
		Topic:             topic,
		PhysicalPartition: physical,
		Offset:            5,
	}); err != nil {
		t.Fatalf("RecordCommandOffsetCommit: %v", err)
	}

	got, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords: %v", err)
	}
	if len(got) != 1 || got[0].Offset != 8 {
		t.Fatalf("records = %+v, want stale physical offset skipped and target offset 8 returned", got)
	}
	if len(runtime.setOffsets) == 0 {
		t.Fatalf("set offsets were not called")
	}
}

func TestFranzCommandLogClientFetchReturnsEmptyAfterPollTimeout(t *testing.T) {
	ctx := context.Background()
	client := newFranzCommandLogClient(&fakeFranzCommandLogRuntime{}, FranzCommandLogClientOptions{
		PollTimeout: time.Millisecond,
	})

	got, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:             "budgie.commands",
		Partition:         core.LogPartition{Kind: "board", Key: "general"},
		PhysicalPartition: 2,
		Limit:             10,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("records = %+v, want none after poll timeout", got)
	}
}

func TestFastDrainFranzCommandLogClientOptionsUseShortPollsAndLargerFetches(t *testing.T) {
	options := FastDrainFranzCommandLogClientOptions()
	if options.PollTimeout <= 0 || options.PollTimeout >= 100*time.Millisecond {
		t.Fatalf("PollTimeout = %s, want a short bounded drain poll", options.PollTimeout)
	}
	if options.PollRecordLimit < 1024 {
		t.Fatalf("PollRecordLimit = %d, want large drain prefetch", options.PollRecordLimit)
	}
}

func TestFranzCommandLogClientFetchRetriesEmptyPollsBeforeTimeout(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	record := testKafkaCommandLogRecord(t, topic, partition, physical, 8, "cid-after-empty-poll")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{
			{},
			franzFetch(topic, physical, record),
		},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     50 * time.Millisecond,
		PollRecordLimit: 10,
	})

	got, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords: %v", err)
	}
	if len(got) != 1 || got[0].Offset != 8 {
		t.Fatalf("records = %+v, want fetched record after an empty poll", got)
	}
	if runtime.pollCalls != 2 {
		t.Fatalf("poll calls = %d, want retry after empty poll", runtime.pollCalls)
	}
}

func TestFranzCommandLogClientFetchResetsRuntimeOffsetToRequestOffset(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	record := testKafkaCommandLogRecord(t, topic, partition, physical, 8, "cid-reset-offset")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical, record)},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     50 * time.Millisecond,
		PollRecordLimit: 10,
	})

	_, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 7,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords: %v", err)
	}
	if len(runtime.setOffsets) != 1 {
		t.Fatalf("set offsets calls = %d, want 1", len(runtime.setOffsets))
	}
	got := runtime.setOffsets[0][topic][physical]
	if got.Offset != 7 || got.Epoch != -1 {
		t.Fatalf("set offset = %+v, want epoch -1 offset 7", got)
	}
}

func TestFranzCommandLogClientCommitsAndReadsGroupOffsets(t *testing.T) {
	ctx := context.Background()
	runtime := &fakeFranzCommandLogRuntime{
		committed: map[string]map[int32]kgo.EpochOffset{},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{})

	err := client.CommitCommandOffset(ctx, CommandOffsetCommit{
		ConsumerGroup:     "budgie-writers",
		Topic:             "budgie.commands",
		PhysicalPartition: 2,
		Offset:            41,
		LogicalPartition:  core.LogPartition{Kind: "board", Key: "general"},
		LogicalOffset:     41,
	})
	if err != nil {
		t.Fatalf("CommitCommandOffset: %v", err)
	}
	wantCommit := map[string]map[int32]kgo.EpochOffset{
		"budgie.commands": {2: {Offset: 41}},
	}
	if !reflect.DeepEqual(runtime.committed, wantCommit) {
		t.Fatalf("committed offsets = %+v, want %+v", runtime.committed, wantCommit)
	}

	offset, err := client.CommittedCommandOffset(ctx, CommandTopicPartition{
		Topic:             "budgie.commands",
		PhysicalPartition: 2,
	})
	if err != nil {
		t.Fatalf("CommittedCommandOffset: %v", err)
	}
	if offset != 41 {
		t.Fatalf("committed offset = %d, want 41", offset)
	}
}

func TestFranzCommandLogClientRecordCommitPrunesBufferedRecords(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.commands"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	record := testKafkaCommandLogRecord(t, topic, partition, physical, 4, "cid-prune-recorded")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical, record)},
	}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	got, err := client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("records = %+v, want one fetched record", got)
	}
	if err := client.RecordCommandOffsetCommit(ctx, CommandOffsetCommit{
		Topic:             topic,
		PhysicalPartition: physical,
		Offset:            5,
	}); err != nil {
		t.Fatalf("RecordCommandOffsetCommit: %v", err)
	}
	got, err = client.FetchCommandRecords(ctx, CommandFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("FetchCommandRecords after recorded commit: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("records after recorded commit = %+v, want pruned buffer", got)
	}
}

func TestFranzCommandLogClientAllowsRebalance(t *testing.T) {
	runtime := &fakeFranzCommandLogRuntime{}
	client := newFranzCommandLogClient(runtime, FranzCommandLogClientOptions{})

	client.AllowRebalance()

	if runtime.allowRebalanceCalls != 1 {
		t.Fatalf("allow rebalance calls = %d, want 1", runtime.allowRebalanceCalls)
	}
}

func TestFranzCommandLogClientSurfacesCommitFailure(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("commit rejected")
	client := newFranzCommandLogClient(&fakeFranzCommandLogRuntime{commitErr: wantErr}, FranzCommandLogClientOptions{})

	err := client.CommitCommandOffset(ctx, CommandOffsetCommit{
		Topic:             "budgie.commands",
		PhysicalPartition: 2,
		Offset:            41,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CommitCommandOffset err = %v, want %v", err, wantErr)
	}
}

type fakeFranzCommandLogRuntime struct {
	produceCalls        int
	produced            []*kgo.Record
	produceResults      kgo.ProduceResults
	fetches             []kgo.Fetches
	pollCalls           int
	pollLimits          []int
	commitErr           error
	committed           map[string]map[int32]kgo.EpochOffset
	setOffsets          []map[string]map[int32]kgo.EpochOffset
	allowRebalanceCalls int
}

func (r *fakeFranzCommandLogRuntime) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	r.produceCalls++
	for _, record := range records {
		r.produced = append(r.produced, cloneKafkaRecord(record))
	}
	return r.produceResults
}

func (r *fakeFranzCommandLogRuntime) PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches {
	r.pollCalls++
	r.pollLimits = append(r.pollLimits, maxPollRecords)
	if len(r.fetches) > 0 {
		fetches := r.fetches[0]
		r.fetches = r.fetches[1:]
		return fetches
	}
	<-ctx.Done()
	return kgo.NewErrFetch(ctx.Err())
}

func (r *fakeFranzCommandLogRuntime) CommitOffsetsSync(ctx context.Context, offsets map[string]map[int32]kgo.EpochOffset, onDone func(*kgo.Client, *kmsg.OffsetCommitRequest, *kmsg.OffsetCommitResponse, error)) {
	if r.committed == nil {
		r.committed = map[string]map[int32]kgo.EpochOffset{}
	}
	for topic, partitions := range offsets {
		if r.committed[topic] == nil {
			r.committed[topic] = map[int32]kgo.EpochOffset{}
		}
		for partition, offset := range partitions {
			r.committed[topic][partition] = offset
		}
	}
	onDone(nil, kmsg.NewPtrOffsetCommitRequest(), kmsg.NewPtrOffsetCommitResponse(), r.commitErr)
}

func (r *fakeFranzCommandLogRuntime) CommittedOffsets() map[string]map[int32]kgo.EpochOffset {
	return r.committed
}

func (r *fakeFranzCommandLogRuntime) SetOffsets(offsets map[string]map[int32]kgo.EpochOffset) {
	cloned := make(map[string]map[int32]kgo.EpochOffset, len(offsets))
	for topic, partitions := range offsets {
		cloned[topic] = make(map[int32]kgo.EpochOffset, len(partitions))
		for partition, offset := range partitions {
			cloned[topic][partition] = offset
		}
	}
	r.setOffsets = append(r.setOffsets, cloned)
}

func (r *fakeFranzCommandLogRuntime) AllowRebalance() {
	r.allowRebalanceCalls++
}

func franzFetch(topic string, physicalPartition int32, records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: topic,
			Partitions: []kgo.FetchPartition{{
				Partition: physicalPartition,
				Records:   records,
			}},
		}},
	}}
}
