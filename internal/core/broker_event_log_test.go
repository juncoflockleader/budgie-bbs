package core

import (
	"context"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestBrokerEventSubjectRoundTripEscapesPartitionTokens(t *testing.T) {
	partition := LogPartition{Kind: "board.topic", Key: "general/space:alpha"}
	subject := BrokerEventSubject(partition)
	if !strings.HasPrefix(subject, "budgie.eventlog.") {
		t.Fatalf("subject = %q, want budgie event-log prefix", subject)
	}
	tokens := strings.Split(subject, ".")
	if len(tokens) != 4 {
		t.Fatalf("subject tokens = %v, want four tokens", tokens)
	}
	for _, token := range tokens[2:] {
		if strings.ContainsAny(token, "/=: \t\n") {
			t.Fatalf("subject token %q contains unsafe subject characters", token)
		}
	}
	decoded, ok := ParseBrokerEventSubject(subject)
	if !ok {
		t.Fatalf("parse subject %q failed", subject)
	}
	if decoded != partition.Normalize() {
		t.Fatalf("decoded partition = %+v, want %+v", decoded, partition.Normalize())
	}
}

func TestBrokerEventStoreAppendAndReplayPartition(t *testing.T) {
	ctx := context.Background()
	client := NewMemoryBrokerEventLogClient()
	store := NewBrokerEventStore(client)

	general, err := store.Append(ctx, testEventAppendWithTitle("general", "Broker general"))
	if err != nil {
		t.Fatalf("append general: %v", err)
	}
	life, err := store.Append(ctx, testEventAppendWithTitle("life", "Broker life"))
	if err != nil {
		t.Fatalf("append life: %v", err)
	}
	nextGeneral, err := store.Append(ctx, testEventAppendWithTitle("general", "Broker general 2"))
	if err != nil {
		t.Fatalf("append second general: %v", err)
	}

	if general.PartitionKind != partitionBoard || general.PartitionKey != "general" || general.PartitionOffset != 1 {
		t.Fatalf("general partition = (%q,%q,%d), want board/general/1", general.PartitionKind, general.PartitionKey, general.PartitionOffset)
	}
	if life.PartitionKind != partitionBoard || life.PartitionKey != "life" || life.PartitionOffset != 1 {
		t.Fatalf("life partition = (%q,%q,%d), want board/life/1", life.PartitionKind, life.PartitionKey, life.PartitionOffset)
	}
	if nextGeneral.PartitionOffset != 2 {
		t.Fatalf("next general offset = %d, want 2", nextGeneral.PartitionOffset)
	}

	events, err := store.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay general: %v", err)
	}
	if len(events) != 2 || events[0].PartitionOffset != 1 || events[1].PartitionOffset != 2 {
		t.Fatalf("general events = %+v, want offsets 1 and 2", events)
	}
	afterOne, err := store.ReplayPartition(ctx, partitionBoard, "general", 1, 10)
	if err != nil {
		t.Fatalf("replay general after one: %v", err)
	}
	if len(afterOne) != 1 || afterOne[0].PartitionOffset != 2 {
		t.Fatalf("after-one replay = %+v, want only offset 2", afterOne)
	}
}

func TestBrokerEventStoreAppendIsIdempotentByEventID(t *testing.T) {
	ctx := context.Background()
	client := NewMemoryBrokerEventLogClient()
	store := NewBrokerEventStore(client)
	appendEvent := testEventAppendWithTitle("general", "Idempotent broker append")
	appendEvent.ID = "evt_broker_idempotent"

	first, err := store.Append(ctx, appendEvent)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := store.Append(ctx, appendEvent)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if second.Seq != first.Seq || second.PartitionOffset != first.PartitionOffset {
		t.Fatalf("second event = %+v, want same seq/offset as first %+v", second, first)
	}
	if head, err := store.Head(ctx); err != nil || head != 1 {
		t.Fatalf("head = %d, %v; want 1, nil", head, err)
	}
	events, err := store.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one stored event after duplicate append", events)
	}
}

func TestBrokerEventStoreRejectsDuplicateEventIDDifferentContent(t *testing.T) {
	ctx := context.Background()
	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	first := testEventAppendWithTitle("general", "Original broker append")
	first.ID = "evt_broker_duplicate_content"
	if _, err := store.Append(ctx, first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	second := testEventAppendWithTitle("general", "Different broker append")
	second.ID = first.ID
	if _, err := store.Append(ctx, second); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("second append err = %v, want duplicate content error", err)
	}
	events, err := store.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want original event only", events)
	}
}

func TestBrokerEventStoreRejectsEventIDWithoutTimestamp(t *testing.T) {
	ctx := context.Background()
	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	appendEvent := testEventAppendWithTitle("general", "Missing timestamp")
	appendEvent.ID = "evt_broker_missing_ts"
	appendEvent.TS = 0

	_, err := store.Append(ctx, appendEvent)
	if err == nil || !strings.Contains(err.Error(), "event timestamp is required") {
		t.Fatalf("append err = %v, want timestamp required error", err)
	}
}

