package natsconn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	nats "github.com/nats-io/nats.go"
)

func TestJetStreamCommandPartitionAssignerCreatesAndUpdatesMembership(t *testing.T) {
	ctx := context.Background()
	kv := newFakeCommandAssignmentKV()
	assigner := newJetStreamCommandPartitionAssignerWithKV(kv, "writers")
	generation, err := assigner.SetMembers(ctx, []string{"writer-b", "writer-a", "writer-a", ""})
	if err != nil {
		t.Fatalf("set members: %v", err)
	}
	if generation != 1 {
		t.Fatalf("generation = %d, want 1", generation)
	}
	members, gotGeneration, err := assigner.Members(ctx)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if gotGeneration != 1 || !reflect.DeepEqual(members, []string{"writer-a", "writer-b"}) {
		t.Fatalf("members = %v generation %d, want sorted unique members generation 1", members, gotGeneration)
	}

	sameGeneration, err := assigner.SetMembers(ctx, []string{"writer-a", "writer-b"})
	if err != nil {
		t.Fatalf("set same members: %v", err)
	}
	if sameGeneration != 1 {
		t.Fatalf("same generation = %d, want unchanged generation 1", sameGeneration)
	}
	updatedGeneration, err := assigner.SetMembers(ctx, []string{"writer-c"})
	if err != nil {
		t.Fatalf("set updated members: %v", err)
	}
	if updatedGeneration != 2 {
		t.Fatalf("updated generation = %d, want 2", updatedGeneration)
	}
	members, gotGeneration, err = assigner.Members(ctx)
	if err != nil {
		t.Fatalf("members after update: %v", err)
	}
	if gotGeneration != 2 || !reflect.DeepEqual(members, []string{"writer-c"}) {
		t.Fatalf("members = %v generation %d, want writer-c generation 2", members, gotGeneration)
	}
}

func TestJetStreamCommandPartitionAssignerUsesBrokerRecordForOwnership(t *testing.T) {
	ctx := context.Background()
	kv := newFakeCommandAssignmentKV()
	assigner := newJetStreamCommandPartitionAssignerWithKV(kv, "writers")
	if _, err := assigner.SetMembers(ctx, []string{"writer-a", "writer-b"}); err != nil {
		t.Fatalf("set members: %v", err)
	}
	partition := natsAssignmentPartitionAssignedTo(t, ctx, assigner, "writer-a")
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign writer-a: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-a" || assignment.Generation != 1 {
		t.Fatalf("assignment = %+v assigned=%v, want writer-a generation 1", assignment, assigned)
	}

	if _, err := assigner.SetMembers(ctx, []string{"writer-b"}); err != nil {
		t.Fatalf("rebalance members: %v", err)
	}
	rebalanced, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign after rebalance: %v", err)
	}
	if assigned || rebalanced.OwnerID != "writer-b" || rebalanced.Generation != 2 {
		t.Fatalf("rebalanced assignment = %+v assigned=%v, want writer-b generation 2 not assigned to writer-a", rebalanced, assigned)
	}
}

