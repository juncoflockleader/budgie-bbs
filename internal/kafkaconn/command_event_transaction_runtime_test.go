package kafkaconn

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestFranzCommandEventTransactionRuntimeCommitsExactCommandOffset(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	runtime := &fakeFranzCommandEventRuntime{endCommitted: true}
	allocator := &fakeEventPositionAllocator{
		allocations: []EventPositionAllocation{{
			Partition:        partition,
			PartitionOffset:  5,
			CompatibilitySeq: 9,
		}},
	}
	client := NewCommandEventTransactionClient(
		newFranzCommandEventTransactionBeginner(runtime, allocator),
		CommandEventTransactionOptions{},
	)
	command := testKafkaCommandCommit(partition, 7)
	record := testBrokerEventRecord(t, "evt_franz_transaction_runtime_success", "Franz transaction runtime")

	result, err := client.AppendEventsAndCommitCommand(ctx, command, []core.BrokerEventRecord{record})
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommand: %v", err)
	}
	if runtime.beginCalls != 1 {
		t.Fatalf("begin calls = %d, want 1", runtime.beginCalls)
	}
	if allocator.calls != 1 {
		t.Fatalf("allocator calls = %d, want 1", allocator.calls)
	}
	if len(runtime.produced) != 1 {
		t.Fatalf("produced records = %d, want 1", len(runtime.produced))
	}
	produced := runtime.produced[0]
	if produced.Topic != DefaultEventTopic || string(produced.Key) != LogicalPartitionKey(partition) {
		t.Fatalf("produced topic/key = %s/%s, want %s/%s", produced.Topic, produced.Key, DefaultEventTopic, LogicalPartitionKey(partition))
	}
	producedRecord, err := core.DecodeBrokerEventRecord(produced.Value)
	if err != nil {
		t.Fatalf("DecodeBrokerEventRecord: %v", err)
	}
	if producedRecord.PartitionOffset != 5 || producedRecord.CompatibilitySeq != 9 {
		t.Fatalf("produced event position = offset %d seq %d, want 5/9", producedRecord.PartitionOffset, producedRecord.CompatibilitySeq)
	}
	if len(runtime.endCalls) != 1 || runtime.endCalls[0] != kgo.TryCommit {
		t.Fatalf("end calls = %+v, want one commit", runtime.endCalls)
	}
	if runtime.endCommit == nil || *runtime.endCommit != commandOffsetCommitForTest(t, command) {
		t.Fatalf("transaction command commit = %+v, want %+v", runtime.endCommit, commandOffsetCommitForTest(t, command))
	}
	wantOffsets := franzCommandOffsetMap(commandOffsetCommitForTest(t, command))
	if !reflect.DeepEqual(runtime.setOffsets, []map[string]map[int32]kgo.EpochOffset{wantOffsets}) {
		t.Fatalf("set offsets = %+v, want %+v", runtime.setOffsets, []map[string]map[int32]kgo.EpochOffset{wantOffsets})
	}
	if result.CommittedPartition != partition || result.CommittedOffset != command.Offset {
		t.Fatalf("result commit = %+v/%d, want %+v/%d", result.CommittedPartition, result.CommittedOffset, partition, command.Offset)
	}
	if len(result.Messages) != 1 || result.Messages[0].Offset != 5 || result.Messages[0].StreamSeq != 9 {
		t.Fatalf("result messages = %+v, want allocated event evidence", result.Messages)
	}
}

func TestFranzCommandEventTransactionRuntimeCommitsOffsetOnlyTransaction(t *testing.T) {
	ctx := context.Background()
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	runtime := &fakeFranzCommandEventRuntime{endCommitted: true}
	allocator := &fakeEventPositionAllocator{}
	client := NewCommandEventTransactionClient(
		newFranzCommandEventTransactionBeginner(runtime, allocator),
		CommandEventTransactionOptions{},
	)
	command := testKafkaCommandCommit(partition, 11)

	result, err := client.AppendEventsAndCommitCommand(ctx, command, nil)
	if err != nil {
		t.Fatalf("AppendEventsAndCommitCommand: %v", err)
	}
	if runtime.beginCalls != 1 {
		t.Fatalf("begin calls = %d, want 1", runtime.beginCalls)
	}
	if allocator.calls != 0 {
		t.Fatalf("allocator calls = %d, want no event-position allocation for offset-only transaction", allocator.calls)
	}
	if len(runtime.produced) != 0 {
		t.Fatalf("produced records = %+v, want no event records", runtime.produced)
	}
	if len(runtime.endCalls) != 1 || runtime.endCalls[0] != kgo.TryCommit {
		t.Fatalf("end calls = %+v, want one commit", runtime.endCalls)
	}
	if runtime.endCommit == nil || *runtime.endCommit != commandOffsetCommitForTest(t, command) {
		t.Fatalf("transaction command commit = %+v, want %+v", runtime.endCommit, commandOffsetCommitForTest(t, command))
	}
	wantOffsets := franzCommandOffsetMap(commandOffsetCommitForTest(t, command))
	if !reflect.DeepEqual(runtime.setOffsets, []map[string]map[int32]kgo.EpochOffset{wantOffsets}) {
		t.Fatalf("set offsets = %+v, want %+v", runtime.setOffsets, []map[string]map[int32]kgo.EpochOffset{wantOffsets})
	}
	if result.CommittedPartition != partition || result.CommittedOffset != command.Offset {
		t.Fatalf("result commit = %+v/%d, want %+v/%d", result.CommittedPartition, result.CommittedOffset, partition, command.Offset)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("result messages = %+v, want none for offset-only transaction", result.Messages)
	}
}

