package kafkaconn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestLogicalPartitionKeyRoundTripEscapesUnsafeTokens(t *testing.T) {
	partition := core.LogPartition{Kind: "board.topic", Key: "general/space:alpha"}
	key := LogicalPartitionKey(partition)
	if strings.ContainsAny(key, "/=: \t\n") {
		t.Fatalf("logical partition key %q contains unsafe delimiter characters", key)
	}
	decoded, ok := ParseLogicalPartitionKey(key)
	if !ok {
		t.Fatalf("ParseLogicalPartitionKey(%q) failed", key)
	}
	if decoded != partition.Normalize() {
		t.Fatalf("decoded partition = %+v, want %+v", decoded, partition.Normalize())
	}
}

func TestCommandEventTransactionClientCommitsEventsAndCommandOffset(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	transactions := &fakeTransactionBeginner{next: tx}
	options := CommandEventTransactionOptions{
		CommandTopic:  "budgie.commands",
		EventTopic:    "budgie.events",
		ConsumerGroup: "budgie-writers",
	}
	client := NewCommandEventTransactionClient(transactions, options)
	store := core.NewBrokerCommandEventTransactionStore(client)
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}
	commandSource := logmodel.CommandLogSourcePosition{
		Backend:           "kafka",
		Topic:             options.CommandTopic,
		PhysicalPartition: 9,
		PhysicalOffset:    40,
		CommitOffset:      41,
		LogicalPartition:  commandPartition,
		LogicalOffset:     7,
	}

	result, err := store.CommitCommandEvents(ctx, logmodel.CommandEventTransaction{
		CommandPartition:      commandPartition,
		CommandOffset:         7,
		CommandSourcePosition: commandSource,
		Events: []core.EventAppend{
			testEventAppend("evt_kafka_transaction_success", "Kafka transaction success"),
		},
	})
	if err != nil {
		t.Fatalf("CommitCommandEvents: %v", err)
	}
	if transactions.beginCalls != 1 {
		t.Fatalf("begin calls = %d, want 1", transactions.beginCalls)
	}
	if len(tx.appended) != 1 {
		t.Fatalf("appended = %+v, want one event append", tx.appended)
	}
	if tx.allocateCalls != 1 {
		t.Fatalf("allocate calls = %d, want 1", tx.allocateCalls)
	}
	if tx.appended[0].topic != options.EventTopic || tx.appended[0].key != LogicalPartitionKey(commandPartition) {
		t.Fatalf("append target = %s/%s, want %s/%s", tx.appended[0].topic, tx.appended[0].key, options.EventTopic, LogicalPartitionKey(commandPartition))
	}
	if tx.appended[0].record.PartitionOffset != 1 || tx.appended[0].record.CompatibilitySeq != 1 {
		t.Fatalf("appended record position = offset %d seq %d, want 1/1", tx.appended[0].record.PartitionOffset, tx.appended[0].record.CompatibilitySeq)
	}
	if tx.offsetCommit.ConsumerGroup != options.ConsumerGroup ||
		tx.offsetCommit.Topic != options.CommandTopic ||
		tx.offsetCommit.PhysicalPartition != commandSource.PhysicalPartition ||
		tx.offsetCommit.Key != LogicalPartitionKey(commandPartition) ||
		tx.offsetCommit.Offset != commandSource.CommitOffset ||
		tx.offsetCommit.LogicalPartition != commandPartition.Normalize() ||
		tx.offsetCommit.LogicalOffset != 7 {
		t.Fatalf("offset commit = %+v, want physical source partition/offset with logical evidence", tx.offsetCommit)
	}
	if !tx.committed || tx.aborted {
		t.Fatalf("committed=%v aborted=%v, want committed transaction without abort", tx.committed, tx.aborted)
	}
	if result.CommittedPartition != commandPartition.Normalize() || result.CommittedOffset != 7 {
		t.Fatalf("result commit = %+v/%d, want command partition offset 7", result.CommittedPartition, result.CommittedOffset)
	}
	if len(result.Events) != 1 || result.Events[0].Seq != 1 || result.Events[0].PartitionOffset != 1 {
		t.Fatalf("result events = %+v, want returned scalar and partition evidence", result.Events)
	}
}

