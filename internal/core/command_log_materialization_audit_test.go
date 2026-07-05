package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestCommandLogMaterializationAuditReportsAppliedAndTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	go c.Run(ctx)

	goodPayload := marshalCoreTestJSON(t, "marshal good payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Audit applied",
		Body:  "this should materialize",
	})
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, goodPayload, "audit-applied"); reply.Err != nil {
		t.Fatalf("enqueue good command: %+v", reply.Err)
	}
	badPayload := marshalCoreTestJSON(t, "marshal bad payload", proto.CreateThreadPayload{
		Board: "missing",
		Title: "Audit terminal",
		Body:  "this should terminally fail",
	})
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, badPayload, "audit-terminal"); reply.Err != nil {
		t.Fatalf("enqueue bad command: %+v", reply.Err)
	}

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	drainCommandLogWorkerOnce(t, ctx, worker, "DrainOnce")

	report, err := c.AuditCommandLogMaterialization(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("AuditCommandLogMaterialization: %v", err)
	}
	if !report.Complete {
		t.Fatalf("audit report = %+v, want complete", report)
	}
	if report.CommittedCommands != 2 || report.AppliedCommands != 1 || report.TerminalFailures != 1 {
		t.Fatalf("audit counts = committed %d applied %d terminal %d, want 2/1/1",
			report.CommittedCommands, report.AppliedCommands, report.TerminalFailures)
	}
	if report.MissingMaterialization != 0 || report.RetryingCommitted != 0 || report.MissingRecords != 0 {
		t.Fatalf("audit report = %+v, want no unsafe committed offsets", report)
	}
}

func TestCommandLogMaterializationAuditDetectsCommittedMissingMaterialization(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Committed without materialization",
		Body:  "this command is intentionally not executed",
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record := produceBrokerCommandLogTestRecord(t, ctx, commandLog, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "audit-missing",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	}, "Produce")
	if err := commandLog.CommitPartition(ctx, partition, record.Offset); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}

	report, err := c.AuditCommandLogMaterialization(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("AuditCommandLogMaterialization: %v", err)
	}
	if report.Complete {
		t.Fatalf("audit report = %+v, want incomplete", report)
	}
	if report.MissingMaterialization != 1 || len(report.MissingSamples) != 1 {
		t.Fatalf("audit report = %+v, want one missing materialization sample", report)
	}
	if sample := report.MissingSamples[0]; sample.CommandID != "audit-missing" || sample.Status != "missing" || sample.Offset != record.Offset {
		t.Fatalf("missing sample = %+v, want committed missing command", sample)
	}
}

func TestCommandLogMaterializationAuditAllowsSourceBackedSparseOffsets(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	record := sparseCommandLogWorkerRecord(partition, 4, logmodel.CommandLogSourcePosition{
		Backend:           "kafka",
		Topic:             "budgie.commands",
		PhysicalPartition: 2,
		PhysicalOffset:    3,
		CommitOffset:      4,
		LogicalPartition:  partition,
		LogicalOffset:     4,
	})
	commandLog := sparseAuditCommandLog{
		partition: partition,
		committed: record.Offset,
		records:   []CommandLogRecord{record},
	}
	c := newCoreTestCore(t)
	if err := c.RecordCommandLogApplied(ctx, record, &proto.AckResult{
		Status:               proto.AckStatusPending,
		CommandPartitionKind: partition.Kind,
		CommandPartitionKey:  partition.Key,
		CommandOffset:        record.Offset,
		CommandID:            record.CID,
	}); err != nil {
		t.Fatalf("RecordCommandLogApplied: %v", err)
	}

	report, err := c.AuditCommandLogMaterialization(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("AuditCommandLogMaterialization: %v", err)
	}
	if !report.Complete || report.CommittedCommands != 1 || report.AppliedCommands != 1 || report.MissingRecords != 0 {
		t.Fatalf("audit report = %+v, want one sparse applied command and no missing records", report)
	}
}

func TestCommandLogMaterializationAuditFailsWhenPartitionLimitTruncatesCoverage(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t)

	for _, boardID := range []string{"general", "meta"} {
		produceBrokerCommandLogTestRecord(t, ctx, commandLog, CommandLogRecord{
			Partition:  LogPartition{Kind: partitionBoard, Key: boardID},
			ActorID:    "usr_audit_limit",
			CID:        "audit-limit-" + boardID,
			Command:    proto.CmdCreateThread,
			Payload:    json.RawMessage(`{}`),
			EnqueuedAt: nowMS(),
		}, "Produce "+boardID)
	}

	report, err := c.AuditCommandLogMaterialization(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{PartitionLimit: 1})
	if err != nil {
		t.Fatalf("AuditCommandLogMaterialization: %v", err)
	}
	if report.Complete || !report.PartitionLimitExceeded {
		t.Fatalf("audit report = %+v, want incomplete because partition limit truncated coverage", report)
	}
	if report.Partitions != 1 {
		t.Fatalf("partitions checked = %d, want 1", report.Partitions)
	}
}

type sparseAuditCommandLog struct {
	partition LogPartition
	committed int64
	records   []CommandLogRecord
}

func (l sparseAuditCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	return CommandLogRecord{}, errors.New("sparse audit command log does not support produce")
}

func (l sparseAuditCommandLog) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if partition.Normalize() != l.partition {
		return nil, nil
	}
	out := make([]CommandLogRecord, 0, len(l.records))
	for _, record := range l.records {
		if record.Offset <= afterOffset {
			continue
		}
		out = append(out, record)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (l sparseAuditCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	return errors.New("sparse audit command log does not support commit")
}

func (l sparseAuditCommandLog) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if partition.Normalize() != l.partition {
		return 0, nil
	}
	return l.committed, nil
}

func (l sparseAuditCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]logmodel.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []logmodel.CommandPartitionOffset{{
		Partition:       l.partition,
		TailOffset:      l.committed,
		CommittedOffset: l.committed,
	}}, nil
}
