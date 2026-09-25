package kafkaconn

import (
	"context"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestFranzEventLogClientBuffersNeighborLogicalPartitions(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partitionA := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	partitionB := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physical := int32(2)
	recordB := testKafkaEventLogRecord(t, topic, partitionB, physical, 4, 1, 101, "evt_buffered_other")
	recordA := testKafkaEventLogRecord(t, topic, partitionA, physical, 8, 1, 102, "evt_target")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical, recordB, recordA)},
	}
	client := newFranzEventLogClient(runtime, FranzEventLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	gotA, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords partition A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Offset != 8 {
		t.Fatalf("partition A records = %+v, want physical offset 8", gotA)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls = %d, want one poll", runtime.pollCalls)
	}

	gotB, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partitionB,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords partition B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Offset != 4 {
		t.Fatalf("partition B records = %+v, want buffered physical offset 4", gotB)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls after partition B fetch = %d, want buffered record without another poll", runtime.pollCalls)
	}
}

func TestFranzEventLogClientBuffersOtherPhysicalPartitions(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partitionA := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	partitionB := core.LogPartition{Kind: "board", Key: "other"}.Normalize()
	physicalA := int32(2)
	physicalB := int32(7)
	recordA := testKafkaEventLogRecord(t, topic, partitionA, physicalA, 8, 1, 101, "evt_target")
	recordB := testKafkaEventLogRecord(t, topic, partitionB, physicalB, 4, 1, 102, "evt_buffered_physical")
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
	client := newFranzEventLogClient(runtime, FranzEventLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	gotA, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partitionA,
		PhysicalPartition:  physicalA,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords partition A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Offset != 8 {
		t.Fatalf("partition A records = %+v, want physical offset 8", gotA)
	}

	gotB, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partitionB,
		PhysicalPartition:  physicalB,
		AfterLogicalOffset: 0,
		Limit:              1,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords partition B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Offset != 4 {
		t.Fatalf("partition B records = %+v, want buffered physical offset 4", gotB)
	}
	if runtime.pollCalls != 1 {
		t.Fatalf("poll calls = %d, want buffered second physical partition without another poll", runtime.pollCalls)
	}
}

func TestFranzEventLogClientPrunesAppliedLogicalOffsets(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{franzFetch(topic, physical,
			testKafkaEventLogRecord(t, topic, partition, physical, 4, 1, 101, "evt_applied"),
			testKafkaEventLogRecord(t, topic, partition, physical, 5, 2, 102, "evt_next"),
		)},
	}
	client := newFranzEventLogClient(runtime, FranzEventLogClientOptions{
		PollTimeout:     time.Millisecond,
		PollRecordLimit: 10,
	})

	got, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 1,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords: %v", err)
	}
	if len(got) != 1 || got[0].Offset != 5 {
		t.Fatalf("records = %+v, want only unapplied physical offset 5", got)
	}
}

func TestFranzEventLogClientWaitsForContiguousLogicalOffsets(t *testing.T) {
	ctx := context.Background()
	topic := "budgie.events"
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	physical := int32(2)
	offsetTwo := testKafkaEventLogRecord(t, topic, partition, physical, 5, 2, 102, "evt_offset_2")
	offsetOne := testKafkaEventLogRecord(t, topic, partition, physical, 6, 1, 101, "evt_offset_1")
	runtime := &fakeFranzCommandLogRuntime{
		fetches: []kgo.Fetches{
			franzFetch(topic, physical, offsetTwo),
			franzFetch(topic, physical, offsetOne),
		},
	}
	client := newFranzEventLogClient(runtime, FranzEventLogClientOptions{
		PollTimeout:     50 * time.Millisecond,
		PollRecordLimit: 10,
	})

	got, err := client.FetchEventRecords(ctx, EventFetchRequest{
		Topic:              topic,
		Partition:          partition,
		PhysicalPartition:  physical,
		AfterLogicalOffset: 0,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("FetchEventRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("records = %+v, want contiguous offsets 1 and 2", got)
	}
	first, err := DecodeKafkaEventRecord(got[0])
	if err != nil {
		t.Fatalf("decode first: %v", err)
	}
	second, err := DecodeKafkaEventRecord(got[1])
	if err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if first.Offset != 1 || second.Offset != 2 {
		t.Fatalf("logical offsets = %d,%d; want 1,2", first.Offset, second.Offset)
	}
}
