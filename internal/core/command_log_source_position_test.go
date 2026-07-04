package core

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestCommandLogSourcePositionValidatesRecordMatch(t *testing.T) {
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    42,
		Command:   proto.CmdCreateThread,
	}
	position := CommandLogSourcePosition{
		Backend:           " kafka ",
		Topic:             " budgie.commandlog ",
		PhysicalPartition: 7,
		PhysicalOffset:    41,
		CommitOffset:      42,
		LogicalPartition:  LogPartition{Kind: partitionBoard, Key: "general"},
		LogicalOffset:     42,
	}
	if err := position.ValidateForRecord(record); err != nil {
		t.Fatalf("ValidateForRecord: %v", err)
	}
	normalized := position.Normalize()
	if normalized.Backend != "kafka" || normalized.Topic != "budgie.commandlog" {
		t.Fatalf("normalized = %+v, want trimmed backend/topic", normalized)
	}
}

func TestCommandLogSourcePositionAllowsZeroForNonBrokerLogs(t *testing.T) {
	if err := (CommandLogSourcePosition{}).ValidateForRecord(CommandLogRecord{}); err != nil {
		t.Fatalf("zero ValidateForRecord: %v", err)
	}
}

func TestCommandLogSourcePositionRejectsUnsafeCommitEvidence(t *testing.T) {
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    42,
	}
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
				LogicalPartition: record.Partition,
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
				LogicalPartition: record.Partition,
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
				LogicalPartition:  record.Partition,
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
				LogicalPartition: LogPartition{Kind: partitionBoard, Key: "other"},
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
				LogicalPartition: record.Partition,
				LogicalOffset:    43,
			},
			want: "does not match record offset",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.position.ValidateForRecord(record)
			requireErrorContains(t, err, tt.want)
		})
	}
}

func TestCommandLogCommitPositionValidatesSourceEvidence(t *testing.T) {
	position := CommandLogCommitPosition{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    42,
		SourcePosition: CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 7,
			PhysicalOffset:    41,
			CommitOffset:      42,
			LogicalPartition:  LogPartition{Kind: partitionBoard, Key: "other"},
			LogicalOffset:     42,
		},
	}
	err := position.Validate()
	requireErrorContains(t, err, "does not match record partition")
}
