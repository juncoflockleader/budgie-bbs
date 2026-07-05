package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func requireCommandEventTransactionRollback(t *testing.T, ctx context.Context, commandLog *BrokerCommandLog, eventStore EventStore, partition LogPartition, label string) {
	t.Helper()
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != 0 {
		t.Fatalf("%s committed offset = %d, %v; want 0, nil", label, got, err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("%s replay event partition: %v", label, err)
	}
	if len(events) != 0 {
		t.Fatalf("%s events = %+v, want no events", label, events)
	}
}

func TestMemoryBrokerCommandEventTransactionRejectsConflictingDuplicateEventIDAtomically(t *testing.T) {
	ctx := context.Background()
	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	eventStore := harness.eventStore
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-conflicting-event-transaction")

	first := commandEventTransactionTestEvent("First title")
	second := commandEventTransactionTestEvent("Second title")
	if _, err := transactionStore.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: partition,
		CommandOffset:    1,
		Events:           []EventAppend{first, second},
	}); err == nil {
		t.Fatalf("CommitCommandEvents succeeded, want duplicate event id conflict")
	}
	requireCommandEventTransactionRollback(t, ctx, commandLog, eventStore, partition, "conflicting duplicate event")
}

func TestBrokerCommandEventTransactionStoreRejectsMissingReturnedEvent(t *testing.T) {
	ctx := context.Background()
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events: []EventAppend{
			commandEventTransactionTestEventWithID("evt_transaction_missing_return", "Missing return"),
		},
	})
	requireErrorContains(t, err, "returned 0 events for 1 requested events")
}

func TestBrokerCommandEventTransactionStoreRejectsMismatchedReturnedEvent(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_requested", "Requested")
	returned := commandEventTransactionTestEventWithID("evt_transaction_returned", "Returned")
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, returned, 1),
		},
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, `returned event 0 id "evt_transaction_returned" for requested id "evt_transaction_requested"`)
}

func TestBrokerCommandEventTransactionStoreRejectsTimestampDrift(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_ts_requested", "Timestamp requested")
	returned := requested
	returned.TS = requested.TS + 1000
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, returned, 1),
		},
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, `returned event 0 id "evt_transaction_ts_requested" for requested id "evt_transaction_ts_requested"`)
}

func TestBrokerCommandEventTransactionStoreRejectsReturnedEventWithoutSequence(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_missing_seq", "Missing sequence")
	message := commandEventTransactionBrokerMessage(t, requested, 1)
	message.StreamSeq = 0
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{message},
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, `returned event 0 id "evt_transaction_missing_seq" without scalar sequence evidence`)
}

func TestBrokerCommandEventTransactionStoreAcceptsPartitionOnlyReturnedEventWithOptions(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_partition_only", "Partition-only")
	message := commandEventTransactionBrokerMessage(t, requested, 1)
	message.StreamSeq = 0
	store := NewBrokerCommandEventTransactionStoreWithOptions(
		fakeBrokerCommandEventTransactionClient{messages: []logmodel.BrokerEventLogMessage{message}},
		BrokerCommandEventTransactionStoreOptions{AllowPartitionOnlyEvents: true},
	)
	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].Seq != 0 || result.Events[0].PartitionOffset != 1 {
		t.Fatalf("events = %+v, want partition offset evidence without scalar sequence", result.Events)
	}
}

