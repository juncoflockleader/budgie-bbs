package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func requireCommandLogWorkerCommittedOffset(t *testing.T, ctx context.Context, log interface {
	CommittedOffset(context.Context, LogPartition) (int64, error)
}, partition LogPartition, want int64, label string) {
	t.Helper()
	if got, err := log.CommittedOffset(ctx, partition); err != nil || got != want {
		t.Fatalf("%s = %d, %v; want %d, nil", label, got, err, want)
	}
}

func drainCommandLogWorkerOnce(t *testing.T, ctx context.Context, worker *CommandLogWorker, label string) []CommandLogWorkerResult {
	t.Helper()
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return results
}

func replayCommandLogWorkerPartition(t *testing.T, ctx context.Context, store EventStore, partition LogPartition, after int64, limit int, label string) []*proto.Event {
	t.Helper()
	events, err := store.ReplayPartition(ctx, partition.Kind, partition.Key, after, limit)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return events
}

func commandLogWorkerThreadNewEventWithID(eventID, threadID, title string) EventAppend {
	return EventAppend{
		ID:     eventID,
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       threadID,
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    title,
			TS:       1234,
		},
		TS: 1234,
	}
}

func commandLogWorkerThreadNewEvent(record CommandLogRecord, eventIDPrefix, titlePrefix string) []EventAppend {
	return []EventAppend{
		commandLogWorkerThreadNewEventWithID(eventIDPrefix+record.CID, "thr_"+record.CID, titlePrefix+record.CID),
	}
}

func TestCommandLogWorkerCommitsSuccessfulRecordsInPartitionOrder(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-2")

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.Partition != partition.Normalize() || result.StartedOffset != 0 || result.LastOffset != 2 || result.Processed != 2 {
		t.Fatalf("result = %+v, want partition processed through offset 2", result)
	}
	if result.TerminalFailures != 0 || result.RetryableFailure != nil {
		t.Fatalf("result failures = terminal %d retryable %+v, want none", result.TerminalFailures, result.RetryableFailure)
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Fatalf("seen offsets = %v, want [1 2]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 2, "committed offset")
}

func TestCommandLogWorkerDrainsPartitionsConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	produceCommandLogWorkerRecord(t, ctx, log, general, "cid-concurrent-general")
	produceCommandLogWorkerRecord(t, ctx, log, life, "cid-concurrent-life")

	started := make(chan LogPartition, 2)
	release := make(chan struct{})
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		BatchSize:            10,
		PartitionConcurrency: 2,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			started <- record.Partition.Normalize()
			select {
			case <-release:
			case <-ctx.Done():
			}
			return commandexec.Reply{}
		}),
	})

	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := worker.DrainOnce(ctx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()

	first := waitForCommandLogWorkerStart(t, started)
	second := waitForCommandLogWorkerStart(t, started)
	if first == second {
		close(release)
		t.Fatalf("concurrent starts = %v and %v, want distinct partitions", first, second)
	}
	close(release)
	drain := waitForCommandLogWorkerDrain(t, done)
	if drain.err != nil {
		t.Fatalf("drain once: %v", drain.err)
	}
	byPartition := commandLogWorkerResultsByPartition(drain.results)
	for _, partition := range []LogPartition{general.Normalize(), life.Normalize()} {
		result := byPartition[partition]
		if result.Processed != 1 || result.LastOffset != 1 {
			t.Fatalf("result for %+v = %+v, want one processed command", partition, result)
		}
	}
}

func TestCommandLogWorkerPartitionConcurrencyPreservesPartitionOrder(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	produceCommandLogWorkerRecord(t, ctx, log, general, "cid-order-general-1")
	produceCommandLogWorkerRecord(t, ctx, log, general, "cid-order-general-2")
	produceCommandLogWorkerRecord(t, ctx, log, life, "cid-order-life-1")
	produceCommandLogWorkerRecord(t, ctx, log, life, "cid-order-life-2")

	seen := map[LogPartition][]int64{}
	var mu sync.Mutex
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		BatchSize:            10,
		PartitionConcurrency: 2,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			mu.Lock()
			seen[record.Partition.Normalize()] = append(seen[record.Partition.Normalize()], record.Offset)
			mu.Unlock()
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two partition results", results)
	}
	for _, partition := range []LogPartition{general.Normalize(), life.Normalize()} {
		if !reflect.DeepEqual(seen[partition], []int64{1, 2}) {
			t.Fatalf("seen offsets for %+v = %v, want [1 2]", partition, seen[partition])
		}
		if got, err := log.CommittedOffset(ctx, partition); err != nil || got != 2 {
			t.Fatalf("committed offset for %+v = %d, %v; want 2, nil", partition, got, err)
		}
	}
}

func TestCommandLogWorkerAllowsRebalanceAfterFetchedBatch(t *testing.T) {
	ctx := context.Background()
	base := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	log := &rebalanceAwareCommandLog{CommandLog: base}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, base, partition, "cid-rebalance")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:        log,
		Partitions: base,
		BatchSize:  10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 {
		t.Fatalf("results = %+v, want one processed command", results)
	}
	if log.allowRebalanceCalls != 1 {
		t.Fatalf("allow rebalance calls = %d, want 1", log.allowRebalanceCalls)
	}
}

func TestCommandLogWorkerDoesNotCommitWhenAppliedReceiptFails(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-applied-fails")
	wantErr := errors.New("applied receipt unavailable")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
			return wantErr
		}),
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_applied_fails", Seq: 1}}
		}),
	})

	results, err := worker.DrainOnce(ctx)
	if !errors.Is(err, wantErr) {
		t.Fatalf("drain err = %v, want applied receipt error", err)
	}
	if len(results) != 1 || results[0].Processed != 0 || results[0].LastOffset != 0 {
		t.Fatalf("results = %+v, want no committed progress", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

type rebalanceAwareCommandLog struct {
	CommandLog
	allowRebalanceCalls int
}

func (l *rebalanceAwareCommandLog) AllowCommandLogRebalance() {
	l.allowRebalanceCalls++
}

func TestCommandLogWorkerStopsBeforeRetryableFailure(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-2")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-3")

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			if record.Offset == 2 {
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "temporary_failure", Message: "try again", Retryable: true}}
			}
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.LastOffset != 1 || result.Processed != 1 || result.RetryableFailure == nil {
		t.Fatalf("result = %+v, want committed through offset 1 and retryable stop at offset 2", result)
	}
	if result.TerminalFailures != 0 {
		t.Fatalf("terminal failures = %d, want 0", result.TerminalFailures)
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Fatalf("seen offsets = %v, want [1 2]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset")
}

func TestCommandLogWorkerCommitsTerminalFailures(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-2")

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			if record.Offset == 1 {
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "validation_failed", Message: "bad command", Retryable: false}}
			}
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.LastOffset != 2 || result.Processed != 2 || result.TerminalFailures != 1 || result.RetryableFailure != nil {
		t.Fatalf("result = %+v, want terminal failure acknowledged and partition committed through offset 2", result)
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Fatalf("seen offsets = %v, want [1 2]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 2, "committed offset")
}

func TestCommandLogWorkerRetriesCommandOffsetCommit(t *testing.T) {
	ctx := context.Background()
	base := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	log := &flakyCommitCommandLog{BrokerCommandLog: base, failCommits: 2}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.LastOffset != 1 || result.Processed != 1 || result.CommitFailures != 2 || result.CommitFailure != "" {
		t.Fatalf("result = %+v, want recovered commit after two failures", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want one command execution", seen)
	}
	if log.commitAttempts != 3 {
		t.Fatalf("commit attempts = %d, want 3", log.commitAttempts)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset")
}

func TestCommandLogWorkerReportsCommandOffsetCommitFailure(t *testing.T) {
	ctx := context.Background()
	base := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	log := &flakyCommitCommandLog{BrokerCommandLog: base, failCommits: 3}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:            log,
		BatchSize:      10,
		CommitAttempts: 2,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results, err := worker.DrainOnce(ctx)
	if err == nil {
		t.Fatalf("drain once succeeded, want commit failure")
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want partial partition result", results)
	}
	result := results[0]
	if result.LastOffset != 0 || result.Processed != 0 || result.CommitFailures != 2 || result.CommitFailure == "" {
		t.Fatalf("result = %+v, want failed commit result without offset advancement", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want one command execution before commit failure", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

func TestCommandLogWorkerSupportsTransactionalFinalizer(t *testing.T) {
	ctx := context.Background()
	commandLog := NewMemoryCommandLog()
	eventStore := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-transactional-finalizer")

	var finalized []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_transactional_finalizer", Seq: 1}}
		}),
		Finalizer: CommandLogFinalizerFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) (CommandLogFinalizationResult, error) {
			finalized = append(finalized, record.Offset)
			if reply.Result == nil {
				return CommandLogFinalizationResult{}, errors.New("missing command result")
			}
			_, err := eventStore.Append(ctx, commandLogWorkerThreadNewEventWithID(
				"evt_transactional_finalizer",
				"thr_transactional_finalizer",
				"Transactional finalizer",
			))
			if err != nil {
				return CommandLogFinalizationResult{}, err
			}
			if err := commandLog.CommitPartition(ctx, record.Partition, record.Offset); err != nil {
				return CommandLogFinalizationResult{}, err
			}
			return CommandLogFinalizationResult{Applied: 1, Committed: true}, nil
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 || results[0].Applied != 1 || results[0].LastOffset != 1 {
		t.Fatalf("results = %+v, want transactional finalizer to apply and commit one record", results)
	}
	if !reflect.DeepEqual(finalized, []int64{1}) {
		t.Fatalf("finalized offsets = %v, want [1]", finalized)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay event partition")
	if len(events) != 1 || events[0].Kind != proto.EvtThreadNew || events[0].PartitionOffset != 1 {
		t.Fatalf("events = %+v, want one broker-appended thread event", events)
	}
}

func TestCommandLogWorkerAllowsSourcePositionBackedSparseOffsets(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	log := newStaticCommandLogForWorker(partition, 1, []CommandLogRecord{
		sparseCommandLogWorkerRecord(partition, 4, CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 3,
			PhysicalOffset:    3,
			CommitOffset:      4,
			LogicalPartition:  partition,
			LogicalOffset:     4,
		}),
	})

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].StartedOffset != 1 || results[0].LastOffset != 4 || results[0].Processed != 1 {
		t.Fatalf("results = %+v, want sparse source-backed progress through offset 4", results)
	}
	if !reflect.DeepEqual(seen, []int64{4}) {
		t.Fatalf("seen offsets = %v, want [4]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 4, "committed offset")
}

func TestCommandLogWorkerRejectsSparseOffsetsWithoutSourcePosition(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	log := newStaticCommandLogForWorker(partition, 1, []CommandLogRecord{
		sparseCommandLogWorkerRecord(partition, 4, CommandLogSourcePosition{}),
	})

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{}
		}),
	})

	results, err := worker.DrainOnce(ctx)
	requireErrorContains(t, err, "offset gap")
	if len(results) != 1 || results[0].LastOffset != 1 || results[0].Processed != 0 {
		t.Fatalf("results = %+v, want no progress after sparse offset without source position", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset")
}

func TestCommandLogWorkerRejectsInvalidSparseSourcePosition(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	log := newStaticCommandLogForWorker(partition, 1, []CommandLogRecord{
		sparseCommandLogWorkerRecord(partition, 4, CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 3,
			PhysicalOffset:    3,
			CommitOffset:      4,
			LogicalPartition:  partition,
			LogicalOffset:     3,
		}),
	})

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{}
		}),
	})

	results, err := worker.DrainOnce(ctx)
	requireErrorContains(t, err, "invalid source position")
	if len(results) != 1 || results[0].LastOffset != 1 || results[0].Processed != 0 {
		t.Fatalf("results = %+v, want no progress after invalid sparse source position", results)
	}
}