func TestCommandEventTransactionClientCommitsPartitionOnlyEventsWhenAllowed(t *testing.T) {
	ctx := context.Background()
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	tx := newFakeTransaction()
	tx.overrideAllocations = true
	tx.allocatedPositions = []EventPositionAllocation{{
		Partition:       commandPartition,
		PartitionOffset: 1,
	}}
	transactions := &fakeTransactionBeginner{next: tx}
	options := CommandEventTransactionOptions{
		CommandTopic:             "budgie.commands",
		EventTopic:               "budgie.events",
		ConsumerGroup:            "budgie-writers",
		AllowPartitionOnlyEvents: true,
	}
	client := NewCommandEventTransactionClient(transactions, options)
	command := logmodel.CommandLogCommitPosition{
		Partition: commandPartition,
		Offset:    7,
		SourcePosition: logmodel.CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             options.CommandTopic,
			PhysicalPartition: 9,
			PhysicalOffset:    40,
			CommitOffset:      41,
			LogicalPartition:  commandPartition,
			LogicalOffset:     7,
		},
	}

	result, err := client.AppendEventsAndCommitCommand(ctx, command, []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_partition_only", "Kafka partition-only transaction"),
	})
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommand: %v", err)
	}
	if len(tx.appended) != 1 {
		t.Fatalf("appended = %+v, want one partition-only event append", tx.appended)
	}
	if tx.appended[0].record.PartitionOffset != 1 || tx.appended[0].record.CompatibilitySeq != 0 {
		t.Fatalf("appended record position = offset %d seq %d, want 1/0", tx.appended[0].record.PartitionOffset, tx.appended[0].record.CompatibilitySeq)
	}
	if !tx.committed || tx.aborted || tx.offsetCalls != 1 {
		t.Fatalf("committed=%v aborted=%v offsetCalls=%d, want committed partition-only transaction", tx.committed, tx.aborted, tx.offsetCalls)
	}
	if len(result.Messages) != 1 || result.Messages[0].Offset != 1 || result.Messages[0].StreamSeq != 0 {
		t.Fatalf("result messages = %+v, want partition offset evidence without scalar sequence", result.Messages)
	}
}

