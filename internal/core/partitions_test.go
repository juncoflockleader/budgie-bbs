package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestAppendEventStoresAndReplaysPartitionMetadata(t *testing.T) {
	c := newCoreTestCore(t)

	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	seq, err := appendEvent(tx, "evt_partition_test", proto.EvtThreadNew, []string{"board:general"}, &proto.ThreadNewPayload{
		ID:     "thr_partition",
		Board:  "general",
		Author: "alice",
		Title:  "Partitioned",
		TS:     1000,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("append event: %v", err)
	}
	seq2, err := appendEvent(tx, "evt_partition_test_2", proto.EvtThreadNew, []string{"board:general"}, &proto.ThreadNewPayload{
		ID:     "thr_partition_2",
		Board:  "general",
		Author: "alice",
		Title:  "Partitioned again",
		TS:     1001,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("append second event: %v", err)
	}
	otherSeq, err := appendEvent(tx, "evt_partition_other", proto.EvtThreadNew, []string{"board:life"}, &proto.ThreadNewPayload{
		ID:     "thr_partition_life",
		Board:  "life",
		Author: "alice",
		Title:  "Different partition",
		TS:     1002,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("append other partition event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var kind, key string
	var offset int64
	if err := qQueryRow(c.DB, `SELECT partition_kind, partition_key, partition_offset FROM events WHERE seq=?`, seq).Scan(&kind, &key, &offset); err != nil {
		t.Fatalf("query partition columns: %v", err)
	}
	if kind != partitionBoard || key != "general" || offset != 1 {
		t.Fatalf("partition row = (%q,%q,%d), want (%q,%q,1)", kind, key, offset, partitionBoard, "general")
	}
	if err := qQueryRow(c.DB, `SELECT partition_offset FROM events WHERE seq=?`, seq2).Scan(&offset); err != nil {
		t.Fatalf("query second partition offset: %v", err)
	}
	if offset != 2 {
		t.Fatalf("same partition second offset = %d, want 2", offset)
	}
	if err := qQueryRow(c.DB, `SELECT partition_offset FROM events WHERE seq=?`, otherSeq).Scan(&offset); err != nil {
		t.Fatalf("query other partition offset: %v", err)
	}
	if offset != 1 {
		t.Fatalf("other partition first offset = %d, want 1", offset)
	}

	replayed, err := c.Replay(0, nil, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed) != 3 {
		t.Fatalf("replay len = %d, want 3", len(replayed))
	}
	if replayed[0].PartitionKind != partitionBoard || replayed[0].PartitionKey != "general" || replayed[0].PartitionOffset != 1 {
		t.Fatalf("replayed partition = (%q,%q,%d)", replayed[0].PartitionKind, replayed[0].PartitionKey, replayed[0].PartitionOffset)
	}

	partitionEvents, err := c.ReplayPartition(partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay partition: %v", err)
	}
	if len(partitionEvents) != 2 || partitionEvents[0].Seq != seq || partitionEvents[1].Seq != seq2 {
		t.Fatalf("partition replay = %+v, want seqs %d,%d", partitionEvents, seq, seq2)
	}
	after, err := c.ReplayPartition(partitionBoard, "general", 1, 10)
	if err != nil {
		t.Fatalf("replay partition after first offset: %v", err)
	}
	if len(after) != 1 || after[0].Seq != seq2 {
		t.Fatalf("partition replay after first offset = %+v, want seq %d", after, seq2)
	}
	after, err = c.ReplayPartition(partitionBoard, "general", 2, 10)
	if err != nil {
		t.Fatalf("replay partition after second offset: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("partition replay after offset returned %d events, want 0", len(after))
	}
}

func TestReplayCursorUsesPartitionOffsetsWithoutScalarSeq(t *testing.T) {
	c := newCoreTestCore(t)

	appendThreadEvent := func(id, board, title string) {
		t.Helper()
		tx, err := c.DB.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := appendEvent(tx, "evt_"+id, proto.EvtThreadNew, []string{"board:" + board}, &proto.ThreadNewPayload{
			ID:     id,
			Board:  board,
			Author: "alice",
			Title:  title,
			TS:     1000,
		}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("append event %s: %v", id, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %s: %v", id, err)
		}
	}

	appendThreadEvent("thr_cursor_first", "general", "cursor first")
	cursor, err := c.HeadCursor()
	if err != nil {
		t.Fatalf("head cursor: %v", err)
	}
	if cursor.Seq == 0 || len(cursor.Partitions) == 0 {
		t.Fatalf("cursor = %+v, want scalar and partition heads", cursor)
	}

	appendThreadEvent("thr_cursor_life", "life", "other partition")
	appendThreadEvent("thr_cursor_second", "general", "cursor second")
	cursor.Seq = 0
	events, err := c.ReplayCursor(cursor, []string{"board:general"}, 10)
	if err != nil {
		t.Fatalf("replay cursor: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want only second general event", events)
	}
	payload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ThreadNewPayload", events[0].Payload)
	}
	if payload.Title != "cursor second" || events[0].PartitionOffset != 2 {
		t.Fatalf("event = %+v payload=%+v, want second general event at offset 2", events[0], payload)
	}
}

func TestCommandPartitionSpecsCoverRoutedCommands(t *testing.T) {
	commandValues := commandConstantValues(t)
	raw, err := os.ReadFile("handler/route.go")
	if err != nil {
		t.Fatalf("read route.go: %v", err)
	}
	re := regexp.MustCompile(`case proto\.(Cmd[A-Za-z0-9]+):`)
	matches := re.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		t.Fatal("found no routed commands")
	}

	for _, match := range matches {
		name := string(match[1])
		value, ok := commandValues[name]
		if !ok {
			t.Fatalf("routed command proto.%s has no constant value in proto/command.go", name)
		}
		cmd := proto.CommandName(value)
		if !logmodel.HasCommandPartitionSpec(cmd) {
			t.Fatalf("missing command partition spec for proto.%s (%q)", name, cmd)
		}
	}
}

func TestClassifyCommandPartitionUsesPayloadAndActorFallbacks(t *testing.T) {
	p, ok := classifyCommandPartition(nil, proto.CmdCreateThread, []byte(`{"board":"general","title":"hello"}`))
	if !ok {
		t.Fatal("createThread did not classify")
	}
	if p.Kind != partitionBoard || p.Key != "general" {
		t.Fatalf("createThread partition = %+v", p)
	}

	actor := &User{ID: "usr_alice"}
	p, ok = classifyCommandPartition(actor, proto.CmdSetPresence, []byte(`{"status":"active"}`))
	if !ok {
		t.Fatal("setPresence did not classify")
	}
	if p.Kind != partitionUser || p.Key != "usr_alice" {
		t.Fatalf("setPresence actor fallback partition = %+v", p)
	}

	p, ok = classifyCommandPartition(actor, proto.CmdSendChatLine, []byte(`{"text":"hi"}`))
	if !ok {
		t.Fatal("sendChatLine did not classify")
	}
	if p.Kind != partitionChat || p.Key != "lobby" {
		t.Fatalf("sendChatLine fallback partition = %+v", p)
	}
}

func TestHotThreadSplitRoutesAppendPostsToReplySubpartitions(t *testing.T) {
	var c Core
	c.SetHotThreadSplit("thr_hot", 4)
	actor := &User{ID: "usr_alice"}
	payload := []byte(`{"thread":"thr_hot","body":"hello"}`)

	p, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, payload)
	if !ok {
		t.Fatal("appendPost did not classify")
	}
	if p.Kind != partitionThread || !strings.HasPrefix(p.Key, "thr_hot#reply-") {
		t.Fatalf("split appendPost partition = %+v, want thread/thr_hot#reply-*", p)
	}
	again, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, payload)
	if !ok || again != p {
		t.Fatalf("split appendPost partition is not stable: first=%+v again=%+v ok=%v", p, again, ok)
	}
	c.SetHotThreadSplit("thr_hot", 1)
	base, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, payload)
	if !ok || base.Kind != partitionThread || base.Key != "thr_hot" {
		t.Fatalf("disabled split appendPost partition = %+v ok=%v, want thread/thr_hot", base, ok)
	}
}

func TestHotThreadSplitSamplesExposeConfiguredShardCounts(t *testing.T) {
	samples := hotThreadSplitSamples(map[string]int{
		"thr_hot":   4,
		"thr_other": 2,
		"thr_off":   1,
		"":          8,
	})

	if got := hotThreadSplitSample(samples, "thr_hot"); got != 4 {
		t.Fatalf("thr_hot split sample = %v, want 4", got)
	}
	if got := hotThreadSplitSample(samples, "thr_other"); got != 2 {
		t.Fatalf("thr_other split sample = %v, want 2", got)
	}
	if got := hotThreadSplitSample(samples, "thr_off"); got != -1 {
		t.Fatalf("disabled split sample = %v, want absent", got)
	}
}

func TestHotThreadSplitsPersistAndReload(t *testing.T) {
	dbPath := t.TempDir() + "/hot-split-persist.db"
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if err := c.PersistHotThreadSplit("thr_hot", 4); err != nil {
		t.Fatalf("persist hot split: %v", err)
	}
	if got := c.HotThreadSplits()["thr_hot"]; got != 4 {
		t.Fatalf("live split = %d, want 4", got)
	}
	_ = c.DB.Close()

	reopened, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen core: %v", err)
	}
	if got := reopened.HotThreadSplits()["thr_hot"]; got != 4 {
		t.Fatalf("reloaded split = %d, want 4", got)
	}
	if err := reopened.PersistHotThreadSplit("thr_hot", 0); err != nil {
		t.Fatalf("delete persisted hot split: %v", err)
	}
	_ = reopened.DB.Close()

	cleared, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen cleared core: %v", err)
	}
	defer cleared.DB.Close()
	if _, ok := cleared.HotThreadSplits()["thr_hot"]; ok {
		t.Fatalf("expected split to stay deleted, got %+v", cleared.HotThreadSplits())
	}
}