func TestCommandLogWorkerSupportsCommandEventTransactionFinalizer(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	eventStore := harness.eventStore
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-command-event-transaction")

	var decided []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_command_event_transaction", Seq: 1}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: transactionStore,
			Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
				decided = append(decided, record.Offset)
				if reply.Result == nil {
					return nil, errors.New("missing command result")
				}
				return []EventAppend{commandLogWorkerThreadNewEventWithID(
					"evt_command_event_transaction",
					"thr_command_event_transaction",
					"Command event transaction",
				)}, nil
			}),
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 || results[0].Applied != 1 || results[0].LastOffset != 1 {
		t.Fatalf("results = %+v, want command/event transaction finalizer to apply and commit one record", results)
	}
	if !reflect.DeepEqual(decided, []int64{1}) {
		t.Fatalf("decided offsets = %v, want [1]", decided)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay event partition")
	if len(events) != 1 || events[0].Kind != proto.EvtThreadNew || events[0].PartitionOffset != 1 || events[0].Seq != 1 {
		t.Fatalf("events = %+v, want one broker-appended thread event", events)
	}
}

func TestCommandLogWorkerBatchFinalizerCommitsFetchedPartitionBatch(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	eventStore := harness.eventStore
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-1")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-2")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-3")

	var calls int
	var gotTx CommandEventTransaction
	var applied []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_batch"}}
		}),
		Finalizer: CommandEventTransactionBatchFinalizer{
			CommandEventTransactionFinalizer: CommandEventTransactionFinalizer{
				Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
					calls++
					gotTx = tx
					return transactionStore.CommitCommandEvents(ctx, tx)
				}),
				Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
					return commandLogWorkerThreadNewEvent(record, "evt_batch_", "Batch "), nil
				}),
				Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
					applied = append(applied, record.Offset)
					if result.Seq <= 0 {
						t.Fatalf("applied result for offset %d missing sequence: %+v", record.Offset, result)
					}
					return nil
				}),
			},
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 3 || results[0].Applied != 3 || results[0].LastOffset != 3 {
		t.Fatalf("results = %+v, want one batch committed through offset 3", results)
	}
	if calls != 1 || gotTx.CommandOffset != 3 || len(gotTx.Events) != 3 {
		t.Fatalf("transaction calls=%d tx=%+v, want one transaction through offset 3 with 3 events", calls, gotTx)
	}
	if !reflect.DeepEqual(applied, []int64{1, 2, 3}) {
		t.Fatalf("applied offsets = %v, want [1 2 3]", applied)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 3, "committed offset")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay event partition")
	if len(events) != 3 {
		t.Fatalf("events = %+v, want 3 broker-appended events", events)
	}
}

func TestCommandLogWorkerBatchFinalizerRecordsIndexedCommit(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	harness := newBrokerCommandEventTestHarness()
	commandLog := NewIndexedCommandLog(harness.commandLog, NewSQLCommandLogPartitionIndex(c.DB))
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-indexed-batch-1")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-indexed-batch-2")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_indexed_batch"}}
		}),
		Finalizer: CommandEventTransactionBatchFinalizer{
			CommandEventTransactionFinalizer: CommandEventTransactionFinalizer{
				Transactions: transactionStore,
				Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
					return commandLogWorkerThreadNewEvent(record, "evt_indexed_batch_", "Indexed Batch "), nil
				}),
			},
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "first DrainOnce")
	if len(results) != 1 || results[0].Processed != 2 || results[0].LastOffset != 2 {
		t.Fatalf("first results = %+v, want indexed batch committed through offset 2", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 2, "indexed committed offset")
	second := drainCommandLogWorkerOnce(t, ctx, worker, "second DrainOnce")
	if len(second) != 1 || second[0].Processed != 0 || second[0].StartedOffset != 2 {
		t.Fatalf("second results = %+v, want no replay from indexed committed offset 2", second)
	}
}

func TestCommandLogWorkerBatchFinalizerStopsBeforeRetryableFailure(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-retry-1")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-retry-2")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-batch-retry-3")

	var calls int
	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			if record.Offset == 2 {
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true}}
			}
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_batch_retry"}}
		}),
		Finalizer: CommandEventTransactionBatchFinalizer{
			CommandEventTransactionFinalizer: CommandEventTransactionFinalizer{
				Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
					calls++
					return transactionStore.CommitCommandEvents(ctx, tx)
				}),
				Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
					return commandLogWorkerThreadNewEvent(record, "evt_batch_retry_", "Batch retry "), nil
				}),
				RetryableFailures: commandLogRetryableFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
					if record.Offset != 2 || errDetail == nil || !errDetail.Retryable {
						t.Fatalf("retryable receipt record=%+v err=%+v, want offset 2 retryable", record, errDetail)
					}
					return nil
				}),
			},
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 || results[0].LastOffset != 1 || results[0].RetryableFailure == nil {
		t.Fatalf("results = %+v, want committed offset 1 and retryable stop at offset 2", results)
	}
	if calls != 1 {
		t.Fatalf("transaction calls = %d, want one flushed successful prefix", calls)
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Fatalf("seen offsets = %v, want [1 2]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
}

func TestCommandLogWorkerUsesPerRecordNativeFinalizerUnlessBatchOptedIn(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-single-finalizer-1")
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-single-finalizer-2")

	var calls int
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_single_finalizer"}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
				calls++
				return transactionStore.CommitCommandEvents(ctx, tx)
			}),
			Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
				return commandLogWorkerThreadNewEvent(record, "evt_single_finalizer_", "Single finalizer "), nil
			}),
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 2 || results[0].LastOffset != 2 {
		t.Fatalf("results = %+v, want two processed records", results)
	}
	if calls != 2 {
		t.Fatalf("transaction calls = %d, want per-record finalizer calls without batch opt-in", calls)
	}
}

func TestCommandLogWorkerReportsCommittedProgressWhenNativeAppliedReceiptFails(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-applied-recorder-error")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_applied_recorder_error", Seq: 1}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: transactionStore,
			Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
				return []EventAppend{commandLogWorkerThreadNewEventWithID(
					"evt_applied_recorder_error",
					"thr_applied_recorder_error",
					"Applied recorder error",
				)}, nil
			}),
			Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
				return errors.New("applied receipt write failed")
			}),
		},
	})

	results, err := worker.DrainOnce(ctx)
	requireErrorContains(t, err, "applied receipt write failed")
	if len(results) != 1 || results[0].Processed != 1 || results[0].Applied != 1 || results[0].LastOffset != 1 {
		t.Fatalf("results = %+v, want committed progress reflected despite recorder error", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
}

func TestCommandEventTransactionFinalizerDoesNotCommitWhenEventDecisionIsInvalid(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	eventStore := harness.eventStore
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-invalid-command-event-transaction")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_invalid_command_event_transaction", Seq: 1}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: transactionStore,
			Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
				return nil, errors.New("native event decision failed")
			}),
		},
	})

	results, err := worker.DrainOnce(ctx)
	if err == nil {
		t.Fatalf("drain once succeeded, want invalid event decision error")
	}
	if len(results) != 1 || results[0].Processed != 0 || results[0].LastOffset != 0 {
		t.Fatalf("results = %+v, want failed transaction without offset advancement", results)
	}
	if !strings.Contains(results[0].FinalizerFailure, "native event decision failed") {
		t.Fatalf("finalizer failure = %q, want invalid event decision detail", results[0].FinalizerFailure)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 0, "committed offset")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay event partition")
	if len(events) != 0 {
		t.Fatalf("events = %+v, want no broker events after invalid transaction", events)
	}
}

