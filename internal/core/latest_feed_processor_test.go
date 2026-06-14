package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestLatestFeedProcessorMaterializesAndRebuildsFeed(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "latest-feed-processor.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "secret", Name: "Secret"})
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "secret",
		MemberReadMode: latestFeedBoolPtr(true),
	})

	publicThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Public latest",
		Body:  "visible global post",
	})
	secretThread := execPostSearchTestCommand(t, c, admin, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "secret",
		Title: "Secret latest",
		Body:  "member-read global post",
	})

	if rows := latestFeedRowCount(t, c); rows != 0 {
		t.Fatalf("latest feed rows before processor = %d, want 0", rows)
	}
	fallback, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("fallback latest feed: %v", err)
	}
	assertLatestFeedThreads(t, fallback, []string{publicThread.ID}, []string{secretThread.ID})
	adminFallback, err := c.ListLatestFeedPosts(admin, 10, 0)
	if err != nil {
		t.Fatalf("admin fallback latest feed: %v", err)
	}
	assertLatestFeedThreads(t, adminFallback, []string{publicThread.ID, secretThread.ID}, nil)

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	result, err := c.ProcessLatestFeedOnce(100)
	if err != nil {
		t.Fatalf("ProcessLatestFeedOnce: %v", err)
	}
	if result.FromSeq != 0 || result.AppliedSeq != head || result.HeadSeq != head || result.FeedChanges < 2 {
		t.Fatalf("latest processor result = %+v, want catch-up through head %d with feed changes", result, head)
	}
	if rows := latestFeedRowCount(t, c); rows < 2 {
		t.Fatalf("latest feed rows after processor = %d, want at least two", rows)
	}
	materialized, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("materialized latest feed: %v", err)
	}
	assertLatestFeedThreads(t, materialized, []string{publicThread.ID}, []string{secretThread.ID})

	newThread := execPostSearchTestCommand(t, c, bob, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Delayed global latest",
		Body:  "local reads should not wait for feed freshness",
	})
	localPosts, err := c.ListPosts(newThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("local post read after delayed latest write: %v", err)
	}
	if len(localPosts) != 1 || localPosts[0].Thread != newThread.ID {
		t.Fatalf("local posts for new thread = %+v, want the new post visible", localPosts)
	}
	staleFeed, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("stale latest feed: %v", err)
	}
	assertLatestFeedThreads(t, staleFeed, []string{publicThread.ID}, []string{newThread.ID, secretThread.ID})

	update, err := c.ProcessLatestFeedOnce(100)
	if err != nil {
		t.Fatalf("ProcessLatestFeedOnce update: %v", err)
	}
	if update.FeedChanges == 0 || update.AppliedSeq <= result.AppliedSeq {
		t.Fatalf("latest feed update result = %+v, want new feed change", update)
	}
	fresh, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("fresh latest feed: %v", err)
	}
	assertLatestFeedThreads(t, fresh, []string{newThread.ID, publicThread.ID}, []string{secretThread.ID})

	newPosts, err := c.ListPosts(newThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list new posts: %v", err)
	}
	execPostSearchTestCommand(t, c, admin, proto.CmdRedactPost, proto.RedactPostPayload{
		Post:   newPosts[0].ID,
		Reason: "hide from latest",
	})
	redactResult, err := c.ProcessLatestFeedOnce(100)
	if err != nil {
		t.Fatalf("ProcessLatestFeedOnce redact: %v", err)
	}
	if redactResult.FeedChanges == 0 {
		t.Fatalf("redact result = %+v, want feed cleanup", redactResult)
	}
	afterRedact, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("latest feed after redact: %v", err)
	}
	assertLatestFeedThreads(t, afterRedact, []string{publicThread.ID}, []string{newThread.ID, secretThread.ID})

	if _, err := c.DB.Exec(`DELETE FROM latest_feed_posts`); err != nil {
		t.Fatalf("delete latest feed rows: %v", err)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{DerivedViewLatestFeed}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog feeds.latest: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != DerivedViewLatestFeed {
		t.Fatalf("backfill result = %+v", backfill)
	}
	repaired, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("latest feed after backfill: %v", err)
	}
	assertLatestFeedThreads(t, repaired, []string{publicThread.ID}, []string{newThread.ID, secretThread.ID})
}

