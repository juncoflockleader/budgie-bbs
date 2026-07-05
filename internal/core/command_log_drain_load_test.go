package core

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestAccumulateCommandLogDrainWorkerResultsKeepsPartialProgress(t *testing.T) {
	stage := loadmodel.CommandLogDrainStage{}
	errSamples := map[string]bool{}
	accumulateCommandLogDrainWorkerResults(&stage, errSamples, []CommandLogWorkerResult{
		{
			Partition:        LogPartition{Kind: partitionBoard, Key: "general"},
			Processed:        1,
			Applied:          1,
			TerminalFailures: 1,
			TerminalFailure:  &proto.ErrorDetail{Message: "terminal stop"},
			CommitFailures:   2,
			AssignmentLost:   true,
			ClaimLost:        true,
			RetryableFailure: &proto.ErrorDetail{Message: "retry later"},
			CommitFailure:    "commit failed after retries",
		},
	})

	if stage.Processed != 1 || stage.Applied != 1 || stage.TerminalFailures != 1 || stage.CommitFailures != 2 ||
		stage.RetryableFailures != 1 || stage.AssignmentLosses != 1 || stage.ClaimLosses != 1 {
		t.Fatalf("stage = %+v, want partial worker progress and failures accumulated", stage)
	}
	for _, want := range []string{
		"board/general terminal: terminal stop",
		"board/general retryable: retry later",
		"board/general assignment lost",
		"board/general claim lost",
		"commit failed after retries",
	} {
		if !commandLogDrainTestContainsSample(stage.SampleErrorText, want) {
			t.Fatalf("samples = %+v, missing %q", stage.SampleErrorText, want)
		}
	}
}

func TestAccumulateCommandLogDrainWorkerResultsSamplesFinalizerFailures(t *testing.T) {
	stage := loadmodel.CommandLogDrainStage{}
	errSamples := map[string]bool{}
	accumulateCommandLogDrainWorkerResults(&stage, errSamples, []CommandLogWorkerResult{
		{
			Partition:        LogPartition{Kind: partitionBoard, Key: "general"},
			FinalizerFailure: "native decision failed",
		},
	})

	if !commandLogDrainTestContainsSample(stage.SampleErrorText, "board/general finalizer: native decision failed") {
		t.Fatalf("samples = %+v, want finalizer failure sample", stage.SampleErrorText)
	}
}

func TestCommandLogDrainLoadRunnerReportsSubmitAndDrain(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunCommandLogDrainLoad(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            2,
		CommandsPerBoard:  2,
		SubmitConcurrency: 2,
		Writers:           2,
		BatchSize:         1,
		BodyBytes:         32,
		BoardPrefix:       "cmdlogtest",
		UserName:          "cmdlog_runner",
	})
	if err != nil {
		t.Fatalf("RunCommandLogDrainLoad: %v", err)
	}
	if report.TotalCommands != 4 {
		t.Fatalf("total commands = %d, want 4", report.TotalCommands)
	}
	if report.Submit.Succeeded != 4 || report.Submit.Failed != 0 {
		t.Fatalf("submit report = %+v, want all commands submitted", report.Submit)
	}
	if report.MaxPartitionLagBeforeDrain != 2 {
		t.Fatalf("max lag before drain = %d, want 2", report.MaxPartitionLagBeforeDrain)
	}
	if report.Drain.Applied != 4 || report.Drain.Processed != 4 || report.Drain.TerminalFailures != 0 || report.Drain.RetryableFailures != 0 {
		t.Fatalf("drain report = %+v, want all commands applied", report.Drain)
	}
	if report.Drain.Rounds < 2 {
		t.Fatalf("drain rounds = %d, want multiple rounds with batch size 1", report.Drain.Rounds)
	}
	if report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("max lag after drain = %d, want 0", report.MaxPartitionLagAfterDrain)
	}
	if !report.PromotionReadiness.Ready || report.PromotionReadiness.LaggingPartitions != 0 || report.PromotionReadiness.TotalLag != 0 {
		t.Fatalf("promotion readiness = %+v, want ready with no lag", report.PromotionReadiness)
	}
	if !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 4 || report.MaterializationAudit.MissingMaterialization != 0 {
		t.Fatalf("materialization audit = %+v, want all applied and complete", report.MaterializationAudit)
	}
	for _, boardID := range []string{"cmdlogtest_00", "cmdlogtest_01"} {
		threads, err := c.ListThreads(boardID, 10, 0)
		if err != nil {
			t.Fatalf("ListThreads(%s): %v", boardID, err)
		}
		if len(threads) != 2 {
			t.Fatalf("board %s threads = %d, want 2", boardID, len(threads))
		}
	}
}