func TestCommandLogWorkerReportsNativeTransactionCommitFailure(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-native-transaction-commit-failure")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_native_transaction_commit_failure", Seq: 1}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
				return CommandEventTransactionResult{}, errors.New("injected native transaction commit failure")
			}),
			Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
				return []EventAppend{commandLogWorkerThreadNewEventWithID(
					"evt_native_transaction_commit_failure",
					"thr_native_transaction_commit_failure",
					"Native transaction commit failure",
				)}, nil
			}),
		},
	})

	results, err := worker.DrainOnce(ctx)
	requireErrorContains(t, err, "injected native transaction commit failure")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.Processed != 0 || result.LastOffset != 0 || result.CommitFailures != 1 || !strings.Contains(result.CommitFailure, "injected native transaction commit failure") {
		t.Fatalf("result = %+v, want transaction commit failure without offset progress", result)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 0, "committed offset")
}

func TestCommandLogWorkerReportsNativeRetryableReceiptFailure(t *testing.T) {
	ctx := context.Background()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-native-retryable-receipt-failure")

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Err: &proto.ErrorDetail{Code: "dependency_unavailable", Message: "native retry later", Retryable: true}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
				t.Fatalf("retryable command should not commit a command/event transaction")
				return CommandEventTransactionResult{}, nil
			}),
			RetryableFailures: commandLogRetryableFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
				return errors.New("retryable receipt write failed")
			}),
		},
	})

	results, err := worker.DrainOnce(ctx)
	requireErrorContains(t, err, "retryable receipt write failed")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.Processed != 0 || result.LastOffset != 0 || result.RetryableFailure == nil || result.RetryableFailure.Message != "native retry later" {
		t.Fatalf("result = %+v, want retryable failure evidence without offset progress", result)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 0, "committed offset")
}

func TestCommandEventTransactionFinalizerRecordsTerminalFailureReceipts(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-terminal-command-event-transaction")

	var terminalRecords []CommandLogRecord
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "native command unsupported"}}
		}),
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: transactionStore,
			TerminalFailures: commandLogTerminalFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
				committed, err := commandLog.CommittedOffset(ctx, record.Partition)
				if err != nil {
					t.Fatalf("terminal recorder committed offset: %v", err)
				}
				if committed < record.Offset {
					t.Fatalf("terminal recorder saw committed offset %d before record offset %d", committed, record.Offset)
				}
				terminalRecords = append(terminalRecords, record)
				if errDetail == nil || errDetail.Code != proto.ErrValidationFailed {
					t.Fatalf("terminal err = %+v, want validation failure", errDetail)
				}
				return nil
			}),
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 || results[0].TerminalFailures != 1 || results[0].LastOffset != 1 {
		t.Fatalf("results = %+v, want terminal failure recorded and committed", results)
	}
	if results[0].TerminalFailure == nil || results[0].TerminalFailure.Message != "native command unsupported" {
		t.Fatalf("terminal failure detail = %+v, want native command unsupported", results[0].TerminalFailure)
	}
	if len(terminalRecords) != 1 || terminalRecords[0].Offset != 1 {
		t.Fatalf("terminal records = %+v, want offset 1 recorded", terminalRecords)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
}

func TestCommandLogWorkerClaimsPartitionBeforeDraining(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	claimer := NewSQLCommandPartitionClaimer(c.DB)
	now := int64(1000)
	claimer.now = func() int64 { return now }

	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-2")

	var seenA []int64
	workerA := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		Claims:    claimer,
		OwnerID:   "writer-a",
		ClaimTTL:  100 * time.Millisecond,
		BatchSize: 1,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seenA = append(seenA, record.Offset)
			return commandexec.Reply{}
		}),
	})
	firstResults := drainCommandLogWorkerOnce(t, ctx, workerA, "writer-a drain")
	if len(firstResults) != 1 || !firstResults[0].Claimed || firstResults[0].ClaimOwnerID != "writer-a" || firstResults[0].Processed != 1 {
		t.Fatalf("writer-a results = %+v, want one claimed processed record", firstResults)
	}
	if !reflect.DeepEqual(seenA, []int64{1}) {
		t.Fatalf("writer-a seen offsets = %v, want [1]", seenA)
	}

	var seenB []int64
	workerB := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		Claims:    claimer,
		OwnerID:   "writer-b",
		ClaimTTL:  100 * time.Millisecond,
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seenB = append(seenB, record.Offset)
			return commandexec.Reply{}
		}),
	})
	now = 1050
	skippedResults := drainCommandLogWorkerOnce(t, ctx, workerB, "writer-b drain before lease expiry")
	if len(skippedResults) != 1 || skippedResults[0].Claimed || skippedResults[0].ClaimOwnerID != "writer-a" {
		t.Fatalf("writer-b early results = %+v, want partition skipped because writer-a owns it", skippedResults)
	}
	if len(seenB) != 0 {
		t.Fatalf("writer-b executed before lease expiry: %v", seenB)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset before lease expiry")

	now = 1200
	takeoverResults := drainCommandLogWorkerOnce(t, ctx, workerB, "writer-b drain after lease expiry")
	if len(takeoverResults) != 1 || !takeoverResults[0].Claimed || takeoverResults[0].ClaimOwnerID != "writer-b" || takeoverResults[0].Processed != 1 {
		t.Fatalf("writer-b takeover results = %+v, want claimed processed record", takeoverResults)
	}
	if !reflect.DeepEqual(seenB, []int64{2}) {
		t.Fatalf("writer-b seen offsets = %v, want [2]", seenB)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 2, "committed offset after takeover")
}

