package redisconn

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/readmodel"
)

func TestCommandLogPartitionIndexTracksOffsetsWithRedisHashes(t *testing.T) {
	ctx := context.Background()
	redis := newFakeRedisCommander()
	index := NewCommandLogPartitionIndex(redis, CommandLogPartitionIndexOptions{Prefix: "test"})
	board := core.LogPartition{Kind: "board", Key: "general"}.Normalize()
	thread := core.LogPartition{Kind: "thread", Key: "thr_1"}.Normalize()

	if err := index.RecordCommandPartitionTail(ctx, board, 2); err != nil {
		t.Fatalf("RecordCommandPartitionTail board: %v", err)
	}
	if err := index.RecordCommandPartitionTail(ctx, thread, 1); err != nil {
		t.Fatalf("RecordCommandPartitionTail thread: %v", err)
	}
	if err := index.RecordCommandPartitionCommit(ctx, board, 5); err != nil {
		t.Fatalf("RecordCommandPartitionCommit board: %v", err)
	}
	if err := index.RecordCommandPartitionTail(ctx, board, 1); err != nil {
		t.Fatalf("RecordCommandPartitionTail stale board: %v", err)
	}

	offsets, err := index.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommandPartitionOffsets: %v", err)
	}
	want := []logmodel.CommandPartitionOffset{
		{Partition: thread, TailOffset: 1, CommittedOffset: 0},
		{Partition: board, TailOffset: 2, CommittedOffset: 2},
	}
	if len(offsets) != len(want) {
		t.Fatalf("offsets = %+v, want %+v", offsets, want)
	}
	for i := range want {
		if offsets[i] != want[i] {
			t.Fatalf("offsets = %+v, want %+v", offsets, want)
		}
	}
	partitions, err := index.ListCommandPartitions(ctx, 1)
	if err != nil {
		t.Fatalf("ListCommandPartitions: %v", err)
	}
	if len(partitions) != 1 || partitions[0] != board {
		t.Fatalf("partitions = %+v, want highest tail board", partitions)
	}
	if redis.evalCalls != 4 {
		t.Fatalf("eval calls = %d, want max-write script for each offset update", redis.evalCalls)
	}
}

func TestReadCacheStoresLatestFeedPostsInRedis(t *testing.T) {
	ctx := context.Background()
	redis := newFakeRedisCommander()
	cache := NewReadCache(redis, ReadCacheOptions{Prefix: "test", TTL: 1500 * time.Millisecond})
	key := readmodel.LatestFeedKey{
		ViewerID:   "usr_alice",
		Limit:      10,
		AppliedSeq: 5,
		HeadSeq:    7,
	}
	posts := []projections.Post{{
		ID:         "post_1",
		Thread:     "thread_1",
		Board:      "general",
		Author:     "alice",
		Body:       "cached",
		CreatedSeq: 7,
		UpdatedSeq: 7,
	}}

	cached, ok, err := cache.GetLatestFeedPosts(ctx, key)
	if err != nil || ok || len(cached) != 0 {
		t.Fatalf("initial cache get = posts:%+v ok:%v err:%v, want miss", cached, ok, err)
	}
	if err := cache.PutLatestFeedPosts(ctx, key, posts); err != nil {
		t.Fatalf("PutLatestFeedPosts: %v", err)
	}
	cached, ok, err = cache.GetLatestFeedPosts(ctx, key)
	if err != nil {
		t.Fatalf("GetLatestFeedPosts: %v", err)
	}
	if !ok || len(cached) != 1 || cached[0].ID != "post_1" {
		t.Fatalf("cached posts = %+v ok=%v, want stored post", cached, ok)
	}
	if redis.setEX != 2 {
		t.Fatalf("SET EX = %d, want rounded-up 2 second TTL", redis.setEX)
	}
}

func TestParseRedisURL(t *testing.T) {
	addr, username, password, db, err := parseRedisURL("redis://alice:secret@redis.internal:6379/3")
	if err != nil {
		t.Fatalf("parse redis URL: %v", err)
	}
	if addr != "redis.internal:6379" || username != "alice" || password != "secret" || db != 3 {
		t.Fatalf("parsed redis URL = addr:%q username:%q password:%q db:%d", addr, username, password, db)
	}

	addr, username, password, db, err = parseRedisURL("127.0.0.1:6379")
	if err != nil {
		t.Fatalf("parse redis address: %v", err)
	}
	if addr != "127.0.0.1:6379" || username != "" || password != "" || db != 0 {
		t.Fatalf("parsed redis address = addr:%q username:%q password:%q db:%d", addr, username, password, db)
	}

	if _, _, _, _, err := parseRedisURL("rediss://redis.internal:6379"); err == nil {
		t.Fatalf("parse rediss URL succeeded, want unsupported scheme error")
	}
}

type fakeRedisCommander struct {
	hashes    map[string]map[string]string
	values    map[string][]byte
	evalCalls int
	setEX     int64
}

func newFakeRedisCommander() *fakeRedisCommander {
	return &fakeRedisCommander{
		hashes: map[string]map[string]string{},
		values: map[string][]byte{},
	}
}

func (c *fakeRedisCommander) Do(ctx context.Context, args ...any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, nil
	}
	switch redisString(args[0]) {
	case "EVAL":
		c.evalCalls++
		key := redisString(args[3])
		field := redisString(args[4])
		value, _ := strconv.ParseInt(redisString(args[5]), 10, 64)
		if c.hashes[key] == nil {
			c.hashes[key] = map[string]string{}
		}
		current, _ := strconv.ParseInt(c.hashes[key][field], 10, 64)
		if value > current {
			current = value
			c.hashes[key][field] = strconv.FormatInt(value, 10)
		}
		return current, nil
	case "HGETALL":
		hash := c.hashes[redisString(args[1])]
		out := make([]any, 0, len(hash)*2)
		for field, value := range hash {
			out = append(out, []byte(field), []byte(value))
		}
		return out, nil
	case "GET":
		value, ok := c.values[redisString(args[1])]
		if !ok {
			return nil, nil
		}
		return append([]byte(nil), value...), nil
	case "SET":
		c.values[redisString(args[1])] = append([]byte(nil), []byte(redisString(args[2]))...)
		if len(args) >= 5 && redisString(args[3]) == "EX" {
			c.setEX, _ = strconv.ParseInt(redisString(args[4]), 10, 64)
		}
		return "OK", nil
	default:
		return nil, nil
	}
}
