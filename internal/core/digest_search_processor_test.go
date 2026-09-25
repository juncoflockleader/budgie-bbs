package core

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestDigestEventsFeedSearchWatermarkAndRebuild(t *testing.T) {
	c := newCoreTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	thread := execPostSearchTestCommand(t, c, alice, proto.CmdCreateThread, proto.CreateThreadPayload{
		Board: "general",
		Title: "digest event stream",
		Body:  "ordinary post body",
	})
	posts, err := c.ListPosts(thread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one post", posts)
	}

	execPostSearchTestCommand(t, c, alice, proto.CmdCreateDigestDirectory, proto.CreateDigestDirectoryPayload{
		Board: "general",
		Kind:  "digest",
		Path:  "guides",
	})
	entry := execPostSearchTestCommand(t, c, alice, proto.CmdCuratePost, proto.CuratePostPayload{
		Post:  posts[0].ID,
		Kind:  "digest",
		Title: "curated digesttoken",
		Path:  "guides",
		Note:  "initial note",
	})
	execPostSearchTestCommand(t, c, alice, proto.CmdSetDigestEntryBody, proto.SetDigestEntryBodyPayload{
		Entry: entry.ID,
		Body:  "bodydigesttoken",
	})
	execPostSearchTestCommand(t, c, alice, proto.CmdCopyDigestPath, proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "digest",
		FromPath: "guides",
		ToPath:   "guides-copy",
	})
	execPostSearchTestCommand(t, c, alice, proto.CmdMoveDigestPath, proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "digest",
		FromPath: "guides-copy",
		ToPath:   "library",
	})
	execPostSearchTestCommand(t, c, alice, proto.CmdDeleteDigestPath, proto.DeleteDigestPathPayload{
		Board: "general",
		Kind:  "digest",
		Path:  "guides",
	})

	events, err := c.Replay(0, nil, 100)
	if err != nil {
		t.Fatalf("replay events: %v", err)
	}
	var sawUpsert, sawBody, sawCopy, sawMove, sawDelete bool
	var copiedEntryID string
	for _, evt := range events {
		switch payload := evt.Payload.(type) {
		case *proto.DigestEntryUpsertedPayload:
			if payload.ID == entry.ID && payload.Board == "general" && payload.Path == "guides" {
				sawUpsert = true
			}
		case *proto.DigestEntryBodySetPayload:
			if payload.ID == entry.ID && payload.Body == "bodydigesttoken" {
				sawBody = true
			}
		case *proto.DigestPathCopiedPayload:
			if payload.Board == "general" && payload.FromPath == "guides" && payload.ToPath == "guides-copy" && len(payload.EntryIDs) == 1 {
				sawCopy = true
				copiedEntryID = payload.EntryIDs[0]
			}
		case *proto.DigestPathMovedPayload:
			if payload.Board == "general" && payload.FromPath == "guides-copy" && payload.ToPath == "library" {
				sawMove = true
			}
		case *proto.DigestPathDeletedPayload:
			if payload.Board == "general" && payload.Path == "guides" {
				sawDelete = true
			}
		}
	}
	if !sawUpsert || !sawBody || !sawCopy || !sawMove || !sawDelete {
		t.Fatalf("digest events missing: sawUpsert=%v sawBody=%v sawCopy=%v sawMove=%v sawDelete=%v", sawUpsert, sawBody, sawCopy, sawMove, sawDelete)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	result, err := c.ProcessDigestSearchOnce(100)
	if err != nil {
		t.Fatalf("ProcessDigestSearchOnce: %v", err)
	}
	if result.FromSeq != 0 || result.AppliedSeq != head || result.HeadSeq != head || result.DigestChanges < 6 {
		t.Fatalf("digest processor result = %+v, want catch-up through head %d with digest changes", result, head)
	}

	found, err := c.SearchDigestEntries(alice, "", "digest", "library", "bodydigesttoken", 10, 0)
	if err != nil {
		t.Fatalf("search digest before delete: %v", err)
	}
	if len(found) != 1 || found[0].ID != copiedEntryID {
		t.Fatalf("search digest before delete = %+v, want copied entry %s", found, copiedEntryID)
	}

	if _, err := c.DB.Exec(`DELETE FROM digest_entries`); err != nil {
		t.Fatalf("delete digest entries: %v", err)
	}
	if _, err := c.DB.Exec(`DELETE FROM digest_directories`); err != nil {
		t.Fatalf("delete digest directories: %v", err)
	}
	missing, err := c.SearchDigestEntries(alice, "", "digest", "", "bodydigesttoken", 10, 0)
	if err != nil {
		t.Fatalf("search digest after delete: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("search digest after delete = %+v, want no entries", missing)
	}

	if err := c.RebuildProjectionsFromEventLog(0); err != nil {
		t.Fatalf("RebuildProjectionsFromEventLog: %v", err)
	}
	repaired, err := c.SearchDigestEntries(alice, "", "digest", "library", "bodydigesttoken", 10, 0)
	if err != nil {
		t.Fatalf("search digest after rebuild: %v", err)
	}
	if len(repaired) != 1 || repaired[0].ID != copiedEntryID {
		t.Fatalf("search digest after rebuild = %+v, want copied entry %s", repaired, copiedEntryID)
	}
	original, err := c.ListDigestEntries("general", "digest", "guides", 10, 0)
	if err != nil {
		t.Fatalf("list original path after rebuild: %v", err)
	}
	if len(original) != 0 {
		t.Fatalf("original guides path after rebuild = %+v, want deleted", original)
	}
	tree, err := c.ListDigestPathTree("general", "digest")
	if err != nil {
		t.Fatalf("digest path tree after rebuild: %v", err)
	}
	hasLibrary := false
	hasGuides := false
	for _, node := range tree {
		if node.Path == "guides" {
			hasGuides = true
		}
		if node.Path == "library" {
			hasLibrary = true
		}
	}
	if !hasLibrary || hasGuides {
		t.Fatalf("digest path tree after rebuild = %+v, want library directory and deleted guides", tree)
	}
}