func TestBrokerCommandEventTransactionStoreUsesBatchClient(t *testing.T) {
	ctx := context.Background()
	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	first := commandEventTransactionTestEventWithID("evt_transaction_batch_general", "General")
	second := commandEventTransactionTestEventWithID("evt_transaction_batch_life", "Life")
	second.Scopes = []string{"board:life"}
	second.Payload.(*proto.ThreadNewPayload).Board = "life"
	client := &recordingBatchBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, first, 1),
			commandEventTransactionBrokerMessageForPartition(t, second, life, 1, 2),
		},
	}
	store := NewBrokerCommandEventTransactionStore(client)

	results, err := store.CommitCommandEventBatch(ctx, []logmodel.CommandEventTransaction{
		{CommandPartition: general, CommandOffset: 2, Events: []EventAppend{first}},
		{CommandPartition: life, CommandOffset: 3, Events: []EventAppend{second}},
	})
	if err != nil {
		t.Fatalf("CommitCommandEventBatch: %v", err)
	}
	if client.batchCalls != 1 || client.singleCalls != 0 {
		t.Fatalf("client calls batch=%d single=%d, want one batch call", client.batchCalls, client.singleCalls)
	}
	if len(client.commands) != 2 || client.commands[0].Partition != general.Normalize() || client.commands[1].Partition != life.Normalize() {
		t.Fatalf("commands = %+v, want general and life commit positions", client.commands)
	}
	if len(client.records) != 2 || client.records[0].ID != first.ID || client.records[1].ID != second.ID {
		t.Fatalf("records = %+v, want flattened event records", client.records)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two results", results)
	}
	if results[0].CommittedPartition != general.Normalize() || results[0].CommittedOffset != 2 || len(results[0].Events) != 1 {
		t.Fatalf("result[0] = %+v, want general committed through 2 with one event", results[0])
	}
	if results[1].CommittedPartition != life.Normalize() || results[1].CommittedOffset != 3 || len(results[1].Events) != 1 {
		t.Fatalf("result[1] = %+v, want life committed through 3 with one event", results[1])
	}
}

func TestBrokerCommandEventTransactionStoreBatchFallsBackToSingleClient(t *testing.T) {
	ctx := context.Background()
	client := &countingBrokerCommandEventTransactionClient{}
	store := NewBrokerCommandEventTransactionStore(client)

	results, err := store.CommitCommandEventBatch(ctx, []logmodel.CommandEventTransaction{
		{CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"}, CommandOffset: 2},
		{CommandPartition: LogPartition{Kind: partitionBoard, Key: "life"}, CommandOffset: 3},
	})
	if err != nil {
		t.Fatalf("CommitCommandEventBatch: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("single client calls = %d, want fallback to call once per command", client.calls)
	}
	if len(results) != 2 || results[0].CommittedOffset != 2 || results[1].CommittedOffset != 3 {
		t.Fatalf("results = %+v, want two committed fallback results", results)
	}
}

func TestBrokerCommandEventTransactionStoreAcceptsCompatibilitySequence(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_compat_seq", "Compatibility sequence")
	requested.CompatibilitySeq = 42
	message := commandEventTransactionBrokerMessage(t, requested, 1)
	message.StreamSeq = 0
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{message},
	})
	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].Seq != 42 {
		t.Fatalf("events = %+v, want compatibility sequence 42", result.Events)
	}
}

func TestBrokerCommandEventTransactionStoreAcceptsAdapterAssignedCompatibilitySequence(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_assigned_compat_seq", "Assigned compatibility sequence")
	message := commandEventTransactionBrokerMessage(t, requested, 1)
	message.StreamSeq = 0
	message = commandEventTransactionMessageWithCompatibilitySeq(t, message, 42)
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{message},
	})
	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].Seq != 42 {
		t.Fatalf("events = %+v, want adapter-assigned compatibility sequence 42", result.Events)
	}
}

func TestBrokerCommandEventTransactionStoreRejectsReturnedCompatibilitySequenceDrift(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_compat_seq_drift", "Compatibility sequence drift")
	requested.CompatibilitySeq = 42
	message := commandEventTransactionBrokerMessage(t, requested, 1)
	message.StreamSeq = 0
	message = commandEventTransactionMessageWithCompatibilitySeq(t, message, 43)
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{message},
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, `returned event 0 id "evt_transaction_compat_seq_drift" for requested id "evt_transaction_compat_seq_drift"`)
}

func TestBrokerCommandEventTransactionStoreRejectsNonIncreasingReturnedSequence(t *testing.T) {
	ctx := context.Background()
	first := commandEventTransactionTestEventWithID("evt_transaction_first_seq", "First sequence")
	second := commandEventTransactionTestEventWithID("evt_transaction_second_seq", "Second sequence")
	messages := []logmodel.BrokerEventLogMessage{
		commandEventTransactionBrokerMessage(t, first, 1),
		commandEventTransactionBrokerMessage(t, second, 2),
	}
	messages[0].StreamSeq = 10
	messages[1].StreamSeq = 9
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: messages,
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{first, second},
	})
	requireErrorContains(t, err, `returned event 1 id "evt_transaction_second_seq" with non-increasing scalar sequence 9 after 10`)
}