func TestCommandLogDrainLoadRunnerKeepsFailedCreateSubmitEvidence(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunCommandLogDrainLoadWithCommandLog(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  2,
		SubmitConcurrency: 1,
		Writers:           1,
		BatchSize:         1,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogfailedsubmit",
		UserName:          "cmdlog_failed_submit_runner",
	}, failingProduceCommandLog{err: errors.New("synthetic produce failure")})
	if err == nil {
		t.Fatal("RunCommandLogDrainLoadWithCommandLog succeeded, want submit failure")
	}
	if report.Submit.Commands != 2 || report.Submit.Succeeded != 0 || report.Submit.Failed != 2 {
		t.Fatalf("submit stage = %+v, want failed create submit evidence", report.Submit)
	}
	if !commandLogDrainTestContainsSample(report.Submit.SampleErrorText, "synthetic produce failure") {
		t.Fatalf("submit samples = %+v, want synthetic failure", report.Submit.SampleErrorText)
	}
	if report.FinishedAt == 0 {
		t.Fatal("FinishedAt = 0, want failed report timestamped")
	}
}

func TestCommandLogDrainLoadRunnerKeepsFailedReplySubmitEvidence(t *testing.T) {
	harness := newBrokerCommandEventTestHarness()
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunNativeCommandEventProjectionLoadWithStores(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  2,
		RepliesPerThread:  1,
		SubmitConcurrency: 1,
		Writers:           1,
		BatchSize:         1,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogfailedreply",
		UserName:          "cmdlog_failed_reply_runner",
	}, failAppendPostProduceCommandLog{
		BrokerCommandLog: harness.commandLog,
		err:              errors.New("synthetic reply produce failure"),
	}, harness.transactionStore, harness.eventStore)
	if err == nil {
		t.Fatal("RunNativeCommandEventProjectionLoadWithStores succeeded, want reply submit failure")
	}
	if report.Submit.Commands != 4 || report.Submit.Succeeded != 2 || report.Submit.Failed != 2 {
		t.Fatalf("err = %v; submit stage = %+v, want merged create success and failed reply evidence", err, report.Submit)
	}
	if !commandLogDrainTestContainsSample(report.Submit.SampleErrorText, "synthetic reply produce failure") {
		t.Fatalf("submit samples = %+v, want synthetic reply failure", report.Submit.SampleErrorText)
	}
	if report.Drain.Applied != 2 {
		t.Fatalf("drain stage = %+v, want create-thread drain evidence preserved before reply failure", report.Drain)
	}
	if report.FinishedAt == 0 {
		t.Fatal("FinishedAt = 0, want failed report timestamped")
	}
}

func TestCommandLogDrainLoadRunnerKeepsFailedCreateDrainEvidence(t *testing.T) {
	baseLog := NewMemoryCommandLog()
	commandLog := noFetchCommandLog{CommandLog: baseLog, partitions: baseLog, offsets: baseLog}
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunCommandLogDrainLoadWithCommandLog(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  1,
		SubmitConcurrency: 1,
		Writers:           1,
		BatchSize:         1,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogfaileddrain",
		UserName:          "cmdlog_failed_drain_runner",
	}, commandLog)
	if err == nil {
		t.Fatal("RunCommandLogDrainLoadWithCommandLog succeeded, want drain failure")
	}
	if report.Submit.Succeeded != 1 {
		t.Fatalf("submit stage = %+v, want successful create submit before drain failure", report.Submit)
	}
	if report.Drain.Commands != 1 || report.Drain.Rounds != 3 || report.Drain.Processed != 0 {
		t.Fatalf("drain stage = %+v, want failed create drain evidence preserved", report.Drain)
	}
	if !commandLogDrainTestContainsSample(report.Drain.SampleErrorText, "command log drain load: no worker progress after 3 rounds with lag 1") {
		t.Fatalf("drain samples = %+v, want no-progress sample", report.Drain.SampleErrorText)
	}
	if report.FinishedAt == 0 {
		t.Fatal("FinishedAt = 0, want failed report timestamped")
	}
}

