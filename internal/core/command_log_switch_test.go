package core

import (
	"context"
	"testing"
)

func TestSwitchableCommandLogProducesToSubmitLogAndDrainsFromSwitchedLog(t *testing.T) {
	ctx := context.Background()
	producer := NewMemoryCommandLog()
	drain := NewMemoryCommandLog()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	log := NewSwitchableCommandLog(producer)

	produced := produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-switch-producer")
	if produced.Offset != 1 {
		t.Fatalf("produced offset = %d, want 1", produced.Offset)
	}
	if records, err := producer.FetchPartition(ctx, partition, 0, 10); err != nil || len(records) != 1 {
		t.Fatalf("producer records = %+v, %v; want one submitted record", records, err)
	}

	produceCommandLogWorkerRecord(t, ctx, drain, partition, "cid-switch-drain")
	log.SetDrainLog(drain)
	records, err := log.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch switched drain log: %v", err)
	}
	if len(records) != 1 || records[0].CID != "cid-switch-drain" {
		t.Fatalf("switched records = %+v, want drain log record", records)
	}
	if err := log.CommitPartition(ctx, partition, records[0].Offset); err != nil {
		t.Fatalf("commit switched drain log: %v", err)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, drain, partition, 1, "drain committed offset")
	requireCommandLogWorkerCommittedOffset(t, ctx, producer, partition, 0, "producer committed offset")
}