func TestHotThreadSplitOptionOverridesPersistedConfig(t *testing.T) {
	dbPath := t.TempDir() + "/hot-split-override.db"
	c, err := New(dbPath)
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if err := c.PersistHotThreadSplit("thr_hot", 4); err != nil {
		t.Fatalf("persist hot split: %v", err)
	}
	_ = c.DB.Close()

	override, err := New(dbPath, WithHotThreadSplits(map[string]int{"thr_hot": 6}))
	if err != nil {
		t.Fatalf("reopen core with override: %v", err)
	}
	defer override.DB.Close()
	if got := override.HotThreadSplits()["thr_hot"]; got != 6 {
		t.Fatalf("override split = %d, want 6", got)
	}
	if err := override.PersistHotThreadSplit("thr_hot", 8); err != nil {
		t.Fatalf("persist changed split under override: %v", err)
	}
	if got := override.HotThreadSplits()["thr_hot"]; got != 6 {
		t.Fatalf("local override after persisted change = %d, want 6", got)
	}
}

func TestHotThreadSplitBlockingLagRequiresAffectedPartitionsToDrain(t *testing.T) {
	ctx := context.Background()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))

	actor := &User{ID: "usr_alice"}
	baseReply := c.ExecCmd(ctx, actor, proto.CmdAppendPost, []byte(`{"thread":"thr_hot","body":"base queued"}`), "cid-base-queued")
	if baseReply.Err != nil {
		t.Fatalf("enqueue base append: %+v", baseReply.Err)
	}
	blocking, err := c.HotThreadSplitBlockingLag(ctx, "thr_hot", 4)
	if err != nil {
		t.Fatalf("blocking lag for split enable: %v", err)
	}
	basePartition := LogPartition{Kind: baseReply.Result.CommandPartitionKind, Key: baseReply.Result.CommandPartitionKey}
	if !hasBlockingCommandPartition(blocking, basePartition, 1) {
		t.Fatalf("expected base partition lag before split enable, got %+v", blocking)
	}
	if err := commandLog.CommitPartition(ctx, basePartition, baseReply.Result.CommandOffset); err != nil {
		t.Fatalf("commit base partition: %v", err)
	}

	c.SetHotThreadSplit("thr_hot", 4)
	splitReply := c.ExecCmd(ctx, actor, proto.CmdAppendPost, []byte(`{"thread":"thr_hot","body":"split queued"}`), "cid-split-queued")
	if splitReply.Err != nil {
		t.Fatalf("enqueue split append: %+v", splitReply.Err)
	}
	blocking, err = c.HotThreadSplitBlockingLag(ctx, "thr_hot", 0)
	if err != nil {
		t.Fatalf("blocking lag for split rollback: %v", err)
	}
	splitPartition := LogPartition{Kind: splitReply.Result.CommandPartitionKind, Key: splitReply.Result.CommandPartitionKey}
	if !hasBlockingCommandPartition(blocking, splitPartition, 1) {
		t.Fatalf("expected reply shard lag before rollback, got %+v", blocking)
	}
	if err := commandLog.CommitPartition(ctx, splitPartition, splitReply.Result.CommandOffset); err != nil {
		t.Fatalf("commit split partition: %v", err)
	}
	blocking, err = c.HotThreadSplitBlockingLag(ctx, "thr_hot", 0)
	if err != nil {
		t.Fatalf("blocking lag after drain: %v", err)
	}
	if len(blocking) != 0 {
		t.Fatalf("expected no blocking partitions after drain, got %+v", blocking)
	}
}