func TestBrokerCommandEventTransactionStoreAcceptsOrderedCompatibilitySequenceBatch(t *testing.T) {
	ctx := context.Background()
	first := commandEventTransactionTestEventWithID("evt_transaction_compat_seq_first", "Compatibility sequence first")
	second := commandEventTransactionTestEventWithID("evt_transaction_compat_seq_second", "Compatibility sequence second")
	first.CompatibilitySeq = 41
	second.CompatibilitySeq = 42
	messages := []logmodel.BrokerEventLogMessage{
		commandEventTransactionBrokerMessage(t, first, 1),
		commandEventTransactionBrokerMessage(t, second, 2),
	}
	messages[0].StreamSeq = 0
	messages[1].StreamSeq = 0
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: messages,
	})
	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{first, second},
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if len(result.Events) != 2 || result.Events[0].Seq != 41 || result.Events[1].Seq != 42 {
		t.Fatalf("events = %+v, want ordered compatibility sequences 41, 42", result.Events)
	}
}

func TestBrokerCommandEventTransactionStoreRejectsNonIncreasingReturnedPartitionOffset(t *testing.T) {
	ctx := context.Background()
	first := commandEventTransactionTestEventWithID("evt_transaction_first_partition_offset", "First partition offset")
	second := commandEventTransactionTestEventWithID("evt_transaction_second_partition_offset", "Second partition offset")
	messages := []logmodel.BrokerEventLogMessage{
		commandEventTransactionBrokerMessage(t, first, 10),
		commandEventTransactionBrokerMessage(t, second, 9),
	}
	messages[0].StreamSeq = 100
	messages[1].StreamSeq = 101
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: messages,
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{first, second},
	})
	requireErrorContains(t, err, `returned event 1 id "evt_transaction_second_partition_offset" for partition board/general with non-increasing partition offset 9 after 10`)
}

func TestBrokerCommandEventTransactionStoreRejectsStaleCommittedOffset(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_stale_commit", "Stale commit")
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, requested, 1),
		},
		committedOffset: 4,
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    5,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, "committed offset 4 before command offset 5")
}

func TestBrokerCommandEventTransactionStoreRejectsWrongCommittedPartition(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_wrong_commit_partition", "Wrong partition")
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, requested, 1),
		},
		committedPartition: LogPartition{Kind: partitionBoard, Key: "other"},
		committedOffset:    5,
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    5,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, "committed partition board/other for command partition board/general")
}

func TestBrokerCommandEventTransactionStoreRejectsMissingCommittedPartition(t *testing.T) {
	ctx := context.Background()
	requested := commandEventTransactionTestEventWithID("evt_transaction_missing_commit_partition", "Missing partition")
	store := NewBrokerCommandEventTransactionStore(fakeBrokerCommandEventTransactionClient{
		messages: []logmodel.BrokerEventLogMessage{
			commandEventTransactionBrokerMessage(t, requested, 1),
		},
		committedPartitionMissing: true,
		committedOffset:           5,
	})
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    5,
		Events:           []EventAppend{requested},
	})
	requireErrorContains(t, err, "missing committed partition")
}

func TestBrokerCommandEventTransactionStoreRequiresEventTimestamp(t *testing.T) {
	ctx := context.Background()
	client := &countingBrokerCommandEventTransactionClient{}
	event := commandEventTransactionTestEventWithID("evt_transaction_missing_ts", "Missing timestamp")
	event.TS = 0
	store := NewBrokerCommandEventTransactionStore(client)
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events:           []EventAppend{event},
	})
	requireErrorContains(t, err, "event timestamp is required")
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want timestamp-less batch rejected before client", client.calls)
	}
}

