package natsconn

import (
	"context"
	"fmt"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestJetStreamCommandEventTransactionAppendsEventsBeforeCommit(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{
		onCommit: func() error {
			if got := events.storedCount(); got != 1 {
				return fmt.Errorf("stored events at commit = %d, want 1", got)
			}
			return nil
		},
	}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}

	result, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(commandPartition, 3), []logmodel.BrokerEventRecord{
		testTransactionBrokerEvent("evt_nats_transaction_before_commit", "Before commit"),
	})
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommand: %v", err)
	}
	messages := result.Messages
	if len(messages) != 1 || messages[0].Offset != 1 {
		t.Fatalf("messages = %+v, want one event at offset 1", messages)
	}
	if result.CommittedOffset != 3 {
		t.Fatalf("committed offset = %d, want 3", result.CommittedOffset)
	}
	if result.CommittedPartition != commandPartition.Normalize() {
		t.Fatalf("committed partition = %+v, want %+v", result.CommittedPartition, commandPartition.Normalize())
	}
	if commands.committedPartition != commandPartition.Normalize() || commands.committedOffset != 3 {
		t.Fatalf("committed = %s/%s/%d, want board/general/3",
			commands.committedPartition.Kind, commands.committedPartition.Key, commands.committedOffset)
	}
	if events.batchCalls != 1 {
		t.Fatalf("batch appends = %d, want transaction to use event batch appender", events.batchCalls)
	}
}

func TestJetStreamCommandEventTransactionCommitsCommandBatch(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	general := core.LogPartition{Kind: "board", Key: "general"}
	life := core.LogPartition{Kind: "board", Key: "life"}
	generalEvent := testTransactionBrokerEvent("evt_nats_transaction_batch_general", "General batch")
	lifeEvent := testTransactionBrokerEvent("evt_nats_transaction_batch_life", "Life batch")
	lifeEvent.Scopes = []string{"board:life"}
	lifeEvent.Payload = []byte(`{"id":"thr_nats_transaction_life","board":"life","author":"alice","authorId":"usr_alice","title":"Life batch","ts":1234}`)
	lifeEvent.PartitionKey = "life"

	result, err := client.AppendEventsAndCommitCommands(ctx, []core.CommandLogCommitPosition{
		testCommandCommit(general, 3),
		testCommandCommit(life, 4),
	}, []logmodel.BrokerEventRecord{generalEvent, lifeEvent})
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommands: %v", err)
	}
	if events.batchCalls != 1 {
		t.Fatalf("batch appends = %d, want one flattened event batch", events.batchCalls)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages = %+v, want two appended events", result.Messages)
	}
	if len(result.Commits) != 2 || result.Commits[0].Partition != general.Normalize() || result.Commits[0].Offset != 3 ||
		result.Commits[1].Partition != life.Normalize() || result.Commits[1].Offset != 4 {
		t.Fatalf("commits = %+v, want general/3 and life/4", result.Commits)
	}
	if commands.commits != 2 || len(commands.committed) != 2 {
		t.Fatalf("commit calls = %d history=%+v, want two command commits", commands.commits, commands.committed)
	}
}

func TestJetStreamCommandEventTransactionReplaysEventAppendAfterCommitFailure(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{failures: 1}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}
	record := testTransactionBrokerEvent("evt_nats_transaction_replay", "Replay transaction")

	if _, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(commandPartition, 4), []logmodel.BrokerEventRecord{record}); err == nil {
		t.Fatalf("first AppendEventsAndCommitCommand succeeded, want commit failure")
	}
	if got := events.storedCount(); got != 1 {
		t.Fatalf("stored events after failed commit = %d, want 1", got)
	}
	if commands.committedOffset != 0 {
		t.Fatalf("committed offset after failed commit = %d, want 0", commands.committedOffset)
	}

	result, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(commandPartition, 4), []logmodel.BrokerEventRecord{record})
	if err != nil {
		t.Fatalf("second AppendEventsAndCommitCommand: %v", err)
	}
	messages := result.Messages
	if len(messages) != 1 || messages[0].Offset != 1 {
		t.Fatalf("messages = %+v, want replay to resolve original event offset", messages)
	}
	if result.CommittedOffset != 4 {
		t.Fatalf("committed offset = %d, want 4", result.CommittedOffset)
	}
	if result.CommittedPartition != commandPartition.Normalize() {
		t.Fatalf("committed partition = %+v, want %+v", result.CommittedPartition, commandPartition.Normalize())
	}
	if got := events.storedCount(); got != 1 {
		t.Fatalf("stored events after replay = %d, want duplicate append to stay idempotent", got)
	}
	if commands.committedOffset != 4 {
		t.Fatalf("committed offset after replay = %d, want 4", commands.committedOffset)
	}
}

