package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const projectionConsistencyEventual = "eventual"
const projectionMinSeqHeader = "X-Budgie-Min-Seq"
const projectionReadYourWritesHeader = "X-Budgie-Read-Your-Writes"
const projectionReadYourWritesSatisfied = "satisfied"
const projectionReadYourWritesStale = "stale"
const projectionRetryAfterSeconds = "1"
const localReadFreshnessView = "canonical"
const statusTooEarly = 425

type projectionMeta struct {
	Consistency string `json:"consistency"`
	View        string `json:"view,omitempty"`
	HeadSeq     int64  `json:"headSeq"`
	AppliedSeq  int64  `json:"appliedSeq"`
	LagEvents   int64  `json:"lagEvents"`
}

func (s *Server) projectionMeta(w http.ResponseWriter, appliedSeq int64) projectionMeta {
	return s.projectionMetaWithView(w, "", appliedSeq, true)
}

func (s *Server) projectionMetaForView(w http.ResponseWriter, view string) projectionMeta {
	appliedSeq, err := s.core.DerivedViewAppliedSeq(view)
	if err != nil {
		appliedSeq = 0
	}
	return s.projectionMetaWithView(w, view, appliedSeq, false)
}

func (s *Server) ensureProjectionFreshForView(w http.ResponseWriter, r *http.Request, view string) bool {
	minSeq, requested, ok := parseProjectionMinSeq(w, r)
	if !ok {
		return false
	}
	if !requested {
		return true
	}

	meta := s.projectionMetaForView(w, view)
	w.Header().Set(projectionMinSeqHeader, strconv.FormatInt(minSeq, 10))
	if meta.AppliedSeq >= minSeq {
		w.Header().Set(projectionReadYourWritesHeader, projectionReadYourWritesSatisfied)
		return true
	}

	w.Header().Set(projectionReadYourWritesHeader, projectionReadYourWritesStale)
	w.Header().Set("Retry-After", projectionRetryAfterSeconds)
	writeJSON(w, statusTooEarly, map[string]any{
		"error": proto.ErrorDetail{
			Code:         proto.ErrProjectionStale,
			Message:      "projection has not reached requested sequence",
			Retryable:    true,
			RetryAfterMs: 1000,
		},
		"meta":   meta,
		"minSeq": minSeq,
	})
	return false
}

func (s *Server) ensureLocalReadFresh(w http.ResponseWriter, r *http.Request) bool {
	minSeq, requested, ok := parseProjectionMinSeq(w, r)
	if !ok {
		return false
	}
	if !requested {
		return true
	}

	headSeq, err := s.core.Head()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, proto.ErrProjectionStale, "local read head is unavailable", true)
		return false
	}
	lag := minSeq - headSeq
	if lag < 0 {
		lag = 0
	}
	meta := projectionMeta{
		Consistency: projectionConsistencyEventual,
		View:        localReadFreshnessView,
		HeadSeq:     headSeq,
		AppliedSeq:  headSeq,
		LagEvents:   lag,
	}
	writeProjectionMetaHeaders(w, meta)
	w.Header().Set(projectionMinSeqHeader, strconv.FormatInt(minSeq, 10))
	if headSeq >= minSeq {
		w.Header().Set(projectionReadYourWritesHeader, projectionReadYourWritesSatisfied)
		return true
	}

	w.Header().Set(projectionReadYourWritesHeader, projectionReadYourWritesStale)
	w.Header().Set("Retry-After", projectionRetryAfterSeconds)
	writeJSON(w, statusTooEarly, map[string]any{
		"error": proto.ErrorDetail{
			Code:         proto.ErrProjectionStale,
			Message:      "local read store has not reached requested sequence",
			Retryable:    true,
			RetryAfterMs: 1000,
		},
		"meta":   meta,
		"minSeq": minSeq,
	})
	return false
}

func parseProjectionMinSeq(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	raw := strings.TrimSpace(r.Header.Get(projectionMinSeqHeader))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("minSeq"))
	}
	if raw == "" {
		return 0, false, true
	}
	minSeq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || minSeq < 0 {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "minSeq must be a non-negative integer", false)
		return 0, true, false
	}
	return minSeq, true, true
}

func (s *Server) projectionMetaWithView(w http.ResponseWriter, view string, appliedSeq int64, defaultZeroToHead bool) projectionMeta {
	headSeq, err := s.core.Head()
	if err != nil {
		headSeq = appliedSeq
	}
	if defaultZeroToHead && appliedSeq <= 0 {
		appliedSeq = headSeq
	}
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	if appliedSeq > headSeq {
		appliedSeq = headSeq
	}
	lag := headSeq - appliedSeq
	if lag < 0 {
		lag = 0
	}
	meta := projectionMeta{
		Consistency: projectionConsistencyEventual,
		View:        view,
		HeadSeq:     headSeq,
		AppliedSeq:  appliedSeq,
		LagEvents:   lag,
	}
	writeProjectionMetaHeaders(w, meta)
	return meta
}

func writeProjectionMetaHeaders(w http.ResponseWriter, meta projectionMeta) {
	w.Header().Set("X-Projection-Consistency", meta.Consistency)
	if meta.View != "" {
		w.Header().Set("X-Projection-View", meta.View)
	}
	w.Header().Set("X-Projection-Head-Seq", strconv.FormatInt(meta.HeadSeq, 10))
	w.Header().Set("X-Projection-Applied-Seq", strconv.FormatInt(meta.AppliedSeq, 10))
	w.Header().Set("X-Projection-Lag-Events", strconv.FormatInt(meta.LagEvents, 10))
}