func commandLogDrainTestContainsSample(samples []string, want string) bool {
	for _, sample := range samples {
		if sample == want {
			return true
		}
	}
	return false
}

type failingProduceCommandLog struct {
	err error
}

func (l failingProduceCommandLog) Produce(context.Context, CommandLogRecord) (CommandLogRecord, error) {
	return CommandLogRecord{}, l.err
}

func (l failingProduceCommandLog) FetchPartition(context.Context, LogPartition, int64, int) ([]CommandLogRecord, error) {
	return nil, nil
}

func (l failingProduceCommandLog) CommitPartition(context.Context, LogPartition, int64) error {
	return nil
}

func (l failingProduceCommandLog) CommittedOffset(context.Context, LogPartition) (int64, error) {
	return 0, nil
}

type failAppendPostProduceCommandLog struct {
	*BrokerCommandLog
	err error
}

func (l failAppendPostProduceCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	if record.Command == proto.CmdAppendPost {
		return CommandLogRecord{}, l.err
	}
	return l.BrokerCommandLog.Produce(ctx, record)
}

type noFetchCommandLog struct {
	CommandLog
	partitions CommandPartitionLister
	offsets    CommandPartitionOffsetLister
}

func (l noFetchCommandLog) FetchPartition(context.Context, LogPartition, int64, int) ([]CommandLogRecord, error) {
	return nil, nil
}

func (l noFetchCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	return l.partitions.ListCommandPartitions(ctx, limit)
}

func (l noFetchCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	return l.offsets.ListCommandPartitionOffsets(ctx, limit)
}

func TestCommandLogDrainLoadRunnerCanSubmitThroughAuthoritativeCommandLog(t *testing.T) {
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunAuthoritativeCommandLogDrainLoad(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            2,
		CommandsPerBoard:  2,
		SubmitConcurrency: 2,
		Writers:           2,
		BatchSize:         1,
		BodyBytes:         32,
		BoardPrefix:       "cmdlogauth",
		UserName:          "cmdlog_auth_runner",
	})
	if err != nil {
		t.Fatalf("RunAuthoritativeCommandLogDrainLoad: %v", err)
	}
	if !report.Config.AuthoritativeSubmit {
		t.Fatalf("config = %+v, want authoritative submit mode", report.Config)
	}
	if report.Submit.Succeeded != 4 || report.Submit.Failed != 0 {
		t.Fatalf("submit report = %+v, want authoritative pending acks for all commands", report.Submit)
	}
	if report.Drain.Applied != 4 || report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("drain report = %+v maxLag=%d, want all materialized with no lag", report.Drain, report.MaxPartitionLagAfterDrain)
	}
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete {
		t.Fatalf("promotion readiness = %+v audit = %+v, want ready and complete", report.PromotionReadiness, report.MaterializationAudit)
	}
	for _, boardID := range []string{"cmdlogauth_00", "cmdlogauth_01"} {
		threads, err := c.ListThreads(boardID, 10, 0)
		if err != nil {
			t.Fatalf("ListThreads(%s): %v", boardID, err)
		}
		if len(threads) != 2 {
			t.Fatalf("board %s threads = %d, want 2", boardID, len(threads))
		}
	}
}