func TestFranzCommandEventTransactionRuntimeDoesNotSetOffsetsWhenCommitAborts(t *testing.T) {
	ctx := context.Background()
	runtime := &fakeFranzCommandEventRuntime{endCommitted: false}
	allocator := &fakeEventPositionAllocator{allocations: []EventPositionAllocation{{
		Partition:        core.LogPartition{Kind: "board", Key: "general"},
		PartitionOffset:  1,
		CompatibilitySeq: 1,
	}}}
	client := NewCommandEventTransactionClient(
		newFranzCommandEventTransactionBeginner(runtime, allocator),
		CommandEventTransactionOptions{},
	)

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []core.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_franz_transaction_runtime_aborted_commit", "Aborted commit"),
	})
	requireErrorContains(t, err, "transaction aborted before commit")
	if len(runtime.setOffsets) != 0 {
		t.Fatalf("set offsets = %+v, want none after aborted transaction", runtime.setOffsets)
	}
}

func TestFranzCommandEventTransactionRuntimeSurfacesProduceFailureAndAborts(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("produce rejected")
	runtime := &fakeFranzCommandEventRuntime{produceErr: wantErr}
	allocator := &fakeEventPositionAllocator{allocations: []EventPositionAllocation{{
		Partition:        core.LogPartition{Kind: "board", Key: "general"},
		PartitionOffset:  1,
		CompatibilitySeq: 1,
	}}}
	client := NewCommandEventTransactionClient(
		newFranzCommandEventTransactionBeginner(runtime, allocator),
		CommandEventTransactionOptions{},
	)

	_, err := client.AppendEventsAndCommitCommand(ctx, testKafkaCommandCommit(core.LogPartition{Kind: "board", Key: "general"}, 3), []core.BrokerEventRecord{
		testBrokerEventRecord(t, "evt_franz_transaction_runtime_produce_fail", "Produce failure"),
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("AppendEventsAndCommitCommand err = %v, want %v", err, wantErr)
	}
	if len(runtime.endCalls) != 1 || runtime.endCalls[0] != kgo.TryAbort {
		t.Fatalf("end calls = %+v, want one abort", runtime.endCalls)
	}
	if runtime.endCommit != nil {
		t.Fatalf("abort carried command commit = %+v, want nil", runtime.endCommit)
	}
}

func TestFranzCommandEventTransactionBeginnerSerializesTransactions(t *testing.T) {
	ctx := context.Background()
	runtime := &fakeFranzCommandEventRuntime{}
	allocator := &fakeEventPositionAllocator{}
	beginner := newFranzCommandEventTransactionBeginner(runtime, allocator)

	tx1, err := beginner.BeginCommandEventTransaction(ctx)
	if err != nil {
		t.Fatalf("first BeginCommandEventTransaction: %v", err)
	}
	type beginResult struct {
		tx  Transaction
		err error
	}
	begun := make(chan beginResult, 1)
	go func() {
		tx, err := beginner.BeginCommandEventTransaction(ctx)
		begun <- beginResult{tx: tx, err: err}
	}()

	select {
	case result := <-begun:
		t.Fatalf("second transaction began before first ended: tx=%T err=%v", result.tx, result.err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := tx1.Abort(ctx); err != nil {
		t.Fatalf("abort first transaction: %v", err)
	}
	var result beginResult
	select {
	case result = <-begun:
	case <-time.After(time.Second):
		t.Fatal("second transaction did not begin after first ended")
	}
	if result.err != nil {
		t.Fatalf("second BeginCommandEventTransaction: %v", result.err)
	}
	if result.tx == nil {
		t.Fatal("second transaction is nil")
	}
	if err := result.tx.Abort(ctx); err != nil {
		t.Fatalf("abort second transaction: %v", err)
	}
	if runtime.beginCalls != 2 {
		t.Fatalf("begin calls = %d, want 2", runtime.beginCalls)
	}
}

func TestRewriteTxnOffsetCommitRequestPinsSingleCommandOffset(t *testing.T) {
	req := kmsg.NewPtrTxnOffsetCommitRequest()
	req.Group = DefaultWriterConsumerGroup
	req.MemberID = "member-a"
	req.Topics = []kmsg.TxnOffsetCommitRequestTopic{{
		Topic: "dirty.commands",
		Partitions: []kmsg.TxnOffsetCommitRequestTopicPartition{{
			Partition: 9,
			Offset:    100,
		}},
	}}
	commit := CommandOffsetCommit{
		ConsumerGroup:     DefaultWriterConsumerGroup,
		Topic:             DefaultCommandTopic,
		PhysicalPartition: 3,
		Offset:            42,
	}

	if err := rewriteTxnOffsetCommitRequest(req, commit); err != nil {
		t.Fatalf("rewriteTxnOffsetCommitRequest: %v", err)
	}
	if len(req.Topics) != 1 || req.Topics[0].Topic != DefaultCommandTopic || len(req.Topics[0].Partitions) != 1 {
		t.Fatalf("rewritten topics = %+v, want one command topic partition", req.Topics)
	}
	got := req.Topics[0].Partitions[0]
	if got.Partition != 3 || got.Offset != 42 || got.LeaderEpoch != -1 {
		t.Fatalf("rewritten partition = %+v, want partition 3 offset 42 epoch -1", got)
	}
	if got.Metadata == nil || *got.Metadata != "member-a" {
		t.Fatalf("rewritten metadata = %+v, want member id", got.Metadata)
	}
}

func TestRewriteTxnOffsetCommitRequestRejectsGroupMismatch(t *testing.T) {
	req := kmsg.NewPtrTxnOffsetCommitRequest()
	req.Group = "other-group"

	err := rewriteTxnOffsetCommitRequest(req, CommandOffsetCommit{
		ConsumerGroup:     DefaultWriterConsumerGroup,
		Topic:             DefaultCommandTopic,
		PhysicalPartition: 1,
		Offset:            2,
	})
	requireErrorContains(t, err, "does not match command commit group")
}

func commandOffsetCommitForTest(t *testing.T, command core.CommandLogCommitPosition) CommandOffsetCommit {
	t.Helper()
	commit, err := commandOffsetCommit(normalizeCommandEventTransactionOptions(CommandEventTransactionOptions{}), command)
	if err != nil {
		t.Fatalf("commandOffsetCommit: %v", err)
	}
	return commit
}

type fakeEventPositionAllocator struct {
	calls       int
	records     [][]core.BrokerEventRecord
	allocations []EventPositionAllocation
	err         error
}

func (a *fakeEventPositionAllocator) AllocateEventPositions(ctx context.Context, records []core.BrokerEventRecord) ([]EventPositionAllocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.calls++
	a.records = append(a.records, append([]core.BrokerEventRecord(nil), records...))
	if a.err != nil {
		return nil, a.err
	}
	return append([]EventPositionAllocation(nil), a.allocations...), nil
}

type fakeFranzCommandEventRuntime struct {
	beginCalls   int
	beginErr     error
	produced     []*kgo.Record
	produceErr   error
	noProduce    bool
	endCalls     []kgo.TransactionEndTry
	endCommit    *CommandOffsetCommit
	endCommitted bool
	endErr       error
	setOffsets   []map[string]map[int32]kgo.EpochOffset
}

func (r *fakeFranzCommandEventRuntime) Begin() error {
	r.beginCalls++
	return r.beginErr
}

func (r *fakeFranzCommandEventRuntime) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	if r.noProduce {
		return nil
	}
	out := make(kgo.ProduceResults, 0, len(records))
	for _, record := range records {
		produced := cloneKafkaRecord(record)
		r.produced = append(r.produced, cloneKafkaRecord(produced))
		out = append(out, kgo.ProduceResult{Record: produced, Err: r.produceErr})
	}
	return out
}

func (r *fakeFranzCommandEventRuntime) End(ctx context.Context, end kgo.TransactionEndTry, commandCommit *CommandOffsetCommit) (bool, error) {
	r.endCalls = append(r.endCalls, end)
	if commandCommit != nil {
		commit := *commandCommit
		r.endCommit = &commit
	} else {
		r.endCommit = nil
	}
	if r.endErr != nil {
		return false, r.endErr
	}
	if end == kgo.TryAbort {
		return false, nil
	}
	return r.endCommitted, nil
}

func (r *fakeFranzCommandEventRuntime) SetOffsets(offsets map[string]map[int32]kgo.EpochOffset) {
	copied := map[string]map[int32]kgo.EpochOffset{}
	for topic, partitions := range offsets {
		copied[topic] = map[int32]kgo.EpochOffset{}
		for partition, offset := range partitions {
			copied[topic][partition] = offset
		}
	}
	r.setOffsets = append(r.setOffsets, copied)
}
