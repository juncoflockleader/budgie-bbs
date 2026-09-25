package readmodel

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestMemoryCacheNormalizesLatestFeedKeyAndCopiesPosts(t *testing.T) {
	cache := NewMemoryCache()
	key := LatestFeedKey{
		ViewerID:       "usr_alice",
		IncludePrivate: true,
		Limit:          0,
		Offset:         -10,
		AppliedSeq:     -1,
		HeadSeq:        12,
	}
	posts := []projections.Post{{
		ID:     "post_1",
		Thread: "thread_1",
		Attachments: []projections.PostAttachment{{
			ID: "att_1",
		}},
	}}
	if err := cache.PutLatestFeedPosts(context.Background(), key, posts); err != nil {
		t.Fatalf("PutLatestFeedPosts: %v", err)
	}
	posts[0].ID = "mutated"
	posts[0].Attachments[0].ID = "mutated_att"

	normalizedKey := LatestFeedKey{
		ViewerID:       "usr_alice",
		IncludePrivate: true,
		Limit:          30,
		Offset:         0,
		AppliedSeq:     0,
		HeadSeq:        12,
	}
	got, ok, err := cache.GetLatestFeedPosts(context.Background(), normalizedKey)
	if err != nil || !ok {
		t.Fatalf("GetLatestFeedPosts ok=%v err=%v, want cache hit", ok, err)
	}
	if len(got) != 1 || got[0].ID != "post_1" || len(got[0].Attachments) != 1 || got[0].Attachments[0].ID != "att_1" {
		t.Fatalf("cached posts = %+v, want original post and attachment", got)
	}

	got[0].ID = "caller_mutation"
	got[0].Attachments[0].ID = "caller_mutation_att"
	got, ok, err = cache.GetLatestFeedPosts(context.Background(), normalizedKey)
	if err != nil || !ok {
		t.Fatalf("GetLatestFeedPosts after caller mutation ok=%v err=%v, want cache hit", ok, err)
	}
	if got[0].ID != "post_1" || got[0].Attachments[0].ID != "att_1" {
		t.Fatalf("cached posts after caller mutation = %+v, want stored copy intact", got)
	}
}

func TestNormalizePagination(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		offset       int
		defaultLimit int
		maxLimit     int
		wantLimit    int
		wantOffset   int
	}{
		{name: "valid", limit: 10, offset: 5, defaultLimit: 30, maxLimit: 100, wantLimit: 10, wantOffset: 5},
		{name: "negative offset", limit: 10, offset: -5, defaultLimit: 30, maxLimit: 100, wantLimit: 10, wantOffset: 0},
		{name: "invalid limit", limit: 0, offset: 1, defaultLimit: 30, maxLimit: 100, wantLimit: 30, wantOffset: 1},
		{name: "over max", limit: 101, offset: 1, defaultLimit: 30, maxLimit: 100, wantLimit: 30, wantOffset: 1},
		{name: "fallback defaults", limit: 0, offset: 0, defaultLimit: 0, maxLimit: 0, wantLimit: 30, wantOffset: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLimit, gotOffset := NormalizePagination(tt.limit, tt.offset, tt.defaultLimit, tt.maxLimit)
			if gotLimit != tt.wantLimit || gotOffset != tt.wantOffset {
				t.Fatalf("NormalizePagination() = (%d, %d), want (%d, %d)", gotLimit, gotOffset, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}