func TestJetStreamCommandPartitionAssignerSupportsPartitionOverrides(t *testing.T) {
	ctx := context.Background()
	kv := newFakeCommandAssignmentKV()
	assigner := newJetStreamCommandPartitionAssignerWithKV(kv, "writers")
	if _, err := assigner.SetMembers(ctx, []string{"writer-a", "writer-b"}); err != nil {
		t.Fatalf("set members: %v", err)
	}
	partition := core.LogPartition{Kind: "thread", Key: "thr_hot#reply-0"}
	if generation, err := assigner.SetOverrides(ctx, map[core.LogPartition]string{partition: "writer-b"}); err != nil || generation != 2 {
		t.Fatalf("set override generation = %d, %v; want 2, nil", generation, err)
	}
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-b", partition)
	if err != nil {
		t.Fatalf("assign override owner: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-b" || assignment.Generation != 2 {
		t.Fatalf("override assignment = %+v assigned=%v, want writer-b generation 2", assignment, assigned)
	}
	if _, err := assigner.SetMembers(ctx, []string{"writer-a"}); err != nil {
		t.Fatalf("remove override owner: %v", err)
	}
	rebalanced, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", partition)
	if err != nil {
		t.Fatalf("assign after removing override owner: %v", err)
	}
	if !assigned || rebalanced.OwnerID != "writer-a" || rebalanced.Generation != 3 {
		t.Fatalf("assignment after removing override owner = %+v assigned=%v, want writer-a generation 3", rebalanced, assigned)
	}
}

func TestJetStreamCommandPartitionAssignerRejectsInvalidPartitionOverrides(t *testing.T) {
	ctx := context.Background()
	kv := newFakeCommandAssignmentKV()
	assigner := newJetStreamCommandPartitionAssignerWithKV(kv, "writers")
	if _, err := assigner.SetMembers(ctx, []string{"writer-a"}); err != nil {
		t.Fatalf("set members: %v", err)
	}
	partition := core.LogPartition{Kind: "board", Key: "general"}

	if _, err := assigner.SetOverrides(ctx, map[core.LogPartition]string{partition: "writer-b"}); err == nil || !strings.Contains(err.Error(), `override owner "writer-b" is not a member`) {
		t.Fatalf("set unknown-owner override err = %v, want owner membership error", err)
	}
	if _, err := assigner.SetOverrides(ctx, map[core.LogPartition]string{partition: ""}); err == nil || !strings.Contains(err.Error(), "missing override owner for board/general") {
		t.Fatalf("set empty-owner override err = %v, want missing owner error", err)
	}
	if _, generation, err := assigner.Members(ctx); err != nil || generation != 1 {
		t.Fatalf("members generation after rejected overrides = %d, %v; want 1, nil", generation, err)
	}
}

func TestJetStreamCommandPartitionAssignerRequiresInitializedGroup(t *testing.T) {
	ctx := context.Background()
	assigner := newJetStreamCommandPartitionAssignerWithKV(newFakeCommandAssignmentKV(), "writers")
	_, _, err := assigner.AssignCommandPartition(ctx, "writer-a", core.LogPartition{Kind: "board", Key: "general"})
	if !errors.Is(err, nats.ErrKeyNotFound) {
		t.Fatalf("assign err = %v, want key not found", err)
	}
}

func TestJetStreamCommandPartitionAssignerRebalanceReplayDoesNotDuplicateSQLCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := core.New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	go c.Run(ctx)

	commandLog := core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient())
	partition := core.LogPartition{Kind: "board", Key: "general"}
	payload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "NATS KV rebalance replay",
		Body:  "created once",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, core.CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-nats-kv-rebalance",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: 1000,
	})
	if err != nil {
		t.Fatalf("produce command: %v", err)
	}

	kv := newFakeCommandAssignmentKV()
	assigner := newJetStreamCommandPartitionAssignerWithKV(kv, "writers")
	if generation, err := assigner.SetMembers(ctx, []string{"writer-a"}); err != nil || generation != 1 {
		t.Fatalf("initial assignment generation = %d, %v; want 1, nil", generation, err)
	}
	firstExecution := make(chan core.Reply, 1)
	releaseExecution := make(chan struct{})
	workerA := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:                  commandLog,
		Assignments:          assigner,
		OwnerID:              "writer-a",
		BatchSize:            10,
		ClaimRefreshInterval: 10 * time.Millisecond,
		Executor: core.CommandLogExecutorFunc(func(ctx context.Context, record core.CommandLogRecord) core.Reply {
			reply := c.ExecuteCommandLogRecord(ctx, record)
			firstExecution <- reply
			select {
			case <-releaseExecution:
			case <-ctx.Done():
			}
			return reply
		}),
	})
	workerADone := make(chan commandAssignmentWorkerDrainResult, 1)
	go func() {
		results, err := workerA.DrainOnce(ctx)
		workerADone <- commandAssignmentWorkerDrainResult{results: results, err: err}
	}()
	firstReply := waitForCommandAssignmentReply(t, firstExecution)
	if firstReply.Err != nil {
		t.Fatalf("first execution failed: %+v", firstReply.Err)
	}
	headAfterFirst, err := c.Head()
	if err != nil {
		t.Fatalf("head after first execution: %v", err)
	}
	if headAfterFirst == 0 {
		t.Fatalf("head after first execution = 0, want SQL command committed before offset commit")
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != 0 {
		t.Fatalf("committed offset while worker-a execution is in flight = %d, %v; want 0, nil", got, err)
	}

	observedRebalance := kv.NotifyGeneration(2)
	if generation, err := assigner.SetMembers(ctx, []string{"writer-b"}); err != nil || generation != 2 {
		t.Fatalf("rebalance assignment generation = %d, %v; want 2, nil", generation, err)
	}
	waitForCommandAssignmentGeneration(t, observedRebalance)
	close(releaseExecution)
	drainA := waitForCommandAssignmentWorkerDrain(t, workerADone)
	if drainA.err != nil {
		t.Fatalf("worker-a drain: %v", drainA.err)
	}
	if len(drainA.results) != 1 {
		t.Fatalf("worker-a results = %+v, want one partition result", drainA.results)
	}
	resultA := drainA.results[0]
	if resultA.Assigned || !resultA.AssignmentLost || resultA.AssignmentOwnerID != "writer-b" || resultA.AssignmentGeneration != 2 || resultA.Processed != 0 || resultA.LastOffset != 0 {
		t.Fatalf("worker-a result = %+v, want assignment loss without command offset commit", resultA)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != 0 {
		t.Fatalf("committed offset after worker-a assignment loss = %d, %v; want 0, nil", got, err)
	}

	workerB := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
		Log:         commandLog,
		Assignments: assigner,
		OwnerID:     "writer-b",
		BatchSize:   10,
		Executor:    c,
	})
	resultsB, err := workerB.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("worker-b drain: %v", err)
	}
	if len(resultsB) != 1 {
		t.Fatalf("worker-b results = %+v, want one partition result", resultsB)
	}
	resultB := resultsB[0]
	if !resultB.Assigned || resultB.AssignmentLost || resultB.AssignmentOwnerID != "writer-b" || resultB.AssignmentGeneration != 2 || resultB.LastOffset != record.Offset || resultB.Processed != 1 {
		t.Fatalf("worker-b result = %+v, want replayed command offset committed by new owner", resultB)
	}
	headAfterReplay, err := c.Head()
	if err != nil {
		t.Fatalf("head after replay: %v", err)
	}
	if headAfterReplay != headAfterFirst {
		t.Fatalf("head after replay = %d, want unchanged %d", headAfterReplay, headAfterFirst)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("committed offset after worker-b replay = %d, %v; want %d, nil", got, err, record.Offset)
	}
	assertSingleThreadWithTitle(t, c, "general", "NATS KV rebalance replay")
}