func TestHotThreadSplitDistributesReplyBacklogAndLagFalls(t *testing.T) {
	ctx := context.Background()
	actor := &User{ID: "usr_alice"}
	const threadID = "thr_hot"
	const replies = 8

	unsplitLog := NewMemoryCommandLog()
	var unsplitCore Core
	for i := 0; i < replies; i++ {
		partition := enqueueClassifiedAppendPostCommand(t, ctx, &unsplitCore, unsplitLog, actor, threadID, fmt.Sprintf("unsplit reply %d", i), i)
		if partition != (LogPartition{Kind: partitionThread, Key: threadID}) {
			t.Fatalf("unsplit reply partition = %+v, want base thread partition", partition)
		}
	}
	unsplitSamples, err := commandPartitionOffsetSamples(ctx, unsplitLog, 100)
	if err != nil {
		t.Fatalf("unsplit command partition samples: %v", err)
	}
	unsplitMax := sampleValue(unsplitSamples, "budgie_command_partition_lag_max")
	if unsplitMax != replies {
		t.Fatalf("unsplit max lag = %v, want %d", unsplitMax, replies)
	}
	if got := commandPartitionSample(unsplitSamples, "budgie_command_partition_lag", partitionThread, threadID); got != replies {
		t.Fatalf("unsplit base thread lag = %v, want %d", got, replies)
	}
	if got := commandPartitionSignalSample(unsplitSamples, "budgie_hot_partition_candidate", partitionThread, threadID, "command_lag"); got != replies {
		t.Fatalf("unsplit hot candidate lag = %v, want %d", got, replies)
	}

	splitLog := NewMemoryCommandLog()
	var splitCore Core
	splitCore.SetHotThreadSplit(threadID, 4)
	distinctBodies := splitReplyBodiesForDistinctPartitions(t, &splitCore, actor, threadID, 4)
	splitTails := map[LogPartition]int64{}
	for round := 0; round < 2; round++ {
		for _, body := range distinctBodies {
			partition := enqueueClassifiedAppendPostCommand(t, ctx, &splitCore, splitLog, actor, threadID, body, len(splitTails)+round)
			if partition.Kind != partitionThread || !strings.HasPrefix(partition.Key, threadID+"#reply-") {
				t.Fatalf("split reply partition = %+v, want reply subpartition", partition)
			}
			splitTails[partition]++
		}
	}
	if len(splitTails) != 4 {
		t.Fatalf("split replies landed on %d partitions: %+v, want 4", len(splitTails), splitTails)
	}

	splitSamples, err := commandPartitionOffsetSamples(ctx, splitLog, 100)
	if err != nil {
		t.Fatalf("split command partition samples: %v", err)
	}
	splitMax := sampleValue(splitSamples, "budgie_command_partition_lag_max")
	if splitMax >= unsplitMax {
		t.Fatalf("split max lag = %v, want below unsplit max lag %v", splitMax, unsplitMax)
	}
	if splitMax != 2 {
		t.Fatalf("split max lag = %v, want 2 replies per shard", splitMax)
	}
	if got := sampleValue(splitSamples, "budgie_command_partition_lag_total"); got != replies {
		t.Fatalf("split total lag = %v, want %d", got, replies)
	}
	if got := commandPartitionSample(splitSamples, "budgie_command_partition_lag", partitionThread, threadID); got != -1 {
		t.Fatalf("split base thread lag sample = %v, want absent", got)
	}
	for partition, tail := range splitTails {
		if got := commandPartitionSample(splitSamples, "budgie_command_partition_lag", partition.Kind, partition.Key); got != float64(tail) {
			t.Fatalf("split partition %s/%s lag = %v, want %d", partition.Kind, partition.Key, got, tail)
		}
		if got := commandPartitionSignalSample(splitSamples, "budgie_hot_partition_candidate", partition.Kind, partition.Key, "command_lag"); got != float64(tail) {
			t.Fatalf("split partition %s/%s candidate = %v, want %d", partition.Kind, partition.Key, got, tail)
		}
	}

	for partition, tail := range splitTails {
		if err := splitLog.CommitPartition(ctx, partition, tail); err != nil {
			t.Fatalf("commit split partition %s/%s: %v", partition.Kind, partition.Key, err)
		}
	}
	drainedSamples, err := commandPartitionOffsetSamples(ctx, splitLog, 100)
	if err != nil {
		t.Fatalf("drained command partition samples: %v", err)
	}
	if got := sampleValue(drainedSamples, "budgie_command_partition_lag_max"); got != 0 {
		t.Fatalf("drained max lag = %v, want 0", got)
	}
	if got := sampleValue(drainedSamples, "budgie_command_partition_lag_total"); got != 0 {
		t.Fatalf("drained total lag = %v, want 0", got)
	}
	for partition := range splitTails {
		if got := commandPartitionSignalSample(drainedSamples, "budgie_hot_partition_candidate", partition.Kind, partition.Key, "command_lag"); got != -1 {
			t.Fatalf("drained partition %s/%s candidate = %v, want absent", partition.Kind, partition.Key, got)
		}
	}
}

