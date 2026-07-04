package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func marshalCoreTestJSON(t *testing.T, label string, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return raw
}

func TestCommandLogPromotionReadinessReportsReadyAfterDrain(t *testing.T) {
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
		Title: "Readiness applied",
		Body:  "this should materialize",
	})
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, goodPayload, "readiness-applied"); reply.Err != nil {
		t.Fatalf("enqueue good command: %+v", reply.Err)
	}
	badPayload := marshalCoreTestJSON(t, "marshal bad payload", proto.CreateThreadPayload{
		Board: "missing",
		Title: "Readiness terminal",
		Body:  "this should terminally fail",
	})
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, badPayload, "readiness-terminal"); reply.Err != nil {
		t.Fatalf("enqueue bad command: %+v", reply.Err)
	}

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 1,
	})
	drainCommandLogWorkerOnce(t, ctx, worker, "DrainOnce")

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("CheckCommandLogPromotionReadiness: %v", err)
	}
	if !report.Ready {
		t.Fatalf("readiness report = %+v, want ready", report)
	}
	if report.LaggingPartitions != 0 || report.TotalLag != 0 || report.MaxLag != 0 {
		t.Fatalf("readiness lag = partitions %d total %d max %d, want zero",
			report.LaggingPartitions, report.TotalLag, report.MaxLag)
	}
	if report.TailCommands != 2 || report.CommittedCommands != 2 {
		t.Fatalf("readiness offsets = tail %d committed %d, want 2/2", report.TailCommands, report.CommittedCommands)
	}
	if !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 1 || report.MaterializationAudit.TerminalFailures != 1 {
		t.Fatalf("materialization audit = %+v, want complete with one applied and one terminal", report.MaterializationAudit)
	}
}

func TestCommandLogPromotionReadinessDetectsUncommittedLag(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Queued but not committed",
		Body:  "this command is intentionally left pending",
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceBrokerCommandLogTestRecord(t, ctx, commandLog, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "readiness-lag",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	}, "Produce")

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("CheckCommandLogPromotionReadiness: %v", err)
	}
	if report.Ready {
		t.Fatalf("readiness report = %+v, want not ready", report)
	}
	if report.LaggingPartitions != 1 || report.TotalLag != 1 || report.MaxLag != 1 {
		t.Fatalf("readiness lag = partitions %d total %d max %d, want 1/1/1",
			report.LaggingPartitions, report.TotalLag, report.MaxLag)
	}
	if len(report.LaggingSamples) != 1 || report.LaggingSamples[0].PartitionKey != "general" || report.LaggingSamples[0].Lag != 1 {
		t.Fatalf("lag samples = %+v, want one general lag sample", report.LaggingSamples)
	}
	if !report.MaterializationAudit.Complete || report.MaterializationAudit.CommittedCommands != 0 {
		t.Fatalf("materialization audit = %+v, want complete with no committed commands", report.MaterializationAudit)
	}
}

func TestCommandLogPromotionReadinessDetectsCommittedMissingMaterialization(t *testing.T) {
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
		CID:        "readiness-missing",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	}, "Produce")
	if err := commandLog.CommitPartition(ctx, partition, record.Offset); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("CheckCommandLogPromotionReadiness: %v", err)
	}
	if report.Ready {
		t.Fatalf("readiness report = %+v, want not ready", report)
	}
	if report.LaggingPartitions != 0 || report.TotalLag != 0 {
		t.Fatalf("readiness lag = partitions %d total %d, want zero", report.LaggingPartitions, report.TotalLag)
	}
	if report.MaterializationAudit.Complete || report.MaterializationAudit.MissingMaterialization != 1 {
		t.Fatalf("materialization audit = %+v, want one missing materialization", report.MaterializationAudit)
	}
}

func TestCommandLogPromotionReadinessReusesOffsetSnapshotForAudit(t *testing.T) {
	ctx := context.Background()
	baseLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	commandLog := &countingCommandLogOffsetAccess{
		CommandLog: baseLog,
		offsets:    baseLog,
	}
	c := newCoreTestCore(t)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record := produceBrokerCommandLogTestRecord(t, ctx, commandLog, CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_readiness_offset_snapshot",
		CID:        "readiness-offset-snapshot",
		Command:    proto.CmdCreateThread,
		Payload:    json.RawMessage(`{}`),
		EnqueuedAt: nowMS(),
	}, "Produce")
	if err := commandLog.CommitPartition(ctx, partition, record.Offset); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{BatchSize: 1})
	if err != nil {
		t.Fatalf("CheckCommandLogPromotionReadiness: %v", err)
	}
	if report.MaterializationAudit.MissingMaterialization != 1 {
		t.Fatalf("materialization audit = %+v, want one missing materialization from reused snapshot", report.MaterializationAudit)
	}
	_, listOffsetCalls := commandLog.snapshot()
	if listOffsetCalls != 1 {
		t.Fatalf("ListCommandPartitionOffsets calls = %d, want one shared snapshot for readiness and audit", listOffsetCalls)
	}
}

func TestCommandLogPromotionReadinessFailsWhenPartitionLimitTruncatesCoverage(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t)

	for _, boardID := range []string{"general", "meta"} {
		produceBrokerCommandLogTestRecord(t, ctx, commandLog, CommandLogRecord{
			Partition:  LogPartition{Kind: partitionBoard, Key: boardID},
			ActorID:    "usr_readiness_limit",
			CID:        "readiness-limit-" + boardID,
			Command:    proto.CmdCreateThread,
			Payload:    json.RawMessage(`{}`),
			EnqueuedAt: nowMS(),
		}, "Produce "+boardID)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{PartitionLimit: 1})
	if err != nil {
		t.Fatalf("CheckCommandLogPromotionReadiness: %v", err)
	}
	if report.Ready || !report.PartitionLimitExceeded {
		t.Fatalf("readiness report = %+v, want not ready because partition limit truncated coverage", report)
	}
	if report.Partitions != 1 {
		t.Fatalf("partitions checked = %d, want 1", report.Partitions)
	}
	if !report.MaterializationAudit.PartitionLimitExceeded {
		t.Fatalf("materialization audit = %+v, want matching partition limit signal", report.MaterializationAudit)
	}
}
