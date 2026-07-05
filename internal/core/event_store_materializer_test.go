package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func newEventStoreProjectionWorkerForTest(t *testing.T, c *Core, store EventStore, source string, batchSize, partitionLimit int) *EventStoreProjectionWorker {
	t.Helper()
	worker, err := NewEventStoreProjectionWorker(EventStoreProjectionWorkerConfig{
		Core:           c,
		Store:          store,
		Source:         source,
		BatchSize:      batchSize,
		PartitionLimit: partitionLimit,
	})
	if err != nil {
		t.Fatalf("NewEventStoreProjectionWorker: %v", err)
	}
	return worker
}

func drainEventStoreProjectionWorkerOnce(t *testing.T, ctx context.Context, worker *EventStoreProjectionWorker, label string) []EventStorePartitionMaterializationResult {
	t.Helper()
	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return results
}

func TestMaterializeEventStorePartitionFailsClosedOnOffsetGap(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	client := NewMemoryBrokerEventLogClient()
	store := NewBrokerEventStore(client)
	if err := store.SeedEventPartitionOffset(ctx, partition, 3); err != nil {
		t.Fatalf("seed event partition: %v", err)
	}
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_gap_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_gap_thread",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Gap thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append broker event")

	_, err := c.MaterializeEventStorePartition(ctx, store, EventStorePartitionMaterializationConfig{
		Source:    "gap-test",
		Partition: partition,
		Limit:     10,
	})
	requireErrorContains(t, err, "offset gap")
	thread, err := projections.GetThread(c.DB, "thr_gap_thread")
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread != nil {
		t.Fatalf("thread = %+v, want no projection writes after offset gap", thread)
	}
}

func TestSeedEventStoreProjectionWatermarksFromExistingPartitionOffsets(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}.Normalize()
	if _, err := qExec(c.DB,
		`INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
		 VALUES (?, ?, ?)`,
		partition.Kind, partition.Key, 1,
	); err != nil {
		t.Fatalf("seed SQL partition offset: %v", err)
	}
	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	if err := store.SeedEventPartitionOffset(ctx, partition, 1); err != nil {
		t.Fatalf("seed broker event partition: %v", err)
	}
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_seeded_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_seeded_thread",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Seeded thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append broker event")

	seeded, err := c.seedEventStoreProjectionWatermarksFromEventPartitionOffsets(ctx, "seed-test")
	if err != nil {
		t.Fatalf("seed watermarks: %v", err)
	}
	if seeded != 1 {
		t.Fatalf("seeded = %d, want one partition watermark", seeded)
	}
	result, err := c.MaterializeEventStorePartition(ctx, store, EventStorePartitionMaterializationConfig{
		Source:    "seed-test",
		Partition: partition,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("MaterializeEventStorePartition: %v", err)
	}
	if result.StartedOffset != 1 || result.LastOffset != 2 || result.Applied != 1 {
		t.Fatalf("result = %+v, want seeded offset 1 then applied offset 2", result)
	}
	thread, err := projections.GetThread(c.DB, "thr_seeded_thread")
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread == nil || thread.Title != "Seeded thread" {
		t.Fatalf("thread = %+v, want materialized broker thread", thread)
	}
}