func enqueueClassifiedAppendPostCommand(t *testing.T, ctx context.Context, c *Core, commandLog CommandLog, actor *User, threadID, body string, sequence int) LogPartition {
	t.Helper()
	raw, err := json.Marshal(proto.AppendPostPayload{Thread: threadID, Body: body})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	partition, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, raw)
	if !ok {
		t.Fatal("appendPost did not classify")
	}
	logPartition := LogPartition{Kind: partition.Kind, Key: partition.Key}.Normalize()
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  logPartition,
		ActorID:    actor.ID,
		Command:    proto.CmdAppendPost,
		Payload:    raw,
		EnqueuedAt: int64(1000 + sequence),
	}); err != nil {
		t.Fatalf("produce appendPost command: %v", err)
	}
	return logPartition
}

func hasBlockingCommandPartition(offsets []CommandPartitionOffset, partition LogPartition, lag int64) bool {
	partition = partition.Normalize()
	for _, offset := range offsets {
		if offset.Partition.Normalize() == partition && offset.TailOffset-offset.CommittedOffset == lag {
			return true
		}
	}
	return false
}

func TestHotThreadSplitAuthoritativeCommandUsesSplitPartition(t *testing.T) {
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	c.SetHotThreadSplit("thr_hot", 4)

	reply := c.ExecCmd(context.Background(), &User{ID: "usr_alice"}, proto.CmdAppendPost, []byte(`{"thread":"thr_hot","body":"hello"}`), "cid-hot-reply")
	if reply.Err != nil {
		t.Fatalf("exec split append: %+v", reply.Err)
	}
	if reply.Result == nil || reply.Result.CommandPartitionKind != partitionThread || !strings.HasPrefix(reply.Result.CommandPartitionKey, "thr_hot#reply-") {
		t.Fatalf("pending reply partition = %+v, want split thread partition", reply.Result)
	}
}