func TestCommandLogDrainLoadRunnerCanUseNativeCommandEventProjection(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunNativeCommandEventProjectionLoad(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            2,
		CommandsPerBoard:  2,
		SubmitConcurrency: 2,
		Writers:           2,
		BatchSize:         1,
		BodyBytes:         32,
		BoardPrefix:       "cmdlognative",
		UserName:          "cmdlog_native_runner",
	})
	if err != nil {
		t.Fatalf("RunNativeCommandEventProjectionLoad: %v", err)
	}
	if report.Config.ExecutorMode != loadmodel.CommandLogDrainExecutorNative {
		t.Fatalf("executor mode = %q, want native", report.Config.ExecutorMode)
	}
	if report.Submit.Succeeded != 4 || report.Submit.Failed != 0 {
		t.Fatalf("submit report = %+v, want all commands submitted", report.Submit)
	}
	if report.Drain.Applied != 4 || report.Drain.Processed != 4 || report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("drain report = %+v maxLag=%d, want all native commands committed", report.Drain, report.MaxPartitionLagAfterDrain)
	}
	if !report.EventProjection.Enabled || report.EventProjection.ExpectedEvents != 8 || report.EventProjection.AppliedEvents != 8 {
		t.Fatalf("event projection = %+v, want 8 expected/projected broker events", report.EventProjection)
	}
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 4 {
		t.Fatalf("promotion readiness = %+v audit = %+v, want ready native materialization", report.PromotionReadiness, report.MaterializationAudit)
	}
	for _, boardID := range []string{"cmdlognative_00", "cmdlognative_01"} {
		threads, err := c.ListThreads(boardID, 10, 0)
		if err != nil {
			t.Fatalf("ListThreads(%s): %v", boardID, err)
		}
		if len(threads) != 2 {
			t.Fatalf("board %s threads = %d, want 2", boardID, len(threads))
		}
	}
}

func TestCommandLogDrainLoadRunnerCanUseNativeAppendPostProjection(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunNativeCommandEventProjectionLoad(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  2,
		RepliesPerThread:  2,
		DirectedReplies:   true,
		SubmitConcurrency: 2,
		Writers:           2,
		BatchSize:         1,
		BodyBytes:         32,
		BoardPrefix:       "cmdlogreply",
		UserName:          "cmdlog_reply_runner",
	})
	if err != nil {
		t.Fatalf("RunNativeCommandEventProjectionLoad replies: %v", err)
	}
	if report.Config.ExecutorMode != loadmodel.CommandLogDrainExecutorNative || !report.Config.DirectedReplies {
		t.Fatalf("config = %+v, want native directed reply load", report.Config)
	}
	if report.TotalCommands != 6 || report.Submit.Succeeded != 6 || report.Drain.Applied != 6 {
		t.Fatalf("report total=%d submit=%+v drain=%+v, want six create/reply commands applied", report.TotalCommands, report.Submit, report.Drain)
	}
	if !report.EventProjection.Enabled || report.EventProjection.ExpectedEvents != 8 || report.EventProjection.AppliedEvents != 8 {
		t.Fatalf("event projection = %+v, want 8 expected/projected broker events", report.EventProjection)
	}
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 6 {
		t.Fatalf("promotion readiness = %+v audit = %+v, want ready appendPost materialization", report.PromotionReadiness, report.MaterializationAudit)
	}
	threads, err := c.ListThreads("cmdlogreply_00", 10, 0)
	if err != nil {
		t.Fatalf("ListThreads(cmdlogreply_00): %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(threads))
	}
	for _, thread := range threads {
		posts, err := c.ListPosts(thread.ID, 10, 0)
		if err != nil {
			t.Fatalf("ListPosts(%s): %v", thread.ID, err)
		}
		if len(posts) != 3 {
			t.Fatalf("thread %s posts = %d, want root plus two replies", thread.ID, len(posts))
		}
		for _, post := range posts[1:] {
			if post.ReplyTo != posts[0].ID {
				t.Fatalf("thread %s reply %s ReplyTo = %q, want root %q", thread.ID, post.ID, post.ReplyTo, posts[0].ID)
			}
		}
	}
}

