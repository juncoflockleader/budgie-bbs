package core

import (
	"context"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func produceBrokerCommandLogTestRecord(t *testing.T, ctx context.Context, log interface {
	Produce(context.Context, CommandLogRecord) (CommandLogRecord, error)
}, record CommandLogRecord, label string) CommandLogRecord {
	t.Helper()
	produced, err := log.Produce(ctx, record)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return produced
}

func TestBrokerCommandSubjectRoundTripEscapesPartitionTokens(t *testing.T) {
	partition := LogPartition{Kind: "thread.topic", Key: "general/thread:alpha"}
	subject := BrokerCommandSubject(partition)
	if !strings.HasPrefix(subject, "budgie.commandlog.") {
		t.Fatalf("subject = %q, want command-log prefix", subject)
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
	decoded, ok := ParseBrokerCommandSubject(subject)
	if !ok {
		t.Fatalf("parse subject %q failed", subject)
	}
	if decoded != partition.Normalize() {
		t.Fatalf("decoded partition = %+v, want %+v", decoded, partition.Normalize())
	}
	commitSubject := BrokerCommandCommitSubject(partition)
	decodedCommit, ok := ParseBrokerCommandCommitSubject(commitSubject)
	if !ok {
		t.Fatalf("parse commit subject %q failed", commitSubject)
	}
	if decodedCommit != partition.Normalize() {
		t.Fatalf("decoded commit partition = %+v, want %+v", decodedCommit, partition.Normalize())
	}
	messageID := BrokerCommandMessageID(partition, "actor.1", " cid/1 ")
	messageTokens := strings.Split(messageID, ".")
	if len(messageTokens) != 7 {
		t.Fatalf("message id tokens = %v, want seven tokens", messageTokens)
	}
	for _, token := range messageTokens[3:] {
		if strings.ContainsAny(token, "/=: \t\n") {
			t.Fatalf("message id token %q contains unsafe subject characters", token)
		}
	}
	if BrokerCommandMessageID(partition, "actor.1", " ") != "" {
		t.Fatalf("blank command receipt produced message id")
	}
}

func TestBrokerCommandLogPartitionsAndCommitsAreIndependent(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())

	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	first := produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  general,
		ActorID:    "usr_alice",
		CID:        "cid-1",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	}, "produce first")
	second := produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  general,
		ActorID:    "usr_alice",
		CID:        "cid-2",
		Command:    proto.CmdAppendPost,
		Payload:    []byte(`{"thread":"thr_general","body":"hello"}`),
		EnqueuedAt: 1001,
	}, "produce second")
	other := produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  life,
		ActorID:    "usr_bob",
		CID:        "cid-3",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"life","title":"Life"}`),
		EnqueuedAt: 1002,
	}, "produce other")

	if first.Offset != 1 || second.Offset != 2 || other.Offset != 1 {
		t.Fatalf("offsets = general(%d,%d) life(%d), want general(1,2) life(1)", first.Offset, second.Offset, other.Offset)
	}
	fetched, err := log.FetchPartition(ctx, general, 0, 10)
	if err != nil {
		t.Fatalf("fetch general: %v", err)
	}
	if len(fetched) != 2 || fetched[0].CID != "cid-1" || fetched[1].CID != "cid-2" {
		t.Fatalf("fetched general = %+v, want two ordered commands", fetched)
	}
	afterFirst, err := log.FetchPartition(ctx, general, 1, 10)
	if err != nil {
		t.Fatalf("fetch after first: %v", err)
	}
	if len(afterFirst) != 1 || afterFirst[0].Offset != 2 {
		t.Fatalf("after-first = %+v, want only offset 2", afterFirst)
	}
	if err := log.CommitPartition(ctx, general, 2); err != nil {
		t.Fatalf("commit general: %v", err)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, log, general, 2, "committed general")
	requireCommandLogWorkerCommittedOffset(t, ctx, log, life, 0, "committed life")
}

func TestBrokerCommandLogSynthesizesCIDWhenMissing(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record := produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt: 1000,
	}, "produce")
	wantCID := logmodel.SyntheticCommandLogCID(partition, record.Offset)
	if record.CID != wantCID {
		t.Fatalf("record cid = %q, want %q", record.CID, wantCID)
	}
	fetched, err := log.FetchPartition(ctx, partition, 0, 1)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(fetched) != 1 || fetched[0].CID != wantCID {
		t.Fatalf("fetched = %+v, want persisted synthetic cid %q", fetched, wantCID)
	}
}

func TestBrokerCommandLogProduceIsIdempotentByCID(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record := CommandLogRecord{
		Partition:  partition,
		ActorID:    "usr_alice",
		CID:        "cid-retry",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"general","title":"Retry"}`),
		EnqueuedAt: 1000,
	}
	first := produceBrokerCommandLogTestRecord(t, ctx, log, record, "first produce")
	second := produceBrokerCommandLogTestRecord(t, ctx, log, record, "second produce")
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
	_, err = log.Produce(ctx, drift)
	requireErrorContains(t, err, "different content")

	otherActor := record
	otherActor.ActorID = "usr_bob"
	third := produceBrokerCommandLogTestRecord(t, ctx, log, otherActor, "produce same cid for different actor")
	if third.Offset != 2 {
		t.Fatalf("third offset = %d, want independent actor-scoped receipt at offset 2", third.Offset)
	}

	conflict := record
	conflict.Payload = []byte(`{"board":"general","title":"Changed"}`)
	_, err = log.Produce(ctx, conflict)
	requireErrorContains(t, err, "different content")
}