func TestHotThreadSplitCommandLogValidationAcceptsThreadPartitionFamily(t *testing.T) {
	var c Core
	c.SetHotThreadSplit("thr_hot", 4)
	actor := &User{ID: "usr_alice"}
	payload := []byte(`{"thread":"thr_hot","body":"hello"}`)
	p, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, payload)
	if !ok {
		t.Fatal("appendPost did not classify")
	}
	actual := logPartitionFromEventPartition(p)
	record := CommandLogRecord{
		Partition: actual,
		ActorID:   actor.ID,
		Command:   proto.CmdAppendPost,
		Payload:   payload,
		CID:       "cid-hot-reply",
	}
	if errDetail := c.validateCommandLogRecordPartition(actor, record, actual); errDetail != nil {
		t.Fatalf("split partition validation rejected matching record: %+v", errDetail)
	}
	base := LogPartition{Kind: partitionThread, Key: "thr_hot"}
	if errDetail := c.validateCommandLogRecordPartition(actor, record, base); errDetail != nil {
		t.Fatalf("split partition validation rejected pre-split base partition: %+v", errDetail)
	}
	oldShard := LogPartition{Kind: partitionThread, Key: "thr_hot#reply-99"}
	if errDetail := c.validateCommandLogRecordPartition(actor, record, oldShard); errDetail != nil {
		t.Fatalf("split partition validation rejected old reply shard partition: %+v", errDetail)
	}
	wrongThread := LogPartition{Kind: partitionThread, Key: "thr_other#reply-0"}
	if errDetail := c.validateCommandLogRecordPartition(actor, record, wrongThread); errDetail == nil {
		t.Fatal("split partition validation accepted wrong thread reply partition")
	}
}