func TestCommandEventTransactionClientRejectsPartitionOnlyAllocationByDefault(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.overrideAllocations = true
	tx.allocatedPositions = []EventPositionAllocation{{
		Partition:       core.LogPartition{Kind: "board", Key: "general"},
		PartitionOffset: 1,
	}}
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_partition_only_default_reject", "Partition-only rejected by default"),
	})
	requireErrorContains(t, err, "allocated event 0 without scalar sequence evidence")
	if !tx.aborted || len(tx.appended) != 0 || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v appended=%d offsetCalls=%d commitCalls=%d, want abort before append/offset/commit", tx.aborted, len(tx.appended), tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientCommitsCommandOffsetWithoutEvents(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	transactions := &fakeTransactionBeginner{next: tx}
	options := CommandEventTransactionOptions{
		CommandTopic:  "budgie.commands",
		EventTopic:    "budgie.events",
		ConsumerGroup: "budgie-writers",
	}
	client := NewCommandEventTransactionClient(transactions, options)
	commandPartition := core.LogPartition{Kind: "board", Key: "general"}
	command := logmodel.CommandLogCommitPosition{
		Partition: commandPartition,
		Offset:    9,
		SourcePosition: logmodel.CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             options.CommandTopic,
			PhysicalPartition: 4,
			PhysicalOffset:    50,
			CommitOffset:      51,
			LogicalPartition:  commandPartition,
			LogicalOffset:     9,
		},
	}

	result, err := client.AppendEventsAndCommitCommand(ctx, command, nil)
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommand: %v", err)
	}
	if transactions.beginCalls != 1 {
		t.Fatalf("begin calls = %d, want 1", transactions.beginCalls)
	}
	if tx.allocateCalls != 0 || len(tx.appended) != 0 {
		t.Fatalf("allocate calls=%d appended=%d, want offset-only transaction without event allocation/append", tx.allocateCalls, len(tx.appended))
	}
	if tx.offsetCalls != 1 || tx.commitCalls != 1 || !tx.committed || tx.aborted {
		t.Fatalf("offsetCalls=%d commitCalls=%d committed=%v aborted=%v, want committed offset-only transaction", tx.offsetCalls, tx.commitCalls, tx.committed, tx.aborted)
	}
	if tx.offsetCommit.ConsumerGroup != options.ConsumerGroup ||
		tx.offsetCommit.Topic != options.CommandTopic ||
		tx.offsetCommit.PhysicalPartition != command.SourcePosition.PhysicalPartition ||
		tx.offsetCommit.Key != LogicalPartitionKey(commandPartition) ||
		tx.offsetCommit.Offset != command.SourcePosition.CommitOffset ||
		tx.offsetCommit.LogicalPartition != commandPartition.Normalize() ||
		tx.offsetCommit.LogicalOffset != command.Offset {
		t.Fatalf("offset commit = %+v, want physical source commit with logical evidence", tx.offsetCommit)
	}
	if result.CommittedPartition != commandPartition.Normalize() || result.CommittedOffset != command.Offset {
		t.Fatalf("result commit = %+v/%d, want command partition offset %d", result.CommittedPartition, result.CommittedOffset, command.Offset)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("result messages = %+v, want no event evidence for offset-only transaction", result.Messages)
	}
}

func TestCommandEventTransactionClientAbortsWhenEventPositionAllocationFails(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.allocateErr = fmt.Errorf("injected position allocation failure")
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_position_fail", "Position failure"),
	})
	requireErrorContains(t, err, "injected position allocation failure")
	if !tx.aborted || len(tx.appended) != 0 || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v appended=%d offsetCalls=%d commitCalls=%d, want abort before append/offset/commit", tx.aborted, len(tx.appended), tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientRejectsAllocatedPartitionMismatch(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.overrideAllocations = true
	tx.allocatedPositions = []EventPositionAllocation{
		{Partition: core.LogPartition{Kind: "board", Key: "other"}, PartitionOffset: 1, CompatibilitySeq: 1},
	}
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_alloc_wrong_partition", "Wrong allocated partition"),
	})
	requireErrorContains(t, err, "allocated event 0 for partition board/other, want board/general")
	if !tx.aborted || len(tx.appended) != 0 || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v appended=%d offsetCalls=%d commitCalls=%d, want abort before append/offset/commit", tx.aborted, len(tx.appended), tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenEventAppendFails(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.appendErr = fmt.Errorf("injected append failure")
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_append_fail", "Append failure"),
	})
	requireErrorContains(t, err, "injected append failure")
	if !tx.aborted || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before offset/commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenAppendEvidenceMismatchesTopic(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.resultTopic = "other.events"
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{EventTopic: "budgie.events"})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_wrong_topic", "Wrong topic"),
	})
	requireErrorContains(t, err, `returned topic "other.events" for requested topic "budgie.events"`)
	if !tx.aborted || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before offset/commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenAppendEvidenceMismatchesKey(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.resultKey = LogicalPartitionKey(core.LogPartition{Kind: "board", Key: "other"})
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_wrong_key", "Wrong key"),
	})
	requireErrorContains(t, err, "returned key")
	if !tx.aborted || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before offset/commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenAppendEvidenceMismatchesAllocatedOffset(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.resultPartitionOffset = 2
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_wrong_allocated_offset", "Wrong allocated offset"),
	})
	requireErrorContains(t, err, "returned logical partition offset 2 for allocated offset 1")
	if !tx.aborted || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before offset/commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenAppendEvidenceMismatchesAllocatedSequence(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.resultCompatibilitySeq = 2
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_wrong_allocated_seq", "Wrong allocated sequence"),
	})
	requireErrorContains(t, err, "returned scalar sequence 2 for allocated scalar sequence 1")
	if !tx.aborted || tx.offsetCalls != 0 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before offset/commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientAbortsWhenCommandOffsetCommitFails(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.offsetErr = fmt.Errorf("injected offset failure")
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_offset_fail", "Offset failure"),
	})
	requireErrorContains(t, err, "injected offset failure")
	if !tx.aborted || tx.offsetCalls != 1 || tx.commitCalls != 0 {
		t.Fatalf("aborted=%v offsetCalls=%d commitCalls=%d, want abort before transaction commit", tx.aborted, tx.offsetCalls, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientDoesNotAbortAfterTransactionCommitAttempt(t *testing.T) {
	ctx := context.Background()
	tx := newFakeTransaction()
	tx.commitErr = fmt.Errorf("ambiguous transaction commit failure")
	transactions := &fakeTransactionBeginner{next: tx}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_commit_fail", "Commit failure"),
	})
	requireErrorContains(t, err, "ambiguous transaction commit failure")
	if tx.aborted || tx.commitCalls != 1 {
		t.Fatalf("aborted=%v commitCalls=%d, want no abort after transaction commit attempt", tx.aborted, tx.commitCalls)
	}
}

