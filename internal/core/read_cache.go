package core

import (
	"context"
	"sync"
)

type LatestFeedReadCacheKey struct {
	ViewerID       string
	IncludePrivate bool
	Limit          int
	Offset         int
	AppliedSeq     int64
	HeadSeq        int64
}

type ReadCache interface {
	GetLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey) ([]Post, bool, error)
	PutLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey, posts []Post) error
}

type MemoryReadCache struct {
	mu         sync.Mutex
	latestFeed map[LatestFeedReadCacheKey][]Post
}

func NewMemoryReadCache() *MemoryReadCache {
	return &MemoryReadCache{
		latestFeed: map[LatestFeedReadCacheKey][]Post{},
	}
}

func (c *MemoryReadCache) GetLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey) ([]Post, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if c == nil {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	posts, ok := c.latestFeed[normalizeLatestFeedReadCacheKey(key)]
	if !ok {
		return nil, false, nil
	}
	return cloneReadCachePosts(posts), true, nil
}

func (c *MemoryReadCache) PutLatestFeedPosts(ctx context.Context, key LatestFeedReadCacheKey, posts []Post) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latestFeed == nil {
		c.latestFeed = map[LatestFeedReadCacheKey][]Post{}
	}
	c.latestFeed[normalizeLatestFeedReadCacheKey(key)] = cloneReadCachePosts(posts)
	return nil
}

func latestFeedReadCacheKey(viewerID string, includePrivate bool, limit, offset int, appliedSeq, headSeq int64) LatestFeedReadCacheKey {
	limit, offset = normalizeReadCachePagination(limit, offset, 30, 100)
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	if headSeq < 0 {
		headSeq = 0
	}
	return LatestFeedReadCacheKey{
		ViewerID:       viewerID,
		IncludePrivate: includePrivate,
		Limit:          limit,
		Offset:         offset,
		AppliedSeq:     appliedSeq,
		HeadSeq:        headSeq,
	}
}

func normalizeLatestFeedReadCacheKey(key LatestFeedReadCacheKey) LatestFeedReadCacheKey {
	return latestFeedReadCacheKey(key.ViewerID, key.IncludePrivate, key.Limit, key.Offset, key.AppliedSeq, key.HeadSeq)
}

func normalizeReadCachePagination(limit, offset, defaultLimit, maxLimit int) (int, int) {
	if defaultLimit <= 0 {
		defaultLimit = 30
	}
	if maxLimit <= 0 {
		maxLimit = defaultLimit
	}
	if limit <= 0 || limit > maxLimit {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func cloneReadCachePosts(posts []Post) []Post {
	if len(posts) == 0 {
		return nil
	}
	out := make([]Post, len(posts))
	copy(out, posts)
	for i := range out {
		if len(posts[i].Attachments) > 0 {
			out[i].Attachments = append([]PostAttachment(nil), posts[i].Attachments...)
		}
	}
	return out
}