func TestCommandLogDrainLoadRunnerUsesNativeBatchTransactionStore(t *testing.T) {
	harness := newBrokerCommandEventTestHarness()
	transactionClient := &countingBatchBrokerCommandEventTransactionClient{
		inner: NewMemoryBrokerCommandEventTransactionClient(harness.commandClient, harness.eventClient),
	}
	commandLog := &countingCommandLogOffsetAccess{
		CommandLog: harness.commandLog,
		offsets:    harness.commandLog,
	}
	transactionStore := NewBrokerCommandEventTransactionStore(transactionClient)
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunNativeCommandEventProjectionLoadWithStores(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            2,
		CommandsPerBoard:  2,
		RepliesPerThread:  1,
		DirectedReplies:   true,
		SubmitConcurrency: 2,
		Writers:           1,
		BatchSize:         10,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogbatchdrain",
		UserName:          "cmdlog_batch_drain_runner",
	}, commandLog, transactionStore, harness.eventStore)
	if err != nil {
		t.Fatalf("RunNativeCommandEventProjectionLoadWithStores batch drain: %v", err)
	}
	if report.TotalCommands != 8 || report.Submit.Succeeded != 8 || report.Drain.Applied != 8 || report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("report total=%d submit=%+v drain=%+v maxLag=%d, want all create/reply commands applied", report.TotalCommands, report.Submit, report.Drain, report.MaxPartitionLagAfterDrain)
	}
	if !report.EventProjection.Enabled || report.EventProjection.ExpectedEvents != 12 || report.EventProjection.AppliedEvents != 12 {
		t.Fatalf("event projection = %+v, want 12 expected/projected broker events", report.EventProjection)
	}
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 8 {
		t.Fatalf("promotion readiness = %+v audit = %+v, want ready batch-drained materialization", report.PromotionReadiness, report.MaterializationAudit)
	}
	batchCalls, singleCalls, batchCommandCounts := transactionClient.snapshot()
	if singleCalls != 0 {
		t.Fatalf("single transaction calls = %d, want fixture to use batch transaction store", singleCalls)
	}
	if batchCalls == 0 {
		t.Fatal("batch transaction calls = 0, want fixture to use batch transaction store")
	}
	maxBatchCommands := 0
	for _, count := range batchCommandCounts {
		if count > maxBatchCommands {
			maxBatchCommands = count
		}
	}
	if maxBatchCommands < 2 {
		t.Fatalf("batch command counts = %+v, want at least one multi-partition batch", batchCommandCounts)
	}
	if batchCalls >= report.Drain.Processed {
		t.Fatalf("batch calls = %d processed = %d, want coalesced commits across commands", batchCalls, report.Drain.Processed)
	}
	committedOffsetCalls, listOffsetCalls := commandLog.snapshot()
	if committedOffsetCalls != 0 {
		t.Fatalf("CommittedOffset calls = %d, want native batch drain to use partition-offset snapshots", committedOffsetCalls)
	}
	if listOffsetCalls == 0 {
		t.Fatal("ListCommandPartitionOffsets calls = 0, want partition-offset snapshots to drive native batch drain")
	}
}

func TestCommandLogDrainLoadRunnerFailsClosedOnNativeEventProjectionPartitionLimit(t *testing.T) {
	harness := newBrokerCommandEventTestHarness()
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if err := harness.eventStore.SeedEventPartitionOffset(ctx, LogPartition{Kind: partitionUser, Key: "projection-limit-overflow"}, 1); err != nil {
		t.Fatalf("SeedEventPartitionOffset: %v", err)
	}
	report, err := c.RunNativeCommandEventProjectionLoadWithStores(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  1,
		SubmitConcurrency: 1,
		Writers:           1,
		BatchSize:         1,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogprojectionlimit",
		UserName:          "cmdlog_projection_limit_runner",
	}, harness.commandLog, harness.transactionStore, harness.eventStore)
	if err == nil {
		t.Fatalf("RunNativeCommandEventProjectionLoadWithStores succeeded, want partition-limit error")
	}
	if !report.EventProjection.Enabled || !report.EventProjection.PartitionLimitExceeded {
		t.Fatalf("event projection = %+v, want enabled partition-limit failure", report.EventProjection)
	}
	if report.EventProjection.PartitionLimit != 1 {
		t.Fatalf("event projection partition limit = %d, want 1", report.EventProjection.PartitionLimit)
	}
}