func TestRecordPostActivityFromEventComputesTrustInline(t *testing.T) {
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin activity tx: %v", err)
	}
	if err := recordPostActivityFromEvent(tx, alice.ID, 1, 1234); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("record first activity: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit first activity: %v", err)
	}
	var postsCreated, daysVisited, trustLevel int
	if err := qQueryRow(c.DB,
		`SELECT posts_created, days_visited, trust_level FROM user_activity WHERE user_id=?`,
		alice.ID,
	).Scan(&postsCreated, &daysVisited, &trustLevel); err != nil {
		t.Fatalf("query first activity: %v", err)
	}
	if postsCreated != 1 || daysVisited != 1 || trustLevel != 1 {
		t.Fatalf("first activity = posts %d days %d trust %d, want 1/1/1", postsCreated, daysVisited, trustLevel)
	}

	if _, err := qExec(c.DB, `UPDATE user_activity SET trust_level=4 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatalf("grant TL4: %v", err)
	}
	tx, err = c.DB.Begin()
	if err != nil {
		t.Fatalf("begin TL4 activity tx: %v", err)
	}
	if err := recordPostActivityFromEvent(tx, alice.ID, 2, 24*60*60*1000+1234); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("record TL4 activity: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit TL4 activity: %v", err)
	}
	if err := qQueryRow(c.DB,
		`SELECT posts_created, days_visited, trust_level FROM user_activity WHERE user_id=?`,
		alice.ID,
	).Scan(&postsCreated, &daysVisited, &trustLevel); err != nil {
		t.Fatalf("query TL4 activity: %v", err)
	}
	if postsCreated != 2 || daysVisited != 2 || trustLevel != 4 {
		t.Fatalf("TL4 activity = posts %d days %d trust %d, want 2/2/4", postsCreated, daysVisited, trustLevel)
	}
}

func TestEventStoreProjectionWorkerDrainsBrokerPartitionsToSQLProjections(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_worker_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_worker_thread",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Worker thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append thread event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_worker_post",
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:general", "thread:thr_worker_thread"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_worker_post",
			Thread:      "thr_worker_thread",
			Author:      "alice",
			AuthorID:    "usr_alice",
			Body:        "hello from worker",
			RawBody:     "hello from worker",
			ContentType: "markup",
			TS:          1235,
		},
		TS: 1235,
	}, "append post event")

	worker := newEventStoreProjectionWorkerForTest(t, c, store, "worker-test", 1, 10)
	results := drainEventStoreProjectionWorkerOnce(t, ctx, worker, "DrainOnce")
	if got := eventStoreProjectionAppliedTotal(results); got != 2 {
		t.Fatalf("applied total = %d results=%+v, want two projected broker events", got, results)
	}
	thread, err := projections.GetThread(c.DB, "thr_worker_thread")
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread == nil || thread.Title != "Worker thread" || thread.PostCount != 1 || thread.LastSeq != 2 {
		t.Fatalf("thread = %+v, want materialized thread with one post", thread)
	}
	post, err := projections.GetPost(c.DB, "pst_worker_post")
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post == nil || post.Thread != "thr_worker_thread" || post.Body != "hello from worker" {
		t.Fatalf("post = %+v, want materialized broker post", post)
	}

	second := drainEventStoreProjectionWorkerOnce(t, ctx, worker, "second DrainOnce")
	if got := eventStoreProjectionAppliedTotal(second); got != 0 {
		t.Fatalf("second applied total = %d results=%+v, want checkpointed no-op", got, second)
	}
}

func TestEventStoreProjectionWorkerIndexesPartitionOnlyCompatibilityEvents(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	store := NewBrokerEventStore(&partitionOnlyBrokerEventLogClient{inner: NewMemoryBrokerEventLogClient()})
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_partition_only_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_partition_only_thread",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Partition-only thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append thread event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_partition_only_post",
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:general", "thread:thr_partition_only_thread"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_partition_only_post",
			Thread:      "thr_partition_only_thread",
			Author:      "alice",
			AuthorID:    "usr_alice",
			Body:        "partition-only broker projection",
			RawBody:     "partition-only broker projection",
			ContentType: "markup",
			TS:          1235,
		},
		TS: 1235,
	}, "append post event")

	worker := newEventStoreProjectionWorkerForTest(t, c, store, "partition-only-test", 10, 10)
	results := drainEventStoreProjectionWorkerOnce(t, ctx, worker, "DrainOnce")
	if got := eventStoreProjectionAppliedTotal(results); got != 2 {
		t.Fatalf("applied total = %d results=%+v, want two projected broker events", got, results)
	}

	events, err := NewSQLEventStore(c.DB).Replay(ctx, 0, nil, 10)
	if err != nil {
		t.Fatalf("replay compatibility events: %v", err)
	}
	if len(events) != 2 ||
		events[0].ID != "evt_partition_only_thread" ||
		events[0].Seq != 1 ||
		events[1].ID != "evt_partition_only_post" ||
		events[1].Seq != 2 {
		t.Fatalf("compatibility events = %+v, want thread seq 1 then post seq 2", events)
	}
	offsets, err := NewSQLEventStore(c.DB).ListEventPartitionOffsets(ctx, 10)
	if err != nil {
		t.Fatalf("list compatibility partition offsets: %v", err)
	}
	if len(offsets) != 1 ||
		offsets[0].Partition != (LogPartition{Kind: partitionBoard, Key: "general"}) ||
		offsets[0].LastOffset != 2 {
		t.Fatalf("compatibility partition offsets = %+v, want board/general tail 2", offsets)
	}
	thread, err := projections.GetThread(c.DB, "thr_partition_only_thread")
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread == nil || thread.LastSeq != 2 || thread.PostCount != 1 {
		t.Fatalf("thread = %+v, want projection to use SQL compatibility seq 2 for partition-only post", thread)
	}
}

func TestCommandLogDrainEventProjectionWaitsForSourcePartitionOffsets(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	client := &shortBatchBrokerEventLogClient{
		inner: NewMemoryBrokerEventLogClient(),
		max:   1,
	}
	store := NewBrokerEventStore(client)
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_short_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_short_thread",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Short batch thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append thread event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_short_post",
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:general", "thread:thr_short_thread"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_short_post",
			Thread:      "thr_short_thread",
			Author:      "alice",
			AuthorID:    "usr_alice",
			Body:        "short broker reads still drain",
			RawBody:     "short broker reads still drain",
			ContentType: "markup",
			TS:          1235,
		},
		TS: 1235,
	}, "append post event")

	stage, err := c.projectCommandLogDrainLoadEvents(ctx, store, loadmodel.CommandLogDrainLoadConfig{
		Boards:           1,
		CommandsPerBoard: 1,
		BatchSize:        10,
		ExecutorMode:     loadmodel.CommandLogDrainExecutorNative,
	})
	if err != nil {
		t.Fatalf("projectCommandLogDrainLoadEvents: %v", err)
	}
	if stage.AppliedEvents != 2 {
		t.Fatalf("applied events = %d stage=%+v, want both short-read events", stage.AppliedEvents, stage)
	}
	post, err := projections.GetPost(c.DB, "pst_short_post")
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post == nil || post.Thread != "thr_short_thread" {
		t.Fatalf("post = %+v, want materialized post after short broker reads", post)
	}
}

func TestEventStoreProjectionWorkerFailsClosedOnPartitionLimit(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)

	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_limit_general",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_limit_general",
			Board:    "general",
			Author:   "alice",
			AuthorID: "usr_alice",
			Title:    "Limit general",
			TS:       1234,
		},
		TS: 1234,
	}, "append general event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_limit_other",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:other"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_limit_other",
			Board:    "other",
			Author:   "bob",
			AuthorID: "usr_bob",
			Title:    "Limit other",
			TS:       1235,
		},
		TS: 1235,
	}, "append other event")

	worker := newEventStoreProjectionWorkerForTest(t, c, store, "limit-test", 10, 1)
	results, err := worker.MaterializeOnce(ctx)
	requireErrorContains(t, err, "partition limit")
	if len(results) != 0 {
		t.Fatalf("results = %+v, want no partial projection batches", results)
	}
	for _, id := range []string{"thr_limit_general", "thr_limit_other"} {
		thread, err := projections.GetThread(c.DB, id)
		if err != nil {
			t.Fatalf("get thread %s: %v", id, err)
		}
		if thread != nil {
			t.Fatalf("thread %s = %+v, want no projection writes after partition-limit failure", id, thread)
		}
	}
}

type shortBatchBrokerEventLogClient struct {
	inner *MemoryBrokerEventLogClient
	max   int
}

func (c *shortBatchBrokerEventLogClient) AppendEvent(ctx context.Context, partition LogPartition, record logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	return c.inner.AppendEvent(ctx, partition, record)
}

func (c *shortBatchBrokerEventLogClient) FetchEvents(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerEventLogMessage, error) {
	if c.max > 0 && (limit <= 0 || limit > c.max) {
		limit = c.max
	}
	return c.inner.FetchEvents(ctx, partition, afterOffset, limit)
}

func (c *shortBatchBrokerEventLogClient) Head(ctx context.Context) (int64, error) {
	return c.inner.Head(ctx)
}

func (c *shortBatchBrokerEventLogClient) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	return c.inner.ListEventPartitions(ctx, limit)
}

func (c *shortBatchBrokerEventLogClient) ListEventPartitionOffsets(ctx context.Context, limit int) ([]logmodel.EventPartitionOffset, error) {
	return c.inner.ListEventPartitionOffsets(ctx, limit)
}

type partitionOnlyBrokerEventLogClient struct {
	inner *MemoryBrokerEventLogClient
}

func (c *partitionOnlyBrokerEventLogClient) AppendEvent(ctx context.Context, partition LogPartition, record logmodel.BrokerEventRecord) (logmodel.BrokerEventLogMessage, error) {
	msg, err := c.inner.AppendEvent(ctx, partition, record)
	msg.StreamSeq = 0
	return msg, err
}

func (c *partitionOnlyBrokerEventLogClient) FetchEvents(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]logmodel.BrokerEventLogMessage, error) {
	messages, err := c.inner.FetchEvents(ctx, partition, afterOffset, limit)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		messages[i].StreamSeq = 0
	}
	return messages, nil
}

func (c *partitionOnlyBrokerEventLogClient) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return 0, nil
}

func (c *partitionOnlyBrokerEventLogClient) SeedEventPartitionOffset(ctx context.Context, partition LogPartition, offset int64) error {
	return c.inner.SeedEventPartitionOffset(ctx, partition, offset)
}

func (c *partitionOnlyBrokerEventLogClient) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	return c.inner.ListEventPartitions(ctx, limit)
}

func (c *partitionOnlyBrokerEventLogClient) ListEventPartitionOffsets(ctx context.Context, limit int) ([]logmodel.EventPartitionOffset, error) {
	return c.inner.ListEventPartitionOffsets(ctx, limit)
}

func TestMaterializeEventStorePartitionEnqueuesPostCommittedSideEffects(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	store := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_side_effect_thread",
		Kind:   proto.EvtThreadNew,
		Scopes: []string{"board:general"},
		Payload: &proto.ThreadNewPayload{
			ID:       "thr_side_effect_thread",
			Board:    "general",
			Author:   alice.Name,
			AuthorID: alice.ID,
			Title:    "Side effects thread",
			TS:       1234,
		},
		TS: 1234,
	}, "append thread event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_side_effect_root",
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:general", "thread:thr_side_effect_thread"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_side_effect_root",
			Thread:      "thr_side_effect_thread",
			Author:      alice.Name,
			AuthorID:    alice.ID,
			Body:        "root from broker",
			RawBody:     "root from broker",
			ContentType: "markup",
			TS:          1235,
		},
		TS: 1235,
	}, "append root post event")
	appendCoreTestEvent(t, ctx, store, EventAppend{
		ID:     "evt_side_effect_reply",
		Kind:   proto.EvtPostAppended,
		Scopes: []string{"board:general", "thread:thr_side_effect_thread"},
		Payload: &proto.PostAppendedPayload{
			ID:          "pst_side_effect_reply",
			Thread:      "thr_side_effect_thread",
			Author:      bob.Name,
			AuthorID:    bob.ID,
			Body:        "reply from broker",
			RawBody:     "reply from broker",
			ContentType: "markup",
			ReplyTo:     "pst_side_effect_root",
			TS:          1236,
		},
		TS: 1236,
	}, "append reply post event")

	result, err := c.MaterializeEventStorePartition(ctx, store, EventStorePartitionMaterializationConfig{
		Source:    "side-effect-test",
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("materialize broker events: %v", err)
	}
	if result.Applied != 3 || result.LastOffset != 3 {
		t.Fatalf("materialization result = %+v, want three applied broker events", result)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 2 {
		t.Fatalf("processed outbox jobs = %d, want one job per projected post", processed)
	}
	notifications, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(alice): %v", err)
	}
	if len(notifications) != 1 ||
		notifications[0].Kind != "reply" ||
		notifications[0].ThreadID != "thr_side_effect_thread" ||
		notifications[0].PostID != "pst_side_effect_reply" ||
		notifications[0].Actor != bob.Name {
		t.Fatalf("notifications = %+v, want broker-projected reply notification for alice", notifications)
	}
}

func drainOutboxJobsForTest(t *testing.T, c *Core) int {
	t.Helper()
	processed := 0
	for {
		job, err := claimOutboxJob(c.DB)
		if err != nil {
			t.Fatalf("claim outbox job: %v", err)
		}
		if job == nil {
			return processed
		}
		if err := processOutboxJob(c.DB, c.Bus, job); err != nil {
			t.Fatalf("process outbox job %s: %v", job.ID, err)
		}
		if err := completeOutboxJob(c.DB, job.ID); err != nil {
			t.Fatalf("complete outbox job %s: %v", job.ID, err)
		}
		processed++
	}
}

func eventStoreProjectionAppliedTotal(results []EventStorePartitionMaterializationResult) int {
	total := 0
	for _, result := range results {
		total += result.Applied
	}
	return total
}