func TestCommandAssignmentRecordCodecNormalizesAndValidates(t *testing.T) {
	raw, err := encodeCommandAssignmentRecord(commandAssignmentRecord{
		Group:      "writers",
		Members:    []string{"writer-b", "writer-a", "writer-a"},
		Generation: 3,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	record, err := decodeCommandAssignmentRecord(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if record.Version != jetStreamCommandAssignmentRecordVersion || record.Group != "writers" || record.Generation != 3 || !reflect.DeepEqual(record.Members, []string{"writer-a", "writer-b"}) {
		t.Fatalf("record = %+v, want normalized assignment record", record)
	}
	if _, err := encodeCommandAssignmentRecord(commandAssignmentRecord{Group: "writers", Generation: 1}); err == nil || !strings.Contains(err.Error(), "missing members") {
		t.Fatalf("missing members err = %v, want missing members error", err)
	}
}

func TestCommandAssignmentRecordDecodeRejectsMalformedOverrides(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name:    "invalid partition",
			raw:     `{"v":1,"group":"writers","members":["writer-a"],"overrides":{"badpartition":"writer-a"},"generation":1}`,
			wantErr: `invalid override partition "badpartition"`,
		},
		{
			name:    "missing owner",
			raw:     `{"v":1,"group":"writers","members":["writer-a"],"overrides":{"board/general":" "},"generation":1}`,
			wantErr: `missing override owner for "board/general"`,
		},
		{
			name:    "unknown owner",
			raw:     `{"v":1,"group":"writers","members":["writer-a"],"overrides":{"board/general":"writer-b"},"generation":1}`,
			wantErr: `override owner "writer-b" is not a member`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeCommandAssignmentRecord([]byte(tt.raw))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("decodeCommandAssignmentRecord err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func natsAssignmentPartitionAssignedTo(t *testing.T, ctx context.Context, assigner *JetStreamCommandPartitionAssigner, ownerID string) core.LogPartition {
	t.Helper()
	for i := 0; i < 200; i++ {
		partition := core.LogPartition{Kind: "board", Key: "board-" + string(rune('a'+(i%26))) + "-" + string(rune('a'+((i/26)%26)))}
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			t.Fatalf("assign partition %+v: %v", partition, err)
		}
		if assigned && assignment.OwnerID == ownerID {
			return partition.Normalize()
		}
	}
	t.Fatalf("could not find partition assigned to %s", ownerID)
	return core.LogPartition{}
}

type commandAssignmentWorkerDrainResult struct {
	results []core.CommandLogWorkerResult
	err     error
}

func waitForCommandAssignmentWorkerDrain(t *testing.T, ch <-chan commandAssignmentWorkerDrainResult) commandAssignmentWorkerDrainResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command assignment worker drain")
		return commandAssignmentWorkerDrainResult{}
	}
}

func waitForCommandAssignmentReply(t *testing.T, ch <-chan core.Reply) core.Reply {
	t.Helper()
	select {
	case reply := <-ch:
		return reply
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for command execution")
		return core.Reply{}
	}
}

func waitForCommandAssignmentGeneration(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for worker to observe assignment generation")
	}
}