func TestBrokerEventStoreRejectsDuplicateEventIDTimestampDrift(t *testing.T) {
	ctx := context.Background()
	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	first := testEventAppendWithTitle("general", "Timestamp stable")
	first.ID = "evt_broker_duplicate_ts"
	if _, err := store.Append(ctx, first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	second := first
	second.TS = first.TS + 1000
	if _, err := store.Append(ctx, second); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("second append err = %v, want duplicate timestamp error", err)
	}
	events, err := store.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 || events[0].TS != first.TS {
		t.Fatalf("events = %+v, want original timestamp %d only", events, first.TS)
	}
}

func TestBrokerEventStoreSeedsLogicalPartitionOffset(t *testing.T) {
	ctx := context.Background()
	client := NewMemoryBrokerEventLogClient()
	store := NewBrokerEventStore(client)
	partition := LogPartition{Kind: partitionBoard, Key: "general"}

	if err := store.SeedEventPartitionOffset(ctx, partition, 41); err != nil {
		t.Fatalf("seed: %v", err)
	}
	appended, err := store.Append(ctx, testEventAppendWithTitle("general", "After seed"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if appended.PartitionOffset != 42 {
		t.Fatalf("partition offset = %d, want 42", appended.PartitionOffset)
	}
	partitions, err := store.ListEventPartitions(ctx, 10)
	if err != nil {
		t.Fatalf("list partitions: %v", err)
	}
	if len(partitions) != 1 || partitions[0] != partition {
		t.Fatalf("partitions = %+v, want board/general", partitions)
	}
}

func TestBrokerEventStoreParticipatesInReplayParity(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	recorder := &EventParityRecorder{}

	for _, title := range []string{"First", "Second"} {
		if _, err := primary.Append(ctx, testEventAppendWithTitle("general", title)); err != nil {
			t.Fatalf("primary append %q: %v", title, err)
		}
		if _, err := shadow.Append(ctx, testEventAppendWithTitle("general", title)); err != nil {
			t.Fatalf("shadow append %q: %v", title, err)
		}
	}
	result, err := CheckEventReplayParity(ctx, primary, shadow, LogPartition{Kind: partitionBoard, Key: "general"}, 0, 10, recorder)
	if err != nil {
		t.Fatalf("check parity: %v", err)
	}
	if len(result.Issues) != 0 || result.Compared != 2 {
		t.Fatalf("parity result = %+v, want two clean comparisons", result)
	}
	if issues := recorder.Issues(); len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestBrokerEventStoreReplayUsesCompatibilitySeqAcrossPartitions(t *testing.T) {
	ctx := context.Background()
	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())

	if _, err := store.Append(ctx, EventAppend{
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: 2,
		Scopes:           []string{"board:life"},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_broker_life",
			Board:  "life",
			Author: "alice",
			Title:  "Life",
			TS:     1002,
		},
	}); err != nil {
		t.Fatalf("append seq 2: %v", err)
	}
	if _, err := store.Append(ctx, EventAppend{
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: 1,
		Scopes:           []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_broker_general",
			Board:  "general",
			Author: "alice",
			Title:  "General",
			TS:     1001,
		},
	}); err != nil {
		t.Fatalf("append seq 1: %v", err)
	}

	events, err := store.Replay(ctx, 0, nil, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want two events", events)
	}
	if events[0].Seq != 1 || events[0].PartitionKey != "general" || events[1].Seq != 2 || events[1].PartitionKey != "life" {
		t.Fatalf("events order = %+v, want compatibility seq order general then life", events)
	}
}

func TestRebuildProjectionsFromBrokerEventStore(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()

	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	if _, err := store.Append(ctx, EventAppend{
		Kind:             proto.EvtThreadNew,
		CompatibilitySeq: 10,
		Scopes:           []string{"board:general", "thread:thr_broker_rebuild"},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_broker_rebuild",
			Board:  "general",
			Author: "alice",
			Title:  "Broker rebuild",
			TS:     1000,
		},
	}); err != nil {
		t.Fatalf("append broker event: %v", err)
	}

	if err := c.RebuildProjectionsFromEventStore(ctx, store, 0); err != nil {
		t.Fatalf("rebuild from broker store: %v", err)
	}
	thread, err := getThread(c.DB, "thr_broker_rebuild")
	if err != nil {
		t.Fatalf("get rebuilt thread: %v", err)
	}
	if thread == nil || thread.Title != "Broker rebuild" || thread.LastSeq != 10 {
		t.Fatalf("rebuilt thread = %+v, want title and compatibility seq", thread)
	}
}

func TestDecodeBrokerEventMessageRejectsOffsetMismatch(t *testing.T) {
	record := BrokerEventRecord{
		Version:         brokerEventRecordVersion,
		Kind:            proto.EvtThreadNew,
		Scopes:          []string{"board:general"},
		Payload:         []byte(`{"id":"thr_broker","board":"general","author":"alice","title":"Broker","ts":1000}`),
		TS:              1000,
		PartitionKind:   partitionBoard,
		PartitionKey:    "general",
		PartitionOffset: 7,
	}
	data, err := EncodeBrokerEventRecord(record)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeBrokerEventMessage(BrokerEventLogMessage{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    8,
		Data:      data,
	}); err == nil || !strings.Contains(err.Error(), "offset metadata mismatch") {
		t.Fatalf("decode err = %v, want offset metadata mismatch", err)
	}
}