func TestHotThreadSplitRepliesRemainReadableInCreatedSeqOrder(t *testing.T) {
	shadow := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithCommandLogShadow(shadow))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	threadAck := execPartitionTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Hot topic",
		Body:  "root",
	}, "cid-create-hot")
	c.SetHotThreadSplit(threadAck.ID, 4)
	execPartitionTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: threadAck.ID, Body: "reply one"}, "cid-reply-one")
	execPartitionTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{Thread: threadAck.ID, Body: "reply two"}, "cid-reply-two")

	offsets, err := shadow.ListCommandPartitionOffsets(context.Background(), 100)
	if err != nil {
		t.Fatalf("list shadow partitions: %v", err)
	}
	foundSplit := false
	for _, offset := range offsets {
		if offset.Partition.Kind == partitionThread && strings.HasPrefix(offset.Partition.Key, threadAck.ID+"#reply-") {
			foundSplit = true
			break
		}
	}
	if !foundSplit {
		t.Fatalf("shadow command log offsets = %+v, want split reply partition for %s", offsets, threadAck.ID)
	}

	posts, err := c.ListPosts(threadAck.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("posts len = %d, want root plus two replies: %+v", len(posts), posts)
	}
	if posts[0].Body != "root" || posts[1].Body != "reply one" || posts[2].Body != "reply two" {
		t.Fatalf("posts order = [%q, %q, %q], want created_seq order", posts[0].Body, posts[1].Body, posts[2].Body)
	}
}

func TestHotThreadSplitAuthoritativeRepliesMergeIntoStableReadPresentation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	execPartitionTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Authoritative hot merge",
		Body:  "root",
	}, "cid-authoritative-hot-create")
	drainPartitionTestCommandLog(t, ctx, c, commandLog)
	thread := findPartitionTestThreadByTitle(t, c, "general", "Authoritative hot merge")
	if err := c.PersistHotThreadSplit(thread.ID, 4); err != nil {
		t.Fatalf("persist hot split: %v", err)
	}

	replyBodies := splitReplyBodiesForDistinctPartitions(t, c, alice, thread.ID, 3)
	seenPartitions := map[string]bool{}
	for i, body := range replyBodies {
		ack := execPartitionTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
			Thread: thread.ID,
			Body:   body,
		}, fmt.Sprintf("cid-authoritative-hot-reply-%d", i))
		if ack.Status != proto.AckStatusPending || !strings.HasPrefix(ack.CommandPartitionKey, thread.ID+"#reply-") {
			t.Fatalf("reply ack = %+v, want pending split partition", ack)
		}
		seenPartitions[ack.CommandPartitionKey] = true
	}
	if len(seenPartitions) < 2 {
		t.Fatalf("reply bodies landed on too few split partitions: %+v", seenPartitions)
	}
	drainPartitionTestCommandLog(t, ctx, c, commandLog)

	firstRead, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts first read: %v", err)
	}
	secondRead, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts second read: %v", err)
	}
	assertStablePostPresentation(t, firstRead, secondRead, append([]string{"root"}, replyBodies...))
}

func TestHotThreadSplitModerationRemainsCausalForTargetPost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	commandLog := NewMemoryCommandLog()
	c := newCoreTestCore(t, WithAuthoritativeCommandLog(commandLog))
	go c.Run(ctx)

	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	execPartitionTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Hot moderation",
		Body:  "root",
	}, "cid-hot-moderation-create")
	drainPartitionTestCommandLog(t, ctx, c, commandLog)
	thread := findPartitionTestThreadByTitle(t, c, "general", "Hot moderation")
	if err := c.PersistHotThreadSplit(thread.ID, 4); err != nil {
		t.Fatalf("persist hot split: %v", err)
	}

	execPartitionTestCommand(t, c, alice, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: thread.ID,
		Body:   "reply to moderate",
	}, "cid-hot-moderation-reply")
	drainPartitionTestCommandLog(t, ctx, c, commandLog)
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after reply drain: %v", err)
	}
	if len(posts) != 2 || posts[1].Body != "reply to moderate" {
		t.Fatalf("posts after split reply = %+v, want root plus target reply", posts)
	}
	target := posts[1]

	redact := execPartitionTestCommand(t, c, admin, proto.CmdRedactPost, proto.RedactPostPayload{
		Post:   target.ID,
		Reason: "split moderation",
	}, "cid-hot-moderation-redact")
	if redact.Status != proto.AckStatusPending || redact.CommandPartitionKind != partitionPost || redact.CommandPartitionKey != target.ID {
		t.Fatalf("redact ack = %+v, want pending post partition for target", redact)
	}
	drainPartitionTestCommandLog(t, ctx, c, commandLog)
	redacted, err := c.GetPost(target.ID)
	if err != nil {
		t.Fatalf("get redacted post: %v", err)
	}
	if redacted == nil || !redacted.Redacted || redacted.UpdatedSeq <= redacted.CreatedSeq {
		t.Fatalf("redacted post = %+v, want redacted with later update seq", redacted)
	}

	restore := execPartitionTestCommand(t, c, admin, proto.CmdRestorePost, proto.RestorePostPayload{Post: target.ID}, "cid-hot-moderation-restore")
	if restore.Status != proto.AckStatusPending || restore.CommandPartitionKind != partitionPost || restore.CommandPartitionKey != target.ID {
		t.Fatalf("restore ack = %+v, want pending post partition for target", restore)
	}
	drainPartitionTestCommandLog(t, ctx, c, commandLog)
	restored, err := c.GetPost(target.ID)
	if err != nil {
		t.Fatalf("get restored post: %v", err)
	}
	if restored == nil || restored.Redacted || restored.UpdatedSeq <= redacted.UpdatedSeq {
		t.Fatalf("restored post = %+v after redacted %+v, want restored with later update seq", restored, redacted)
	}
}