func assertSingleThreadWithTitle(t *testing.T, c *core.Core, boardID, title string) {
	t.Helper()
	threads, err := c.ListThreads(boardID, 10, 0)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	count := 0
	for _, thread := range threads {
		if thread.Title == title {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thread count for %q = %d, want 1", title, count)
	}
}

type fakeCommandAssignmentKV struct {
	mu               sync.Mutex
	revision         uint64
	values           map[string]fakeCommandAssignmentKVEntry
	notifyGeneration int64
	notify           chan struct{}
	notified         bool
}

func newFakeCommandAssignmentKV() *fakeCommandAssignmentKV {
	return &fakeCommandAssignmentKV{values: map[string]fakeCommandAssignmentKVEntry{}}
}

func (kv *fakeCommandAssignmentKV) Get(key string) (commandAssignmentKVEntry, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	entry, ok := kv.values[key]
	if !ok {
		return nil, nats.ErrKeyNotFound
	}
	kv.maybeNotifyGenerationLocked(entry.value)
	return entry.clone(), nil
}

func (kv *fakeCommandAssignmentKV) Create(key string, value []byte) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if _, ok := kv.values[key]; ok {
		return 0, nats.ErrKeyExists
	}
	kv.revision++
	kv.values[key] = fakeCommandAssignmentKVEntry{
		value:    append([]byte(nil), value...),
		revision: kv.revision,
	}
	return kv.revision, nil
}

func (kv *fakeCommandAssignmentKV) Update(key string, value []byte, revision uint64) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	entry, ok := kv.values[key]
	if !ok {
		if revision != 0 {
			return 0, nats.ErrKeyNotFound
		}
	} else if entry.revision != revision {
		return 0, nats.ErrKeyExists
	}
	kv.revision++
	kv.values[key] = fakeCommandAssignmentKVEntry{
		value:    append([]byte(nil), value...),
		revision: kv.revision,
	}
	return kv.revision, nil
}

func (kv *fakeCommandAssignmentKV) NotifyGeneration(generation int64) <-chan struct{} {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	ch := make(chan struct{})
	kv.notifyGeneration = generation
	kv.notify = ch
	kv.notified = false
	for _, entry := range kv.values {
		kv.maybeNotifyGenerationLocked(entry.value)
	}
	return ch
}

func (kv *fakeCommandAssignmentKV) maybeNotifyGenerationLocked(value []byte) {
	if kv.notify == nil || kv.notified {
		return
	}
	record, err := decodeCommandAssignmentRecord(value)
	if err != nil || record.Generation != kv.notifyGeneration {
		return
	}
	close(kv.notify)
	kv.notified = true
}

type fakeCommandAssignmentKVEntry struct {
	value    []byte
	revision uint64
}

func (e fakeCommandAssignmentKVEntry) Value() []byte {
	return append([]byte(nil), e.value...)
}

func (e fakeCommandAssignmentKVEntry) Revision() uint64 {
	return e.revision
}

func (e fakeCommandAssignmentKVEntry) clone() fakeCommandAssignmentKVEntry {
	return fakeCommandAssignmentKVEntry{
		value:    append([]byte(nil), e.value...),
		revision: e.revision,
	}
}