func TestBrokerCommandLogRequiresEnqueuedAtForExplicitReceipt(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	_, err := log.Produce(ctx, CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		ActorID:   "usr_alice",
		CID:       "cid-missing-enqueued-at",
		Command:   proto.CmdCreateThread,
		Payload:   []byte(`{"board":"general","title":"Missing enqueue"}`),
	})
	requireErrorContains(t, err, "enqueue time is required")
}

func TestMemoryBrokerCommandLogClientRequiresEnqueuedAtForExplicitReceipt(t *testing.T) {
	ctx := context.Background()
	client := NewMemoryBrokerCommandLogClient()
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	_, err := client.AppendCommand(ctx, partition, logmodel.BrokerCommandRecord{
		Version:       brokerCommandRecordVersion,
		ActorID:       "usr_alice",
		CID:           "cid-direct-missing-enqueued-at",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"Missing enqueue"}`),
		PartitionKind: partitionBoard,
		PartitionKey:  "general",
	})
	requireErrorContains(t, err, "enqueue time is required")
	records, fetchErr := NewBrokerCommandLog(client).FetchPartition(ctx, partition, 0, 10)
	if fetchErr != nil {
		t.Fatalf("fetch partition: %v", fetchErr)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v, want rejected append to leave partition empty", records)
	}
}

func TestBrokerCommandLogListsHotPartitions(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	for _, board := range []string{"general", "life", "general"} {
		produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
			Partition:  LogPartition{Kind: partitionBoard, Key: board},
			ActorID:    "usr_alice",
			Command:    proto.CmdCreateThread,
			Payload:    []byte(`{"board":"` + board + `","title":"` + board + `"}`),
			EnqueuedAt: 1000,
		}, "produce "+board)
	}
	partitions, err := log.ListCommandPartitions(ctx, 1)
	if err != nil {
		t.Fatalf("list partitions: %v", err)
	}
	if len(partitions) != 1 || partitions[0] != (LogPartition{Kind: partitionBoard, Key: "general"}) {
		t.Fatalf("partitions = %+v, want hottest board/general only", partitions)
	}
}

func TestCommandPartitionOffsetMetrics(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	general := LogPartition{Kind: partitionBoard, Key: "general"}
	life := LogPartition{Kind: partitionBoard, Key: "life"}
	for _, partition := range []LogPartition{general, general, life} {
		produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
			Partition:  partition,
			ActorID:    "usr_alice",
			Command:    proto.CmdCreateThread,
			Payload:    []byte(`{"board":"` + partition.Key + `","title":"` + partition.Key + `"}`),
			EnqueuedAt: 1000,
		}, "produce "+partition.Key)
	}
	if err := log.CommitPartition(ctx, general, 2); err != nil {
		t.Fatalf("commit general: %v", err)
	}

	samples, err := commandPartitionOffsetSamples(ctx, log, 100)
	if err != nil {
		t.Fatalf("command partition offset samples: %v", err)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_tail_offset", partitionBoard, "general"); got != 2 {
		t.Fatalf("general tail metric = %v, want 2", got)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_committed_offset", partitionBoard, "general"); got != 2 {
		t.Fatalf("general committed metric = %v, want 2", got)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_lag", partitionBoard, "general"); got != 0 {
		t.Fatalf("general lag metric = %v, want 0", got)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_lag", partitionBoard, "life"); got != 1 {
		t.Fatalf("life lag metric = %v, want 1", got)
	}
	if got := commandPartitionSignalSample(samples, "budgie_hot_partition_candidate", partitionBoard, "life", "command_lag"); got != 1 {
		t.Fatalf("life hot partition candidate metric = %v, want 1", got)
	}
	if got := commandPartitionSignalSample(samples, "budgie_hot_partition_candidate", partitionBoard, "general", "command_lag"); got != -1 {
		t.Fatalf("general hot partition candidate metric = %v, want absent", got)
	}
	if got := sampleValue(samples, "budgie_command_partition_count"); got != 2 {
		t.Fatalf("command partition count metric = %v, want 2", got)
	}
	if got := sampleValue(samples, "budgie_command_partition_lag_total"); got != 1 {
		t.Fatalf("command partition lag total metric = %v, want 1", got)
	}
	if got := sampleValue(samples, "budgie_command_partition_lag_max"); got != 1 {
		t.Fatalf("command partition lag max metric = %v, want 1", got)
	}
	if got := sampleValue(samples, "budgie_command_partition_lag_skew"); got != 1 {
		t.Fatalf("command partition lag skew metric = %v, want 1", got)
	}
}

func TestCommandPartitionOffsetMetricsNormalizeListedOffsets(t *testing.T) {
	ctx := context.Background()
	samples, err := commandPartitionOffsetSamples(ctx, commandPartitionOffsetSliceLister{
		{Partition: LogPartition{}, TailOffset: 2, CommittedOffset: 5},
	}, 100)
	if err != nil {
		t.Fatalf("command partition offset samples: %v", err)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_tail_offset", partitionGlobal, partitionGlobal); got != 2 {
		t.Fatalf("normalized global tail metric = %v, want 2", got)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_committed_offset", partitionGlobal, partitionGlobal); got != 2 {
		t.Fatalf("normalized global committed metric = %v, want clamped 2", got)
	}
	if got := commandPartitionSample(samples, "budgie_command_partition_lag", partitionGlobal, partitionGlobal); got != 0 {
		t.Fatalf("normalized global lag metric = %v, want 0", got)
	}
}

func TestCommandPartitionAssignmentMetrics(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	assigner := logmodel.NewHashCommandPartitionAssigner([]string{"writer-a", "writer-b"}, 4)
	owned := commandPartitionAssignedToForMetrics(t, ctx, assigner, "writer-a")
	skipped := commandPartitionAssignedToForMetrics(t, ctx, assigner, "writer-b")
	produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  owned,
		ActorID:    "usr_alice",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"` + owned.Key + `","title":"owned"}`),
		EnqueuedAt: 1000,
	}, "produce owned")
	produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
		Partition:  skipped,
		ActorID:    "usr_alice",
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{"board":"` + skipped.Key + `","title":"skipped"}`),
		EnqueuedAt: 1000,
	}, "produce skipped")

	samples, err := commandPartitionAssignmentSamples(ctx, assigner, log, "writer-a", 100)
	if err != nil {
		t.Fatalf("command partition assignment samples: %v", err)
	}
	if got := commandPartitionAssignmentSample(samples, "budgie_command_partition_assigned", owned.Kind, owned.Key); got != 1 {
		t.Fatalf("owned assignment metric = %v, want 1", got)
	}
	if got := commandPartitionAssignmentSample(samples, "budgie_command_partition_assigned", skipped.Kind, skipped.Key); got != 0 {
		t.Fatalf("skipped assignment metric = %v, want 0", got)
	}
	if got := commandPartitionAssignmentSample(samples, "budgie_command_partition_assignment_generation", owned.Kind, owned.Key); got != 4 {
		t.Fatalf("owned generation metric = %v, want 4", got)
	}
	if got := sampleValue(samples, "budgie_command_partition_assigned_count"); got != 1 {
		t.Fatalf("assigned count metric = %v, want 1", got)
	}
}

func TestHotPartitionReassignmentLetsNewOwnerDrainLag(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	hot := LogPartition{Kind: partitionThread, Key: "thr_hot#reply-0"}
	assigner := logmodel.NewSnapshotCommandPartitionAssigner(logmodel.CommandPartitionAssignmentSnapshot{
		Generation: 17,
		Owners: map[LogPartition]string{
			hot: "writer-a",
		},
	})

	for i := 0; i < 3; i++ {
		produceBrokerCommandLogTestRecord(t, ctx, log, CommandLogRecord{
			Partition:  hot,
			ActorID:    "usr_alice",
			Command:    proto.CmdAppendPost,
			Payload:    []byte(`{"thread":"thr_hot","body":"hot reply"}`),
			EnqueuedAt: 1000 + int64(i),
		}, "produce hot command")
	}

	before, err := commandPartitionOffsetSamples(ctx, log, 100)
	if err != nil {
		t.Fatalf("command partition offset samples before reassignment: %v", err)
	}
	if got := commandPartitionSample(before, "budgie_command_partition_lag", partitionThread, hot.Key); got != 3 {
		t.Fatalf("hot partition lag before reassignment = %v, want 3", got)
	}
	if got := commandPartitionSignalSample(before, "budgie_hot_partition_candidate", partitionThread, hot.Key, "command_lag"); got != 3 {
		t.Fatalf("hot partition candidate before reassignment = %v, want 3", got)
	}
	if _, assigned, err := assigner.AssignCommandPartition(ctx, "writer-b", hot); err != nil {
		t.Fatalf("assign stale owner: %v", err)
	} else if assigned {
		t.Fatal("writer-b unexpectedly owned hot partition before reassignment")
	}

	assigner.ApplySnapshot(logmodel.CommandPartitionAssignmentSnapshot{
		Generation: 18,
		Owners: map[LogPartition]string{
			hot: "writer-b",
		},
	})
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-b", hot)
	if err != nil {
		t.Fatalf("assign new owner: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 18 {
		t.Fatalf("assignment after reassignment = %+v assigned=%v, want writer-b generation 18", assignment, assigned)
	}
	assignedSamples, err := commandPartitionAssignmentSamples(ctx, assigner, log, "writer-b", 100)
	if err != nil {
		t.Fatalf("command partition assignment samples after reassignment: %v", err)
	}
	if got := commandPartitionAssignmentSample(assignedSamples, "budgie_command_partition_assigned", partitionThread, hot.Key); got != 1 {
		t.Fatalf("writer-b assignment metric after reassignment = %v, want 1", got)
	}

	records, err := log.FetchPartition(ctx, hot, 0, 10)
	if err != nil {
		t.Fatalf("fetch reassigned hot partition: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("fetched hot records = %d, want 3", len(records))
	}
	last := records[len(records)-1]
	if err := log.CommitPartition(ctx, hot, last.Offset); err != nil {
		t.Fatalf("commit hot partition after reassignment: %v", err)
	}

	after, err := commandPartitionOffsetSamples(ctx, log, 100)
	if err != nil {
		t.Fatalf("command partition offset samples after drain: %v", err)
	}
	if got := commandPartitionSample(after, "budgie_command_partition_lag", partitionThread, hot.Key); got != 0 {
		t.Fatalf("hot partition lag after reassignment drain = %v, want 0", got)
	}
	if got := commandPartitionSignalSample(after, "budgie_hot_partition_candidate", partitionThread, hot.Key, "command_lag"); got != -1 {
		t.Fatalf("hot partition candidate after reassignment drain = %v, want absent", got)
	}
	if got := sampleValue(after, "budgie_command_partition_lag_max"); got != 0 {
		t.Fatalf("lag max after reassignment drain = %v, want 0", got)
	}
}

func TestDecodeBrokerCommandMessageRejectsMetadataMismatch(t *testing.T) {
	record := logmodel.BrokerCommandRecord{
		Version:       brokerCommandRecordVersion,
		ActorID:       "usr_alice",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"General"}`),
		EnqueuedAt:    1000,
		PartitionKind: partitionBoard,
		PartitionKey:  "general",
		Offset:        7,
	}
	data, err := logmodel.EncodeBrokerCommandRecord(record)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, err = logmodel.DecodeBrokerCommandMessage(logmodel.BrokerCommandLogMessage{
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:    8,
		Data:      data,
	})
	requireErrorContains(t, err, "offset metadata mismatch")
}