func TestMemoryBrokerCommandEventTransactionClientRequiresEventTimestamp(t *testing.T) {
	ctx := context.Background()
	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-memory-transaction-missing-ts")
	event := commandEventTransactionTestEventWithID("evt_memory_transaction_missing_ts", "Missing timestamp")
	record, err := brokerEventTransactionRecord(event)
	if err != nil {
		t.Fatalf("brokerEventTransactionRecord: %v", err)
	}
	record.TS = 0
	client := NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient)

	_, err = client.AppendEventsAndCommitCommand(ctx, logmodel.CommandLogCommitPosition{Partition: partition, Offset: 1}, []logmodel.BrokerEventRecord{record})
	requireErrorContains(t, err, "event timestamp is required")
	requireCommandEventTransactionRollback(t, ctx, commandLog, NewBrokerEventStore(eventClient), partition, "missing timestamp")
}

func TestMemoryBrokerCommandEventTransactionClientRejectsDuplicateEventIDInOneBatch(t *testing.T) {
	ctx := context.Background()
	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	produceCommandLogWorkerRecord(t, ctx, commandLog, partition, "cid-memory-transaction-duplicate")
	event := commandEventTransactionTestEventWithID("evt_memory_transaction_duplicate", "Duplicate")
	record, err := brokerEventTransactionRecord(event)
	if err != nil {
		t.Fatalf("brokerEventTransactionRecord: %v", err)
	}
	client := NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient)

	_, err = client.AppendEventsAndCommitCommand(ctx, logmodel.CommandLogCommitPosition{Partition: partition, Offset: 1}, []logmodel.BrokerEventRecord{record, record})
	requireErrorContains(t, err, `duplicate event id "evt_memory_transaction_duplicate" in one transaction`)
	requireCommandEventTransactionRollback(t, ctx, commandLog, eventStore, partition, "duplicate event batch")
}

func TestBrokerCommandEventTransactionStoreRejectsDuplicateEventIDBeforeClient(t *testing.T) {
	ctx := context.Background()
	client := &countingBrokerCommandEventTransactionClient{}
	store := NewBrokerCommandEventTransactionStore(client)
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    1,
		Events: []EventAppend{
			commandEventTransactionTestEventWithID("evt_transaction_duplicate", "Duplicate"),
			commandEventTransactionTestEventWithID("evt_transaction_duplicate", "Duplicate"),
		},
	})
	requireErrorContains(t, err, `duplicate event id "evt_transaction_duplicate" in one transaction`)
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want duplicate batch rejected before client", client.calls)
	}
}

func TestBrokerCommandEventTransactionStorePassesCommandSourcePosition(t *testing.T) {
	ctx := context.Background()
	client := &recordingBrokerCommandEventTransactionClient{}
	store := NewBrokerCommandEventTransactionStore(client)
	source := logmodel.CommandLogSourcePosition{
		Backend:           "kafka",
		Topic:             "budgie.commandlog",
		PhysicalPartition: 7,
		PhysicalOffset:    40,
		CommitOffset:      41,
		LogicalPartition:  LogPartition{Kind: partitionBoard, Key: "general"},
		LogicalOffset:     12,
	}

	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition:      LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:         12,
		CommandSourcePosition: source,
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	if client.command.SourcePosition != source || client.command.Partition != source.LogicalPartition.Normalize() || client.command.Offset != source.LogicalOffset {
		t.Fatalf("client command = %+v, want source %+v", client.command, source)
	}
	if result.CommittedPartition != source.LogicalPartition.Normalize() || result.CommittedOffset != source.LogicalOffset {
		t.Fatalf("result commit = %+v/%d, want logical source commit", result.CommittedPartition, result.CommittedOffset)
	}
}

func TestBrokerCommandEventTransactionStoreRejectsUnsafeCommandSourcePositionBeforeClient(t *testing.T) {
	ctx := context.Background()
	client := &countingBrokerCommandEventTransactionClient{}
	store := NewBrokerCommandEventTransactionStore(client)
	_, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition: LogPartition{Kind: partitionBoard, Key: "general"},
		CommandOffset:    12,
		CommandSourcePosition: logmodel.CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 7,
			PhysicalOffset:    40,
			CommitOffset:      41,
			LogicalPartition:  LogPartition{Kind: partitionBoard, Key: "general"},
			LogicalOffset:     13,
		},
	})
	requireErrorContains(t, err, "logical offset 13 does not match record offset 12")
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want invalid source position rejected before client", client.calls)
	}
}

