package logmodel

import (
	"fmt"
	"strings"
)

// CommandLogSourcePosition is the broker source offset for one command-log
// record. Logical Partition/Offset remain the Budgie execution identity;
// physical topic-partition offsets are what Kafka/Redpanda commits must use.
type CommandLogSourcePosition struct {
	Backend           string
	Topic             string
	PhysicalPartition int32
	PhysicalOffset    int64
	CommitOffset      int64
	LogicalPartition  Partition
	LogicalOffset     int64
}

func (p CommandLogSourcePosition) IsZero() bool {
	return p == (CommandLogSourcePosition{})
}

func (p CommandLogSourcePosition) Normalize() CommandLogSourcePosition {
	p.Backend = strings.TrimSpace(p.Backend)
	p.Topic = strings.TrimSpace(p.Topic)
	if p.LogicalPartition != (Partition{}) {
		p.LogicalPartition = p.LogicalPartition.Normalize()
	}
	return p
}

func (p CommandLogSourcePosition) ValidateFor(partition Partition, offset int64) error {
	if p.IsZero() {
		return nil
	}
	p = p.Normalize()
	partition = partition.Normalize()
	if p.Backend == "" {
		return fmt.Errorf("command log source position: backend is required")
	}
	if p.Topic == "" {
		return fmt.Errorf("command log source position: topic is required")
	}
	if p.PhysicalPartition < 0 {
		return fmt.Errorf("command log source position: physical partition %d is negative", p.PhysicalPartition)
	}
	if p.PhysicalOffset < 0 {
		return fmt.Errorf("command log source position: physical offset %d is negative", p.PhysicalOffset)
	}
	if p.CommitOffset <= p.PhysicalOffset {
		return fmt.Errorf("command log source position: commit offset %d must advance physical offset %d", p.CommitOffset, p.PhysicalOffset)
	}
	if p.LogicalPartition != partition {
		return fmt.Errorf("command log source position: logical partition %s/%s does not match record partition %s/%s",
			p.LogicalPartition.Kind, p.LogicalPartition.Key, partition.Kind, partition.Key)
	}
	if p.LogicalOffset != offset {
		return fmt.Errorf("command log source position: logical offset %d does not match record offset %d", p.LogicalOffset, offset)
	}
	return nil
}

// CommandLogCommitPosition is the exact command-log cursor a transaction must
// advance after its events are durable. Partition/Offset are Budgie's logical
// progress marker; SourcePosition carries physical broker evidence when the
// command source has distinct commit coordinates, such as Kafka/Redpanda.
type CommandLogCommitPosition struct {
	Partition      Partition
	Offset         int64
	SourcePosition CommandLogSourcePosition
}

func (p CommandLogCommitPosition) Normalize() CommandLogCommitPosition {
	p.Partition = p.Partition.Normalize()
	p.SourcePosition = p.SourcePosition.Normalize()
	return p
}

func (p CommandLogCommitPosition) Validate() error {
	p = p.Normalize()
	if p.Offset <= 0 {
		return fmt.Errorf("command log commit position: offset is required")
	}
	if err := p.SourcePosition.ValidateFor(p.Partition, p.Offset); err != nil {
		return err
	}
	return nil
}