func commandPartitionSample(samples []metrics.Sample, name, kind, key string) float64 {
	for _, s := range samples {
		if s.Name == name && s.Labels["kind"] == kind && s.Labels["key"] == key {
			return s.Value
		}
	}
	return -1
}

func commandPartitionSignalSample(samples []metrics.Sample, name, kind, key, signal string) float64 {
	for _, s := range samples {
		if s.Name == name && s.Labels["kind"] == kind && s.Labels["key"] == key && s.Labels["signal"] == signal {
			return s.Value
		}
	}
	return -1
}

func commandPartitionAssignmentSample(samples []metrics.Sample, name, kind, key string) float64 {
	for _, s := range samples {
		if s.Name == name && s.Labels["kind"] == kind && s.Labels["key"] == key {
			return s.Value
		}
	}
	return -1
}

func commandPartitionAssignedToForMetrics(t *testing.T, ctx context.Context, assigner CommandPartitionAssigner, ownerID string) LogPartition {
	t.Helper()
	for i := 0; i < 200; i++ {
		partition := LogPartition{Kind: partitionBoard, Key: "metrics-" + string(rune('a'+(i%26))) + "-" + string(rune('a'+((i/26)%26)))}
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

func TestBrokerCommandLogRejectsInvalidPayload(t *testing.T) {
	ctx := context.Background()
	log := NewBrokerCommandLog(NewMemoryBrokerCommandLogClient())
	_, err := log.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Command:    proto.CmdCreateThread,
		Payload:    []byte(`{invalid-json`),
		EnqueuedAt: 1000,
	})
	requireErrorContains(t, err, "valid JSON")
}

func TestCoreCommandLogShadowCapturesSubmittedCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commandClient := NewMemoryBrokerCommandLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	c := newCoreTestCore(t, WithCommandLogShadow(commandLog))
	go c.Run(ctx)

	reply := c.ExecCmd(ctx, nil, proto.CommandName("test.unknown"), []byte(`{"hello":"world"}`), "cid-shadow")
	if reply.Err == nil {
		t.Fatalf("reply = %+v, want normal handler to reject unknown command", reply)
	}
	records, err := commandLog.FetchPartition(ctx, LogPartition{Kind: partitionGlobal, Key: partitionGlobal}, 0, 10)
	if err != nil {
		t.Fatalf("fetch shadow commands: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one shadow command", records)
	}
	if records[0].CID != "cid-shadow" || records[0].Command != proto.CommandName("test.unknown") || string(records[0].Payload) != `{"hello":"world"}` {
		t.Fatalf("record = %+v, want submitted command details", records[0])
	}
}