func TestCommandEventTransactionFinalizerRecordsTerminalFailureOnlyAfterCommit(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-terminal-after-commit",
	}
	var terminalRecords []CommandLogRecord
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			return logmodel.CommandEventTransactionResult{}, errors.New("injected transaction commit failure")
		}),
		TerminalFailures: commandLogTerminalFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
			terminalRecords = append(terminalRecords, record)
			return nil
		}),
	}

	result, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{
		Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "terminal"},
	})
	requireErrorContains(t, err, "injected transaction commit failure")
	if result.CommitFailures != 1 || !strings.Contains(result.CommitFailure, "injected transaction commit failure") || result.Committed {
		t.Fatalf("finalization result = %+v, want visible transaction commit failure without committed progress", result)
	}
	if len(terminalRecords) != 0 {
		t.Fatalf("terminal records = %+v, want none before command offset commit", terminalRecords)
	}
}

func TestCommandEventTransactionFinalizerPassesCommandSourcePosition(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-finalizer-source-position",
		SourcePosition: logmodel.CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             "budgie.commandlog",
			PhysicalPartition: 3,
			PhysicalOffset:    40,
			CommitOffset:      41,
			LogicalPartition:  LogPartition{Kind: partitionBoard, Key: "general"},
			LogicalOffset:     7,
		},
	}
	var got logmodel.CommandLogSourcePosition
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			got = tx.CommandSourcePosition
			return logmodel.CommandEventTransactionResult{
				CommittedPartition: tx.CommandPartition,
				CommittedOffset:    tx.CommandOffset,
			}, nil
		}),
		Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
			return nil, nil
		}),
	}

	result, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{Result: &proto.AckResult{ID: "ack_source_position"}})
	if err != nil {
		t.Fatalf("FinalizeCommandLogRecord: %v", err)
	}
	if !result.Committed || got != record.SourcePosition {
		t.Fatalf("committed=%v source=%+v, want forwarded source %+v", result.Committed, got, record.SourcePosition)
	}
}

func TestCommandEventTransactionFinalizerReturnsRetryableProgressWhenReceiptFails(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-retryable-recorder-fails",
	}
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			t.Fatalf("retryable command should not commit a command/event transaction")
			return logmodel.CommandEventTransactionResult{}, nil
		}),
		RetryableFailures: commandLogRetryableFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
			return errors.New("retryable receipt write failed")
		}),
	}

	result, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{
		Err: &proto.ErrorDetail{Code: "dependency_unavailable", Message: "try again", Retryable: true},
	})
	requireErrorContains(t, err, "retryable receipt write failed")
	if result.RetryableFailure == nil || result.RetryableFailure.Message != "try again" || result.Committed {
		t.Fatalf("finalization result = %+v, want retryable progress without committed offset", result)
	}
}

func TestCommandEventTransactionFinalizerReturnsCommittedTerminalProgressWhenReceiptFails(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-terminal-recorder-fails",
	}
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			return logmodel.CommandEventTransactionResult{
				CommittedPartition: tx.CommandPartition,
				CommittedOffset:    tx.CommandOffset,
			}, nil
		}),
		TerminalFailures: commandLogTerminalFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
			return errors.New("terminal receipt write failed")
		}),
	}

	result, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{
		Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "terminal"},
	})
	requireErrorContains(t, err, "terminal receipt write failed")
	if !result.Committed || result.TerminalFailures != 1 {
		t.Fatalf("finalization result = %+v, want committed terminal progress despite recorder error", result)
	}
	if result.TerminalFailure == nil || result.TerminalFailure.Message != "terminal" {
		t.Fatalf("terminal failure detail = %+v, want terminal message", result.TerminalFailure)
	}
}

func TestCommandEventTransactionFinalizerReturnsCommittedAppliedProgressWhenReceiptFails(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-applied-recorder-fails",
	}
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			return logmodel.CommandEventTransactionResult{
				CommittedPartition: tx.CommandPartition,
				CommittedOffset:    tx.CommandOffset,
			}, nil
		}),
		Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
			return nil, nil
		}),
		Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
			return errors.New("applied receipt write failed")
		}),
	}

	result, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{Result: &proto.AckResult{ID: "ack_applied_recorder_fails"}})
	requireErrorContains(t, err, "applied receipt write failed")
	if !result.Committed || result.Applied != 1 {
		t.Fatalf("finalization result = %+v, want committed applied progress despite recorder error", result)
	}
}

