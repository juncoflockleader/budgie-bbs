package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestMemoryCommandLogPartitionsAreIndependent(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryCommandLog()

	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	first, err := log.Produce(ctx, CommandLogRecord{
		Partition:  general,
		ActorID:    "usr_alice",
		CID:        "cid-1",
		Command:    proto.CmdCreateThread,
		Payload:    json.RawMessage(`{"board":"general"}`),
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("produce general: %v", err)
	}
	second, err := log.Produce(ctx, CommandLogRecord{
		Partition:  general,
		ActorID:    "usr_alice",
		CID:        "cid-2",
		Command:    proto.CmdAppendPost,
		Payload:    json.RawMessage(`{"thread":"thr_1"}`),
		EnqueuedAt: 1001,
	})
	if err != nil {
		t.Fatalf("produce second general: %v", err)
	}
	other, err := log.Produce(ctx, CommandLogRecord{
		Partition:  life,
		ActorID:    "usr_bob",
		CID:        "cid-3",
		Command:    proto.CmdCreateThread,
		Payload:    json.RawMessage(`{"board":"life"}`),
		EnqueuedAt: 1002,
	})
	if err != nil {
		t.Fatalf("produce life: %v", err)
	}
	if first.Offset != 1 || second.Offset != 2 || other.Offset != 1 {
		t.Fatalf("offsets = general(%d,%d) life(%d), want general(1,2) life(1)", first.Offset, second.Offset, other.Offset)
	}

	generalRecords, err := log.FetchPartition(ctx, general, 0, 10)
	if err != nil {
		t.Fatalf("fetch general: %v", err)
	}
	if len(generalRecords) != 2 {
		t.Fatalf("general records = %d, want 2", len(generalRecords))
	}
	if generalRecords[0].Partition != general.Normalize() || generalRecords[1].Offset != 2 {
		t.Fatalf("unexpected general records: %+v", generalRecords)
	}
	lifeRecords, err := log.FetchPartition(ctx, life, 0, 10)
	if err != nil {
		t.Fatalf("fetch life: %v", err)
	}
	if len(lifeRecords) != 1 || lifeRecords[0].Offset != 1 || lifeRecords[0].Partition != life.Normalize() {
		t.Fatalf("unexpected life records: %+v", lifeRecords)
	}

	if err := log.CommitPartition(ctx, general, second.Offset); err != nil {
		t.Fatalf("commit general: %v", err)
	}
	if got, err := log.CommittedOffset(ctx, general); err != nil || got != second.Offset {
		t.Fatalf("committed general = %d, %v; want %d, nil", got, err, second.Offset)
	}
	if got, err := log.CommittedOffset(ctx, life); err != nil || got != 0 {
		t.Fatalf("committed life = %d, %v; want 0, nil", got, err)
	}
	partitions, err := log.ListCommandPartitions(ctx, 1)
	if err != nil {
		t.Fatalf("list partitions: %v", err)
	}
	if len(partitions) != 1 || partitions[0] != general.Normalize() {
		t.Fatalf("partitions = %+v, want hottest general partition only", partitions)
	}
	offsets, err := log.ListCommandPartitionOffsets(ctx, 1)
	if err != nil {
		t.Fatalf("list partition offsets: %v", err)
	}
	if len(offsets) != 1 ||
		offsets[0].Partition != life.Normalize() ||
		offsets[0].TailOffset != other.Offset ||
		offsets[0].CommittedOffset != 0 {
		t.Fatalf("offsets = %+v, want lagging life partition only", offsets)
	}
}

func TestMemoryCommandLogProduceIsIdempotentByCID(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryCommandLog()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record := CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        "cid-retry",
		Command:    proto.CmdCreateThread,
		Payload:    json.RawMessage(`{"board":"general","title":"Retry"}`),
		EnqueuedAt: 1000,
	}
	first, err := log.Produce(ctx, record)
	if err != nil {
		t.Fatalf("first produce: %v", err)
	}
	second, err := log.Produce(ctx, record)
	if err != nil {
		t.Fatalf("second produce: %v", err)
	}
	if second.Offset != first.Offset || second.CID != first.CID {
		t.Fatalf("second record = %+v, want same receipt offset as first %+v", second, first)
	}
	records, err := log.FetchPartition(ctx, partition, 0, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one command after duplicate produce", records)
	}
	drift := record
	drift.EnqueuedAt = 2000
	if _, err := log.Produce(ctx, drift); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("timestamp drift produce err = %v, want different content error", err)
	}
	conflict := record
	conflict.Payload = json.RawMessage(`{"board":"general","title":"Changed"}`)
	if _, err := log.Produce(ctx, conflict); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("conflict produce err = %v, want different content error", err)
	}
}

