package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/readmodel"

type LatestFeedReadCacheKey = readmodel.LatestFeedKey
type ReadCache = readmodel.LatestFeedCache
type MemoryReadCache = readmodel.MemoryCache

func NewMemoryReadCache() *MemoryReadCache {
	return readmodel.NewMemoryCache()
}
