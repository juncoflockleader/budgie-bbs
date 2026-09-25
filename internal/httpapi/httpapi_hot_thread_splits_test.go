package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type hotThreadSplitHTTPDTO struct {
	ThreadID string `json:"threadId"`
	Shards   int    `json:"shards"`
}

type hotThreadSplitLagDTO struct {
	Kind            string `json:"kind"`
	Key             string `json:"key"`
	TailOffset      int64  `json:"tailOffset"`
	CommittedOffset int64  `json:"committedOffset"`
	Lag             int64  `json:"lag"`
}

type hotThreadSplitsHTTPResponse struct {
	Local              bool                    `json:"local"`
	Persistent         bool                    `json:"persistent"`
	Splits             []hotThreadSplitHTTPDTO `json:"splits"`
	BlockingPartitions []hotThreadSplitLagDTO  `json:"blockingPartitions"`
	Error              *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

func TestHTTPAdminHotThreadSplitsLifecycle(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	forbidden := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/admin/hot-thread-splits/thr_hot", aliceToken, map[string]int{
		"shards": 4,
	}, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-admin split update forbidden, got %d response=%+v", status, forbidden)
	}

	invalid := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/admin/hot-thread-splits/thr_hot", adminToken, map[string]int{
		"shards": 1,
	}, &invalid); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected invalid shard count rejected, got %d response=%+v", status, invalid)
	}

	initial := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/admin/hot-thread-splits", adminToken, nil, &initial); status != http.StatusOK {
		t.Fatalf("list initial hot-thread splits status: %d response=%+v", status, initial)
	}
	if !initial.Local || !initial.Persistent || len(initial.Splits) != 0 {
		t.Fatalf("expected empty local split map, got %+v", initial)
	}

	set := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/admin/hot-thread-splits/thr_hot", adminToken, map[string]int{
		"shards": 4,
	}, &set); status != http.StatusOK {
		t.Fatalf("set hot-thread split status: %d response=%+v", status, set)
	}
	assertHTTPHotThreadSplit(t, set, "thr_hot", 4)
	if got := c.HotThreadSplits()["thr_hot"]; got != 4 {
		t.Fatalf("core hot-thread split = %d, want 4", got)
	}

	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/admin/hot-thread-splits/thr_other", adminToken, map[string]int{
		"shards": 2,
	}, &set); status != http.StatusOK {
		t.Fatalf("set second hot-thread split status: %d response=%+v", status, set)
	}
	if len(set.Splits) != 2 || set.Splits[0].ThreadID != "thr_hot" || set.Splits[1].ThreadID != "thr_other" {
		t.Fatalf("expected sorted split readback, got %+v", set.Splits)
	}

	deleted := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/admin/hot-thread-splits/thr_hot", adminToken, nil, &deleted); status != http.StatusOK {
		t.Fatalf("delete hot-thread split status: %d response=%+v", status, deleted)
	}
	if _, ok := c.HotThreadSplits()["thr_hot"]; ok {
		t.Fatalf("expected thr_hot split removed, got %+v", c.HotThreadSplits())
	}
	if len(deleted.Splits) != 1 || deleted.Splits[0].ThreadID != "thr_other" || deleted.Splits[0].Shards != 2 {
		t.Fatalf("expected only thr_other after delete, got %+v", deleted.Splits)
	}
}

func TestHTTPAdminHotThreadSplitChangeRequiresDrainedCommandLog(t *testing.T) {
	ctx := context.Background()
	commandLog := core.NewMemoryCommandLog()
	c, err := core.New(t.TempDir()+"/hot-split-admin-lag.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })
	handler := httpapi.New(c, []byte("test-secret")).Handler()

	adminToken := registerUser(t, handler, "admin")
	admin, err := c.UserByName("admin")
	if err != nil || admin == nil {
		t.Fatalf("admin lookup: %v", err)
	}
	if err := c.PersistHotThreadSplit("thr_hot", 4); err != nil {
		t.Fatalf("persist hot split: %v", err)
	}
	reply := c.ExecCmd(ctx, admin, proto.CmdAppendPost, []byte(`{"thread":"thr_hot","body":"queued reply"}`), "cid-hot-admin-lag")
	if reply.Err != nil {
		t.Fatalf("enqueue split append: %+v", reply.Err)
	}

	blocked := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/admin/hot-thread-splits/thr_hot", adminToken, nil, &blocked); status != http.StatusConflict {
		t.Fatalf("expected rollback blocked by command lag, got %d response=%+v", status, blocked)
	}
	if blocked.Error == nil || blocked.Error.Code != "conflict" || !blocked.Error.Retryable {
		t.Fatalf("expected retryable conflict, got %+v", blocked.Error)
	}
	if len(blocked.BlockingPartitions) != 1 || blocked.BlockingPartitions[0].Lag != 1 {
		t.Fatalf("expected one blocking lag partition, got %+v", blocked.BlockingPartitions)
	}
	if _, ok := c.HotThreadSplits()["thr_hot"]; !ok {
		t.Fatalf("split should remain configured after blocked rollback")
	}

	forced := hotThreadSplitsHTTPResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/admin/hot-thread-splits/thr_hot?force=1", adminToken, nil, &forced); status != http.StatusOK {
		t.Fatalf("expected forced rollback to succeed, got %d response=%+v", status, forced)
	}
	if _, ok := c.HotThreadSplits()["thr_hot"]; ok {
		t.Fatalf("expected forced rollback to remove split, got %+v", c.HotThreadSplits())
	}
	if forced.Error != nil || len(forced.Splits) != 0 {
		t.Fatalf("unexpected forced rollback response: %+v", forced)
	}
}

func assertHTTPHotThreadSplit(t *testing.T, resp hotThreadSplitsHTTPResponse, threadID string, shards int) {
	t.Helper()
	for _, split := range resp.Splits {
		if split.ThreadID == threadID {
			if split.Shards != shards {
				t.Fatalf("split %s shards = %d, want %d", threadID, split.Shards, shards)
			}
			return
		}
	}
	t.Fatalf("split %s not found in %+v", threadID, resp.Splits)
}