func TestLatestFeedReadCacheServesStableWatermarkHit(t *testing.T) {
	cache := &scriptedLatestFeedReadCache{}
	c, err := New(filepath.Join(t.TempDir(), "latest-feed-read-cache.db"), WithReadCache(cache))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice-cache", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	thread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "Cached latest",
		Body:  "first read should populate the latest-feed cache",
	})
	if _, err := c.ProcessLatestFeedOnce(100); err != nil {
		t.Fatalf("ProcessLatestFeedOnce: %v", err)
	}

	first, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("first latest feed: %v", err)
	}
	assertLatestFeedThreads(t, first, []string{thread.ID}, nil)
	if cache.puts != 1 || cache.gets != 1 {
		t.Fatalf("cache after first read gets=%d puts=%d, want one get miss and one put", cache.gets, cache.puts)
	}
	if cache.lastKey.ViewerID != alice.ID || cache.lastKey.Limit != 10 || cache.lastKey.Offset != 0 || cache.lastKey.AppliedSeq <= 0 || cache.lastKey.HeadSeq <= 0 {
		t.Fatalf("cache key = %+v, want viewer, pagination, and watermarks", cache.lastKey)
	}

	cache.hit = true
	cache.posts = []Post{{
		ID:          "cached_post",
		Thread:      "cached_thread",
		Board:       "general",
		Author:      "alice-cache",
		Body:        "served from cache",
		ContentType: "text/plain",
		CreatedSeq:  cache.lastKey.HeadSeq,
		UpdatedSeq:  cache.lastKey.HeadSeq,
	}}
	second, err := c.ListLatestFeedPosts(alice, 10, 0)
	if err != nil {
		t.Fatalf("second latest feed: %v", err)
	}
	if len(second) != 1 || second[0].ID != "cached_post" {
		t.Fatalf("second latest feed = %+v, want scripted cache hit", second)
	}
	if cache.gets != 2 || cache.puts != 1 {
		t.Fatalf("cache after second read gets=%d puts=%d, want one additional get and no put", cache.gets, cache.puts)
	}
}

func latestFeedRowCount(t *testing.T, c *Core) int {
	t.Helper()
	var count int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM latest_feed_posts`).Scan(&count); err != nil {
		t.Fatalf("count latest feed rows: %v", err)
	}
	return count
}

func assertLatestFeedThreads(t *testing.T, posts []Post, wantThreads, absentThreads []string) {
	t.Helper()
	got := map[string]bool{}
	for _, post := range posts {
		got[post.Thread] = true
	}
	for _, threadID := range wantThreads {
		if !got[threadID] {
			t.Fatalf("latest feed threads = %v, want %s present; posts=%+v", got, threadID, posts)
		}
	}
	for _, threadID := range absentThreads {
		if got[threadID] {
			t.Fatalf("latest feed threads = %v, want %s absent; posts=%+v", got, threadID, posts)
		}
	}
}

func latestFeedBoolPtr(v bool) *bool { return &v }

type scriptedLatestFeedReadCache struct {
	gets    int
	puts    int
	hit     bool
	lastKey LatestFeedReadCacheKey
	posts   []Post
}

func (c *scriptedLatestFeedReadCache) GetLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey) ([]Post, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.gets++
	c.lastKey = key
	if !c.hit {
		return nil, false, nil
	}
	return cloneReadCachePosts(c.posts), true, nil
}

func (c *scriptedLatestFeedReadCache) PutLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey, posts []Post) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.puts++
	c.lastKey = key
	c.posts = cloneReadCachePosts(posts)
	return nil
}
