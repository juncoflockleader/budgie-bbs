package readmodel

import (
	"context"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type LatestFeedKey struct {
	ViewerID       string
	IncludePrivate bool
	Limit          int
	Offset         int
	AppliedSeq     int64
	HeadSeq        int64
}

type LatestFeedCache interface {
	GetLatestFeedPosts(ctx context.Context, key LatestFeedKey) ([]projections.Post, bool, error)
	PutLatestFeedPosts(ctx context.Context, key LatestFeedKey, posts []projections.Post) error
}

type MemoryCache struct {
	mu         sync.Mutex
	latestFeed map[LatestFeedKey][]projections.Post
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		latestFeed: map[LatestFeedKey][]projections.Post{},
	}
}

func (c *MemoryCache) GetLatestFeedPosts(ctx context.Context, key LatestFeedKey) ([]projections.Post, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if c == nil {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	posts, ok := c.latestFeed[NormalizeLatestFeedKey(key)]
	if !ok {
		return nil, false, nil
	}
	return ClonePosts(posts), true, nil
}

func (c *MemoryCache) PutLatestFeedPosts(ctx context.Context, key LatestFeedKey, posts []projections.Post) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latestFeed == nil {
		c.latestFeed = map[LatestFeedKey][]projections.Post{}
	}
	c.latestFeed[NormalizeLatestFeedKey(key)] = ClonePosts(posts)
	return nil
}

func NewLatestFeedKey(viewerID string, includePrivate bool, limit, offset int, appliedSeq, headSeq int64) LatestFeedKey {
	limit, offset = NormalizePagination(limit, offset, 30, 100)
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	if headSeq < 0 {
		headSeq = 0
	}
	return LatestFeedKey{
		ViewerID:       viewerID,
		IncludePrivate: includePrivate,
		Limit:          limit,
		Offset:         offset,
		AppliedSeq:     appliedSeq,
		HeadSeq:        headSeq,
	}
}

func NormalizeLatestFeedKey(key LatestFeedKey) LatestFeedKey {
	return NewLatestFeedKey(key.ViewerID, key.IncludePrivate, key.Limit, key.Offset, key.AppliedSeq, key.HeadSeq)
}

func NormalizePagination(limit, offset, defaultLimit, maxLimit int) (int, int) {
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

func ClonePosts(posts []projections.Post) []projections.Post {
	if len(posts) == 0 {
		return nil
	}
	out := make([]projections.Post, len(posts))
	copy(out, posts)
	for i := range out {
		if len(posts[i].Attachments) > 0 {
			out[i].Attachments = append([]projections.PostAttachment(nil), posts[i].Attachments...)
		}
	}
	return out
}