func TestCommandLogWorkerStopsWhenClaimLostDuringDrain(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-2")
	claimer := &scriptedCommandPartitionClaimer{steps: []scriptedCommandPartitionClaim{
		{ownerID: "writer-a", acquired: true, expiresAt: 1000},
		{ownerID: "writer-a", acquired: true, expiresAt: 1001},
		{ownerID: "writer-a", acquired: true, expiresAt: 1002},
		{ownerID: "writer-a", acquired: true, expiresAt: 1003},
		{ownerID: "writer-b", acquired: false, expiresAt: 2000},
	}}

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		Claims:    claimer,
		OwnerID:   "writer-a",
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if !result.Claimed || !result.ClaimLost || result.ClaimOwnerID != "writer-b" || result.Processed != 1 || result.LastOffset != 1 {
		t.Fatalf("result = %+v, want one commit then claim loss to writer-b", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset")
	if claimer.calls != len(claimer.steps) {
		t.Fatalf("claim calls = %d, want %d", claimer.calls, len(claimer.steps))
	}
}

func TestCommandLogWorkerDoesNotCommitAfterClaimLostBeforeCommit(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	claimer := &scriptedCommandPartitionClaimer{steps: []scriptedCommandPartitionClaim{
		{ownerID: "writer-a", acquired: true, expiresAt: 1000},
		{ownerID: "writer-a", acquired: true, expiresAt: 1001},
		{ownerID: "writer-b", acquired: false, expiresAt: 2000},
	}}

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		Claims:    claimer,
		OwnerID:   "writer-a",
		BatchSize: 10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if !result.Claimed || !result.ClaimLost || result.ClaimOwnerID != "writer-b" || result.Processed != 0 || result.LastOffset != 0 {
		t.Fatalf("result = %+v, want execution without offset commit after claim loss", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
	if claimer.calls != len(claimer.steps) {
		t.Fatalf("claim calls = %d, want %d", claimer.calls, len(claimer.steps))
	}
}

func TestCommandLogWorkerRefreshesClaimDuringLongExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	claimer := newHeartbeatCommandPartitionClaimer(0)
	releaseExecution := make(chan struct{})
	executionStarted := make(chan struct{})
	var executionStartedOnce sync.Once
	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		Claims:               claimer,
		OwnerID:              "writer-a",
		ClaimTTL:             90 * time.Millisecond,
		ClaimRefreshInterval: 10 * time.Millisecond,
		BatchSize:            10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			executionStartedOnce.Do(func() { close(executionStarted) })
			select {
			case <-releaseExecution:
				return commandexec.Reply{}
			case <-ctx.Done():
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "cancelled", Message: ctx.Err().Error(), Retryable: true}}
			}
		}),
	})

	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := worker.DrainOnce(ctx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()
	select {
	case <-executionStarted:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command execution to start")
	}
	waitForCommandPartitionClaimCalls(t, claimer, 3)
	close(releaseExecution)
	drain := waitForCommandLogWorkerDrain(t, done)
	if drain.err != nil {
		t.Fatalf("drain once: %v", drain.err)
	}
	if len(drain.results) != 1 {
		t.Fatalf("results = %+v, want one partition result", drain.results)
	}
	result := drain.results[0]
	if !result.Claimed || result.ClaimLost || result.LastOffset != 1 || result.Processed != 1 {
		t.Fatalf("result = %+v, want long-running command committed while claim is refreshed", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	if got := claimer.CallCount(); got < 4 {
		t.Fatalf("claim calls = %d, want at least initial, pre-execute, heartbeat, and pre-commit refreshes", got)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 1, "committed offset")
}

func TestCommandLogWorkerDoesNotCommitIfHeartbeatLosesClaimDuringExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	claimer := newHeartbeatCommandPartitionClaimer(3)
	executionStarted := make(chan struct{})
	var executionStartedOnce sync.Once
	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		Claims:               claimer,
		OwnerID:              "writer-a",
		ClaimTTL:             90 * time.Millisecond,
		ClaimRefreshInterval: 10 * time.Millisecond,
		BatchSize:            10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			executionStartedOnce.Do(func() { close(executionStarted) })
			select {
			case <-claimer.Lost():
				return commandexec.Reply{}
			case <-ctx.Done():
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "cancelled", Message: ctx.Err().Error(), Retryable: true}}
			}
		}),
	})

	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := worker.DrainOnce(ctx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()
	select {
	case <-executionStarted:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command execution to start")
	}
	select {
	case <-claimer.Lost():
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for heartbeat to lose the claim")
	}
	drain := waitForCommandLogWorkerDrain(t, done)
	if drain.err != nil {
		t.Fatalf("drain once: %v", drain.err)
	}
	if len(drain.results) != 1 {
		t.Fatalf("results = %+v, want one partition result", drain.results)
	}
	result := drain.results[0]
	if !result.Claimed || !result.ClaimLost || result.ClaimOwnerID != "writer-b" || result.Processed != 0 || result.LastOffset != 0 {
		t.Fatalf("result = %+v, want no command offset commit after heartbeat loses claim", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	if got := claimer.CallCount(); got != 3 {
		t.Fatalf("claim calls = %d, want initial, pre-execute, and losing heartbeat refresh", got)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

func TestCommandLogWorkerDrainsOnlyAssignedPartitions(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	assigner := logmodel.NewHashCommandPartitionAssigner([]string{"writer-a", "writer-b"}, 7)
	owned := commandPartitionAssignedTo(t, ctx, assigner, "writer-a")
	skipped := commandPartitionAssignedTo(t, ctx, assigner, "writer-b")
	if owned == skipped {
		t.Fatalf("test setup chose same partition for both owners: %+v", owned)
	}
	produceCommandLogWorkerRecord(t, ctx, log, owned, "cid-owned")
	produceCommandLogWorkerRecord(t, ctx, log, skipped, "cid-skipped")

	var seen []LogPartition
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:         log,
		Assignments: assigner,
		OwnerID:     "writer-a",
		BatchSize:   10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Partition)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 2 {
		t.Fatalf("results = %+v, want one result per discovered partition", results)
	}
	byPartition := commandLogWorkerResultsByPartition(results)
	ownedResult := byPartition[owned]
	if !ownedResult.Assigned || ownedResult.AssignmentLost || ownedResult.AssignmentOwnerID != "writer-a" || ownedResult.AssignmentGeneration != 7 || ownedResult.Processed != 1 || ownedResult.LastOffset != 1 {
		t.Fatalf("owned result = %+v, want writer-a to drain its assigned partition", ownedResult)
	}
	skippedResult := byPartition[skipped]
	if skippedResult.Assigned || skippedResult.AssignmentLost || skippedResult.AssignmentOwnerID != "writer-b" || skippedResult.AssignmentGeneration != 7 || skippedResult.Processed != 0 {
		t.Fatalf("skipped result = %+v, want writer-a to skip writer-b partition", skippedResult)
	}
	if !reflect.DeepEqual(seen, []LogPartition{owned}) {
		t.Fatalf("seen partitions = %+v, want only %+v", seen, owned)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, owned, 1, "owned committed offset")
	requireCommandLogWorkerCommittedOffset(t, ctx, log, skipped, 0, "skipped committed offset")
}

func TestCommandLogWorkerUsesAssignmentListerWithoutGlobalPartitionScan(t *testing.T) {
	ctx := context.Background()
	base := NewMemoryCommandLog()
	log := commandLogOnly{inner: base}
	owned := LogPartition{Kind: partitionBoard, Key: "general"}
	unassigned := LogPartition{Kind: partitionBoard, Key: "life"}
	produceCommandLogWorkerRecord(t, ctx, base, owned, "cid-owned")
	produceCommandLogWorkerRecord(t, ctx, base, unassigned, "cid-unassigned")
	assigner := logmodel.NewSnapshotCommandPartitionAssigner(logmodel.CommandPartitionAssignmentSnapshot{
		Generation: 21,
		Owners: map[LogPartition]string{
			owned: "writer-a",
		},
	})

	var seen []LogPartition
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:         log,
		Assignments: assigner,
		OwnerID:     "writer-a",
		BatchSize:   10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Partition)
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want only assigned partition result", results)
	}
	result := results[0]
	if result.Partition != owned.Normalize() || !result.Assigned || result.AssignmentOwnerID != "writer-a" || result.AssignmentGeneration != 21 || result.Processed != 1 || result.LastOffset != 1 {
		t.Fatalf("result = %+v, want snapshot-owned partition drained", result)
	}
	if !reflect.DeepEqual(seen, []LogPartition{owned.Normalize()}) {
		t.Fatalf("seen partitions = %+v, want only assigned %+v", seen, owned.Normalize())
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, base, owned, 1, "owned committed offset")
	requireCommandLogWorkerCommittedOffset(t, ctx, base, unassigned, 0, "unassigned committed offset")
}

func TestCommandLogWorkerDoesNotCommitAfterSnapshotRebalanceBeforeCommit(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryCommandLog()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	assigner := logmodel.NewSnapshotCommandPartitionAssigner(logmodel.CommandPartitionAssignmentSnapshot{
		Generation: 31,
		Owners: map[LogPartition]string{
			partition: "writer-a",
		},
	})

	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:         log,
		Assignments: assigner,
		OwnerID:     "writer-a",
		BatchSize:   10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			assigner.ApplySnapshot(logmodel.CommandPartitionAssignmentSnapshot{
				Generation: 32,
				Owners: map[LogPartition]string{
					partition: "writer-b",
				},
			})
			return commandexec.Reply{}
		}),
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.Assigned || !result.AssignmentLost || result.AssignmentOwnerID != "writer-b" || result.AssignmentGeneration != 32 || result.Processed != 0 || result.LastOffset != 0 {
		t.Fatalf("result = %+v, want execution without offset commit after broker snapshot rebalance", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

func TestCommandLogWorkerDoesNotFinalizeOutcomeAfterAssignmentLost(t *testing.T) {
	ctx := context.Background()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	tests := []struct {
		name  string
		reply commandexec.Reply
	}{
		{
			name:  "applied",
			reply: commandexec.Reply{Result: &proto.AckResult{ID: "ack_lost_assignment", Seq: 1}},
		},
		{
			name:  "terminal",
			reply: commandexec.Reply{Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "bad command", Retryable: false}},
		},
		{
			name:  "retryable",
			reply: commandexec.Reply{Err: &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewMemoryCommandLog()
			produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-"+tt.name)
			assigner := logmodel.NewSnapshotCommandPartitionAssigner(logmodel.CommandPartitionAssignmentSnapshot{
				Generation: 41,
				Owners: map[LogPartition]string{
					partition: "writer-a",
				},
			})
			var applied, terminal, retrying int
			worker := NewCommandLogWorker(CommandLogWorkerConfig{
				Log:         log,
				Assignments: assigner,
				OwnerID:     "writer-a",
				BatchSize:   10,
				Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
					applied++
					return nil
				}),
				TerminalFailures: commandLogTerminalFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
					terminal++
					return nil
				}),
				RetryableFailures: commandLogRetryableFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
					retrying++
					return nil
				}),
				Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
					assigner.ApplySnapshot(logmodel.CommandPartitionAssignmentSnapshot{
						Generation: 42,
						Owners: map[LogPartition]string{
							partition: "writer-b",
						},
					})
					return tt.reply
				}),
			})

			results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
			if len(results) != 1 {
				t.Fatalf("results = %+v, want one partition result", results)
			}
			result := results[0]
			if result.Assigned || !result.AssignmentLost || result.AssignmentOwnerID != "writer-b" || result.AssignmentGeneration != 42 || result.Processed != 0 || result.LastOffset != 0 {
				t.Fatalf("result = %+v, want no finalization after assignment loss", result)
			}
			if result.Applied != 0 || result.TerminalFailures != 0 || result.RetryableFailure != nil {
				t.Fatalf("result finalization = applied %d terminal %d retryable %+v, want none", result.Applied, result.TerminalFailures, result.RetryableFailure)
			}
			if applied != 0 || terminal != 0 || retrying != 0 {
				t.Fatalf("recorder calls applied=%d terminal=%d retrying=%d, want none", applied, terminal, retrying)
			}
			requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
		})
	}
}

