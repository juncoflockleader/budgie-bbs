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
	if got, err := drain.CommittedOffset(ctx, partition); err != nil || got != 1 {
		t.Fatalf("drain committed offset = %d, %v; want 1, nil", got, err)
	}
	if got, err := producer.CommittedOffset(ctx, partition); err != nil || got != 0 {
		t.Fatalf("producer committed offset = %d, %v; want 0, nil", got, err)
	}
}