func TestMemoryCommandLogRequiresEnqueuedAtForExplicitReceipt(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryCommandLog()
	_, err := log.Produce(ctx, CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		ActorID:   "usr_alice",
		CID:       "cid-missing-enqueued-at",
		Command:   proto.CmdCreateThread,
		Payload:   json.RawMessage(`{"board":"general","title":"Missing enqueue"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "enqueue time is required") {
		t.Fatalf("produce err = %v, want enqueue time required error", err)
	}
}

func TestSQLEventStoreAppendAndReplayPartition(t *testing.T) {
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()

	store := NewSQLEventStore(c.DB)
	ctx := context.Background()
	general, err := store.Append(ctx, EventAppend{
		ID:     "evt_sql_store_general",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_sql_store_general",
			Board:  "general",
			Author: "alice",
			Title:  "SQL store general",
			TS:     1000,
		},
	})
	if err != nil {
		t.Fatalf("append general: %v", err)
	}
	life, err := store.Append(ctx, EventAppend{
		ID:     "evt_sql_store_life",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:life"},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_sql_store_life",
			Board:  "life",
			Author: "alice",
			Title:  "SQL store life",
			TS:     1001,
		},
	})
	if err != nil {
		t.Fatalf("append life: %v", err)
	}
	if general.PartitionKind != partitionBoard || general.PartitionKey != "general" || general.PartitionOffset != 1 {
		t.Fatalf("general partition = (%q,%q,%d), want (%q,general,1)", general.PartitionKind, general.PartitionKey, general.PartitionOffset, partitionBoard)
	}
	if life.PartitionKind != partitionBoard || life.PartitionKey != "life" || life.PartitionOffset != 1 {
		t.Fatalf("life partition = (%q,%q,%d), want (%q,life,1)", life.PartitionKind, life.PartitionKey, life.PartitionOffset, partitionBoard)
	}

	head, err := store.Head(ctx)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head != life.Seq {
		t.Fatalf("head = %d, want %d", head, life.Seq)
	}
	partitionEvents, err := store.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay partition: %v", err)
	}
	if len(partitionEvents) != 1 || partitionEvents[0].Seq != general.Seq {
		t.Fatalf("general partition events = %+v, want seq %d", partitionEvents, general.Seq)
	}
	all, err := store.Replay(ctx, 0, nil, 10)
	if err != nil {
		t.Fatalf("replay all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all replay count = %d, want 2", len(all))
	}
}

func TestShadowEventStoreMirrorsCleanAppend(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	store := NewShadowEventStore(primary, shadow, recorder)

	appended, err := store.Append(ctx, testEventAppend("general"))
	if err != nil {
		t.Fatalf("shadow append: %v", err)
	}
	if appended.PartitionKind != partitionBoard || appended.PartitionKey != "general" || appended.PartitionOffset != 1 {
		t.Fatalf("primary appended partition = (%q,%q,%d)", appended.PartitionKind, appended.PartitionKey, appended.PartitionOffset)
	}
	if issues := recorder.Issues(); len(issues) != 0 {
		t.Fatalf("unexpected parity issues: %+v", issues)
	}
	shadowEvents, err := shadow.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("shadow partition replay: %v", err)
	}
	if len(shadowEvents) != 1 ||
		shadowEvents[0].ID != appended.ID ||
		shadowEvents[0].TS != appended.TS ||
		shadowEvents[0].Seq != appended.Seq ||
		shadowEvents[0].PartitionOffset != appended.PartitionOffset {
		t.Fatalf("shadow replay = %+v, want primary identity seq/timestamp/offset from %+v", shadowEvents, appended)
	}
}

func TestShadowEventStoreReportsAppendFailureWithoutFailingPrimary(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	store := NewShadowEventStore(primary, failingEventStore{
		MemoryEventStore: NewMemoryEventStore(),
		err:              errors.New("mirror unavailable"),
	}, recorder)

	appended, err := store.Append(ctx, testEventAppend("general"))
	if err != nil {
		t.Fatalf("primary append should still succeed: %v", err)
	}
	if appended.Seq != 1 {
		t.Fatalf("primary seq = %d, want 1", appended.Seq)
	}
	events, err := primary.Replay(ctx, 0, nil, 10)
	if err != nil {
		t.Fatalf("primary replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("primary replay count = %d, want 1", len(events))
	}
	issues := recorder.Issues()
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one append failure", issues)
	}
	if issues[0].Kind != EventParityAppendError || !strings.Contains(issues[0].Err, "mirror unavailable") {
		t.Fatalf("issue = %+v, want append failure with mirror error", issues[0])
	}
}

func TestShadowEventStoreReportsLogicalMismatchWithoutFailingPrimary(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	store := NewShadowEventStore(primary, offsetMutatingEventStore{MemoryEventStore: NewMemoryEventStore()}, recorder)

	appended, err := store.Append(ctx, testEventAppend("general"))
	if err != nil {
		t.Fatalf("primary append should still succeed: %v", err)
	}
	if appended.PartitionOffset != 1 {
		t.Fatalf("primary offset = %d, want 1", appended.PartitionOffset)
	}
	issues := recorder.Issues()
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one mismatch", issues)
	}
	if issues[0].Kind != EventParityMismatch || !strings.Contains(issues[0].Message, "partition offset mismatch") {
		t.Fatalf("issue = %+v, want partition offset mismatch", issues[0])
	}
}

func TestCheckEventReplayParityCleanPartition(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
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
	if result.PrimaryCount != 2 || result.ShadowCount != 2 || result.Compared != 2 {
		t.Fatalf("result = %+v, want two compared events", result)
	}
	if len(result.Issues) != 0 || len(recorder.Issues()) != 0 {
		t.Fatalf("unexpected issues result=%+v recorder=%+v", result.Issues, recorder.Issues())
	}
}

func TestCheckEventReplayParityReportsMissingShadowEvent(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}

	if _, err := primary.Append(ctx, testEventAppend("general")); err != nil {
		t.Fatalf("primary append: %v", err)
	}
	result, err := CheckEventReplayParity(ctx, primary, shadow, LogPartition{Kind: partitionBoard, Key: "general"}, 0, 10, recorder)
	if err != nil {
		t.Fatalf("check parity: %v", err)
	}
	if len(result.Issues) != 1 || !strings.Contains(result.Issues[0].Message, "shadow missing") {
		t.Fatalf("result issues = %+v, want shadow missing", result.Issues)
	}
	if len(recorder.Issues()) != 1 {
		t.Fatalf("recorder issues = %+v, want one", recorder.Issues())
	}
}

func TestCheckEventReplayParityReportsPayloadMismatch(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}

	if _, err := primary.Append(ctx, testEventAppendWithTitle("general", "Primary title")); err != nil {
		t.Fatalf("primary append: %v", err)
	}
	if _, err := shadow.Append(ctx, testEventAppendWithTitle("general", "Shadow title")); err != nil {
		t.Fatalf("shadow append: %v", err)
	}
	result, err := CheckEventReplayParity(ctx, primary, shadow, LogPartition{Kind: partitionBoard, Key: "general"}, 0, 10, recorder)
	if err != nil {
		t.Fatalf("check parity: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0].Message != "payload mismatch" {
		t.Fatalf("result issues = %+v, want payload mismatch", result.Issues)
	}
}

func TestCheckEventReplayParityReportsCompatibilitySeqMismatch(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	recorder := &EventParityRecorder{}

	if _, err := primary.Append(ctx, testEventAppendWithTitle("general", "Seq guarded")); err != nil {
		t.Fatalf("primary append: %v", err)
	}
	appendEvent := testEventAppendWithTitle("general", "Seq guarded")
	appendEvent.CompatibilitySeq = 41
	if _, err := shadow.Append(ctx, appendEvent); err != nil {
		t.Fatalf("shadow append: %v", err)
	}
	result, err := CheckEventReplayParity(ctx, primary, shadow, LogPartition{Kind: partitionBoard, Key: "general"}, 0, 10, recorder)
	if err != nil {
		t.Fatalf("check parity: %v", err)
	}
	if len(result.Issues) != 1 || !strings.Contains(result.Issues[0].Message, "seq mismatch") {
		t.Fatalf("result issues = %+v, want seq mismatch", result.Issues)
	}
}

func TestCheckEventLogPromotionReadinessCleanCoverage(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	candidate := NewMemoryEventStore()

	for _, appendEvent := range []EventAppend{
		testEventAppendWithTitle("general", "General 1"),
		testEventAppendWithTitle("life", "Life 1"),
		testEventAppendWithTitle("general", "General 2"),
	} {
		if _, err := primary.Append(ctx, appendEvent); err != nil {
			t.Fatalf("primary append: %v", err)
		}
		if _, err := candidate.Append(ctx, appendEvent); err != nil {
			t.Fatalf("candidate append: %v", err)
		}
	}

	report, err := CheckEventLogPromotionReadiness(ctx, EventLogPromotionReadinessConfig{
		Primary:     primary,
		Candidate:   candidate,
		ReplayLimit: 1,
	})
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if !report.Ready || len(report.Issues) != 0 {
		t.Fatalf("report = %+v, want ready without issues", report)
	}
	if report.PartitionsChecked != 2 || report.Compared != 3 || report.WindowsChecked < 5 {
		t.Fatalf("report = %+v, want two partitions, three comparisons, and bounded windows", report)
	}
}

func TestCheckEventLogPromotionReadinessReportsMissingAndExtraEvents(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	candidate := NewMemoryEventStore()

	if _, err := primary.Append(ctx, testEventAppendWithTitle("general", "Only primary")); err != nil {
		t.Fatalf("primary append: %v", err)
	}
	if _, err := candidate.Append(ctx, testEventAppendWithTitle("life", "Only candidate")); err != nil {
		t.Fatalf("candidate append: %v", err)
	}

	report, err := CheckEventLogPromotionReadiness(ctx, EventLogPromotionReadinessConfig{
		Primary:     primary,
		Candidate:   candidate,
		ReplayLimit: 10,
	})
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if report.Ready {
		t.Fatalf("report = %+v, want not ready", report)
	}
	if len(report.Issues) != 2 {
		t.Fatalf("issues = %+v, want missing primary event and extra candidate event", report.Issues)
	}
	messages := report.Issues[0].Message + "\n" + report.Issues[1].Message
	if !strings.Contains(messages, "shadow missing") || !strings.Contains(messages, "shadow has extra") {
		t.Fatalf("issues = %+v, want missing and extra messages", report.Issues)
	}
}

func TestCheckEventLogPromotionReadinessFailsWhenPartitionLimitTruncates(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	candidate := NewMemoryEventStore()

	for _, appendEvent := range []EventAppend{
		testEventAppendWithTitle("general", "General"),
		testEventAppendWithTitle("life", "Life"),
	} {
		if _, err := primary.Append(ctx, appendEvent); err != nil {
			t.Fatalf("primary append: %v", err)
		}
		if _, err := candidate.Append(ctx, appendEvent); err != nil {
			t.Fatalf("candidate append: %v", err)
		}
	}

	report, err := CheckEventLogPromotionReadiness(ctx, EventLogPromotionReadinessConfig{
		Primary:        primary,
		Candidate:      candidate,
		ReplayLimit:    10,
		PartitionLimit: 1,
	})
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if report.Ready {
		t.Fatalf("report = %+v, want not ready after partition limit truncation", report)
	}
	if len(report.Issues) == 0 || !strings.Contains(report.Issues[0].Message, "partition count exceeds") {
		t.Fatalf("issues = %+v, want partition-limit coverage issue", report.Issues)
	}
}

func TestEventStorePartitionListersOrderHotPartitions(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryEventStore()
	for _, board := range []string{"general", "life", "general"} {
		if _, err := memory.Append(ctx, testEventAppend(board)); err != nil {
			t.Fatalf("append memory %s: %v", board, err)
		}
	}
	partitions, err := memory.ListEventPartitions(ctx, 10)
	if err != nil {
		t.Fatalf("list memory partitions: %v", err)
	}
	if len(partitions) != 2 {
		t.Fatalf("memory partitions = %+v, want two", partitions)
	}
	if partitions[0] != (LogPartition{Kind: partitionBoard, Key: "general"}) {
		t.Fatalf("hottest memory partition = %+v, want board/general", partitions[0])
	}

	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	sqlStore := NewSQLEventStore(c.DB)
	for _, board := range []string{"general", "life", "general"} {
		if _, err := sqlStore.Append(ctx, testEventAppend(board)); err != nil {
			t.Fatalf("append sql %s: %v", board, err)
		}
	}
	sqlPartitions, err := sqlStore.ListEventPartitions(ctx, 1)
	if err != nil {
		t.Fatalf("list sql partitions: %v", err)
	}
	if len(sqlPartitions) != 1 || sqlPartitions[0] != (LogPartition{Kind: partitionBoard, Key: "general"}) {
		t.Fatalf("sql partitions = %+v, want only board/general", sqlPartitions)
	}
}

func TestEventReplayParityRunnerAdvancesCleanPartitions(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	runner := NewEventReplayParityRunner(EventReplayParityRunnerConfig{
		Primary:     primary,
		Shadow:      shadow,
		Reporter:    recorder,
		ReplayLimit: 10,
	})

	for _, title := range []string{"First", "Second"} {
		if _, err := primary.Append(ctx, testEventAppendWithTitle("general", title)); err != nil {
			t.Fatalf("primary append %q: %v", title, err)
		}
		if _, err := shadow.Append(ctx, testEventAppendWithTitle("general", title)); err != nil {
			t.Fatalf("shadow append %q: %v", title, err)
		}
	}
	results, err := runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if len(results) != 1 || results[0].AfterOffset != 0 || results[0].LastPrimaryOffset != 2 || len(results[0].Issues) != 0 {
		t.Fatalf("first results = %+v, want clean check through offset 2", results)
	}

	results, err = runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(results) != 1 || results[0].AfterOffset != 2 || results[0].PrimaryCount != 0 {
		t.Fatalf("second results = %+v, want checkpointed empty replay after offset 2", results)
	}
}

func TestEventReplayParityRunnerDoesNotAdvanceOnIssue(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	runner := NewEventReplayParityRunner(EventReplayParityRunnerConfig{
		Primary:     primary,
		Shadow:      shadow,
		Reporter:    recorder,
		ReplayLimit: 10,
	})
	if _, err := primary.Append(ctx, testEventAppend("general")); err != nil {
		t.Fatalf("primary append: %v", err)
	}

	results, err := runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if len(results) != 1 || len(results[0].Issues) != 1 || results[0].AfterOffset != 0 {
		t.Fatalf("first results = %+v, want issue from offset 0", results)
	}
	results, err = runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(results) != 1 || results[0].AfterOffset != 0 {
		t.Fatalf("second results = %+v, want no checkpoint advance after issue", results)
	}

	if _, err := shadow.Append(ctx, testEventAppend("general")); err != nil {
		t.Fatalf("shadow repair append: %v", err)
	}
	results, err = runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("repair check: %v", err)
	}
	if len(results) != 1 || len(results[0].Issues) != 0 || results[0].LastPrimaryOffset != 1 {
		t.Fatalf("repair results = %+v, want clean replay through offset 1", results)
	}
}

func TestEventReplayParityRunnerSurvivesBrokerPartitionOutage(t *testing.T) {
	ctx := context.Background()
	primary := NewMemoryEventStore()
	shadowBase := NewMemoryEventStore()
	shadow := &partitionReplayFailingEventStore{
		MemoryEventStore: shadowBase,
		failed:           map[LogPartition]error{{Kind: partitionBoard, Key: "general"}: errors.New("broker partition unavailable")},
	}
	recorder := &EventParityRecorder{}
	runner := NewEventReplayParityRunner(EventReplayParityRunnerConfig{
		Primary:     primary,
		Shadow:      shadow,
		Reporter:    recorder,
		ReplayLimit: 10,
	})
	for _, board := range []string{"general", "life"} {
		if _, err := primary.Append(ctx, testEventAppendWithTitle(board, "primary "+board)); err != nil {
			t.Fatalf("primary append %s: %v", board, err)
		}
		if _, err := shadowBase.Append(ctx, testEventAppendWithTitle(board, "primary "+board)); err != nil {
			t.Fatalf("shadow append %s: %v", board, err)
		}
	}

	results, err := runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	byPartition := eventReplayResultsByPartition(results)
	outagePartition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	healthyPartition := LogPartition{Kind: partitionBoard, Key: "life"}.Normalize()
	outage := byPartition[outagePartition]
	if len(outage.Issues) != 1 || outage.Issues[0].Kind != EventParityReplayError || !strings.Contains(outage.Issues[0].Err, "broker partition unavailable") {
		t.Fatalf("outage result = %+v, want replay error for unavailable broker partition", outage)
	}
	if got := runner.offsetFor(outagePartition); got != 0 {
		t.Fatalf("outage checkpoint = %d, want 0 while partition replay is failing", got)
	}
	healthy := byPartition[healthyPartition]
	if len(healthy.Issues) != 0 || healthy.LastPrimaryOffset != 1 {
		t.Fatalf("healthy result = %+v, want clean replay through offset 1", healthy)
	}
	if got := runner.offsetFor(healthyPartition); got != 1 {
		t.Fatalf("healthy checkpoint = %d, want 1 despite unrelated broker partition outage", got)
	}

	delete(shadow.failed, outagePartition)
	results, err = runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("repair check: %v", err)
	}
	byPartition = eventReplayResultsByPartition(results)
	outage = byPartition[outagePartition]
	if outage.AfterOffset != 0 || len(outage.Issues) != 0 || outage.LastPrimaryOffset != 1 {
		t.Fatalf("repaired outage result = %+v, want clean replay from original checkpoint", outage)
	}
	if got := runner.offsetFor(outagePartition); got != 1 {
		t.Fatalf("outage checkpoint after repair = %d, want 1", got)
	}
	if issues := recorder.Issues(); len(issues) != 1 || issues[0].Kind != EventParityReplayError {
		t.Fatalf("recorded issues = %+v, want exactly the outage replay error", issues)
	}
}

func TestCoreEventLogShadowSeedsAndMirrorsCommittedEvents(t *testing.T) {
	ctx := context.Background()

	shadow := NewMemoryEventStore()
	recorder := &EventParityRecorder{}
	c, err := New(t.TempDir()+"/budgie.db", WithEventLogShadow(EventLogShadowOptions{
		Shadow:      shadow,
		Reporter:    recorder,
		StartAtHead: true,
		ReplayLimit: 10,
	}))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()

	primary := NewSQLEventStore(c.DB)
	existing, err := primary.Append(ctx, testEventAppendWithTitle("general", "Existing"))
	if err != nil {
		t.Fatalf("append existing primary: %v", err)
	}
	if existing.PartitionOffset != 1 {
		t.Fatalf("existing offset = %d, want 1", existing.PartitionOffset)
	}

	runCtx, stopRunner := context.WithCancel(ctx)
	runner := c.StartEventLogShadowParity(runCtx)
	if runner == nil {
		t.Fatal("shadow parity runner was not created")
	}
	stopRunner()
	next, err := primary.Append(ctx, testEventAppendWithTitle("general", "Next"))
	if err != nil {
		t.Fatalf("append next primary: %v", err)
	}
	c.Bus.Publish(&proto.Event{Kind: next.Kind, Seq: next.Seq, Scopes: next.Scopes, Payload: next.Payload, TS: next.TS})

	shadowEvents, err := shadow.ReplayPartition(ctx, partitionBoard, "general", 1, 10)
	if err != nil {
		t.Fatalf("shadow replay: %v", err)
	}
	if len(shadowEvents) != 1 || shadowEvents[0].PartitionOffset != 2 {
		t.Fatalf("shadow events = %+v, want one mirrored event at offset 2", shadowEvents)
	}
	results, err := runner.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("parity check: %v", err)
	}
	if len(results) != 1 || len(results[0].Issues) != 0 {
		t.Fatalf("parity results = %+v, want clean tail check", results)
	}
	if issues := recorder.Issues(); len(issues) != 0 {
		t.Fatalf("unexpected recorded issues: %+v", issues)
	}
}

func testEventAppend(board string) EventAppend {
	return testEventAppendWithTitle(board, "Shadow "+board)
}

func testEventAppendWithTitle(board, title string) EventAppend {
	return EventAppend{
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:" + board},
		Payload: &proto.ThreadNewPayload{
			ID:     "thr_shadow_" + board,
			Board:  board,
			Author: "alice",
			Title:  title,
			TS:     1000,
		},
		TS: 1000,
	}
}

type failingEventStore struct {
	*MemoryEventStore
	err error
}

func (s failingEventStore) Append(context.Context, EventAppend) (*proto.Event, error) {
	return nil, s.err
}

type offsetMutatingEventStore struct {
	*MemoryEventStore
}

func (s offsetMutatingEventStore) Append(ctx context.Context, event EventAppend) (*proto.Event, error) {
	evt, err := s.MemoryEventStore.Append(ctx, event)
	if err != nil {
		return nil, err
	}
	evt.PartitionOffset++
	return evt, nil
}

type partitionReplayFailingEventStore struct {
	*MemoryEventStore
	failed map[LogPartition]error
}

func (s *partitionReplayFailingEventStore) ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	partition := LogPartition{Kind: partitionKind, Key: partitionKey}.Normalize()
	if err := s.failed[partition]; err != nil {
		return nil, err
	}
	return s.MemoryEventStore.ReplayPartition(ctx, partitionKind, partitionKey, afterOffset, limit)
}

func eventReplayResultsByPartition(results []EventReplayParityResult) map[LogPartition]EventReplayParityResult {
	out := map[LogPartition]EventReplayParityResult{}
	for _, result := range results {
		out[result.Partition.Normalize()] = result
	}
	return out
}
