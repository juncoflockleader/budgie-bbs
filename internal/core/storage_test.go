package core

import (
	"reflect"
	"testing"
)

func TestLogPartitionOrderingHelpers(t *testing.T) {
	partitions := []LogPartition{
		{Kind: partitionThread, Key: "thr_1"},
		{},
		{Kind: partitionBoard, Key: "life"},
		{Kind: partitionBoard, Key: "general"},
	}

	SortLogPartitions(partitions)
	normalized := make([]LogPartition, 0, len(partitions))
	for _, partition := range partitions {
		normalized = append(normalized, partition.Normalize())
	}
	want := []LogPartition{
		{Kind: partitionBoard, Key: "general"},
		{Kind: partitionBoard, Key: "life"},
		{Kind: partitionGlobal, Key: partitionGlobal},
		{Kind: partitionThread, Key: "thr_1"},
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("SortLogPartitions = %+v, want %+v", normalized, want)
	}

	if !(LogPartition{}).Less(LogPartition{Kind: partitionThread, Key: "thr_1"}) {
		t.Fatalf("empty partition should sort as normalized global partition")
	}
}