func TestCommandEventTransactionFinalizerRejectsMissingCommittedPartition(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-finalizer-missing-partition",
	}
	var appliedRecords []CommandLogRecord
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			return logmodel.CommandEventTransactionResult{CommittedOffset: tx.CommandOffset}, nil
		}),
		Events: CommandLogEventDeciderFunc(func(ctx context.Context, record CommandLogRecord, reply commandexec.Reply) ([]EventAppend, error) {
			return nil, nil
		}),
		Applied: commandLogAppliedRecorderFunc(func(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
			appliedRecords = append(appliedRecords, record)
			return nil
		}),
	}

	_, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{Result: &proto.AckResult{ID: "ack_missing_partition"}})
	requireErrorContains(t, err, "missing committed partition")
	if len(appliedRecords) != 0 {
		t.Fatalf("applied records = %+v, want none without committed partition evidence", appliedRecords)
	}
}

func TestCommandEventTransactionFinalizerRejectsWrongCommittedPartition(t *testing.T) {
	ctx := context.Background()
	record := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    7,
		ActorID:   "usr_alice",
		CID:       "cid-finalizer-wrong-partition",
	}
	var terminalRecords []CommandLogRecord
	finalizer := CommandEventTransactionFinalizer{
		Transactions: commandEventTransactionStoreFunc(func(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
			return logmodel.CommandEventTransactionResult{
				CommittedPartition: LogPartition{Kind: partitionBoard, Key: "other"},
				CommittedOffset:    tx.CommandOffset,
			}, nil
		}),
		TerminalFailures: commandLogTerminalFailureRecorderFunc(func(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
			terminalRecords = append(terminalRecords, record)
			return nil
		}),
	}

	_, err := finalizer.FinalizeCommandLogRecord(ctx, record, commandexec.Reply{
		Err: &proto.ErrorDetail{Code: proto.ErrValidationFailed, Message: "terminal"},
	})
	requireErrorContains(t, err, "committed partition board/other for record partition board/general")
	if len(terminalRecords) != 0 {
		t.Fatalf("terminal records = %+v, want none for wrong committed partition", terminalRecords)
	}
}

func commandEventTransactionTestEvent(title string) EventAppend {
	return commandEventTransactionTestEventWithID("evt_conflicting_command_event_transaction", title)
}

func commandEventTransactionTestEventWithID(id, title string) EventAppend {
	return EventAppend{
		ID:     id,
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_" + id,
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    title,
			TS:       1234,
		},
		TS: 1234,
	}
}

func commandEventTransactionBrokerMessage(t *testing.T, event EventAppend, offset int64) logmodel.BrokerEventLogMessage {
	t.Helper()
	record, err := brokerEventTransactionRecord(event)
	if err != nil {
		t.Fatalf("brokerEventTransactionRecord: %v", err)
	}
	partition := LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	return commandEventTransactionBrokerMessageForRecord(t, record, partition, offset, offset)
}

func commandEventTransactionBrokerMessageForPartition(t *testing.T, event EventAppend, partition LogPartition, offset, seq int64) logmodel.BrokerEventLogMessage {
	t.Helper()
	record, err := brokerEventTransactionRecord(event)
	if err != nil {
		t.Fatalf("brokerEventTransactionRecord: %v", err)
	}
	partition = partition.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	return commandEventTransactionBrokerMessageForRecord(t, record, partition, offset, seq)
}

func commandEventTransactionBrokerMessageForRecord(t *testing.T, record logmodel.BrokerEventRecord, partition LogPartition, offset, seq int64) logmodel.BrokerEventLogMessage {
	t.Helper()
	record.PartitionOffset = offset
	data, err := logmodel.EncodeBrokerEventRecord(record)
	if err != nil {
		t.Fatalf("logmodel.EncodeBrokerEventRecord: %v", err)
	}
	return logmodel.BrokerEventLogMessage{
		Partition: partition,
		Offset:    offset,
		StreamSeq: seq,
		Data:      data,
	}
}