func TestJetStreamCommandEventTransactionReplaysPartialMultiEventAppend(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	events.failAfterStoreByID["evt_nats_transaction_partial_second"] = 1
	commands := &fakeTransactionCommandCommitter{}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}
	first := testTransactionBrokerEvent("evt_nats_transaction_partial_first", "Partial first")
	second := testTransactionBrokerEvent("evt_nats_transaction_partial_second", "Partial second")

	if _, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(commandPartition, 5), []logmodel.BrokerEventRecord{first, second}); err == nil {
		t.Fatalf("first AppendEventsAndCommitCommand succeeded, want ambiguous append failure")
	}
	if got := events.storedCount(); got != 2 {
		t.Fatalf("stored events after partial append failure = %d, want both events durably present", got)
	}
	if commands.commits != 0 || commands.committedOffset != 0 {
		t.Fatalf("commit after partial append failure = calls %d offset %d, want no command commit", commands.commits, commands.committedOffset)
	}

	result, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(commandPartition, 5), []logmodel.BrokerEventRecord{first, second})
	if err != nil {
		t.Fatalf("second AppendEventsAndCommitCommand: %v", err)
	}
	messages := result.Messages
	if len(messages) != 2 || messages[0].Offset != 1 || messages[1].Offset != 2 {
		t.Fatalf("messages = %+v, want replay to resolve original offsets 1 and 2", messages)
	}
	if result.CommittedOffset != 5 {
		t.Fatalf("committed offset = %d, want 5", result.CommittedOffset)
	}
	if result.CommittedPartition != commandPartition.Normalize() {
		t.Fatalf("committed partition = %+v, want %+v", result.CommittedPartition, commandPartition.Normalize())
	}
	if got := events.storedCount(); got != 2 {
		t.Fatalf("stored events after replay = %d, want no duplicate events", got)
	}
	if commands.committedOffset != 5 {
		t.Fatalf("committed offset after replay = %d, want 5", commands.committedOffset)
	}
}

func TestJetStreamCommandEventTransactionRejectsInvalidBatchBeforeAppend(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	first := testTransactionBrokerEvent("evt_nats_transaction_conflict", "First")
	second := testTransactionBrokerEvent("evt_nats_transaction_conflict", "Second")

	_, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 5), []logmodel.BrokerEventRecord{first, second})
	requireErrorContains(t, err, "different content")
	if got := events.storedCount(); got != 0 {
		t.Fatalf("stored events = %d, want no append after invalid batch", got)
	}
	if commands.commits != 0 {
		t.Fatalf("commit calls = %d, want 0", commands.commits)
	}
}

func TestJetStreamCommandEventTransactionRejectsDuplicateEventIDInOneBatchBeforeAppend(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	record := testTransactionBrokerEvent("evt_nats_transaction_duplicate", "Duplicate")

	_, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 5), []logmodel.BrokerEventRecord{record, record})
	requireErrorContains(t, err, `duplicate event id "evt_nats_transaction_duplicate" in one transaction`)
	if got := events.storedCount(); got != 0 {
		t.Fatalf("stored events = %d, want no append after duplicate batch", got)
	}
	if commands.commits != 0 {
		t.Fatalf("commit calls = %d, want 0", commands.commits)
	}
}

func TestJetStreamCommandEventTransactionRejectsMissingTimestampBeforeAppend(t *testing.T) {
	ctx := context.Background()
	events := newFakeTransactionEventAppender()
	commands := &fakeTransactionCommandCommitter{}
	client := newJetStreamCommandEventTransactionClient(commands, events)
	record := testTransactionBrokerEvent("evt_nats_transaction_missing_ts", "Missing timestamp")
	record.TS = 0

	_, err := client.AppendEventsAndCommitCommand(ctx, testCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 5), []logmodel.BrokerEventRecord{record})
	requireErrorContains(t, err, "event timestamp is required")
	if got := events.storedCount(); got != 0 {
		t.Fatalf("stored events = %d, want no append after invalid timestamp", got)
	}
	if commands.commits != 0 {
		t.Fatalf("commit calls = %d, want 0", commands.commits)
	}
}

