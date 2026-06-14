package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type projectionMetaDTO struct {
	Consistency string `json:"consistency"`
	View        string `json:"view"`
	HeadSeq     int64  `json:"headSeq"`
	AppliedSeq  int64  `json:"appliedSeq"`
	LagEvents   int64  `json:"lagEvents"`
}

type projectionFreshnessErrorDTO struct {
	Error struct {
		Code         string `json:"code"`
		Message      string `json:"message"`
		Retryable    bool   `json:"retryable"`
		RetryAfterMs int64  `json:"retryAfterMs"`
	} `json:"error"`
	Meta   projectionMetaDTO `json:"meta"`
	MinSeq int64             `json:"minSeq"`
}

func TestGlobalProjectionReadsCarryConsistencyMeta(t *testing.T) {
	c, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "projection meta thread",
		"body":  "projection freshness is explicit",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, create.Error)
	}

	statsRec := getWithToken(t, handler, "/api/v1/stats/community", token)
	if statsRec.Code != http.StatusOK {
		t.Fatalf("stats status: %d body=%s", statsRec.Code, statsRec.Body.String())
	}
	assertProjectionHeaders(t, statsRec, core.DerivedViewCommunityStats)
	var stats struct {
		HeadSeq int64             `json:"headSeq"`
		Meta    projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(statsRec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	assertProjectionMeta(t, stats.Meta)
	if stats.Meta.AppliedSeq != stats.HeadSeq {
		t.Fatalf("stats applied seq should track stats head seq: meta=%+v statsHead=%d", stats.Meta, stats.HeadSeq)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	if err := c.RecordDerivedViewApplied(core.DerivedViewBoardRankings, staleApplied); err != nil {
		t.Fatalf("record stale board ranking watermark: %v", err)
	}

	rankingsRec := getWithToken(t, handler, "/api/v1/rankings/boards", token)
	if rankingsRec.Code != http.StatusOK {
		t.Fatalf("rankings status: %d body=%s", rankingsRec.Code, rankingsRec.Body.String())
	}
	assertProjectionHeaders(t, rankingsRec, core.DerivedViewBoardRankings)
	var rankings struct {
		Boards []json.RawMessage `json:"boards"`
		Meta   projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(rankingsRec.Body.Bytes(), &rankings); err != nil {
		t.Fatalf("decode rankings: %v", err)
	}
	assertProjectionMeta(t, rankings.Meta)
	if rankings.Meta.AppliedSeq != staleApplied {
		t.Fatalf("ranking applied seq = %d, want %d", rankings.Meta.AppliedSeq, staleApplied)
	}
	if rankings.Meta.LagEvents != head-staleApplied {
		t.Fatalf("ranking lag = %d, want %d", rankings.Meta.LagEvents, head-staleApplied)
	}

	if err := c.RecordDerivedViewApplied(core.DerivedViewPostSearch, staleApplied); err != nil {
		t.Fatalf("record stale post search watermark: %v", err)
	}
	searchRec := getWithToken(t, handler, "/api/v1/search?q=projection", token)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status: %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	assertProjectionHeaders(t, searchRec, core.DerivedViewPostSearch)
	var search struct {
		Posts []json.RawMessage `json:"posts"`
		Meta  projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	assertProjectionMeta(t, search.Meta)
	if search.Meta.AppliedSeq != staleApplied {
		t.Fatalf("post search applied seq = %d, want %d", search.Meta.AppliedSeq, staleApplied)
	}
	if search.Meta.LagEvents != head-staleApplied {
		t.Fatalf("post search lag = %d, want %d", search.Meta.LagEvents, head-staleApplied)
	}

	if err := c.RecordDerivedViewApplied(core.DerivedViewDigestSearch, staleApplied); err != nil {
		t.Fatalf("record stale digest search watermark: %v", err)
	}
	digestRec := getWithToken(t, handler, "/api/v1/digest/search?q=projection", token)
	if digestRec.Code != http.StatusOK {
		t.Fatalf("digest search status: %d body=%s", digestRec.Code, digestRec.Body.String())
	}
	assertProjectionHeaders(t, digestRec, core.DerivedViewDigestSearch)
	var digest struct {
		Entries []json.RawMessage `json:"entries"`
		Meta    projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(digestRec.Body.Bytes(), &digest); err != nil {
		t.Fatalf("decode digest search: %v", err)
	}
	assertProjectionMeta(t, digest.Meta)
	if digest.Meta.AppliedSeq != staleApplied {
		t.Fatalf("digest search applied seq = %d, want %d", digest.Meta.AppliedSeq, staleApplied)
	}
	if digest.Meta.LagEvents != head-staleApplied {
		t.Fatalf("digest search lag = %d, want %d", digest.Meta.LagEvents, head-staleApplied)
	}
}

func TestProjectionReadsCanRequireMinimumSequence(t *testing.T) {
	c, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "read your writes projection",
		"body":  "regional clients can wait for projection freshness",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, create.Error)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	if err := c.RecordDerivedViewApplied(core.DerivedViewBoardRankings, staleApplied); err != nil {
		t.Fatalf("record stale board ranking watermark: %v", err)
	}

	minSeq := strconv.FormatInt(head, 10)
	staleRec := getWithTokenAndHeaders(t, handler, "/api/v1/rankings/boards", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if staleRec.Code != 425 {
		t.Fatalf("stale projection status: %d body=%s", staleRec.Code, staleRec.Body.String())
	}
	assertProjectionHeaders(t, staleRec, core.DerivedViewBoardRankings)
	if got := staleRec.Header().Get("X-Budgie-Min-Seq"); got != minSeq {
		t.Fatalf("X-Budgie-Min-Seq = %q, want %q", got, minSeq)
	}
	if got := staleRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "stale" {
		t.Fatalf("X-Budgie-Read-Your-Writes = %q, want stale", got)
	}
	if got := staleRec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	var stale projectionFreshnessErrorDTO
	if err := json.Unmarshal(staleRec.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode stale response: %v", err)
	}
	if stale.Error.Code != proto.ErrProjectionStale || !stale.Error.Retryable || stale.Error.RetryAfterMs != 1000 {
		t.Fatalf("unexpected stale error: %+v", stale.Error)
	}
	if stale.MinSeq != head {
		t.Fatalf("minSeq = %d, want %d", stale.MinSeq, head)
	}
	assertStaleProjectionMeta(t, stale.Meta, staleApplied, head)

	if err := c.RecordDerivedViewApplied(core.DerivedViewBoardRankings, head); err != nil {
		t.Fatalf("record fresh board ranking watermark: %v", err)
	}
	freshRec := getWithTokenAndHeaders(t, handler, "/api/v1/rankings/boards", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if freshRec.Code != http.StatusOK {
		t.Fatalf("fresh projection status: %d body=%s", freshRec.Code, freshRec.Body.String())
	}
	assertProjectionHeaders(t, freshRec, core.DerivedViewBoardRankings)
	if got := freshRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("fresh X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}

	queryRec := getWithToken(t, handler, "/api/v1/rankings/boards?minSeq="+minSeq, token)
	if queryRec.Code != http.StatusOK {
		t.Fatalf("query minSeq status: %d body=%s", queryRec.Code, queryRec.Body.String())
	}
	if got := queryRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("query X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}

	invalidRec := getWithToken(t, handler, "/api/v1/rankings/boards?minSeq=not-a-seq", token)
	if invalidRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid minSeq status: %d body=%s", invalidRec.Code, invalidRec.Body.String())
	}
	var invalid ackResponse
	if err := json.Unmarshal(invalidRec.Body.Bytes(), &invalid); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if invalid.Error == nil || invalid.Error.Code != proto.ErrValidationFailed || invalid.Error.Retryable {
		t.Fatalf("invalid minSeq error = %+v", invalid.Error)
	}
}

func TestProjectionLagChaosBackfillRecoversReadYourWrites(t *testing.T) {
	c, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "projection lag chaos",
		"body":  "derived views can be repaired from the event log",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, create.Error)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	staleApplied := head - 1
	if staleApplied < 0 {
		staleApplied = 0
	}
	if err := c.RecordDerivedViewApplied(core.DerivedViewBoardRankings, staleApplied); err != nil {
		t.Fatalf("record stale board ranking watermark: %v", err)
	}

	minSeq := strconv.FormatInt(head, 10)
	staleRec := getWithTokenAndHeaders(t, handler, "/api/v1/rankings/boards", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if staleRec.Code != 425 {
		t.Fatalf("stale projection status: %d body=%s", staleRec.Code, staleRec.Body.String())
	}
	assertProjectionHeaders(t, staleRec, core.DerivedViewBoardRankings)
	if got := staleRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "stale" {
		t.Fatalf("stale X-Budgie-Read-Your-Writes = %q, want stale", got)
	}
	var stale projectionFreshnessErrorDTO
	if err := json.Unmarshal(staleRec.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode stale response: %v", err)
	}
	assertStaleProjectionMeta(t, stale.Meta, staleApplied, head)

	postsRec := getWithTokenAndHeaders(t, handler, "/api/v1/threads/"+create.Result.ID+"/posts", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if postsRec.Code != http.StatusOK {
		t.Fatalf("canonical posts should remain readable during ranking lag: %d body=%s", postsRec.Code, postsRec.Body.String())
	}
	if got := postsRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("canonical posts X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}

	backfill, err := c.BackfillDerivedViewsFromEventLog([]string{core.DerivedViewBoardRankings}, 0)
	if err != nil {
		t.Fatalf("BackfillDerivedViewsFromEventLog rankings.boards: %v", err)
	}
	if backfill.HeadSeq != head || len(backfill.Views) != 1 || backfill.Views[0] != core.DerivedViewBoardRankings {
		t.Fatalf("backfill result = %+v, want rankings.boards through head %d", backfill, head)
	}

	recoveredRec := getWithTokenAndHeaders(t, handler, "/api/v1/rankings/boards", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if recoveredRec.Code != http.StatusOK {
		t.Fatalf("recovered projection status: %d body=%s", recoveredRec.Code, recoveredRec.Body.String())
	}
	assertProjectionHeaders(t, recoveredRec, core.DerivedViewBoardRankings)
	if got := recoveredRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("recovered X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}
	var recovered struct {
		Boards []json.RawMessage `json:"boards"`
		Meta   projectionMetaDTO `json:"meta"`
	}
	if err := json.Unmarshal(recoveredRec.Body.Bytes(), &recovered); err != nil {
		t.Fatalf("decode recovered response: %v", err)
	}
	if recovered.Meta.AppliedSeq != head || recovered.Meta.LagEvents != 0 {
		t.Fatalf("recovered meta = %+v, want applied head %d with zero lag", recovered.Meta, head)
	}
	if len(recovered.Boards) == 0 {
		t.Fatalf("recovered rankings returned no rows: %+v", recovered)
	}
}

func TestCanonicalReadsCanRequireMinimumSequence(t *testing.T) {
	c, handler := setupHTTPTestServer(t)
	token := registerUser(t, handler, "alice")

	create := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", token, map[string]string{
		"title": "canonical read your writes",
		"body":  "thread and post reads should wait for local durability",
	}, &create); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, create.Error)
	}
	if create.Result == nil || create.Result.ID == "" {
		t.Fatalf("create result = %+v, want thread id", create.Result)
	}

	head, err := c.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	futureMinSeq := strconv.FormatInt(head+1, 10)
	staleBoardRec := getWithTokenAndHeaders(t, handler, "/api/v1/boards/missing", token, map[string]string{
		"X-Budgie-Min-Seq": futureMinSeq,
	})
	if staleBoardRec.Code != 425 {
		t.Fatalf("stale canonical board read status: %d body=%s", staleBoardRec.Code, staleBoardRec.Body.String())
	}
	assertProjectionHeaders(t, staleBoardRec, "canonical")
	if got := staleBoardRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "stale" {
		t.Fatalf("stale board X-Budgie-Read-Your-Writes = %q, want stale", got)
	}

	staleRec := getWithTokenAndHeaders(t, handler, "/api/v1/threads/missing", token, map[string]string{
		"X-Budgie-Min-Seq": futureMinSeq,
	})
	if staleRec.Code != 425 {
		t.Fatalf("stale canonical read status: %d body=%s", staleRec.Code, staleRec.Body.String())
	}
	assertProjectionHeaders(t, staleRec, "canonical")
	if got := staleRec.Header().Get("X-Budgie-Min-Seq"); got != futureMinSeq {
		t.Fatalf("X-Budgie-Min-Seq = %q, want %q", got, futureMinSeq)
	}
	if got := staleRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "stale" {
		t.Fatalf("X-Budgie-Read-Your-Writes = %q, want stale", got)
	}
	var stale projectionFreshnessErrorDTO
	if err := json.Unmarshal(staleRec.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode stale canonical response: %v", err)
	}
	if stale.Error.Code != proto.ErrProjectionStale || !stale.Error.Retryable {
		t.Fatalf("unexpected stale canonical error: %+v", stale.Error)
	}
	if stale.Meta.AppliedSeq != head || stale.Meta.HeadSeq != head || stale.Meta.LagEvents != 1 {
		t.Fatalf("stale canonical meta = %+v, want local head %d and lag 1", stale.Meta, head)
	}

	staleMailRec := getWithTokenAndHeaders(t, handler, "/api/v1/mail/missing", token, map[string]string{
		"X-Budgie-Min-Seq": futureMinSeq,
	})
	if staleMailRec.Code != 425 {
		t.Fatalf("stale canonical mail read status: %d body=%s", staleMailRec.Code, staleMailRec.Body.String())
	}
	assertProjectionHeaders(t, staleMailRec, "canonical")

	staleDirectRec := getWithTokenAndHeaders(t, handler, "/api/v1/messages/missing", token, map[string]string{
		"X-Budgie-Min-Seq": futureMinSeq,
	})
	if staleDirectRec.Code != 425 {
		t.Fatalf("stale canonical direct-message read status: %d body=%s", staleDirectRec.Code, staleDirectRec.Body.String())
	}
	assertProjectionHeaders(t, staleDirectRec, "canonical")

	minSeq := strconv.FormatInt(head, 10)
	freshBoardRec := getWithTokenAndHeaders(t, handler, "/api/v1/boards/general", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if freshBoardRec.Code != http.StatusOK {
		t.Fatalf("fresh canonical board status: %d body=%s", freshBoardRec.Code, freshBoardRec.Body.String())
	}
	assertProjectionHeaders(t, freshBoardRec, "canonical")
	if got := freshBoardRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("fresh canonical board X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}

	freshRec := getWithTokenAndHeaders(t, handler, "/api/v1/threads/"+create.Result.ID+"/posts", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if freshRec.Code != http.StatusOK {
		t.Fatalf("fresh canonical posts status: %d body=%s", freshRec.Code, freshRec.Body.String())
	}
	assertProjectionHeaders(t, freshRec, "canonical")
	if got := freshRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("fresh canonical X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}
	var posts struct {
		Posts []json.RawMessage `json:"posts"`
	}
	if err := json.Unmarshal(freshRec.Body.Bytes(), &posts); err != nil {
		t.Fatalf("decode fresh canonical posts: %v", err)
	}
	if len(posts.Posts) == 0 {
		t.Fatal("fresh canonical posts returned no posts")
	}

	freshMailRec := getWithTokenAndHeaders(t, handler, "/api/v1/mail", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if freshMailRec.Code != http.StatusOK {
		t.Fatalf("fresh canonical mail status: %d body=%s", freshMailRec.Code, freshMailRec.Body.String())
	}
	assertProjectionHeaders(t, freshMailRec, "canonical")
	if got := freshMailRec.Header().Get("X-Budgie-Read-Your-Writes"); got != "satisfied" {
		t.Fatalf("fresh canonical mail X-Budgie-Read-Your-Writes = %q, want satisfied", got)
	}

	freshNotificationsRec := getWithTokenAndHeaders(t, handler, "/api/v1/notifications", token, map[string]string{
		"X-Budgie-Min-Seq": minSeq,
	})
	if freshNotificationsRec.Code != http.StatusOK {
		t.Fatalf("fresh canonical notifications status: %d body=%s", freshNotificationsRec.Code, freshNotificationsRec.Body.String())
	}
	assertProjectionHeaders(t, freshNotificationsRec, "canonical")
}

func getWithToken(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	return getWithTokenAndHeaders(t, handler, path, token, nil)
}

func getWithTokenAndHeaders(t *testing.T, handler http.Handler, path, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	handler.ServeHTTP(rec, req)
	return rec
}

func assertProjectionHeaders(t *testing.T, rec *httptest.ResponseRecorder, view string) {
	t.Helper()
	if got := rec.Header().Get("X-Projection-Consistency"); got != "eventual" {
		t.Fatalf("X-Projection-Consistency = %q, want eventual", got)
	}
	if got := rec.Header().Get("X-Projection-View"); got != view {
		t.Fatalf("X-Projection-View = %q, want %q", got, view)
	}
	if rec.Header().Get("X-Projection-Head-Seq") == "" {
		t.Fatal("missing X-Projection-Head-Seq")
	}
	if rec.Header().Get("X-Projection-Applied-Seq") == "" {
		t.Fatal("missing X-Projection-Applied-Seq")
	}
	if rec.Header().Get("X-Projection-Lag-Events") == "" {
		t.Fatal("missing X-Projection-Lag-Events")
	}
}

func assertProjectionMeta(t *testing.T, meta projectionMetaDTO) {
	t.Helper()
	if meta.Consistency != "eventual" {
		t.Fatalf("consistency = %q, want eventual", meta.Consistency)
	}
	if meta.View == "" {
		t.Fatalf("view should be set: %+v", meta)
	}
	if meta.HeadSeq <= 0 {
		t.Fatalf("head seq should be positive: %+v", meta)
	}
	if meta.AppliedSeq < 0 {
		t.Fatalf("applied seq should not be negative: %+v", meta)
	}
	if meta.LagEvents < 0 {
		t.Fatalf("lag should not be negative: %+v", meta)
	}
}