func TestCommandLogDrainLoadRunnerCanUseAuthoritativeNativeSnapshotReplies(t *testing.T) {
	harness := newBrokerCommandEventTestHarness()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(harness.commandLog))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	report, err := c.RunNativeCommandEventProjectionLoadWithStores(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:              2,
		CommandsPerBoard:    2,
		RepliesPerThread:    1,
		DirectedReplies:     true,
		SubmitConcurrency:   2,
		Writers:             2,
		BatchSize:           1,
		BodyBytes:           32,
		BoardPrefix:         "cmdlogauthnative",
		UserName:            "cmdlog_auth_native_runner",
		AssignmentMode:      loadmodel.CommandLogDrainAssignmentSnapshot,
		AuthoritativeSubmit: true,
	}, harness.commandLog, harness.transactionStore, harness.eventStore)
	if err != nil {
		t.Fatalf("RunNativeCommandEventProjectionLoadWithStores authoritative native replies: %v", err)
	}
	if report.Config.ExecutorMode != loadmodel.CommandLogDrainExecutorNative || !report.Config.AuthoritativeSubmit || report.Config.AssignmentMode != loadmodel.CommandLogDrainAssignmentSnapshot {
		t.Fatalf("config = %+v, want authoritative native snapshot-assignment", report.Config)
	}
	if report.TotalCommands != 8 || report.Submit.Succeeded != 8 || report.Submit.Failed != 0 {
		t.Fatalf("report total=%d submit=%+v, want eight pending-submitted commands", report.TotalCommands, report.Submit)
	}
	if report.Drain.Applied != 8 || report.Drain.AssignmentLosses != 0 || report.Drain.CommitFailures != 0 || report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("drain=%+v maxLag=%d, want all commands committed with no ownership or lag trouble", report.Drain, report.MaxPartitionLagAfterDrain)
	}
	if !report.EventProjection.Enabled || report.EventProjection.ExpectedEvents != 12 || report.EventProjection.AppliedEvents != 12 {
		t.Fatalf("event projection = %+v, want 12 expected/projected broker events", report.EventProjection)
	}
	if !report.PromotionReadiness.Ready || report.PromotionReadiness.Partitions != 6 || report.PromotionReadiness.TotalLag != 0 {
		t.Fatalf("promotion readiness = %+v, want ready coverage across board and thread partitions", report.PromotionReadiness)
	}
	if !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 8 || report.MaterializationAudit.MissingMaterialization != 0 {
		t.Fatalf("materialization audit = %+v, want every committed command explained", report.MaterializationAudit)
	}
	for _, boardID := range []string{"cmdlogauthnative_00", "cmdlogauthnative_01"} {
		threads, err := c.ListThreads(boardID, 10, 0)
		if err != nil {
			t.Fatalf("ListThreads(%s): %v", boardID, err)
		}
		if len(threads) != 2 {
			t.Fatalf("board %s threads = %d, want 2", boardID, len(threads))
		}
		for _, thread := range threads {
			posts, err := c.ListPosts(thread.ID, 10, 0)
			if err != nil {
				t.Fatalf("ListPosts(%s): %v", thread.ID, err)
			}
			if len(posts) != 2 {
				t.Fatalf("thread %s posts = %d, want root plus directed reply", thread.ID, len(posts))
			}
			if posts[1].ReplyTo != posts[0].ID {
				t.Fatalf("thread %s reply %s ReplyTo = %q, want root %q", thread.ID, posts[1].ID, posts[1].ReplyTo, posts[0].ID)
			}
		}
	}
}