func TestCommandEventTransactionClientRejectsInvalidBatchBeforeBegin(t *testing.T) {
	ctx := context.Background()
	transactions := &fakeTransactionBeginner{next: newFakeTransaction()}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})
	record := testBrokerEventRecord(t, "evt_kafka_transaction_missing_ts", "Missing timestamp")
	record.TS = 0

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []logmodel.BrokerEventRecord{record})
	requireErrorContains(t, err, "event timestamp is required")
	if transactions.beginCalls != 0 {
		t.Fatalf("begin calls = %d, want invalid batch rejected before begin", transactions.beginCalls)
	}
}

func TestCommandEventTransactionClientRequiresCommandSourcePositionBeforeBegin(t *testing.T) {
	ctx := context.Background()
	transactions := &fakeTransactionBeginner{next: newFakeTransaction()}
	client := NewCommandEventTransactionClient(transactions, CommandEventTransactionOptions{})

	_, err := client.AppendEventsAndCommitCommand(ctx, logmodel.CommandLogCommitPosition{
		Partition: core.LogPartition{Kind: "board", Key: "general"},
		Offset:    3,
	}, []logmodel.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_kafka_transaction_missing_source", "Missing source"),
	})
	requireErrorContains(t, err, "command source position is required")
	if transactions.beginCalls != 0 {
		t.Fatalf("begin calls = %d, want missing source rejected before begin", transactions.beginCalls)
	}
}

type fakeTransactionBeginner struct {
	next       *fakeTransaction
	beginErr   error
	beginCalls int
}

func (b *fakeTransactionBeginner) BeginCommandEventTransaction(ctx context.Context) (Transaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.beginCalls++
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	return b.next, nil
}

type fakeAppend struct {
	topic  string
	key    string
	record logmodel.BrokerEventRecord
}

type fakeTransaction struct {
	tails                  map[core.LogPartition]int64
	head                   int64
	appended               []fakeAppend
	allocateCalls          int
	allocateErr            error
	overrideAllocations    bool
	allocatedPositions     []EventPositionAllocation
	appendErr              error
	resultTopic            string
	resultKey              string
	resultPartitionOffset  int64
	resultCompatibilitySeq int64
	offsetErr              error
	commitErr              error
	offsetCalls            int
	offsetCommit           CommandOffsetCommit
	commitCalls            int
	committed              bool
	aborted                bool
}

func newFakeTransaction() *fakeTransaction {
	return &fakeTransaction{
		tails: map[core.LogPartition]int64{},
	}
}

