package core

import (
	"fmt"
	"strings"
)

func (p CommandLogSourcePosition) IsZero() bool {
	return p == (CommandLogSourcePosition{})
}

func (p CommandLogSourcePosition) Normalize() CommandLogSourcePosition {
	p.Backend = strings.TrimSpace(p.Backend)
	p.Topic = strings.TrimSpace(p.Topic)
	if p.LogicalPartition != (LogPartition{}) {
		p.LogicalPartition = p.LogicalPartition.Normalize()
	}
	return p
}

func (p CommandLogSourcePosition) ValidateForRecord(record CommandLogRecord) error {
	if p.IsZero() {
		return nil
	}
	p = p.Normalize()
	recordPartition := record.Partition.Normalize()
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
	if p.LogicalPartition != recordPartition {
		return fmt.Errorf("command log source position: logical partition %s/%s does not match record partition %s/%s",
			p.LogicalPartition.Kind, p.LogicalPartition.Key, recordPartition.Kind, recordPartition.Key)
	}
	if p.LogicalOffset != record.Offset {
		return fmt.Errorf("command log source position: logical offset %d does not match record offset %d", p.LogicalOffset, record.Offset)
	}
	return nil
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
	if err := p.SourcePosition.ValidateForRecord(CommandLogRecord{
		Partition: p.Partition,
		Offset:    p.Offset,
	}); err != nil {
		return err
	}
	return nil
}