func TestCommandLogDrainLoadRunnerCanUseSnapshotAssignment(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	baseLog := NewMemoryCommandLog()
	commandLog := commandLogWithOffsetsOnly{
		CommandLog: baseLog,
		offsets:    baseLog,
	}

	report, err := c.RunCommandLogDrainLoadWithCommandLog(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            3,
		CommandsPerBoard:  2,
		SubmitConcurrency: 3,
		Writers:           2,
		BatchSize:         1,
		BodyBytes:         16,
		BoardPrefix:       "cmdlogsnapshot",
		UserName:          "cmdlog_snapshot_runner",
		AssignmentMode:    loadmodel.CommandLogDrainAssignmentSnapshot,
	}, commandLog)
	if err != nil {
		t.Fatalf("RunCommandLogDrainLoadWithCommandLog snapshot assignment: %v", err)
	}
	if report.Config.AssignmentMode != loadmodel.CommandLogDrainAssignmentSnapshot {
		t.Fatalf("assignment mode = %q, want snapshot", report.Config.AssignmentMode)
	}
	if report.Submit.Succeeded != 6 || report.Drain.Applied != 6 || report.MaxPartitionLagAfterDrain != 0 {
		t.Fatalf("report submit=%+v drain=%+v maxLag=%d, want all commands drained", report.Submit, report.Drain, report.MaxPartitionLagAfterDrain)
	}
	if report.Drain.AssignmentLosses != 0 || report.Drain.ClaimLosses != 0 {
		t.Fatalf("drain ownership losses = assignment %d claim %d, want none", report.Drain.AssignmentLosses, report.Drain.ClaimLosses)
	}
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete {
		t.Fatalf("promotion readiness = %+v audit = %+v, want ready and complete", report.PromotionReadiness, report.MaterializationAudit)
	}
}

func TestCommandLogDrainLoadSnapshotAssignerListsOnlyLaggingPartitions(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryCommandLog()
	countingOffsets := &countingCommandPartitionOffsetLister{inner: log}
	complete := LogPartition{Kind: partitionBoard, Key: "complete"}.Normalize()
	lagging := LogPartition{Kind: partitionBoard, Key: "lagging"}.Normalize()
	completeRecord := produceCommandLogWorkerRecord(t, ctx, log, complete, "cid-complete")
	laggingRecord := produceCommandLogWorkerRecord(t, ctx, log, lagging, "cid-lagging")
	produceCommandLogWorkerRecord(t, ctx, log, lagging, "cid-lagging-next")
	if err := log.CommitPartition(ctx, complete, completeRecord.Offset); err != nil {
		t.Fatalf("CommitPartition complete: %v", err)
	}

	rawAssigner, err := newCommandLogDrainLoadAssigner(ctx, commandLogWithOffsetsOnly{
		CommandLog: log,
		offsets:    countingOffsets,
	}, []string{"writer-a", "writer-b"}, loadmodel.CommandLogDrainLoadConfig{
		AssignmentMode: loadmodel.CommandLogDrainAssignmentSnapshot,
	}, 10)
	if err != nil {
		t.Fatalf("newCommandLogDrainLoadAssigner: %v", err)
	}
	assigner, ok := rawAssigner.(commandLogDrainLoadSnapshotAssigner)
	if !ok {
		t.Fatalf("assigner = %T, want commandLogDrainLoadSnapshotAssigner", rawAssigner)
	}
	if countingOffsets.calls != 1 {
		t.Fatalf("offset lister calls after assigner creation = %d, want one shared snapshot", countingOffsets.calls)
	}

	owner := assigner.Snapshot().Owners[lagging]
	if err := log.CommitPartition(ctx, lagging, laggingRecord.Offset); err != nil {
		t.Fatalf("CommitPartition lagging first record: %v", err)
	}
	cursors, err := commandLogDrainLoadMemberPartitionCursors(ctx, commandLogWithOffsetsOnly{
		CommandLog: log,
		offsets:    countingOffsets,
	}, assigner, owner, 10)
	if err != nil {
		t.Fatalf("commandLogDrainLoadMemberPartitionCursors: %v", err)
	}
	if countingOffsets.calls != 2 {
		t.Fatalf("offset lister calls after member cursor list = %d, want fresh command-log snapshot after assignment snapshot", countingOffsets.calls)
	}
	if len(cursors) != 1 || cursors[0].partition != lagging || cursors[0].committedOffset != laggingRecord.Offset {
		t.Fatalf("cursors = %+v, want one cursor for lagging partition after committed offset %d", cursors, laggingRecord.Offset)
	}
	assignments, err := assigner.ListAssignedCommandPartitions(ctx, owner, 10)
	if err != nil {
		t.Fatalf("ListAssignedCommandPartitions: %v", err)
	}
	if countingOffsets.calls != 2 {
		t.Fatalf("offset lister calls after first assignment list = %d, want no extra command-log snapshot", countingOffsets.calls)
	}
	if _, err := assigner.ListAssignedCommandPartitions(ctx, owner, 10); err != nil {
		t.Fatalf("second ListAssignedCommandPartitions: %v", err)
	}
	if countingOffsets.calls != 2 {
		t.Fatalf("offset lister calls after second assignment list = %d, want still no extra command-log snapshot", countingOffsets.calls)
	}
	if len(assignments) != 1 || assignments[0].Partition != lagging {
		t.Fatalf("assignments = %+v, want only lagging partition %s/%s", assignments, lagging.Kind, lagging.Key)
	}
	if assignment, assigned, err := assigner.AssignCommandPartition(ctx, owner, lagging); err != nil || !assigned || assignment.OwnerID != owner {
		t.Fatalf("AssignCommandPartition lagging = %+v assigned=%v err=%v, want owner %q", assignment, assigned, err, owner)
	}
}

