package logmodel

import (
	"strings"
	"testing"
)

func TestCommandLogSourcePositionValidatesLogicalPosition(t *testing.T) {
	partition := Partition{Kind: PartitionBoard, Key: "general"}
	position := CommandLogSourcePosition{
		Backend:           " kafka ",
		Topic:             " budgie.commandlog ",
		PhysicalPartition: 7,
		PhysicalOffset:    41,
		CommitOffset:      42,
		LogicalPartition:  partition,
		LogicalOffset:     42,
	}
	if err := position.ValidateFor(partition, 42); err != nil {
		t.Fatalf("ValidateFor: %v", err)
	}
	normalized := position.Normalize()
	if normalized.Backend != "kafka" || normalized.Topic != "budgie.commandlog" {
		t.Fatalf("normalized = %+v, want trimmed backend/topic", normalized)
	}
}

func TestCommandLogSourcePositionAllowsZeroForNonBrokerLogs(t *testing.T) {
	if err := (CommandLogSourcePosition{}).ValidateFor(Partition{}, 0); err != nil {
		t.Fatalf("zero ValidateFor: %v", err)
	}
}

func TestCommandLogSourcePositionValidatesCommandLogRecord(t *testing.T) {
	partition := Partition{Kind: PartitionBoard, Key: "general"}
	record := CommandLogRecord{
		Partition: partition,
		Offset:    42,
		SourcePosition: CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 7,
			PhysicalOffset:    41,
			CommitOffset:      42,
			LogicalPartition:  partition,
			LogicalOffset:     42,
		},
	}
	if err := record.SourcePosition.ValidateForRecord(record); err != nil {
		t.Fatalf("ValidateForRecord: %v", err)
	}
	record.Offset = 43
	requireErrorContains(t, record.SourcePosition.ValidateForRecord(record), "does not match record offset")
}

func TestCommandLogSourcePositionRejectsUnsafeCommitEvidence(t *testing.T) {
	partition := Partition{Kind: PartitionBoard, Key: "general"}
	tests := []struct {
		name     string
		position CommandLogSourcePosition
		want     string
	}{
		{
			name: "missing backend",
			position: CommandLogSourcePosition{
				Topic:            "budgie.commandlog",
				PhysicalOffset:   41,
				CommitOffset:     42,
				LogicalPartition: partition,
				LogicalOffset:    42,
			},
			want: "backend is required",
		},
		{
			name: "non advancing commit offset",
			position: CommandLogSourcePosition{
				Backend:          "kafka",
				Topic:            "budgie.commandlog",
				PhysicalOffset:   41,
				CommitOffset:     41,
				LogicalPartition: partition,
				LogicalOffset:    42,
			},
			want: "must advance physical offset",
		},
		{
			name: "negative physical partition",
			position: CommandLogSourcePosition{
				Backend:           "kafka",
				Topic:             "budgie.commandlog",
				PhysicalPartition: -1,
				PhysicalOffset:    41,
				CommitOffset:      42,
				LogicalPartition:  partition,
				LogicalOffset:     42,
			},
			want: "physical partition -1 is negative",
		},
		{
			name: "wrong logical partition",
			position: CommandLogSourcePosition{
				Backend:          "kafka",
				Topic:            "budgie.commandlog",
				PhysicalOffset:   41,
				CommitOffset:     42,
				LogicalPartition: Partition{Kind: PartitionBoard, Key: "other"},
				LogicalOffset:    42,
			},
			want: "does not match record partition",
		},
		{
			name: "wrong logical offset",
			position: CommandLogSourcePosition{
				Backend:          "kafka",
				Topic:            "budgie.commandlog",
				PhysicalOffset:   41,
				CommitOffset:     42,
				LogicalPartition: partition,
				LogicalOffset:    43,
			},
			want: "does not match record offset",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireErrorContains(t, tt.position.ValidateFor(partition, 42), tt.want)
		})
	}
}

func TestCommandLogCommitPositionValidatesSourceEvidence(t *testing.T) {
	position := CommandLogCommitPosition{
		Partition: Partition{Kind: PartitionBoard, Key: "general"},
		Offset:    42,
		SourcePosition: CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 7,
			PhysicalOffset:    41,
			CommitOffset:      42,
			LogicalPartition:  Partition{Kind: PartitionBoard, Key: "other"},
			LogicalOffset:     42,
		},
	}
	requireErrorContains(t, position.Validate(), "does not match record partition")
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
