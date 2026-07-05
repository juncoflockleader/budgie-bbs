package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestPartitionLaneIndexIsDeterministic(t *testing.T) {
	partition := CommandPartition{Kind: "board", Key: "general"}
	first := commandexec.PartitionLaneIndex(partition, 16)
	for i := 0; i < 20; i++ {
		if got := commandexec.PartitionLaneIndex(partition, 16); got != first {
			t.Fatalf("lane changed from %d to %d", first, got)
		}
	}
}

func TestPartitionLaneIndexFallsBackToLaneZero(t *testing.T) {
	partition := CommandPartition{Kind: "board", Key: "general"}
	if got := commandexec.PartitionLaneIndex(partition, 0); got != 0 {
		t.Fatalf("lane for zero lanes = %d, want 0", got)
	}
	if got := commandexec.PartitionLaneIndex(partition, 1); got != 0 {
		t.Fatalf("lane for one lane = %d, want 0", got)
	}
}

func TestExecutePartitionPassesPartitionToLock(t *testing.T) {
	SetRuntime(Runtime{
		CheckProcessed: func(_ *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
			return "", false, false
		},
	})
	h := New(nil, nil)
	want := CommandPartition{Kind: "board", Key: "general"}
	got := CommandPartition{}
	h.SetCommandLock(func(ctx context.Context, partition CommandPartition) (func(), error) {
		got = partition
		return func() {}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	reply := h.ExecutePartition(ctx, nil, proto.CommandName("unknown"), json.RawMessage(`{}`), "", want)
	if reply.Err == nil {
		t.Fatal("unknown command unexpectedly succeeded")
	}
	if got != want {
		t.Fatalf("lock partition = %+v, want %+v", got, want)
	}
}

func TestPartitionWorkersRunDifferentPartitionsConcurrently(t *testing.T) {
	SetRuntime(Runtime{
		CheckProcessed: func(_ *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
			return "", false, false
		},
	})
	h := New(nil, nil)
	h.SetPartitionWorkers(8)
	a, b := partitionsOnDifferentLanes(t, 8)
	entered := make(chan CommandPartition, 2)
	release := make(chan struct{})
	h.SetCommandLock(func(ctx context.Context, partition CommandPartition) (func(), error) {
		entered <- partition
		select {
		case <-release:
			return func() {}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	var wg sync.WaitGroup
	for _, partition := range []CommandPartition{a, b} {
		wg.Add(1)
		go func(partition CommandPartition) {
			defer wg.Done()
			reply := h.ExecutePartition(ctx, nil, proto.CommandName("unknown"), json.RawMessage(`{}`), "", partition)
			if reply.Err == nil {
				t.Errorf("unknown command unexpectedly succeeded for %+v", partition)
			}
		}(partition)
	}

	first := receivePartition(t, entered)
	second := receivePartition(t, entered)
	if first == second {
		t.Fatalf("expected two different partitions to enter concurrently, got %+v twice", first)
	}
	close(release)
	wg.Wait()
}

func TestPartitionWorkersSerializeSamePartition(t *testing.T) {
	SetRuntime(Runtime{
		CheckProcessed: func(_ *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
			return "", false, false
		},
	})
	h := New(nil, nil)
	h.SetPartitionWorkers(8)
	partition := CommandPartition{Kind: "board", Key: "general"}
	entered := make(chan int, 2)
	releaseFirst := make(chan struct{})
	var count atomic.Int64
	h.SetCommandLock(func(ctx context.Context, got CommandPartition) (func(), error) {
		if got != partition {
			t.Errorf("partition = %+v, want %+v", got, partition)
		}
		n := int(count.Add(1))
		entered <- n
		if n == 1 {
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return func() {}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply := h.ExecutePartition(ctx, nil, proto.CommandName("unknown"), json.RawMessage(`{}`), "", partition)
			if reply.Err == nil {
				t.Error("unknown command unexpectedly succeeded")
			}
		}()
	}

	if got := receiveInt(t, entered); got != 1 {
		t.Fatalf("first entry = %d, want 1", got)
	}
	select {
	case got := <-entered:
		t.Fatalf("same partition entered again before first released: %d", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if got := receiveInt(t, entered); got != 2 {
		t.Fatalf("second entry = %d, want 2", got)
	}
	wg.Wait()
}

func TestPartitionLockWaitSamples(t *testing.T) {
	resetPartitionLockWaitStatsForTest()
	t.Cleanup(resetPartitionLockWaitStatsForTest)

	observePartitionLockWait(CommandPartition{Kind: "board", Key: "general"}, 2.5)
	observePartitionLockWait(CommandPartition{Kind: "board", Key: "general"}, 1.5)
	observePartitionLockWait(CommandPartition{Kind: "board", Key: "life"}, 1)

	samples := partitionLockWaitSamples(1)
	if got := sampleValue(t, samples, "budgie_writer_partition_lock_wait_count", "board", "general"); got != 2 {
		t.Fatalf("general count = %v, want 2", got)
	}
	if got := sampleValue(t, samples, "budgie_writer_partition_lock_wait_ms_sum", "board", "general"); got != 4 {
		t.Fatalf("general sum = %v, want 4", got)
	}
	if got := sampleValue(t, samples, "budgie_writer_partition_lock_wait_ms_max", "board", "general"); got != 2.5 {
		t.Fatalf("general max = %v, want 2.5", got)
	}
	if got := sampleSignalValue(t, samples, "budgie_hot_partition_candidate", "board", "general", "command_count"); got != 2 {
		t.Fatalf("general command-count candidate = %v, want 2", got)
	}
	if got := sampleSignalValue(t, samples, "budgie_hot_partition_candidate", "board", "general", "writer_lock_wait_ms_max"); got != 2.5 {
		t.Fatalf("general latency candidate = %v, want 2.5", got)
	}
	if hasSample(samples, "budgie_writer_partition_lock_wait_count", "board", "life") {
		t.Fatal("expected lower-wait partition to be excluded by limit")
	}
	if hasSignalSample(samples, "budgie_hot_partition_candidate", "board", "life", "command_count") {
		t.Fatal("expected lower-wait partition candidate to be excluded by limit")
	}
}

func partitionsOnDifferentLanes(t *testing.T, lanes int) (CommandPartition, CommandPartition) {
	t.Helper()
	a := CommandPartition{Kind: "board", Key: "general"}
	for _, key := range []string{"life", "tech", "music", "sports", "arts"} {
		b := CommandPartition{Kind: "board", Key: key}
		if commandexec.PartitionLaneIndex(a, lanes) != commandexec.PartitionLaneIndex(b, lanes) {
			return a, b
		}
	}
	t.Fatalf("could not find two partitions on different lanes")
	return CommandPartition{}, CommandPartition{}
}

func receivePartition(t *testing.T, ch <-chan CommandPartition) CommandPartition {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for partition to enter lock")
		return CommandPartition{}
	}
}

func receiveInt(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lane entry")
		return 0
	}
}

func sampleValue(t *testing.T, samples []metrics.Sample, name, kind, key string) float64 {
	t.Helper()
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["kind"] == kind && sample.Labels["key"] == key {
			return sample.Value
		}
	}
	t.Fatalf("missing sample %s{%s,%s}", name, kind, key)
	return 0
}

func hasSample(samples []metrics.Sample, name, kind, key string) bool {
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["kind"] == kind && sample.Labels["key"] == key {
			return true
		}
	}
	return false
}

func sampleSignalValue(t *testing.T, samples []metrics.Sample, name, kind, key, signal string) float64 {
	t.Helper()
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["kind"] == kind && sample.Labels["key"] == key && sample.Labels["signal"] == signal {
			return sample.Value
		}
	}
	t.Fatalf("missing sample %s{%s,%s,%s}", name, kind, key, signal)
	return 0
}

func hasSignalSample(samples []metrics.Sample, name, kind, key, signal string) bool {
	for _, sample := range samples {
		if sample.Name == name && sample.Labels["kind"] == kind && sample.Labels["key"] == key && sample.Labels["signal"] == signal {
			return true
		}
	}
	return false
}
