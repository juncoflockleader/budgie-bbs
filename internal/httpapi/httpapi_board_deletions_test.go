package httpapi_test

import (
	"net/http"
	"testing"
)

type boardDeletedPostsResponse struct {
	Posts []struct {
		PostID        string `json:"postId"`
		ThreadID      string `json:"threadId"`
		BoardID       string `json:"boardId"`
		BoardName     string `json:"boardName"`
		ThreadTitle   string `json:"threadTitle"`
		DeletedByName string `json:"deletedByName"`
		Reason        string `json:"reason"`
		Kind          string `json:"kind"`
		Post          struct {
			ID          string `json:"id"`
			Author      string `json:"author"`
			Body        string `json:"body"`
			Redacted    bool   `json:"redacted"`
			ThreadTitle string `json:"threadTitle"`
		} `json:"post"`
	} `json:"posts"`
}

func TestHTTPBoardDeletedPostsRecycleAndJunkBins(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	authorThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", bobToken, map[string]string{
		"title": "Author delete",
		"body":  "self-deleted body",
	}, &authorThread); status != http.StatusCreated {
		t.Fatalf("create author-delete thread status: %d error=%+v", status, authorThread.Error)
	}
	authorPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+authorThread.Result.ID+"/posts", bobToken, nil, &authorPosts); status != http.StatusOK {
		t.Fatalf("list author-delete posts status: %d", status)
	}
	if len(authorPosts.Posts) != 1 {
		t.Fatalf("expected one author-delete post, got %+v", authorPosts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/posts/"+authorPosts.Posts[0].ID, bobToken, map[string]string{
		"reason": "author cleanup",
	}, &ackResponse{}); status != http.StatusCreated {
		t.Fatalf("author redact status: %d", status)
	}

	moderatorThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", carolToken, map[string]string{
		"title": "Moderator delete",
		"body":  "moderator-deleted body",
	}, &moderatorThread); status != http.StatusCreated {
		t.Fatalf("create moderator-delete thread status: %d error=%+v", status, moderatorThread.Error)
	}
	moderatorPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+moderatorThread.Result.ID+"/posts", adminToken, nil, &moderatorPosts); status != http.StatusOK {
		t.Fatalf("list moderator-delete posts status: %d", status)
	}
	if len(moderatorPosts.Posts) != 1 {
		t.Fatalf("expected one moderator-delete post, got %+v", moderatorPosts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/posts/"+moderatorPosts.Posts[0].ID, adminToken, map[string]string{
		"reason": "moderator cleanup",
	}, &ackResponse{}); status != http.StatusCreated {
		t.Fatalf("moderator redact status: %d", status)
	}

	forbidden := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=junk", bobToken, nil, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected ordinary user to be forbidden from deleted-post bin, got status %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/general/members/carol", adminToken, map[string]bool{
		"canModeratePosts": true,
	}, &ackResponse{}); status != http.StatusCreated {
		t.Fatalf("grant delegated post moderation status: %d", status)
	}
	delegated := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=recycle", carolToken, nil, &delegated); status != http.StatusOK {
		t.Fatalf("expected delegated post moderator to read deleted-post bin, got status %d", status)
	}

	junk := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=junk", adminToken, nil, &junk); status != http.StatusOK {
		t.Fatalf("list junk bin status: %d", status)
	}
	if len(junk.Posts) != 1 || junk.Posts[0].PostID != authorPosts.Posts[0].ID || junk.Posts[0].Kind != "junk" {
		t.Fatalf("expected author deletion in junk bin, got %+v", junk.Posts)
	}
	if junk.Posts[0].DeletedByName != "bob" || junk.Posts[0].Reason != "author cleanup" || junk.Posts[0].Post.Body != "self-deleted body" || !junk.Posts[0].Post.Redacted {
		t.Fatalf("unexpected junk metadata: %+v", junk.Posts[0])
	}

	recycle := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=recycle", adminToken, nil, &recycle); status != http.StatusOK {
		t.Fatalf("list recycle bin status: %d", status)
	}
	if len(recycle.Posts) != 1 || recycle.Posts[0].PostID != moderatorPosts.Posts[0].ID || recycle.Posts[0].Kind != "recycle" {
		t.Fatalf("expected moderator deletion in recycle bin, got %+v", recycle.Posts)
	}
	if recycle.Posts[0].DeletedByName != "admin" || recycle.Posts[0].Reason != "moderator cleanup" || recycle.Posts[0].Post.Body != "moderator-deleted body" {
		t.Fatalf("unexpected recycle metadata: %+v", recycle.Posts[0])
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+moderatorPosts.Posts[0].ID+"/restore", adminToken, nil, &ackResponse{}); status != http.StatusCreated {
		t.Fatalf("restore moderator-deleted post status: %d", status)
	}
	recycle = boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=recycle", adminToken, nil, &recycle); status != http.StatusOK {
		t.Fatalf("list recycle bin after restore status: %d", status)
	}
	if len(recycle.Posts) != 0 {
		t.Fatalf("expected restored post to leave recycle bin, got %+v", recycle.Posts)
	}
}

