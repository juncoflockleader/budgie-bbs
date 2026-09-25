package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestAsyncPostSearchProcessorIndexesDurableEvents(t *testing.T) {
	c := newCoreTestCore(t, WithAsyncPostSearch())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	thread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "async post search",
		Body:  "alphapostsearchtoken",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one post", posts)
	}

	before, err := c.SearchReadablePosts(alice, "alphapostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search before processor: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("search before processor = %+v, want no command-time index writes", before)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	result, err := c.ProcessPostSearchOnce(100)
	if err != nil {
		t.Fatalf("ProcessPostSearchOnce: %v", err)
	}
	if result.FromSeq != 0 || result.AppliedSeq != head || result.HeadSeq != head || result.Indexed == 0 {
		t.Fatalf("processor result = %+v, want catch-up through head %d", result, head)
	}
	after, err := c.SearchReadablePosts(alice, "alphapostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search after processor: %v", err)
	}
	if len(after) != 1 || after[0].ID != posts[0].ID {
		t.Fatalf("search after processor = %+v, want indexed post %s", after, posts[0].ID)
	}

	execPostSearchTestCommand(t, c, alice, proto.CmdEditPost, proto.EditPostPayload{
		Post: posts[0].ID,
		Body: "betapostsearchtoken",
	})
	staleEdit, err := c.SearchReadablePosts(alice, "betapostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search before edit catch-up: %v", err)
	}
	if len(staleEdit) != 0 {
		t.Fatalf("search before edit catch-up = %+v, want edit lag", staleEdit)
	}
	editResult, err := c.ProcessPostSearchOnce(100)
	if err != nil {
		t.Fatalf("ProcessPostSearchOnce edit: %v", err)
	}
	if editResult.FromSeq != head || editResult.Indexed == 0 {
		t.Fatalf("edit processor result = %+v, want incremental index work after %d", editResult, head)
	}
	edited, err := c.SearchReadablePosts(alice, "betapostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search after edit processor: %v", err)
	}
	if len(edited) != 1 || edited[0].ID != posts[0].ID {
		t.Fatalf("search after edit processor = %+v, want edited post %s", edited, posts[0].ID)
	}

	applied, err := c.DerivedViewAppliedSeq(projections.DerivedViewPostSearch)
	if err != nil {
		t.Fatalf("post search watermark: %v", err)
	}
	finalHead, err := c.Head()
	if err != nil {
		t.Fatalf("final head: %v", err)
	}
	if applied != finalHead {
		t.Fatalf("post search watermark = %d, want final head %d", applied, finalHead)
	}
}

