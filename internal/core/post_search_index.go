package core

import (
	"context"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type PostSearchIndex interface {
	UpsertPost(context.Context, projections.PostSearchDocument) error
	DeletePost(context.Context, string) error
	Clear(context.Context) error
	Search(context.Context, string, string, int) ([]string, error)
}

func (c *Core) rebuildExternalPostSearchIndex(ctx context.Context) (int, error) {
	if c == nil || c.postSearchIndex == nil {
		return 0, nil
	}
	if err := c.postSearchIndex.Clear(ctx); err != nil {
		return 0, fmt.Errorf("clear post search index: %w", err)
	}
	docs, err := projections.ListPostSearchDocuments(c.DB, "", 0)
	if err != nil {
		return 0, err
	}
	for _, doc := range docs {
		if err := c.postSearchIndex.UpsertPost(ctx, doc); err != nil {
			return 0, fmt.Errorf("upsert post search document %s: %w", doc.PostID, err)
		}
	}
	return len(docs), nil
}