func TestCommandLogWorkerDoesNotCommitIfAssignmentLostDuringExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-1")
	assigner := newHeartbeatCommandPartitionAssigner(3)
	executionStarted := make(chan struct{})
	var executionStartedOnce sync.Once
	var seen []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		Assignments:          assigner,
		OwnerID:              "writer-a",
		ClaimTTL:             90 * time.Millisecond,
		ClaimRefreshInterval: 10 * time.Millisecond,
		BatchSize:            10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			seen = append(seen, record.Offset)
			executionStartedOnce.Do(func() { close(executionStarted) })
			select {
			case <-assigner.Lost():
				return commandexec.Reply{}
			case <-ctx.Done():
				return commandexec.Reply{Err: &proto.ErrorDetail{Code: "cancelled", Message: ctx.Err().Error(), Retryable: true}}
			}
		}),
	})

	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := worker.DrainOnce(ctx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()
	select {
	case <-executionStarted:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command execution to start")
	}
	select {
	case <-assigner.Lost():
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for heartbeat to lose assignment")
	}
	drain := waitForCommandLogWorkerDrain(t, done)
	if drain.err != nil {
		t.Fatalf("drain once: %v", drain.err)
	}
	if len(drain.results) != 1 {
		t.Fatalf("results = %+v, want one partition result", drain.results)
	}
	result := drain.results[0]
	if result.Assigned || !result.AssignmentLost || result.AssignmentOwnerID != "writer-b" || result.AssignmentGeneration != 2 || result.Processed != 0 || result.LastOffset != 0 {
		t.Fatalf("result = %+v, want no command offset commit after assignment rebalance", result)
	}
	if !reflect.DeepEqual(seen, []int64{1}) {
		t.Fatalf("seen offsets = %v, want [1]", seen)
	}
	if got := assigner.CallCount(); got != 3 {
		t.Fatalf("assignment calls = %d, want initial, pre-execute, and losing heartbeat refresh", got)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

func TestCommandLogWorkerCancelsFinalizerIfAssignmentLostDuringFinalization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, log, partition, "cid-finalizer-assignment-loss")
	assigner := newHeartbeatCommandPartitionAssigner(4)
	finalizerStarted := make(chan struct{})
	var finalizerStartedOnce sync.Once
	var finalized []int64
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		Assignments:          assigner,
		OwnerID:              "writer-a",
		ClaimTTL:             90 * time.Millisecond,
		ClaimRefreshInterval: 10 * time.Millisecond,
		BatchSize:            10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Result: &proto.AckResult{ID: "ack_finalizer_assignment_loss", Seq: 1}}
		}),
		Finalizer: CommandLogFinalizerFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) (CommandLogFinalizationResult, error) {
			finalizerStartedOnce.Do(func() { close(finalizerStarted) })
			select {
			case <-ctx.Done():
				return CommandLogFinalizationResult{}, ctx.Err()
			case <-time.After(time.Second):
				finalized = append(finalized, record.Offset)
				return CommandLogFinalizationResult{Applied: 1, Committed: true}, nil
			}
		}),
	})

	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := worker.DrainOnce(ctx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()
	select {
	case <-finalizerStarted:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command finalization to start")
	}
	select {
	case <-assigner.Lost():
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for heartbeat to lose assignment during finalization")
	}
	drain := waitForCommandLogWorkerDrain(t, done)
	if drain.err != nil {
		t.Fatalf("drain once: %v", drain.err)
	}
	if len(drain.results) != 1 {
		t.Fatalf("results = %+v, want one partition result", drain.results)
	}
	result := drain.results[0]
	if result.Assigned || !result.AssignmentLost || result.AssignmentOwnerID != "writer-b" || result.AssignmentGeneration != 2 || result.Processed != 0 || result.LastOffset != 0 || result.Applied != 0 {
		t.Fatalf("result = %+v, want no command finalization after assignment rebalance", result)
	}
	if len(finalized) != 0 {
		t.Fatalf("finalized offsets = %v, want finalizer canceled before side effects", finalized)
	}
	if got := assigner.CallCount(); got != 4 {
		t.Fatalf("assignment calls = %d, want initial, pre-execute, pre-finalize, and losing finalization heartbeat", got)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset")
}

func TestCoreExecuteCommandLogRecordDoesNotReshadowCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithCommandLogShadow(commandLog))
	go c.Run(ctx)

	partition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	reply := c.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition: partition,
		CID:       "cid-direct",
		Command:   proto.CommandName("test.unknown"),
		Payload:   []byte(`{"hello":"world"}`),
	})
	if reply.Err == nil {
		t.Fatalf("reply = %+v, want handler rejection for unknown command", reply)
	}
	records, err := commandLog.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("shadow records = %+v, want direct command-log execution to avoid re-shadowing", records)
	}
}

func TestCoreAuthoritativeCommandLogEnqueuesPendingUntilWriterDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Authoritative command log",
		Body:  "visible after the writer drains",
	})
	headBefore, err := c.Head()
	if err != nil {
		t.Fatalf("head before enqueue: %v", err)
	}
	cid := "cid-authoritative-create-thread"
	reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, cid)
	if reply.Err != nil {
		t.Fatalf("enqueue command failed: %+v", reply.Err)
	}
	if reply.Result == nil {
		t.Fatalf("enqueue result = nil, want pending ack")
	}
	if reply.Result.ID != cid || reply.Result.CommandID != cid || reply.Result.Status != proto.AckStatusPending ||
		reply.Result.CommandPartitionKind != partitionBoard || reply.Result.CommandPartitionKey != "general" || reply.Result.CommandOffset != 1 {
		t.Fatalf("pending ack = %+v, want command id, board partition, and offset 1", reply.Result)
	}
	status, err := c.CommandStatus(ctx, alice, cid, LogPartition{Kind: partitionBoard, Key: "general"}, reply.Result.CommandOffset)
	if err != nil {
		t.Fatalf("command status before drain: %v", err)
	}
	if status.Status != CommandStatusPending || status.CommandID != cid || status.CommandOffset != 1 || status.CommittedOffset != 0 || status.Result != nil {
		t.Fatalf("status before drain = %+v, want pending command receipt", status)
	}
	headAfterEnqueue, err := c.Head()
	if err != nil {
		t.Fatalf("head after enqueue: %v", err)
	}
	if headAfterEnqueue != headBefore {
		t.Fatalf("head after enqueue = %d, want unchanged %d before writer drain", headAfterEnqueue, headBefore)
	}
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads before drain: %v", err)
	}
	for _, thread := range threads {
		if thread.Title == "Authoritative command log" {
			t.Fatalf("thread %+v is visible before writer drain", thread)
		}
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	records, err := commandLog.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 1 || records[0].CID != cid || records[0].Offset != 1 {
		t.Fatalf("records = %+v, want one pending command at offset 1", records)
	}
	retry := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, cid)
	if retry.Err != nil {
		t.Fatalf("retry enqueue failed: %+v", retry.Err)
	}
	if retry.Result == nil || retry.Result.CommandOffset != 1 || retry.Result.CommandID != cid {
		t.Fatalf("retry result = %+v, want same pending command", retry.Result)
	}
	records, err = commandLog.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log after retry: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records after retry = %+v, want duplicate receipt to reuse original record", records)
	}

	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Partition != partition.Normalize() || results[0].Processed != 1 || results[0].LastOffset != 1 {
		t.Fatalf("drain results = %+v, want one processed command through offset 1", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, 1, "committed offset")
	status, err = c.CommandStatus(ctx, alice, cid, partition, reply.Result.CommandOffset)
	if err != nil {
		t.Fatalf("command status after drain: %v", err)
	}
	if status.Status != CommandStatusApplied || status.Result == nil || status.Result.ID == "" || status.Result.Seq == 0 {
		t.Fatalf("status after drain = %+v, want applied ack result", status)
	}
	if status.CommittedOffset != reply.Result.CommandOffset {
		t.Fatalf("status committed offset = %d, want %d", status.CommittedOffset, reply.Result.CommandOffset)
	}
	if got := commandLogReceiptCount(t, c, CommandStatusApplied); got != 1 {
		t.Fatalf("applied receipt count after drain = %d, want 1", got)
	}
	if _, err := c.CommandStatus(ctx, alice, cid, partition, reply.Result.CommandOffset+1); !errors.Is(err, ErrCommandStatusNotFound) {
		t.Fatalf("command status with wrong offset err = %v, want not found", err)
	}
	headAfterDrain, err := c.Head()
	if err != nil {
		t.Fatalf("head after drain: %v", err)
	}
	if headAfterDrain <= headBefore {
		t.Fatalf("head after drain = %d, want new durable events after %d", headAfterDrain, headBefore)
	}
	threads, err = c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads after drain: %v", err)
	}
	count := 0
	for _, thread := range threads {
		if thread.Title == "Authoritative command log" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thread count after drain = %d, want 1", count)
	}
}

func TestCoreAuthoritativeCommandLogBypassesUnorderedCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.SetPresencePayload{Status: "active", Mode: "reading", Location: "Lobby"})
	reply := c.ExecCmd(ctx, alice, proto.CmdSetPresence, payload, "cid-presence-local")
	if reply.Err != nil {
		t.Fatalf("set presence failed: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Status == proto.AckStatusPending || reply.Result.CommandOffset != 0 {
		t.Fatalf("presence reply = %+v, want immediate local ack without command offset", reply.Result)
	}
	records, err := commandLog.FetchPartition(ctx, LogPartition{Kind: partitionUser, Key: alice.ID}, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("presence command-log records = %+v, want none", records)
	}
	online, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatalf("list online users: %v", err)
	}
	if len(online) != 1 || online[0].UserID != alice.ID || online[0].Status != "active" {
		t.Fatalf("online users = %+v, want alice active from local presence write", online)
	}
}

