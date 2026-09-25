package kafkaconn

import (
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

func TestSQLEventPositionedEventLogOptions(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "sql-positioned-event-store.db"))
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer c.DB.Close()

	options, allocator, err := SQLEventPositionedEventLogOptions(c.DB, SQLEventPositionedEventStoreOptions{
		PartitionCount: 32,
	})
	if err != nil {
		t.Fatalf("SQLEventPositionedEventLogOptions: %v", err)
	}
	if allocator == nil {
		t.Fatalf("allocator = nil")
	}
	if options.PartitionCount != 32 || options.Partitions != allocator || options.Head != allocator || options.DisableKafkaOffsetStreamSeq {
		t.Fatalf("options = %+v, allocator = %T; want scalar-compatible SQL positioning", options, allocator)
	}

	partitionOnly, partitionAllocator, err := SQLEventPositionedEventLogOptions(c.DB, SQLEventPositionedEventStoreOptions{
		PartitionCount:       16,
		PartitionOnlyOffsets: true,
	})
	if err != nil {
		t.Fatalf("partition-only SQLEventPositionedEventLogOptions: %v", err)
	}
	if partitionAllocator == nil {
		t.Fatalf("partition-only allocator = nil")
	}
	if partitionOnly.PartitionCount != 16 || partitionOnly.Partitions != partitionAllocator || partitionOnly.Head != nil || !partitionOnly.DisableKafkaOffsetStreamSeq {
		t.Fatalf("partition-only options = %+v, allocator = %T; want partition-only SQL positioning", partitionOnly, partitionAllocator)
	}
}

func TestSQLEventPositionedEventLogOptionsRequiresDB(t *testing.T) {
	_, _, err := SQLEventPositionedEventLogOptions(nil, SQLEventPositionedEventStoreOptions{})
	requireErrorContains(t, err, "materialization database")
}