type fakeTransactionCommandCommitter struct {
	failures           int
	commits            int
	committedPartition core.LogPartition
	committedOffset    int64
	committed          []core.CommandLogCommitPosition
	onCommit           func() error
}

func testCommandCommit(partition core.LogPartition, offset int64) core.CommandLogCommitPosition {
	return core.CommandLogCommitPosition{Partition: partition, Offset: offset}
}

func (c *fakeTransactionCommandCommitter) CommitPartition(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.commits++
	if c.onCommit != nil {
		if err := c.onCommit(); err != nil {
			return err
		}
	}
	if c.failures > 0 {
		c.failures--
		return fmt.Errorf("injected commit failure")
	}
	c.committedPartition = partition.Normalize()
	if offset > c.committedOffset {
		c.committedOffset = offset
	}
	c.committed = append(c.committed, core.CommandLogCommitPosition{Partition: partition.Normalize(), Offset: offset})
	return nil
}

type fakeTransactionEventAppender struct {
	tails              map[core.LogPartition]int64
	messages           map[core.LogPartition][]logmodel.BrokerEventLogMessage
	byID               map[string]logmodel.BrokerEventLogMessage
	failAfterStoreByID map[string]int
	head               int64
	batchCalls         int
}

func newFakeTransactionEventAppender() *fakeTransactionEventAppender {
	return &fakeTransactionEventAppender{
		tails:              map[core.LogPartition]int64{},
		messages:           map[core.LogPartition][]logmodel.BrokerEventLogMessage{},
		byID:               map[string]logmodel.BrokerEventLogMessage{},
		failAfterStoreByID: map[string]int{},
	}
}

func (a *fakeTransactionEventAppender) AppendEvents(ctx context.Context, records []logmodel.BrokerEventRecord) ([]logmodel.BrokerEventLogMessage, error) {
	a.batchCalls++
	messages := make([]logmodel.BrokerEventLogMessage, 0, len(records))
	for _, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		msg, err := a.AppendEvent(ctx, partition, record)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (a *fakeTransactionEventAppender) AppendEvent(ctx context.Context, partition core.LogPartition, record logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	partition = partition.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if existing, ok := a.byID[record.ID]; ok {
		existingRecord, err := logmodel.DecodeBrokerEventRecord(existing.Data)
		if err != nil {
			return logmodel.BrokerEventLogMessage{}, err
		}
		if !logmodel.SameBrokerEventRecordIdentity(existingRecord, record) {
			return logmodel.BrokerEventLogMessage{}, fmt.Errorf("duplicate event id %q has different content", record.ID)
		}
		return cloneTransactionBrokerEventMessage(existing), nil
	}
	a.tails[partition]++
	a.head++
	record.PartitionOffset = a.tails[partition]
	data, err := logmodel.EncodeBrokerEventRecord(record)
	if err != nil {
		return logmodel.BrokerEventLogMessage{}, err
	}
	msg := logmodel.BrokerEventLogMessage{
		Partition: partition,
		Offset:    record.PartitionOffset,
		StreamSeq: a.head,
		Data:      data,
	}
	a.messages[partition] = append(a.messages[partition], cloneTransactionBrokerEventMessage(msg))
	a.byID[record.ID] = cloneTransactionBrokerEventMessage(msg)
	if a.failAfterStoreByID[record.ID] > 0 {
		a.failAfterStoreByID[record.ID]--
		return logmodel.BrokerEventLogMessage{}, fmt.Errorf("injected ambiguous append failure after storing %s", record.ID)
	}
	return cloneTransactionBrokerEventMessage(msg), nil
}

func (a *fakeTransactionEventAppender) storedCount() int {
	count := 0
	for _, messages := range a.messages {
		count += len(messages)
	}
	return count
}

func testTransactionBrokerEvent(id, title string) logmodel.BrokerEventRecord {
	return logmodel.BrokerEventRecord{
		Version:       1,
		ID:            id,
		Kind:          proto.EvtThreadNew,
		Scopes:        []string{"board:general"},
		Payload:       []byte(`{"id":"thr_nats_transaction","board":"general","author":"alice","authorId":"usr_alice","title":"` + title + `","ts":1234}`),
		TS:            1234,
		PartitionKind: "board",
		PartitionKey:  "general",
	}
}

func cloneTransactionBrokerEventMessage(msg logmodel.BrokerEventLogMessage) logmodel.BrokerEventLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}