func TestCoreAuthoritativeCommandLogBypassesReadMarkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Read marker bypass",
		Body:  "root",
	})
	createReply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, createPayload, "cid-read-marker-bypass-create")
	if createReply.Err != nil {
		t.Fatalf("create thread failed: %+v", createReply.Err)
	}
	if createReply.Result == nil || createReply.Result.ID == "" {
		t.Fatalf("create thread result = %+v, want thread id", createReply.Result)
	}
	threadID := createReply.Result.ID
	c.commandLogAuthoritative = commandLog

	payload := marshalCoreTestJSON(t, "marshal payload", proto.MarkThreadReadPayload{Thread: threadID})
	reply := c.ExecCmd(ctx, alice, proto.CmdMarkThreadRead, payload, "cid-mark-thread-read-local")
	if reply.Err != nil {
		t.Fatalf("mark thread read failed: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Status == proto.AckStatusPending || reply.Result.CommandOffset != 0 {
		t.Fatalf("mark thread read reply = %+v, want immediate local ack without command offset", reply.Result)
	}
	records, err := commandLog.FetchPartition(ctx, LogPartition{Kind: partitionThread, Key: threadID}, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("read-marker command-log records = %+v, want none", records)
	}
	summaries, err := c.ListThreadSummaries(alice.ID, "general", 10, 0, false)
	if err != nil {
		t.Fatalf("list thread summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].UnreadPosts != 0 {
		t.Fatalf("thread summaries after read marker = %+v, want local unread state cleared", summaries)
	}

	prefPayload := marshalCoreTestJSON(t, "marshal thread pref payload", proto.SetThreadPrefPayload{Thread: threadID, Level: "watch"})
	prefReply := c.ExecCmd(ctx, alice, proto.CmdSetThreadPref, prefPayload, "cid-thread-pref-local")
	if prefReply.Err != nil {
		t.Fatalf("set thread pref failed: %+v", prefReply.Err)
	}
	if prefReply.Result == nil || prefReply.Result.Status == proto.AckStatusPending || prefReply.Result.CommandOffset != 0 {
		t.Fatalf("thread pref reply = %+v, want immediate local ack without command offset", prefReply.Result)
	}
	records, err = commandLog.FetchPartition(ctx, LogPartition{Kind: partitionThread, Key: threadID}, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log after thread pref: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("read-marker/thread-pref command-log records = %+v, want none", records)
	}
	var prefLevel string
	if err := c.DB.QueryRow(`SELECT level FROM thread_prefs WHERE user_id=? AND thread_id=?`, alice.ID, threadID).Scan(&prefLevel); err != nil {
		t.Fatalf("query thread pref: %v", err)
	}
	if prefLevel != "watch" {
		t.Fatalf("thread pref level = %q, want watch", prefLevel)
	}
}

func TestCoreCommandLogShadowBypassesUnorderedCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithCommandLogShadow(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.SetPresencePayload{Status: "active"})
	reply := c.ExecCmd(ctx, alice, proto.CmdSetPresence, payload, "cid-presence-shadow")
	if reply.Err != nil {
		t.Fatalf("set presence failed: %+v", reply.Err)
	}
	records, err := commandLog.FetchPartition(ctx, LogPartition{Kind: partitionUser, Key: alice.ID}, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("shadow records = %+v, want no unordered presence shadow record", records)
	}
}

func TestCommandLogBypassCoversHighVolumeUnorderedCommands(t *testing.T) {
	for _, name := range []proto.CommandName{
		proto.CmdSendChatLine,
		proto.CmdSetPresence,
		proto.CmdReactPost,
		proto.CmdUnreactPost,
		proto.CmdVotePoll,
		proto.CmdMarkBoardRead,
		proto.CmdRestoreBoardRead,
		proto.CmdMarkFavoriteFolderRead,
		proto.CmdRestoreFavoriteFolderRead,
		proto.CmdMarkThreadRead,
		proto.CmdRestoreThreadRead,
		proto.CmdMarkPostRead,
		proto.CmdSetThreadPref,
		proto.CmdSubscribe,
		proto.CmdUnsubscribe,
	} {
		if !logmodel.CommandBypassesCommandLog(name) {
			t.Fatalf("%s should bypass the command log", name)
		}
	}
	for _, name := range []proto.CommandName{
		proto.CmdCreateThread,
		proto.CmdAppendPost,
		proto.CmdEditPost,
		proto.CmdPublishStatsSnapshot,
	} {
		if logmodel.CommandBypassesCommandLog(name) {
			t.Fatalf("%s should stay on the command log", name)
		}
	}
}

func TestCoreAuthoritativeCommandLogBypassesClientSubscriptionCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal subscribe payload", proto.SubscribePayload{Scopes: []string{"board:general"}})
	reply := c.ExecCmd(ctx, alice, proto.CmdSubscribe, payload, "cid-subscribe-local")
	if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed {
		t.Fatalf("subscribe reply = %+v, want immediate local validation failure", reply)
	}
	records, err := commandLog.FetchPartition(ctx, LogPartition{Kind: partitionGlobal, Key: partitionGlobal}, 0, 10)
	if err != nil {
		t.Fatalf("fetch command log: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("subscription command-log records = %+v, want none", records)
	}
}

func TestCoreAuthoritativeCommandLogStatusExplainsTerminalFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "missing",
		Title: "Terminal failure",
		Body:  "this board does not exist",
	})
	cid := "cid-authoritative-terminal-failure"
	reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, cid)
	if reply.Err != nil {
		t.Fatalf("enqueue command failed: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset != 1 {
		t.Fatalf("pending ack = %+v, want queued command at offset 1", reply.Result)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "missing"}
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Partition != partition.Normalize() || results[0].TerminalFailures != 1 || results[0].Processed != 1 || results[0].LastOffset != 1 {
		t.Fatalf("drain results = %+v, want one committed terminal failure", results)
	}

	status, err := c.CommandStatus(ctx, alice, cid, partition, reply.Result.CommandOffset)
	if err != nil {
		t.Fatalf("command status after terminal failure: %v", err)
	}
	if status.Status != CommandStatusFailed || status.Error == nil || status.Error.Code != proto.ErrNotFound || status.Result != nil {
		t.Fatalf("terminal status = %+v, want failed receipt with not_found error", status)
	}
}

func TestCoreAuthoritativeCommandLogStatusReportsRetryableFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Retryable command",
		Body:  "writer dependency is unavailable",
	})
	cid := "cid-authoritative-retryable-failure"
	reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, cid)
	if reply.Err != nil {
		t.Fatalf("enqueue command failed: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset != 1 {
		t.Fatalf("pending ack = %+v, want queued command at offset 1", reply.Result)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	retryableErr := &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true}
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:               commandLog,
		RetryableFailures: c,
		BatchSize:         10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Err: retryableErr}
		}),
	})
	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Partition != partition.Normalize() || results[0].RetryableFailure == nil || results[0].Processed != 0 || results[0].LastOffset != 0 {
		t.Fatalf("drain results = %+v, want retryable stop before offset commit", results)
	}

	status, err := c.CommandStatus(ctx, alice, cid, partition, reply.Result.CommandOffset)
	if err != nil {
		t.Fatalf("command status after retryable failure: %v", err)
	}
	if status.Status != CommandStatusRetrying || status.Error == nil || status.Error.Code != retryableErr.Code || !status.Error.Retryable || status.CommittedOffset != 0 {
		t.Fatalf("retrying status = %+v, want retrying receipt with retryable error", status)
	}
}

func TestCoreAuthoritativeCommandLogClearsRetryingReceiptAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Retry clears receipt",
		Body:  "retryable receipt should clear after success",
	})
	cid := "cid-retrying-clears-after-success"
	reply := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, cid)
	if reply.Err != nil {
		t.Fatalf("enqueue command failed: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset != 1 {
		t.Fatalf("pending ack = %+v, want queued command at offset 1", reply.Result)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	retryableErr := &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true}
	retryingWorker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:               commandLog,
		RetryableFailures: c,
		BatchSize:         10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			return commandexec.Reply{Err: retryableErr}
		}),
	})
	retryingResults := drainCommandLogWorkerOnce(t, ctx, retryingWorker, "retrying drain")
	if len(retryingResults) != 1 || retryingResults[0].RetryableFailure == nil {
		t.Fatalf("retrying drain results = %+v, want retryable stop", retryingResults)
	}
	if got := commandLogReceiptCount(t, c, CommandStatusRetrying); got != 1 {
		t.Fatalf("retrying receipt count after retryable failure = %d, want 1", got)
	}

	successWorker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		Executor:  c,
		BatchSize: 10,
	})
	results := drainCommandLogWorkerOnce(t, ctx, successWorker, "successful drain")
	if len(results) != 1 || results[0].Partition != partition.Normalize() || results[0].Processed != 1 || results[0].LastOffset != reply.Result.CommandOffset {
		t.Fatalf("successful drain results = %+v, want command committed through offset %d", results, reply.Result.CommandOffset)
	}
	status, err := c.CommandStatus(ctx, alice, cid, partition, reply.Result.CommandOffset)
	if err != nil {
		t.Fatalf("command status after success: %v", err)
	}
	if status.Status != CommandStatusApplied || status.Result == nil {
		t.Fatalf("status after success = %+v, want applied result", status)
	}
	if got := commandLogReceiptCount(t, c, CommandStatusRetrying); got != 0 {
		t.Fatalf("retrying receipt count after success = %d, want 0", got)
	}
}