func execPartitionTestCommand(t *testing.T, c *Core, actor *User, name proto.CommandName, payload any, cid string) *proto.AckResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	reply := c.ExecCmd(context.Background(), actor, name, raw, cid)
	if reply.Err != nil {
		t.Fatalf("exec %s: %+v", name, reply.Err)
	}
	if reply.Result == nil {
		t.Fatalf("exec %s returned nil result", name)
	}
	return reply.Result
}

func drainPartitionTestCommandLog(t *testing.T, ctx context.Context, c *Core, commandLog CommandLog) []CommandLogWorkerResult {
	t.Helper()
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:            commandLog,
		Executor:       c,
		BatchSize:      100,
		PartitionLimit: 100,
	})
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain command log: %v", err)
	}
	for _, result := range results {
		if result.AssignmentLost || result.ClaimLost || result.CommitFailures > 0 || result.TerminalFailures > 0 || result.RetryableFailure != nil {
			t.Fatalf("unexpected command log drain result: %+v", result)
		}
	}
	if lister, ok := commandLog.(CommandPartitionOffsetLister); ok {
		offsets, err := lister.ListCommandPartitionOffsets(ctx, 0)
		if err != nil {
			t.Fatalf("list command partition offsets: %v", err)
		}
		for _, offset := range offsets {
			if offset.TailOffset != offset.CommittedOffset {
				t.Fatalf("command partition %s/%s lagged after drain: tail=%d committed=%d",
					offset.Partition.Kind, offset.Partition.Key, offset.TailOffset, offset.CommittedOffset)
			}
		}
	}
	return results
}

func findPartitionTestThreadByTitle(t *testing.T, c *Core, boardID, title string) Thread {
	t.Helper()
	threads, err := c.ListThreads(boardID, 100, 0)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	for _, thread := range threads {
		if thread.Title == title {
			return thread
		}
	}
	t.Fatalf("thread %q not found in %+v", title, threads)
	return Thread{}
}

func splitReplyBodiesForDistinctPartitions(t *testing.T, c *Core, actor *User, threadID string, count int) []string {
	t.Helper()
	bodies := make([]string, 0, count)
	seen := map[string]bool{}
	for i := 0; i < 200 && len(bodies) < count; i++ {
		body := fmt.Sprintf("split reply %03d", i)
		raw, err := json.Marshal(proto.AppendPostPayload{Thread: threadID, Body: body})
		if err != nil {
			t.Fatalf("marshal split reply candidate: %v", err)
		}
		partition, ok := c.classifyCommandPartition(actor, proto.CmdAppendPost, raw)
		if !ok || partition.Kind != partitionThread || !strings.HasPrefix(partition.Key, threadID+"#reply-") {
			t.Fatalf("candidate %q classified to %+v ok=%v, want split thread partition", body, partition, ok)
		}
		if seen[partition.Key] {
			continue
		}
		seen[partition.Key] = true
		bodies = append(bodies, body)
	}
	if len(bodies) < count {
		t.Fatalf("found %d split reply partitions for %s, want %d", len(bodies), threadID, count)
	}
	return bodies
}

