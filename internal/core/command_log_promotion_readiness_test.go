package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestCommandLogPromotionReadinessReportsReadyAfterDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c, err := New(filepath.Join(t.TempDir(), "command-log-readiness.db"), WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	go c.Run(ctx)

	goodPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Readiness applied",
		Body:  "this should materialize",
	})
	if err != nil {
		t.Fatalf("marshal good payload: %v", err)
	}
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, goodPayload, "readiness-applied"); reply.Err != nil {
		t.Fatalf("enqueue good command: %+v", reply.Err)
	}
	badPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "missing",
		Title: "Readiness terminal",
		Body:  "this should terminally fail",
	})
	if err != nil {
		t.Fatalf("marshal bad payload: %v", err)
	}
	if reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, badPayload, "readiness-terminal"); reply.Err != nil {
		t.Fatalf("enqueue bad command: %+v", reply.Err)
	}

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 1,
	})
	if results, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("DrainOnce: %v results=%+v", err, results)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, CommandLogPromotionReadinessConfig{BatchSize: 1})
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
	c, err := New(filepath.Join(t.TempDir(), "command-log-readiness-lag.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	payload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Queued but not committed",
		Body:  "this command is intentionally left pending",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "readiness-lag",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	}); err != nil {
		t.Fatalf("Produce: %v", err)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, CommandLogPromotionReadinessConfig{BatchSize: 1})
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
	c, err := New(filepath.Join(t.TempDir(), "command-log-readiness-missing.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	payload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Committed without materialization",
		Body:  "this command is intentionally not executed",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "readiness-missing",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if err := commandLog.CommitPartition(ctx, partition, record.Offset); err != nil {
		t.Fatalf("CommitPartition: %v", err)
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, CommandLogPromotionReadinessConfig{BatchSize: 1})
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

func TestCommandLogPromotionReadinessFailsWhenPartitionLimitTruncatesCoverage(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c, err := New(filepath.Join(t.TempDir(), "command-log-readiness-limit.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()

	for _, boardID := range []string{"general", "meta"} {
		if _, err := commandLog.Produce(ctx, CommandLogRecord{
			Partition:  LogPartition{Kind: partitionBoard, Key: boardID},
			ActorID:    "usr_readiness_limit",
			CID:        "readiness-limit-" + boardID,
			Command:    proto.CmdCreateThread,
			Payload:    json.RawMessage(`{}`),
			EnqueuedAt: nowMS(),
		}); err != nil {
			t.Fatalf("Produce(%s): %v", boardID, err)
		}
	}

	report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, CommandLogPromotionReadinessConfig{PartitionLimit: 1})
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