func TestCoreExecuteCommandLogRecordSynthesizesCIDForReplaySafety(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Command log replay",
		Body:  "only once",
	})
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   alice.ID,
		Command:   proto.CmdCreateThread,
		Payload:   payload,
	}
	first := c.ExecuteCommandLogRecord(ctx, record)
	if first.Err != nil {
		t.Fatalf("first execute failed: %+v", first.Err)
	}
	if first.Result == nil || first.Result.ID == "" || first.Result.Seq == 0 {
		t.Fatalf("first result = %+v, want created thread ack", first.Result)
	}
	headAfterFirst, err := c.Head()
	if err != nil {
		t.Fatalf("head after first: %v", err)
	}

	second := c.ExecuteCommandLogRecord(ctx, record)
	if second.Err != nil {
		t.Fatalf("second execute failed: %+v", second.Err)
	}
	if second.Result == nil || second.Result.ID != first.Result.ID || second.Result.Seq != first.Result.Seq {
		t.Fatalf("second result = %+v, want cached first result %+v", second.Result, first.Result)
	}
	headAfterSecond, err := c.Head()
	if err != nil {
		t.Fatalf("head after second: %v", err)
	}
	if headAfterSecond != headAfterFirst {
		t.Fatalf("head after replay = %d, want unchanged %d", headAfterSecond, headAfterFirst)
	}
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	count := 0
	for _, thread := range threads {
		if thread.Title == "Command log replay" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thread count for replay title = %d, want 1", count)
	}
}

func TestCommandLogWorkerCommitFailureReplayDoesNotDuplicateSQLCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	base := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	log := &flakyCommitCommandLog{BrokerCommandLog: base, failCommits: 1}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Commit failure replay",
		Body:  "only once",
	})
	record, err := log.Produce(ctx, CommandLogRecord{
		Partition: partition,
		ActorID:   alice.ID,
		Command:   proto.CmdCreateThread,
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("produce command: %v", err)
	}
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:            log,
		BatchSize:      10,
		CommitAttempts: 1,
		Executor:       c,
	})

	firstResults, err := worker.DrainOnce(ctx)
	if err == nil {
		t.Fatalf("first drain succeeded, want commit failure")
	}
	if len(firstResults) != 1 || firstResults[0].CommitFailures != 1 || firstResults[0].Processed != 0 {
		t.Fatalf("first results = %+v, want applied command with failed offset commit", firstResults)
	}
	headAfterFirst, err := c.Head()
	if err != nil {
		t.Fatalf("head after first: %v", err)
	}
	if headAfterFirst == 0 {
		t.Fatalf("head after first = 0, want SQL command to have committed before offset failure")
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset after failed commit")
	if got := commandLogReceiptCount(t, c, CommandStatusApplied); got != 1 {
		t.Fatalf("applied receipt count after failed offset commit = %d, want 1", got)
	}

	secondResults := drainCommandLogWorkerOnce(t, ctx, worker, "second drain")
	if len(secondResults) != 1 || secondResults[0].LastOffset != record.Offset || secondResults[0].Processed != 1 {
		t.Fatalf("second results = %+v, want command offset committed through %d", secondResults, record.Offset)
	}
	headAfterSecond, err := c.Head()
	if err != nil {
		t.Fatalf("head after second: %v", err)
	}
	if headAfterSecond != headAfterFirst {
		t.Fatalf("head after replay = %d, want unchanged %d", headAfterSecond, headAfterFirst)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, record.Offset, "committed offset after replay")
	if got := commandLogReceiptCount(t, c, CommandStatusApplied); got != 1 {
		t.Fatalf("applied receipt count after replay = %d, want 1", got)
	}
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	count := 0
	for _, thread := range threads {
		if thread.Title == "Commit failure replay" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thread count for replay title = %d, want 1", count)
	}
}

func TestCommandLogWorkerCrashBeforeMaterializationReplaysOnReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Crash before materialization",
		Body:  "replacement writer should materialize this once",
	})
	record, err := log.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-writer-crash-before-materialization",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("produce command: %v", err)
	}
	headBeforeCrash, err := c.Head()
	if err != nil {
		t.Fatalf("head before crash: %v", err)
	}

	executionStarted := make(chan struct{})
	releaseExecution := make(chan struct{})
	var executionStartedOnce sync.Once
	crashCtx, crashCancel := context.WithCancel(ctx)
	workerA := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:                  log,
		Claims:               newHeartbeatCommandPartitionClaimer(0),
		OwnerID:              "writer-a",
		ClaimTTL:             90 * time.Millisecond,
		ClaimRefreshInterval: 10 * time.Millisecond,
		BatchSize:            10,
		Executor: CommandLogExecutorFunc(func(ctx context.Context, record CommandLogRecord) commandexec.Reply {
			executionStartedOnce.Do(func() { close(executionStarted) })
			<-releaseExecution
			return commandexec.Reply{}
		}),
	})
	done := make(chan commandLogWorkerDrainResult, 1)
	go func() {
		results, err := workerA.DrainOnce(crashCtx)
		done <- commandLogWorkerDrainResult{results: results, err: err}
	}()
	select {
	case <-executionStarted:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command execution to start")
	}
	crashCancel()
	crashDrain := waitForCommandLogWorkerDrain(t, done)
	close(releaseExecution)
	if !errors.Is(crashDrain.err, context.Canceled) {
		t.Fatalf("crash drain err = %v, want context canceled", crashDrain.err)
	}
	if len(crashDrain.results) != 1 || crashDrain.results[0].Processed != 0 || crashDrain.results[0].LastOffset != 0 {
		t.Fatalf("crash results = %+v, want no materialized command or offset commit", crashDrain.results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, 0, "committed offset after crash")
	headAfterCrash, err := c.Head()
	if err != nil {
		t.Fatalf("head after crash: %v", err)
	}
	if headAfterCrash != headBeforeCrash {
		t.Fatalf("head after crash = %d, want unchanged %d before replacement drain", headAfterCrash, headBeforeCrash)
	}

	workerB := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		OwnerID:   "writer-b",
		BatchSize: 10,
		Executor:  c,
	})
	replayResults := drainCommandLogWorkerOnce(t, ctx, workerB, "replacement drain")
	if len(replayResults) != 1 || replayResults[0].LastOffset != record.Offset || replayResults[0].Processed != 1 {
		t.Fatalf("replacement results = %+v, want command offset committed through %d", replayResults, record.Offset)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, partition, record.Offset, "committed offset after replacement")
	headAfterReplay, err := c.Head()
	if err != nil {
		t.Fatalf("head after replay: %v", err)
	}
	if headAfterReplay <= headAfterCrash {
		t.Fatalf("head after replacement = %d, want durable events after %d", headAfterReplay, headAfterCrash)
	}
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	count := 0
	for _, thread := range threads {
		if thread.Title == "Crash before materialization" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thread count for crash replay title = %d, want 1", count)
	}
}

func TestCoreCommandLogShadowUsesSyntheticCIDForImmediateExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandLog := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	c := newCoreTestCore(t, WithCommandLogShadow(commandLog))
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Shadow synthetic cid",
		Body:  "only once",
	})
	first := c.ExecCmd(ctx, alice, proto.CmdCreateThread, payload, "")
	if first.Err != nil {
		t.Fatalf("exec command: %+v", first.Err)
	}
	if first.Result == nil || first.Result.ID == "" || first.Result.Seq == 0 {
		t.Fatalf("first result = %+v, want created thread ack", first.Result)
	}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	records, err := commandLog.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch shadow command: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one shadow command", records)
	}
	record := records[0]
	wantCID := logmodel.SyntheticCommandLogCID(partition, record.Offset)
	if record.CID != wantCID {
		t.Fatalf("shadow record cid = %q, want %q", record.CID, wantCID)
	}
	headAfterFirst, err := c.Head()
	if err != nil {
		t.Fatalf("head after first: %v", err)
	}

	replayed := c.ExecuteCommandLogRecord(ctx, record)
	if replayed.Err != nil {
		t.Fatalf("replay shadow record failed: %+v", replayed.Err)
	}
	if replayed.Result == nil || replayed.Result.ID != first.Result.ID || replayed.Result.Seq != first.Result.Seq {
		t.Fatalf("replay result = %+v, want cached first result %+v", replayed.Result, first.Result)
	}
	headAfterReplay, err := c.Head()
	if err != nil {
		t.Fatalf("head after replay: %v", err)
	}
	if headAfterReplay != headAfterFirst {
		t.Fatalf("head after replay = %d, want unchanged %d", headAfterReplay, headAfterFirst)
	}
}

func TestCoreExecuteCommandLogRecordRejectsEmptyCIDWithoutOffset(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	reply := c.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Command:   proto.CmdCreateThread,
		Payload:   []byte(`{"board":"general","title":"bad","body":"bad"}`),
	})
	if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Retryable {
		t.Fatalf("reply err = %+v, want terminal validation error", reply.Err)
	}
}

func TestCoreExecuteCommandLogRecordRejectsMismatchedPartition(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	reply := c.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "life"},
		Offset:    1,
		Command:   proto.CmdCreateThread,
		Payload:   []byte(`{"board":"general","title":"wrong partition","body":"nope"}`),
	})
	if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Retryable {
		t.Fatalf("reply err = %+v, want terminal partition validation error", reply.Err)
	}
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head != 0 {
		t.Fatalf("head = %d, want no events appended", head)
	}
}

func TestCommandLogWorkerCommitsMismatchedPartitionAsTerminalFailure(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	wrongPartition := LogPartition{Kind: partitionBoard, Key: "life"}
	record, err := log.Produce(ctx, CommandLogRecord{
		Partition: wrongPartition,
		Offset:    1,
		Command:   proto.CmdCreateThread,
		Payload:   []byte(`{"board":"general","title":"wrong partition","body":"nope"}`),
	})
	if err != nil {
		t.Fatalf("produce wrong-partition record: %v", err)
	}
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       log,
		BatchSize: 10,
		Executor:  c,
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one partition result", results)
	}
	result := results[0]
	if result.Partition != wrongPartition.Normalize() || result.LastOffset != record.Offset || result.Processed != 1 || result.TerminalFailures != 1 || result.RetryableFailure != nil {
		t.Fatalf("result = %+v, want terminal poison record committed through offset %d", result, record.Offset)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, wrongPartition, record.Offset, "committed offset")
	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head != 0 {
		t.Fatalf("head = %d, want no events appended", head)
	}
}