func TestHTTPBoardDeletedPostRangeActionsAndJunkClear(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")

	_, firstPost := createRootPostForDeletionTest(t, handler, bobToken, "Range one", "first range body")
	_, secondPost := createRootPostForDeletionTest(t, handler, bobToken, "Range two", "second range body")

	denied := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/posts/range-delete", bobToken, map[string]any{
		"posts": []string{firstPost, secondPost},
	}, &denied); status != http.StatusForbidden {
		t.Fatalf("expected ordinary author range-delete to be forbidden, got status %d error=%+v", status, denied.Error)
	}

	rangeDelete := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/posts/range-delete", adminToken, map[string]any{
		"posts":  []string{firstPost, secondPost},
		"reason": "range cleanup",
	}, &rangeDelete); status != http.StatusCreated {
		t.Fatalf("range delete status: %d error=%+v", status, rangeDelete.Error)
	}
	if rangeDelete.Result == nil || rangeDelete.Result.ID != "2" {
		t.Fatalf("expected range-delete count 2, got %+v", rangeDelete.Result)
	}

	recycle := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=recycle", adminToken, nil, &recycle); status != http.StatusOK {
		t.Fatalf("list recycle after range delete status: %d", status)
	}
	if !deletedPostIDs(recycle)[firstPost] || !deletedPostIDs(recycle)[secondPost] {
		t.Fatalf("expected both range-deleted posts in recycle bin, got %+v", recycle.Posts)
	}
	for _, post := range recycle.Posts {
		if post.Reason != "range cleanup" || post.Kind != "recycle" {
			t.Fatalf("unexpected range recycle metadata: %+v", post)
		}
	}

	restore := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/deleted/range-restore", adminToken, map[string]any{
		"posts": []string{firstPost},
	}, &restore); status != http.StatusCreated {
		t.Fatalf("range restore status: %d error=%+v", status, restore.Error)
	}
	if restore.Result == nil || restore.Result.ID != "1" {
		t.Fatalf("expected range-restore count 1, got %+v", restore.Result)
	}
	recycle = boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=recycle", adminToken, nil, &recycle); status != http.StatusOK {
		t.Fatalf("list recycle after range restore status: %d", status)
	}
	recycleIDs := deletedPostIDs(recycle)
	if recycleIDs[firstPost] || !recycleIDs[secondPost] {
		t.Fatalf("expected only second post to remain in recycle bin, got %+v", recycle.Posts)
	}

	_, firstJunk := createRootPostForDeletionTest(t, handler, bobToken, "Junk one", "first junk body")
	_, secondJunk := createRootPostForDeletionTest(t, handler, bobToken, "Junk two", "second junk body")
	for _, postID := range []string{firstJunk, secondJunk} {
		if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/posts/"+postID, bobToken, map[string]string{
			"reason": "self cleanup",
		}, &ackResponse{}); status != http.StatusCreated {
			t.Fatalf("author junk delete %s status: %d", postID, status)
		}
	}

	clearOne := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/deleted/junk/clear", adminToken, map[string]any{
		"posts": []string{firstJunk},
	}, &clearOne); status != http.StatusCreated {
		t.Fatalf("selected junk clear status: %d error=%+v", status, clearOne.Error)
	}
	if clearOne.Result == nil || clearOne.Result.ID != "1" {
		t.Fatalf("expected selected junk clear count 1, got %+v", clearOne.Result)
	}
	junk := boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=junk", adminToken, nil, &junk); status != http.StatusOK {
		t.Fatalf("list junk after selected clear status: %d", status)
	}
	junkIDs := deletedPostIDs(junk)
	if junkIDs[firstJunk] || !junkIDs[secondJunk] {
		t.Fatalf("expected only second junk post after selected clear, got %+v", junk.Posts)
	}

	clearAll := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/deleted/junk/clear", adminToken, map[string]any{}, &clearAll); status != http.StatusCreated {
		t.Fatalf("whole-board junk clear status: %d error=%+v", status, clearAll.Error)
	}
	if clearAll.Result == nil || clearAll.Result.ID != "1" {
		t.Fatalf("expected whole-board junk clear count 1, got %+v", clearAll.Result)
	}
	junk = boardDeletedPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/deleted?kind=junk", adminToken, nil, &junk); status != http.StatusOK {
		t.Fatalf("list junk after clear all status: %d", status)
	}
	if len(junk.Posts) != 0 {
		t.Fatalf("expected junk bin to be empty after clear all, got %+v", junk.Posts)
	}
}

func createRootPostForDeletionTest(t *testing.T, handler http.Handler, token, title, body string) (string, string) {
	t.Helper()
	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": title,
		"body":  body,
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create thread %q status: %d error=%+v", title, status, thread.Error)
	}
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", token, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts for %q status: %d", title, status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected one post for %q, got %+v", title, posts.Posts)
	}
	return thread.Result.ID, posts.Posts[0].ID
}

func deletedPostIDs(resp boardDeletedPostsResponse) map[string]bool {
	out := map[string]bool{}
	for _, post := range resp.Posts {
		out[post.PostID] = true
	}
	return out
}
