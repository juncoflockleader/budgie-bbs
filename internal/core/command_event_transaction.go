package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// CommandLogEventDecider extracts the durable events a command-log decision
// needs to append before the command offset can advance.
type CommandLogEventDecider interface {
	DecideCommandLogEvents(ctx context.Context, record CommandLogRecord, reply Reply) ([]EventAppend, error)
}

type CommandLogEventDeciderFunc func(context.Context, CommandLogRecord, Reply) ([]EventAppend, error)

func (f CommandLogEventDeciderFunc) DecideCommandLogEvents(ctx context.Context, record CommandLogRecord, reply Reply) ([]EventAppend, error) {
	return f(ctx, record, reply)
}

// CommandEventTransactionFinalizer is the broker-native command-log finalizer:
// successful command decisions become event appends and a consumed command
// offset commit through one CommandEventTransactionStore boundary.
type CommandEventTransactionFinalizer struct {
	Transactions      CommandEventTransactionStore
	Events            CommandLogEventDecider
	Applied           CommandLogAppliedRecorder
	TerminalFailures  CommandLogTerminalFailureRecorder
	RetryableFailures CommandLogRetryableFailureRecorder
}

// CommandEventTransactionBatchFinalizer opts into committing a fetched
// partition batch through one broker transaction. It is intended for fixture
// loads whose command decisions do not depend on earlier events in the same
// batch being projected before later decisions run.
type CommandEventTransactionBatchFinalizer struct {
	CommandEventTransactionFinalizer
}