func assertStablePostPresentation(t *testing.T, first, second []Post, expectedBodies []string) {
	t.Helper()
	if len(first) != len(expectedBodies) {
		t.Fatalf("first read post len = %d, want %d: %+v", len(first), len(expectedBodies), first)
	}
	if len(second) != len(first) {
		t.Fatalf("second read post len = %d, want %d: %+v", len(second), len(first), second)
	}
	seenBodies := map[string]int{}
	var lastSeq int64
	for i, post := range first {
		if i > 0 && post.CreatedSeq <= lastSeq {
			t.Fatalf("post %d created_seq = %d after %d, want strict read order: %+v", i, post.CreatedSeq, lastSeq, first)
		}
		lastSeq = post.CreatedSeq
		if second[i].ID != post.ID || second[i].CreatedSeq != post.CreatedSeq {
			t.Fatalf("read presentation changed at %d: first=%+v second=%+v", i, post, second[i])
		}
		seenBodies[post.Body]++
	}
	if first[0].Body != "root" {
		t.Fatalf("first read root body = %q, want root first in created_seq presentation", first[0].Body)
	}
	for _, body := range expectedBodies {
		if seenBodies[body] != 1 {
			t.Fatalf("body %q count = %d, want exactly one in posts %+v", body, seenBodies[body], first)
		}
	}
}

func TestPostgresPartitionAdvisoryLockKeyIsDeterministic(t *testing.T) {
	a := commandexec.PartitionAdvisoryLockKey(commandexec.Partition{Kind: partitionBoard, Key: "general"})
	b := commandexec.PartitionAdvisoryLockKey(commandexec.Partition{Kind: partitionBoard, Key: "general"})
	if a == 0 {
		t.Fatal("lock key should not be zero")
	}
	if a == pgScalarSeqAppendLockKey {
		t.Fatalf("partition lock key collided with scalar append gate key: %d", a)
	}
	if a != b {
		t.Fatalf("lock key changed from %d to %d", a, b)
	}
	c := commandexec.PartitionAdvisoryLockKey(commandexec.Partition{Kind: partitionBoard, Key: "life"})
	if c == a {
		t.Fatalf("different partitions produced same lock key: %d", a)
	}
	fallback := commandexec.PartitionAdvisoryLockKey(commandexec.Partition{})
	global := commandexec.PartitionAdvisoryLockKey(commandexec.Partition{Kind: partitionGlobal, Key: partitionGlobal})
	if fallback != global {
		t.Fatalf("empty partition key = %d, want global %d", fallback, global)
	}
}

func TestEventPartitionOffsetMetrics(t *testing.T) {
	c := newCoreTestCore(t)

	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, []string{"board:general"}, &proto.ThreadNewPayload{
			ID:     newID("thr_"),
			Board:  "general",
			Author: "alice",
			Title:  "Metrics",
			TS:     int64(1000 + i),
		}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("append general event: %v", err)
		}
	}
	if _, err := appendEvent(tx, newID("evt_"), proto.EvtThreadNew, []string{"board:life"}, &proto.ThreadNewPayload{
		ID:     newID("thr_"),
		Board:  "life",
		Author: "alice",
		Title:  "Metrics",
		TS:     1002,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("append life event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	samples, err := eventPartitionOffsetSamples(c.DB, 100)
	if err != nil {
		t.Fatalf("partition offset samples: %v", err)
	}
	if got := partitionOffsetSample(samples, partitionBoard, "general"); got != 2 {
		t.Fatalf("general partition offset metric = %v, want 2", got)
	}
	if got := partitionOffsetSample(samples, partitionBoard, "life"); got != 1 {
		t.Fatalf("life partition offset metric = %v, want 1", got)
	}
	if got := sampleValue(samples, "budgie_event_partition_count"); got != 2 {
		t.Fatalf("partition count metric = %v, want 2", got)
	}
	if got := sampleValue(samples, "budgie_event_partition_offset_skew"); got != 1 {
		t.Fatalf("partition skew metric = %v, want 1", got)
	}
}

func partitionOffsetSample(samples []metrics.Sample, kind, key string) float64 {
	for _, s := range samples {
		if s.Name == "budgie_event_partition_offset" && s.Labels["kind"] == kind && s.Labels["key"] == key {
			return s.Value
		}
	}
	return -1
}

func sampleValue(samples []metrics.Sample, name string) float64 {
	for _, s := range samples {
		if s.Name == name {
			return s.Value
		}
	}
	return -1
}

func hotThreadSplitSample(samples []metrics.Sample, threadID string) float64 {
	for _, s := range samples {
		if s.Name == "budgie_hot_thread_split_shards" && s.Labels["thread_id"] == threadID {
			return s.Value
		}
	}
	return -1
}

func commandConstantValues(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../proto/command.go")
	if err != nil {
		t.Fatalf("read command.go: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*(Cmd[A-Za-z0-9]+)\s+(?:CommandName\s+)?=\s+"([^"]+)"`)
	matches := re.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		t.Fatal("found no command constants")
	}
	out := make(map[string]string, len(matches))
	for _, match := range matches {
		out[string(match[1])] = string(match[2])
	}
	return out
}