func TestCommandLogDrainLoadRejectsUnsupportedAssignmentMode(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	_, err := c.RunCommandLogDrainLoad(ctx, loadmodel.CommandLogDrainLoadConfig{
		Boards:            1,
		CommandsPerBoard:  1,
		SubmitConcurrency: 1,
		Writers:           1,
		BatchSize:         1,
		BodyBytes:         8,
		BoardPrefix:       "cmdlogbadassign",
		UserName:          "cmdlog_bad_assignment_runner",
		AssignmentMode:    "etcd",
	})
	if err == nil {
		t.Fatalf("RunCommandLogDrainLoad with unsupported assignment mode succeeded, want error")
	}
}

type commandLogWithOffsetsOnly struct {
	CommandLog
	offsets CommandPartitionOffsetLister
}

func (l commandLogWithOffsetsOnly) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	return l.offsets.ListCommandPartitionOffsets(ctx, limit)
}

type countingCommandPartitionOffsetLister struct {
	inner CommandPartitionOffsetLister
	calls int
}

func (l *countingCommandPartitionOffsetLister) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	l.calls++
	return l.inner.ListCommandPartitionOffsets(ctx, limit)
}

type countingBatchBrokerCommandEventTransactionClient struct {
	mu                 sync.Mutex
	inner              *MemoryBrokerCommandEventTransactionClient
	singleCalls        int
	batchCalls         int
	batchCommandCounts []int
}

func (c *countingBatchBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	c.mu.Lock()
	c.singleCalls++
	c.mu.Unlock()
	return c.inner.AppendEventsAndCommitCommand(ctx, command, records)
}

func (c *countingBatchBrokerCommandEventTransactionClient) AppendEventsAndCommitCommands(ctx context.Context, commands []CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionBatchResult, error) {
	c.mu.Lock()
	c.batchCalls++
	c.batchCommandCounts = append(c.batchCommandCounts, len(commands))
	c.mu.Unlock()
	return c.inner.AppendEventsAndCommitCommands(ctx, commands, records)
}

func (c *countingBatchBrokerCommandEventTransactionClient) snapshot() (int, int, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.batchCalls, c.singleCalls, append([]int(nil), c.batchCommandCounts...)
}

type countingCommandLogOffsetAccess struct {
	CommandLog
	offsets              CommandPartitionOffsetLister
	mu                   sync.Mutex
	committedOffsetCalls int
	listOffsetCalls      int
}

func (l *countingCommandLogOffsetAccess) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	l.mu.Lock()
	l.committedOffsetCalls++
	l.mu.Unlock()
	return l.CommandLog.CommittedOffset(ctx, partition)
}

func (l *countingCommandLogOffsetAccess) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	l.mu.Lock()
	l.listOffsetCalls++
	l.mu.Unlock()
	return l.offsets.ListCommandPartitionOffsets(ctx, limit)
}

func (l *countingCommandLogOffsetAccess) snapshot() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.committedOffsetCalls, l.listOffsetCalls
}