func (f CommandEventTransactionFinalizer) FinalizeCommandLogRecord(ctx context.Context, record CommandLogRecord, reply Reply) (CommandLogFinalizationResult, error) {
	if f.Transactions == nil {
		return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: nil transaction store")
	}
	if reply.Err != nil && reply.Err.Retryable {
		result := CommandLogFinalizationResult{RetryableFailure: reply.Err}
		if f.RetryableFailures != nil {
			if err := f.RetryableFailures.RecordCommandLogRetryableFailure(ctx, record, reply.Err); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if reply.Err != nil {
		result := CommandLogFinalizationResult{TerminalFailures: 1, TerminalFailure: reply.Err, Committed: true}
		committed, err := f.Transactions.CommitCommandEvents(ctx, CommandEventTransaction{
			CommandPartition:      record.Partition,
			CommandOffset:         record.Offset,
			CommandSourcePosition: record.SourcePosition,
		})
		if err != nil {
			return commandLogTransactionCommitFailureResult(err), err
		}
		if err := validateCommandEventTransactionCommit(record, committed); err != nil {
			return commandLogTransactionCommitFailureResult(err), err
		}
		if f.TerminalFailures != nil {
			if err := f.TerminalFailures.RecordCommandLogTerminalFailure(ctx, record, reply.Err); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if f.Events == nil {
		return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: nil event decider")
	}
	events, err := f.Events.DecideCommandLogEvents(ctx, record, reply)
	if err != nil {
		return CommandLogFinalizationResult{}, err
	}
	committed, err := f.Transactions.CommitCommandEvents(ctx, CommandEventTransaction{
		CommandPartition:      record.Partition,
		CommandOffset:         record.Offset,
		CommandSourcePosition: record.SourcePosition,
		Events:                events,
	})
	if err != nil {
		return commandLogTransactionCommitFailureResult(err), err
	}
	if err := validateCommandEventTransactionCommit(record, committed); err != nil {
		return commandLogTransactionCommitFailureResult(err), err
	}
	result := CommandLogFinalizationResult{Applied: 1, Committed: true}
	if f.Applied != nil && reply.Result != nil {
		if reply.Result.Seq <= 0 {
			for _, evt := range committed.Events {
				if evt != nil && evt.Seq > reply.Result.Seq {
					reply.Result.Seq = evt.Seq
				}
			}
		}
		if err := f.Applied.RecordCommandLogApplied(ctx, record, reply.Result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (f CommandEventTransactionBatchFinalizer) FinalizeCommandLogRecord(ctx context.Context, record CommandLogRecord, reply Reply) (CommandLogFinalizationResult, error) {
	return f.CommandEventTransactionFinalizer.FinalizeCommandLogRecord(ctx, record, reply)
}

func (f CommandEventTransactionBatchFinalizer) FinalizeCommandLogBatch(ctx context.Context, records []CommandLogRecord, replies []Reply) (CommandLogFinalizationResult, error) {
	base := f.CommandEventTransactionFinalizer
	if f.Transactions == nil {
		return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: nil transaction store")
	}
	if len(records) == 0 {
		return CommandLogFinalizationResult{}, nil
	}
	if len(records) != len(replies) {
		return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: %d records for %d replies", len(records), len(replies))
	}
	if err := validateCommandEventTransactionBatch(records); err != nil {
		return CommandLogFinalizationResult{}, err
	}
	if base.Events == nil {
		for _, reply := range replies {
			if reply.Err == nil {
				return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: nil event decider")
			}
		}
	}

	type eventRange struct {
		start int
		end   int
	}
	eventRanges := make([]eventRange, len(records))
	events := []EventAppend{}
	result := CommandLogFinalizationResult{Committed: true}
	for i, reply := range replies {
		if reply.Err != nil && reply.Err.Retryable {
			return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: retryable command at offset %d cannot be committed in a batch", records[i].Offset)
		}
		if reply.Err != nil {
			result.TerminalFailures++
			result.TerminalFailure = reply.Err
			continue
		}
		if base.Events == nil {
			return CommandLogFinalizationResult{}, fmt.Errorf("command event transaction finalizer: nil event decider")
		}
		decided, err := base.Events.DecideCommandLogEvents(ctx, records[i], reply)
		if err != nil {
			return CommandLogFinalizationResult{}, err
		}
		eventRanges[i] = eventRange{start: len(events), end: len(events) + len(decided)}
		events = append(events, decided...)
		result.Applied++
	}

	last := records[len(records)-1]
	committed, err := base.Transactions.CommitCommandEvents(ctx, CommandEventTransaction{
		CommandPartition:      last.Partition,
		CommandOffset:         last.Offset,
		CommandSourcePosition: last.SourcePosition,
		Events:                events,
	})
	if err != nil {
		return commandLogTransactionCommitFailureResult(err), err
	}
	if err := validateCommandEventTransactionCommit(last, committed); err != nil {
		return commandLogTransactionCommitFailureResult(err), err
	}

	appliedRecords := make([]CommandLogRecord, 0, result.Applied)
	appliedResults := make([]*proto.AckResult, 0, result.Applied)
	for i, reply := range replies {
		if reply.Err != nil {
			if base.TerminalFailures != nil {
				if err := base.TerminalFailures.RecordCommandLogTerminalFailure(ctx, records[i], reply.Err); err != nil {
					return result, err
				}
			}
			continue
		}
		if base.Applied == nil || reply.Result == nil {
			continue
		}
		if reply.Result.Seq <= 0 {
			for _, evt := range committed.Events[eventRanges[i].start:eventRanges[i].end] {
				if evt != nil && evt.Seq > reply.Result.Seq {
					reply.Result.Seq = evt.Seq
				}
			}
		}
		appliedRecords = append(appliedRecords, records[i])
		appliedResults = append(appliedResults, reply.Result)
	}
	if len(appliedRecords) > 0 {
		if batchApplied, ok := base.Applied.(CommandLogBatchAppliedRecorder); ok {
			if err := batchApplied.RecordCommandLogAppliedBatch(ctx, appliedRecords, appliedResults); err != nil {
				return result, err
			}
		} else {
			for i := range appliedRecords {
				if err := base.Applied.RecordCommandLogApplied(ctx, appliedRecords[i], appliedResults[i]); err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

func commandLogTransactionCommitFailureResult(err error) CommandLogFinalizationResult {
	if err == nil {
		return CommandLogFinalizationResult{}
	}
	return CommandLogFinalizationResult{
		CommitFailures: 1,
		CommitFailure:  err.Error(),
	}
}

func validateCommandEventTransactionCommit(record CommandLogRecord, committed CommandEventTransactionResult) error {
	if committed.CommittedPartition == (LogPartition{}) {
		return fmt.Errorf("command event transaction finalizer: missing committed partition")
	}
	if committed.CommittedPartition.Normalize() != record.Partition.Normalize() {
		return fmt.Errorf("command event transaction finalizer: committed partition %s/%s for record partition %s/%s",
			committed.CommittedPartition.Normalize().Kind, committed.CommittedPartition.Normalize().Key,
			record.Partition.Normalize().Kind, record.Partition.Normalize().Key)
	}
	if committed.CommittedOffset < record.Offset {
		return fmt.Errorf("command event transaction finalizer: committed offset %d before record offset %d", committed.CommittedOffset, record.Offset)
	}
	return nil
}

func validateCommandEventTransactionBatch(records []CommandLogRecord) error {
	if len(records) == 0 {
		return nil
	}
	partition := records[0].Partition.Normalize()
	lastOffset := records[0].Offset - 1
	for _, record := range records {
		if record.Partition.Normalize() != partition {
			return fmt.Errorf("command event transaction finalizer: batch mixes partition %s/%s with %s/%s",
				partition.Kind, partition.Key, record.Partition.Normalize().Kind, record.Partition.Normalize().Key)
		}
		if record.Offset <= lastOffset {
			return fmt.Errorf("command event transaction finalizer: batch offset %d after %d is not increasing", record.Offset, lastOffset)
		}
		if err := record.SourcePosition.ValidateForRecord(record); err != nil {
			return fmt.Errorf("command event transaction finalizer: invalid source position in batch offset %d: %w", record.Offset, err)
		}
		lastOffset = record.Offset
	}
	return nil
}

// BrokerCommandEventTransactionClient is the minimal broker operation needed
// for authoritative IS4 writers after command decisions move off SQL. A
// successful call must prove both durable event appends and advancement of the
// consumed command offset.
type BrokerCommandEventTransactionClient interface {
	AppendEventsAndCommitCommand(ctx context.Context, command CommandLogCommitPosition, events []BrokerEventRecord) (BrokerCommandEventTransactionResult, error)
}

type BrokerCommandEventTransactionResult struct {
	Messages           []BrokerEventLogMessage
	CommittedPartition LogPartition
	CommittedOffset    int64
}

// BrokerCommandEventTransactionBatchClient lets broker adapters flatten a group
// of command/event decisions into one backend transaction. Messages must be
// returned in the same order as the flattened requested events.
type BrokerCommandEventTransactionBatchClient interface {
	AppendEventsAndCommitCommands(ctx context.Context, commands []CommandLogCommitPosition, events []BrokerEventRecord) (BrokerCommandEventTransactionBatchResult, error)
}

type BrokerCommandEventTransactionBatchResult struct {
	Messages []BrokerEventLogMessage
	Commits  []CommandLogCommitPosition
}

type BrokerCommandEventTransactionStoreOptions struct {
	AllowPartitionOnlyEvents bool
}

type BrokerCommandEventTransactionStore struct {
	client  BrokerCommandEventTransactionClient
	options BrokerCommandEventTransactionStoreOptions
}

func NewBrokerCommandEventTransactionStore(client BrokerCommandEventTransactionClient) *BrokerCommandEventTransactionStore {
	return NewBrokerCommandEventTransactionStoreWithOptions(client, BrokerCommandEventTransactionStoreOptions{})
}

func NewBrokerCommandEventTransactionStoreWithOptions(client BrokerCommandEventTransactionClient, options BrokerCommandEventTransactionStoreOptions) *BrokerCommandEventTransactionStore {
	return &BrokerCommandEventTransactionStore{client: client, options: options}
}

func (s *BrokerCommandEventTransactionStore) CommitCommandEvents(ctx context.Context, tx CommandEventTransaction) (CommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandEventTransactionResult{}, err
	}
	if s == nil || s.client == nil {
		return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: nil client")
	}
	command := CommandLogCommitPosition{
		Partition:      tx.CommandPartition,
		Offset:         tx.CommandOffset,
		SourcePosition: tx.CommandSourcePosition,
	}.Normalize()
	if err := command.Validate(); err != nil {
		return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: %w", err)
	}
	records := make([]BrokerEventRecord, 0, len(tx.Events))
	seenIDs := map[string]BrokerEventRecord{}
	for _, event := range tx.Events {
		record, err := brokerEventTransactionRecord(event)
		if err != nil {
			return CommandEventTransactionResult{}, err
		}
		if existing, ok := seenIDs[record.ID]; ok {
			if !sameBrokerEventIdentity(existing, record) {
				return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: duplicate event id %q has different content", record.ID)
			}
			return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: duplicate event id %q in one transaction", record.ID)
		}
		seenIDs[record.ID] = record
		records = append(records, record)
	}
	committed, err := s.client.AppendEventsAndCommitCommand(ctx, command, records)
	if err != nil {
		return CommandEventTransactionResult{}, err
	}
	if committed.CommittedPartition == (LogPartition{}) {
		return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: missing committed partition")
	}
	if committed.CommittedPartition.Normalize() != command.Partition {
		return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: committed partition %s/%s for command partition %s/%s",
			committed.CommittedPartition.Normalize().Kind, committed.CommittedPartition.Normalize().Key,
			command.Partition.Kind, command.Partition.Key)
	}
	if committed.CommittedOffset < command.Offset {
		return CommandEventTransactionResult{}, fmt.Errorf("broker command event transaction store: committed offset %d before command offset %d", committed.CommittedOffset, command.Offset)
	}
	events, err := validateBrokerCommandEventTransactionMessages(records, committed.Messages, s.options)
	if err != nil {
		return CommandEventTransactionResult{}, err
	}
	return CommandEventTransactionResult{
		Events:             events,
		CommittedPartition: committed.CommittedPartition.Normalize(),
		CommittedOffset:    committed.CommittedOffset,
	}, nil
}

func (s *BrokerCommandEventTransactionStore) CommitCommandEventBatch(ctx context.Context, txs []CommandEventTransaction) ([]CommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(txs) == 0 {
		return nil, nil
	}
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("broker command event transaction store: nil client")
	}
	batchClient, ok := s.client.(BrokerCommandEventTransactionBatchClient)
	if !ok {
		results := make([]CommandEventTransactionResult, 0, len(txs))
		for _, tx := range txs {
			result, err := s.CommitCommandEvents(ctx, tx)
			results = append(results, result)
			if err != nil {
				return results, err
			}
		}
		return results, nil
	}

	type eventRange struct {
		start int
		end   int
	}
	commands := make([]CommandLogCommitPosition, 0, len(txs))
	eventRanges := make([]eventRange, len(txs))
	records := []BrokerEventRecord{}
	seenIDs := map[string]BrokerEventRecord{}
	for i, tx := range txs {
		command := CommandLogCommitPosition{
			Partition:      tx.CommandPartition,
			Offset:         tx.CommandOffset,
			SourcePosition: tx.CommandSourcePosition,
		}.Normalize()
		if err := command.Validate(); err != nil {
			return nil, fmt.Errorf("broker command event transaction store: command %d: %w", i, err)
		}
		commands = append(commands, command)
		eventRanges[i].start = len(records)
		for _, event := range tx.Events {
			record, err := brokerEventTransactionRecord(event)
			if err != nil {
				return nil, err
			}
			if existing, ok := seenIDs[record.ID]; ok {
				if !sameBrokerEventIdentity(existing, record) {
					return nil, fmt.Errorf("broker command event transaction store: duplicate event id %q has different content", record.ID)
				}
				return nil, fmt.Errorf("broker command event transaction store: duplicate event id %q in one transaction batch", record.ID)
			}
			seenIDs[record.ID] = record
			records = append(records, record)
		}
		eventRanges[i].end = len(records)
	}

	committed, err := batchClient.AppendEventsAndCommitCommands(ctx, commands, records)
	if err != nil {
		return nil, err
	}
	if len(committed.Commits) != len(commands) {
		return nil, fmt.Errorf("broker command event transaction store: transaction returned %d commits for %d commands", len(committed.Commits), len(commands))
	}
	for i, commit := range committed.Commits {
		commit = commit.Normalize()
		committed.Commits[i] = commit
		if commit.Partition == (LogPartition{}) {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned commit %d without partition", i)
		}
		if commit.Partition != commands[i].Partition {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned commit %d partition %s/%s for command partition %s/%s",
				i, commit.Partition.Kind, commit.Partition.Key, commands[i].Partition.Kind, commands[i].Partition.Key)
		}
		if commit.Offset < commands[i].Offset {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned commit %d offset %d before command offset %d",
				i, commit.Offset, commands[i].Offset)
		}
	}
	events, err := validateBrokerCommandEventTransactionMessages(records, committed.Messages, s.options)
	if err != nil {
		return nil, err
	}
	results := make([]CommandEventTransactionResult, len(txs))
	for i := range txs {
		results[i] = CommandEventTransactionResult{
			Events:             append([]*proto.Event(nil), events[eventRanges[i].start:eventRanges[i].end]...),
			CommittedPartition: committed.Commits[i].Partition,
			CommittedOffset:    committed.Commits[i].Offset,
		}
	}
	return results, nil
}

func validateBrokerCommandEventTransactionMessages(records []BrokerEventRecord, messages []BrokerEventLogMessage, options BrokerCommandEventTransactionStoreOptions) ([]*proto.Event, error) {
	if len(messages) != len(records) {
		return nil, fmt.Errorf("broker command event transaction store: transaction returned %d events for %d requested events", len(messages), len(records))
	}
	events := make([]*proto.Event, 0, len(records))
	var lastSeq int64
	partitionOffsets := map[LogPartition]int64{}
	for _, msg := range messages {
		record, err := DecodeBrokerEventRecord(msg.Data)
		if err != nil {
			return nil, err
		}
		expected := records[len(events)]
		if !sameBrokerEventTransactionResultIdentity(record, expected) {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned event %d id %q for requested id %q",
				len(events), record.ID, expected.ID)
		}
		evt, err := DecodeBrokerEventMessage(msg)
		if err != nil {
			return nil, err
		}
		if evt.Seq <= 0 && !options.AllowPartitionOnlyEvents {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned event %d id %q without scalar sequence evidence",
				len(events), record.ID)
		}
		if evt.Seq > 0 && lastSeq > 0 && evt.Seq <= lastSeq {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned event %d id %q with non-increasing scalar sequence %d after %d",
				len(events), record.ID, evt.Seq, lastSeq)
		}
		if evt.Seq > 0 {
			lastSeq = evt.Seq
		}
		partition := LogPartition{Kind: evt.PartitionKind, Key: evt.PartitionKey}.Normalize()
		if lastOffset := partitionOffsets[partition]; lastOffset > 0 && evt.PartitionOffset <= lastOffset {
			return nil, fmt.Errorf("broker command event transaction store: transaction returned event %d id %q for partition %s/%s with non-increasing partition offset %d after %d",
				len(events), record.ID, partition.Kind, partition.Key, evt.PartitionOffset, lastOffset)
		}
		partitionOffsets[partition] = evt.PartitionOffset
		events = append(events, evt)
	}
	return events, nil
}

func sameBrokerEventTransactionResultIdentity(returned, expected BrokerEventRecord) bool {
	if expected.CompatibilitySeq > 0 {
		return sameBrokerEventIdentity(returned, expected)
	}
	expected.CompatibilitySeq = returned.CompatibilitySeq
	return sameBrokerEventIdentity(returned, expected)
}

func brokerEventTransactionRecord(event EventAppend) (BrokerEventRecord, error) {
	if strings.TrimSpace(event.ID) == "" {
		return BrokerEventRecord{}, fmt.Errorf("broker command event transaction store: event id is required")
	}
	if event.TS <= 0 {
		return BrokerEventRecord{}, fmt.Errorf("broker command event transaction store: event timestamp is required")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return BrokerEventRecord{}, err
	}
	if _, err := unmarshalPayload(event.Kind, payload); err != nil {
		return BrokerEventRecord{}, err
	}
	partition := logPartitionFromEventPartition(eventPartitionFor(event.Kind, event.Scopes))
	return BrokerEventRecord{
		Version:          brokerEventRecordVersion,
		ID:               event.ID,
		Kind:             event.Kind,
		CompatibilitySeq: event.CompatibilitySeq,
		Scopes:           append([]string(nil), event.Scopes...),
		Payload:          append([]byte(nil), payload...),
		TS:               event.TS,
		PartitionKind:    partition.Kind,
		PartitionKey:     partition.Key,
	}, nil
}

type memoryPendingBrokerEvent struct {
	message BrokerEventLogMessage
	record  BrokerEventRecord
	new     bool
}

// MemoryBrokerCommandEventTransactionClient is a broker-shaped transaction
// reference for tests and local promotion fixtures.
type MemoryBrokerCommandEventTransactionClient struct {
	commands *MemoryBrokerCommandLogClient
	events   *MemoryBrokerEventLogClient
}

func NewMemoryBrokerCommandEventTransactionClient(commands *MemoryBrokerCommandLogClient, events *MemoryBrokerEventLogClient) *MemoryBrokerCommandEventTransactionClient {
	return &MemoryBrokerCommandEventTransactionClient{commands: commands, events: events}
}

func (c *MemoryBrokerCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command CommandLogCommitPosition, records []BrokerEventRecord) (BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	batch, err := c.AppendEventsAndCommitCommands(ctx, []CommandLogCommitPosition{command}, records)
	if err != nil {
		return BrokerCommandEventTransactionResult{}, err
	}
	if len(batch.Commits) != 1 {
		return BrokerCommandEventTransactionResult{}, fmt.Errorf("memory broker command event transaction: committed %d commands, want 1", len(batch.Commits))
	}
	return BrokerCommandEventTransactionResult{
		Messages:           batch.Messages,
		CommittedPartition: batch.Commits[0].Partition,
		CommittedOffset:    batch.Commits[0].Offset,
	}, nil
}

func (c *MemoryBrokerCommandEventTransactionClient) AppendEventsAndCommitCommands(ctx context.Context, commands []CommandLogCommitPosition, records []BrokerEventRecord) (BrokerCommandEventTransactionBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return BrokerCommandEventTransactionBatchResult{}, err
	}
	if c == nil || c.commands == nil || c.events == nil {
		return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: nil client")
	}
	commands = append([]CommandLogCommitPosition(nil), commands...)
	for i, command := range commands {
		command = command.Normalize()
		if err := command.Validate(); err != nil {
			return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: command %d: %w", i, err)
		}
		commands[i] = command
	}

	c.events.mu.Lock()
	defer c.events.mu.Unlock()
	c.commands.mu.Lock()
	defer c.commands.mu.Unlock()

	for i, command := range commands {
		if command.Offset > c.commands.tails[command.Partition] {
			return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: command %d offset %d is beyond tail %d for %s/%s",
				i, command.Offset, c.commands.tails[command.Partition], command.Partition.Kind, command.Partition.Key)
		}
	}

	pendingCounts := map[LogPartition]int64{}
	pendingByID := map[string]memoryPendingBrokerEvent{}
	pendingHead := c.events.head
	pending := make([]memoryPendingBrokerEvent, 0, len(records))
	for _, record := range records {
		partition := LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		if record.ID == "" {
			return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: event id is required")
		}
		if record.TS <= 0 {
			return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: event timestamp is required")
		}
		if existing, ok := c.events.byID[record.ID]; ok {
			if existing.Partition.Normalize() != partition {
				return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: duplicate event id %q belongs to %s/%s, not %s/%s",
					record.ID, existing.Partition.Kind, existing.Partition.Key, partition.Kind, partition.Key)
			}
			existingRecord, err := DecodeBrokerEventRecord(existing.Data)
			if err != nil {
				return BrokerCommandEventTransactionBatchResult{}, err
			}
			if !sameBrokerEventIdentity(existingRecord, record) {
				return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: duplicate event id %q has different content", record.ID)
			}
			pending = append(pending, memoryPendingBrokerEvent{message: cloneBrokerEventLogMessage(existing)})
			continue
		}
		if existing, ok := pendingByID[record.ID]; ok {
			if !sameBrokerEventIdentity(existing.record, record) {
				return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: duplicate event id %q has different content", record.ID)
			}
			return BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("memory broker command event transaction: duplicate event id %q in one transaction", record.ID)
		}
		offset := c.events.tails[partition] + pendingCounts[partition] + 1
		pendingCounts[partition]++
		pendingHead++
		record.PartitionOffset = offset
		data, err := EncodeBrokerEventRecord(record)
		if err != nil {
			return BrokerCommandEventTransactionBatchResult{}, err
		}
		appendEvent := memoryPendingBrokerEvent{
			message: BrokerEventLogMessage{
				Partition: partition,
				Offset:    offset,
				StreamSeq: pendingHead,
				Data:      data,
			},
			record: record,
			new:    true,
		}
		pending = append(pending, appendEvent)
		pendingByID[record.ID] = appendEvent
	}

	out := make([]BrokerEventLogMessage, 0, len(pending))
	for _, appendEvent := range pending {
		msg := appendEvent.message
		if appendEvent.new {
			c.events.head = msg.StreamSeq
			c.events.tails[msg.Partition] = msg.Offset
			c.events.messages[msg.Partition] = append(c.events.messages[msg.Partition], cloneBrokerEventLogMessage(msg))
			c.events.byID[appendEvent.record.ID] = cloneBrokerEventLogMessage(msg)
		}
		out = append(out, cloneBrokerEventLogMessage(msg))
	}
	commits := make([]CommandLogCommitPosition, 0, len(commands))
	for _, command := range commands {
		if command.Offset > c.commands.committed[command.Partition] {
			c.commands.committed[command.Partition] = command.Offset
		}
		commits = append(commits, CommandLogCommitPosition{
			Partition:      command.Partition,
			Offset:         c.commands.committed[command.Partition],
			SourcePosition: command.SourcePosition,
		})
	}
	return BrokerCommandEventTransactionBatchResult{
		Messages: out,
		Commits:  commits,
	}, nil
}