func produceCommandLogWorkerRecord(t *testing.T, ctx context.Context, log CommandLog, partition LogPartition, cid string) CommandLogRecord {
	t.Helper()
	record, err := log.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        cid,
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("produce command record %s: %v", cid, err)
	}
	return record
}

func commandLogReceiptCount(t *testing.T, c *Core, status string) int64 {
	t.Helper()
	var count int64
	if err := qQueryRow(c.DB, `SELECT COUNT(*) FROM command_log_receipts WHERE status=?`, status).Scan(&count); err != nil {
		t.Fatalf("count command log receipts with status %s: %v", status, err)
	}
	return count
}

type commandLogAppliedRecorderFunc func(context.Context, CommandLogRecord, *proto.AckResult) error

func (f commandLogAppliedRecorderFunc) RecordCommandLogApplied(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
	return f(ctx, record, result)
}

type commandLogTerminalFailureRecorderFunc func(context.Context, CommandLogRecord, *proto.ErrorDetail) error

func (f commandLogTerminalFailureRecorderFunc) RecordCommandLogTerminalFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
	return f(ctx, record, errDetail)
}

type commandLogRetryableFailureRecorderFunc func(context.Context, CommandLogRecord, *proto.ErrorDetail) error

func (f commandLogRetryableFailureRecorderFunc) RecordCommandLogRetryableFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
	return f(ctx, record, errDetail)
}

type commandLogOnly struct {
	inner CommandLog
}

func (l commandLogOnly) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	return l.inner.Produce(ctx, record)
}

func (l commandLogOnly) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	return l.inner.FetchPartition(ctx, partition, afterOffset, limit)
}

func (l commandLogOnly) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	return l.inner.CommitPartition(ctx, partition, offset)
}

func (l commandLogOnly) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	return l.inner.CommittedOffset(ctx, partition)
}

type scriptedCommandPartitionClaim struct {
	ownerID   string
	acquired  bool
	expiresAt int64
}

type scriptedCommandPartitionClaimer struct {
	steps []scriptedCommandPartitionClaim
	calls int
}

func (c *scriptedCommandPartitionClaimer) ClaimCommandPartition(ctx context.Context, ownerID string, partition LogPartition, ttl time.Duration) (CommandPartitionClaim, bool, error) {
	if c.calls >= len(c.steps) {
		return CommandPartitionClaim{Partition: partition.Normalize(), OwnerID: ownerID}, false, nil
	}
	step := c.steps[c.calls]
	c.calls++
	claimOwner := step.ownerID
	if claimOwner == "" {
		claimOwner = ownerID
	}
	return CommandPartitionClaim{
		Partition: partition.Normalize(),
		OwnerID:   claimOwner,
		ExpiresAt: step.expiresAt,
	}, step.acquired, nil
}

type commandLogWorkerDrainResult struct {
	results []CommandLogWorkerResult
	err     error
}

func commandLogWorkerResultsByPartition(results []CommandLogWorkerResult) map[LogPartition]CommandLogWorkerResult {
	out := map[LogPartition]CommandLogWorkerResult{}
	for _, result := range results {
		out[result.Partition.Normalize()] = result
	}
	return out
}

func commandPartitionAssignedTo(t *testing.T, ctx context.Context, assigner CommandPartitionAssigner, ownerID string) LogPartition {
	t.Helper()
	for i := 0; i < 200; i++ {
		partition := LogPartition{Kind: partitionBoard, Key: "board-" + string(rune('a'+(i%26))) + "-" + string(rune('a'+((i/26)%26)))}
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			t.Fatalf("assign partition %+v: %v", partition, err)
		}
		if assigned && assignment.OwnerID == ownerID {
			return partition.Normalize()
		}
	}
	t.Fatalf("could not find partition assigned to %s", ownerID)
	return LogPartition{}
}

func waitForCommandLogWorkerDrain(t *testing.T, done <-chan commandLogWorkerDrainResult) commandLogWorkerDrainResult {
	t.Helper()
	select {
	case drain := <-done:
		return drain
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command log worker drain")
		return commandLogWorkerDrainResult{}
	}
}

func waitForCommandLogWorkerStart(t *testing.T, started <-chan LogPartition) LogPartition {
	t.Helper()
	select {
	case partition := <-started:
		return partition.Normalize()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command log worker partition start")
		return LogPartition{}
	}
}

type heartbeatCommandPartitionClaimer struct {
	mu       sync.Mutex
	calls    int
	loseAt   int
	notify   chan int
	lost     chan struct{}
	lostOnce sync.Once
}

func newHeartbeatCommandPartitionClaimer(loseAt int) *heartbeatCommandPartitionClaimer {
	return &heartbeatCommandPartitionClaimer{
		loseAt: loseAt,
		notify: make(chan int, 16),
		lost:   make(chan struct{}),
	}
}

func (c *heartbeatCommandPartitionClaimer) ClaimCommandPartition(ctx context.Context, ownerID string, partition LogPartition, ttl time.Duration) (CommandPartitionClaim, bool, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	loseAt := c.loseAt
	c.mu.Unlock()
	select {
	case c.notify <- call:
	default:
	}
	if loseAt > 0 && call >= loseAt {
		c.lostOnce.Do(func() { close(c.lost) })
		return CommandPartitionClaim{
			Partition: partition.Normalize(),
			OwnerID:   "writer-b",
			ExpiresAt: int64(2000 + call),
		}, false, nil
	}
	return CommandPartitionClaim{
		Partition: partition.Normalize(),
		OwnerID:   ownerID,
		ExpiresAt: int64(1000 + call),
	}, true, nil
}

func (c *heartbeatCommandPartitionClaimer) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *heartbeatCommandPartitionClaimer) Lost() <-chan struct{} {
	return c.lost
}

func waitForCommandPartitionClaimCalls(t *testing.T, claimer *heartbeatCommandPartitionClaimer, want int) {
	t.Helper()
	timeout := time.After(time.Second)
	for claimer.CallCount() < want {
		select {
		case <-claimer.notify:
		case <-timeout:
			t.Fatalf("claim calls = %d, want at least %d", claimer.CallCount(), want)
		}
	}
}

type heartbeatCommandPartitionAssigner struct {
	mu       sync.Mutex
	calls    int
	loseAt   int
	notify   chan int
	lost     chan struct{}
	lostOnce sync.Once
}

func newHeartbeatCommandPartitionAssigner(loseAt int) *heartbeatCommandPartitionAssigner {
	return &heartbeatCommandPartitionAssigner{
		loseAt: loseAt,
		notify: make(chan int, 16),
		lost:   make(chan struct{}),
	}
}

func (a *heartbeatCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition LogPartition) (CommandPartitionAssignment, bool, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	loseAt := a.loseAt
	a.mu.Unlock()
	select {
	case a.notify <- call:
	default:
	}
	if loseAt > 0 && call >= loseAt {
		a.lostOnce.Do(func() { close(a.lost) })
		return CommandPartitionAssignment{
			Partition:  partition.Normalize(),
			OwnerID:    "writer-b",
			Generation: 2,
		}, false, nil
	}
	return CommandPartitionAssignment{
		Partition:  partition.Normalize(),
		OwnerID:    ownerID,
		Generation: 1,
	}, true, nil
}

func (a *heartbeatCommandPartitionAssigner) CallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *heartbeatCommandPartitionAssigner) Lost() <-chan struct{} {
	return a.lost
}

type flakyCommitCommandLog struct {
	*BrokerCommandLog
	failCommits    int
	commitAttempts int
}

func (l *flakyCommitCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	l.commitAttempts++
	if l.commitAttempts <= l.failCommits {
		return errors.New("command offset commit unavailable")
	}
	return l.BrokerCommandLog.CommitPartition(ctx, partition, offset)
}

type staticCommandLogForWorker struct {
	partition LogPartition
	committed int64
	records   []CommandLogRecord
}

func newStaticCommandLogForWorker(partition LogPartition, committed int64, records []CommandLogRecord) *staticCommandLogForWorker {
	return &staticCommandLogForWorker{
		partition: partition.Normalize(),
		committed: committed,
		records:   append([]CommandLogRecord(nil), records...),
	}
}

func (l *staticCommandLogForWorker) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	return CommandLogRecord{}, errors.New("static command log does not support produce")
}

func (l *staticCommandLogForWorker) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
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

func (l *staticCommandLogForWorker) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if partition.Normalize() == l.partition && offset > l.committed {
		l.committed = offset
	}
	return nil
}

func (l *staticCommandLogForWorker) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if partition.Normalize() != l.partition {
		return 0, nil
	}
	return l.committed, nil
}

func (l *staticCommandLogForWorker) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []LogPartition{l.partition}, nil
}

func sparseCommandLogWorkerRecord(partition LogPartition, offset int64, source CommandLogSourcePosition) CommandLogRecord {
	return CommandLogRecord{
		Partition:      partition.Normalize(),
		Offset:         offset,
		SourcePosition: source,
		ActorID:        "usr_alice",
		CID:            "cid-sparse",
		Command:        proto.CmdCreateThread,
		Payload:        []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt:     1000,
	}
}
