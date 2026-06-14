package redisconn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

type ReadCacheOptions struct {
	Prefix string
	TTL    time.Duration
}

type ReadCache struct {
	client Commander
	prefix string
	ttl    time.Duration
}

var _ core.ReadCache = (*ReadCache)(nil)

func NewReadCache(client Commander, options ReadCacheOptions) *ReadCache {
	prefix := strings.TrimSpace(options.Prefix)
	if prefix == "" {
		prefix = "budgie"
	}
	ttl := options.TTL
	if ttl < 0 {
		ttl = 0
	}
	return &ReadCache{
		client: client,
		prefix: prefix,
		ttl:    ttl,
	}
}

func (c *ReadCache) GetLatestFeedPosts(ctx context.Context, key core.LatestFeedReadCacheKey) ([]core.Post, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if c == nil || c.client == nil {
		return nil, false, fmt.Errorf("redis read cache: nil client")
	}
	reply, err := c.client.Do(ctx, "GET", c.latestFeedKey(key))
	if err != nil {
		return nil, false, err
	}
	if reply == nil {
		return nil, false, nil
	}
	var posts []core.Post
	if err := json.Unmarshal([]byte(redisString(reply)), &posts); err != nil {
		return nil, false, err
	}
	return posts, true, nil
}

func (c *ReadCache) PutLatestFeedPosts(ctx context.Context, key core.LatestFeedReadCacheKey, posts []core.Post) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.client == nil {
		return fmt.Errorf("redis read cache: nil client")
	}
	data, err := json.Marshal(posts)
	if err != nil {
		return err
	}
	args := []any{"SET", c.latestFeedKey(key), data}
	if c.ttl > 0 {
		args = append(args, "EX", redisTTLSeconds(c.ttl))
	}
	_, err = c.client.Do(ctx, args...)
	return err
}

func (c *ReadCache) latestFeedKey(key core.LatestFeedReadCacheKey) string {
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return c.prefix + ":read-cache:latest-feed:" + hex.EncodeToString(sum[:])
}

func redisTTLSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	seconds := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds <= 0 {
		seconds = 1
	}
	return seconds
}