func (tx *fakeTransaction) AllocateEventPositions(ctx context.Context, records []logmodel.BrokerEventRecord) ([]EventPositionAllocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx.allocateCalls++
	if tx.allocateErr != nil {
		return nil, tx.allocateErr
	}
	if tx.overrideAllocations {
		return append([]EventPositionAllocation(nil), tx.allocatedPositions...), nil
	}
	positions := make([]EventPositionAllocation, 0, len(records))
	for _, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		tx.tails[partition]++
		tx.head++
		compatibilitySeq := tx.head
		if record.CompatibilitySeq > 0 {
			compatibilitySeq = record.CompatibilitySeq
		}
		positions = append(positions, EventPositionAllocation{
			Partition:        partition,
			PartitionOffset:  tx.tails[partition],
			CompatibilitySeq: compatibilitySeq,
		})
	}
	return positions, nil
}

func (tx *fakeTransaction) AppendEvent(ctx context.Context, topic, key string, record logmodel.BrokerEventRecord) (EventAppendResult, error) {
	if err := ctx.Err(); err != nil {
		return EventAppendResult{}, err
	}
	if tx.appendErr != nil {
		return EventAppendResult{}, tx.appendErr
	}
	partition, ok := ParseLogicalPartitionKey(key)
	if !ok {
		return EventAppendResult{}, fmt.Errorf("invalid logical partition key %q", key)
	}
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	resultRecord := record
	if tx.resultPartitionOffset > 0 {
		resultRecord.PartitionOffset = tx.resultPartitionOffset
	}
	if tx.resultCompatibilitySeq > 0 {
		resultRecord.CompatibilitySeq = tx.resultCompatibilitySeq
	}
	data, err := logmodel.EncodeBrokerEventRecord(resultRecord)
	if err != nil {
		return EventAppendResult{}, err
	}
	tx.appended = append(tx.appended, fakeAppend{topic: topic, key: key, record: record})
	resultTopic := topic
	if tx.resultTopic != "" {
		resultTopic = tx.resultTopic
	}
	resultKey := key
	if tx.resultKey != "" {
		resultKey = tx.resultKey
	}
	return EventAppendResult{
		Topic: resultTopic,
		Key:   resultKey,
		Message: logmodel.BrokerEventLogMessage{
			Partition: partition,
			Offset:    resultRecord.PartitionOffset,
			StreamSeq: resultRecord.CompatibilitySeq,
			Data:      data,
		},
	}, nil
}

func (tx *fakeTransaction) CommitCommandOffset(ctx context.Context, commit CommandOffsetCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.offsetCalls++
	tx.offsetCommit = commit
	if tx.offsetErr != nil {
		return tx.offsetErr
	}
	return nil
}

func (tx *fakeTransaction) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.commitCalls++
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakeTransaction) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.aborted = true
	return nil
}

func testEventAppend(id, title string) core.EventAppend {
	return core.EventAppend{
		ID:     id,
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general", "thread:thr_" + id},
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

func testBrokerEventRecord(t *testing.T, id, title string) logmodel.BrokerEventRecord {
	t.Helper()
	payload, err := json.Marshal(testEventAppend(id, title).Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return logmodel.BrokerEventRecord{
		Version:       1,
		ID:            id,
		Kind:          proto.EvtThreadNew,
		Scopes:        []string{"board:general", "thread:thr_" + id},
		Payload:       payload,
		TS:            1234,
		PartitionKind: "board",
		PartitionKey:  "general",
	}
}

func testKafkaCommandCommit(partition core.LogPartition, logicalOffset int64) logmodel.CommandLogCommitPosition {
	partition = partition.Normalize()
	physicalOffset := logicalOffset + 100
	return logmodel.CommandLogCommitPosition{
		Partition: partition,
		Offset:    logicalOffset,
		SourcePosition: logmodel.CommandLogSourcePosition{
			Backend:           "kafka",
			Topic:             DefaultCommandTopic,
			PhysicalPartition: 7,
			PhysicalOffset:    physicalOffset,
			CommitOffset:      physicalOffset + 1,
			LogicalPartition:  partition,
			LogicalOffset:     logicalOffset,
		},
	}
}