func TestExternalPostSearchIndexProcessesEventsAndBackfills(t *testing.T) {
	index := newTestPostSearchIndex()
	c := newCoreTestCore(t, WithAsyncPostSearch(), WithPostSearchIndex(index))
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
	execPostSearchTestCommand(t, c, admin, proto.CmdCreateBoard, proto.CreateBoardPayload{
		ID:   "private_search",
		Name: "Private Search",
	})

	thread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "private_search",
		Title: "external post search",
		Body:  "externalpostsearchtoken",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one post", posts)
	}
	execPostSearchTestCommand(t, c, admin, proto.CmdSetBoardSettings, proto.SetBoardSettingsPayload{
		Board:          "private_search",
		MemberReadMode: postSearchBoolPtr(true),
	})

	before, err := c.SearchReadablePosts(admin, "externalpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search before processor: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("search before processor = %+v, want no external index writes", before)
	}

	result, err := c.ProcessPostSearchOnce(100)
	if err != nil {
		t.Fatalf("ProcessPostSearchOnce: %v", err)
	}
	if result.Indexed == 0 {
		t.Fatalf("processor result = %+v, want external index work", result)
	}
	adminSearch, err := c.SearchReadablePosts(admin, "externalpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("admin search after processor: %v", err)
	}
	if len(adminSearch) != 1 || adminSearch[0].ID != posts[0].ID {
		t.Fatalf("admin search = %+v, want indexed post %s", adminSearch, posts[0].ID)
	}
	bobSearch, err := c.SearchReadablePosts(bob, "externalpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("bob search after processor: %v", err)
	}
	if len(bobSearch) != 0 {
		t.Fatalf("bob search = %+v, want member-read board filtered after external index hit", bobSearch)
	}

	execPostSearchTestCommand(t, c, alice, proto.CmdEditPost, proto.EditPostPayload{
		Post: posts[0].ID,
		Body: "rebuiltpostsearchtoken",
	})
	if stale, err := c.SearchReadablePosts(admin, "rebuiltpostsearchtoken", "", 10); err != nil {
		t.Fatalf("stale edit search: %v", err)
	} else if len(stale) != 0 {
		t.Fatalf("stale edit search = %+v, want edit lag", stale)
	}
	if _, err := c.ProcessPostSearchOnce(100); err != nil {
		t.Fatalf("ProcessPostSearchOnce edit: %v", err)
	}
	edited, err := c.SearchReadablePosts(admin, "rebuiltpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("edited search: %v", err)
	}
	if len(edited) != 1 || edited[0].ID != posts[0].ID {
		t.Fatalf("edited search = %+v, want post %s", edited, posts[0].ID)
	}

	index.clearOnly()
	empty, err := c.SearchReadablePosts(admin, "rebuiltpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("search after index clear: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("search after index clear = %+v, want missing external index", empty)
	}
	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{projections.DerivedViewPostSearch}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog search.posts: %v", err)
	}
	if backfill.HeadSeq <= 0 || len(backfill.Views) != 1 || backfill.Views[0] != projections.DerivedViewPostSearch {
		t.Fatalf("backfill result = %+v", backfill)
	}
	repaired, err := c.SearchReadablePosts(admin, "rebuiltpostsearchtoken", "", 10)
	if err != nil {
		t.Fatalf("repaired search: %v", err)
	}
	if len(repaired) != 1 || repaired[0].ID != posts[0].ID {
		t.Fatalf("repaired search = %+v, want post %s", repaired, posts[0].ID)
	}
	applied, err := c.DerivedViewAppliedSeq(projections.DerivedViewPostSearch)
	if err != nil {
		t.Fatalf("post search watermark: %v", err)
	}
	if applied != backfill.HeadSeq {
		t.Fatalf("post search watermark = %d, want %d", applied, backfill.HeadSeq)
	}
}

type testPostSearchIndex struct {
	mu   sync.Mutex
	docs map[string]projections.PostSearchDocument
}

func newTestPostSearchIndex() *testPostSearchIndex {
	return &testPostSearchIndex{docs: map[string]projections.PostSearchDocument{}}
}

func (i *testPostSearchIndex) UpsertPost(_ context.Context, doc projections.PostSearchDocument) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if doc.ID == "" {
		doc.ID = doc.PostID
	}
	i.docs[doc.ID] = doc
	return nil
}

func (i *testPostSearchIndex) DeletePost(_ context.Context, postID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.docs, postID)
	return nil
}

func (i *testPostSearchIndex) Clear(context.Context) error {
	i.clearOnly()
	return nil
}

func (i *testPostSearchIndex) clearOnly() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.docs = map[string]projections.PostSearchDocument{}
}

func (i *testPostSearchIndex) Search(_ context.Context, query, boardID string, limit int) ([]string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	query = strings.ToLower(strings.TrimSpace(query))
	boardID = strings.TrimSpace(boardID)
	var docs []projections.PostSearchDocument
	for _, doc := range i.docs {
		if boardID != "" && doc.BoardID != boardID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(doc.Body+" "+doc.Author), query) {
			continue
		}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(a, b int) bool {
		if docs[a].CreatedSeq == docs[b].CreatedSeq {
			return docs[a].ID < docs[b].ID
		}
		return docs[a].CreatedSeq > docs[b].CreatedSeq
	})
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if len(docs) > limit {
		docs = docs[:limit]
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return ids, nil
}

func postSearchBoolPtr(v bool) *bool { return &v }

func execPostSearchTestCommand(t *testing.T, c *Core, actor *projections.User, name proto.CommandName, payload any) *proto.AckResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	reply := c.ExecCmd(context.Background(), actor, name, raw, "")
	if reply.Err != nil {
		t.Fatalf("%s: %s (%s)", name, reply.Err.Message, reply.Err.Code)
	}
	if reply.Result == nil {
		t.Fatalf("%s returned nil result", name)
	}
	return reply.Result
}