func commandEventTransactionMessageWithCompatibilitySeq(t *testing.T, message logmodel.BrokerEventLogMessage, seq int64) logmodel.BrokerEventLogMessage {
	t.Helper()
	record, err := logmodel.DecodeBrokerEventRecord(message.Data)
	if err != nil {
		t.Fatalf("logmodel.DecodeBrokerEventRecord: %v", err)
	}
	record.CompatibilitySeq = seq
	data, err := logmodel.EncodeBrokerEventRecord(record)
	if err != nil {
		t.Fatalf("logmodel.EncodeBrokerEventRecord: %v", err)
	}
	message.Data = data
	return message
}

type fakeBrokerCommandEventTransactionClient struct {
	messages                  []logmodel.BrokerEventLogMessage
	committedPartition        LogPartition
	committedPartitionMissing bool
	committedOffset           int64
}

func (c fakeBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command logmodel.CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	command = command.Normalize()
	out := make([]logmodel.BrokerEventLogMessage, len(c.messages))
	copy(out, c.messages)
	committedOffset := c.committedOffset
	if committedOffset == 0 {
		committedOffset = command.Offset
	}
	committedPartition := c.committedPartition
	if committedPartition == (LogPartition{}) && !c.committedPartitionMissing {
		committedPartition = command.Partition
	}
	if !c.committedPartitionMissing {
		committedPartition = committedPartition.Normalize()
	}
	return BrokerCommandEventTransactionResult{
		Messages:           out,
		CommittedPartition: committedPartition,
		CommittedOffset:    committedOffset,
	}, nil
}

type countingBrokerCommandEventTransactionClient struct {
	calls int
}

func (c *countingBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command logmodel.CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	c.calls++
	command = command.Normalize()
	return BrokerCommandEventTransactionResult{
		CommittedPartition: command.Partition,
		CommittedOffset:    command.Offset,
	}, nil
}

type recordingBrokerCommandEventTransactionClient struct {
	calls   int
	command logmodel.CommandLogCommitPosition
}

func (c *recordingBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command logmodel.CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	c.calls++
	c.command = command.Normalize()
	return BrokerCommandEventTransactionResult{
		CommittedPartition: c.command.Partition,
		CommittedOffset:    c.command.Offset,
	}, nil
}

type recordingBatchBrokerCommandEventTransactionClient struct {
	singleCalls int
	batchCalls  int
	command     logmodel.CommandLogCommitPosition
	commands    []logmodel.CommandLogCommitPosition
	records     []logmodel.BrokerEventRecord
	messages    []logmodel.BrokerEventLogMessage
}

func (c *recordingBatchBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command logmodel.CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	c.singleCalls++
	c.command = command.Normalize()
	return BrokerCommandEventTransactionResult{
		Messages:           append([]logmodel.BrokerEventLogMessage(nil), c.messages...),
		CommittedPartition: c.command.Partition,
		CommittedOffset:    c.command.Offset,
	}, nil
}

func (c *recordingBatchBrokerCommandEventTransactionClient) AppendEventsAndCommitCommands(ctx context.Context, commands []logmodel.CommandLogCommitPosition, records []logmodel.BrokerEventRecord) (BrokerCommandEventTransactionBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionBatchResult{}, err
	}
	c.batchCalls++
	c.commands = append([]logmodel.CommandLogCommitPosition(nil), commands...)
	for i := range c.commands {
		c.commands[i] = c.commands[i].Normalize()
	}
	c.records = append([]logmodel.BrokerEventRecord(nil), records...)
	commits := make([]logmodel.CommandLogCommitPosition, 0, len(c.commands))
	for _, command := range c.commands {
		commits = append(commits, command.Normalize())
	}
	return BrokerCommandEventTransactionBatchResult{
		Messages: append([]logmodel.BrokerEventLogMessage(nil), c.messages...),
		Commits:  commits,
	}, nil
}

type commandEventTransactionStoreFunc func(context.Context, logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error)

func (f commandEventTransactionStoreFunc) CommitCommandEvents(ctx context.Context, tx logmodel.CommandEventTransaction) (logmodel.CommandEventTransactionResult, error) {
	return f(ctx, tx)
}
