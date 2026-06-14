package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type hotThreadSplitDTO struct {
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

type hotThreadSplitsResponse struct {
	Local      bool                `json:"local"`
	Persistent bool                `json:"persistent"`
	Splits     []hotThreadSplitDTO `json:"splits"`
}

type hotThreadSplitRequest struct {
	Shards int  `json:"shards"`
	Force  bool `json:"force"`
}

func (s *Server) handleListHotThreadSplits(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if actor == nil || !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	writeJSON(w, http.StatusOK, hotThreadSplitsResponse{
		Local:      true,
		Persistent: true,
		Splits:     hotThreadSplitDTOs(s.core.HotThreadSplits()),
	})
}

func (s *Server) handleSetHotThreadSplit(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if actor == nil || !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	threadID := strings.TrimSpace(r.PathValue("thread"))
	if threadID == "" {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "thread id is required", false)
		return
	}

	var req hotThreadSplitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "invalid body", false)
		return
	}
	if req.Shards < 2 {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "shards must be at least 2", false)
		return
	}
	if !hotThreadSplitForce(r, req.Force) && !s.ensureHotThreadSplitDrained(w, r, threadID, req.Shards) {
		return
	}

	if err := s.core.PersistHotThreadSplit(threadID, req.Shards); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, hotThreadSplitsResponse{
		Local:      true,
		Persistent: true,
		Splits:     hotThreadSplitDTOs(s.core.HotThreadSplits()),
	})
}

func (s *Server) handleDeleteHotThreadSplit(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if actor == nil || !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	threadID := strings.TrimSpace(r.PathValue("thread"))
	if threadID == "" {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "thread id is required", false)
		return
	}
	if !hotThreadSplitForce(r, false) && !s.ensureHotThreadSplitDrained(w, r, threadID, 0) {
		return
	}

	if err := s.core.PersistHotThreadSplit(threadID, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, hotThreadSplitsResponse{
		Local:      true,
		Persistent: true,
		Splits:     hotThreadSplitDTOs(s.core.HotThreadSplits()),
	})
}

func (s *Server) ensureHotThreadSplitDrained(w http.ResponseWriter, r *http.Request, threadID string, nextShards int) bool {
	blocking, err := s.core.HotThreadSplitBlockingLag(r.Context(), threadID, nextShards)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return false
	}
	if len(blocking) == 0 {
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"code":      proto.ErrConflict,
			"message":   "hot thread split partitions must drain before changing split configuration",
			"retryable": true,
		},
		"blockingPartitions": hotThreadSplitLagDTOs(blocking),
	})
	return false
}

func hotThreadSplitForce(r *http.Request, bodyForce bool) bool {
	force := strings.TrimSpace(r.URL.Query().Get("force"))
	if force == "" {
		return bodyForce
	}
	return force == "1" || strings.EqualFold(force, "true")
}

func hotThreadSplitDTOs(splits map[string]int) []hotThreadSplitDTO {
	if len(splits) == 0 {
		return []hotThreadSplitDTO{}
	}
	threadIDs := make([]string, 0, len(splits))
	for threadID := range splits {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)

	out := make([]hotThreadSplitDTO, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		out = append(out, hotThreadSplitDTO{
			ThreadID: threadID,
			Shards:   splits[threadID],
		})
	}
	return out
}

func hotThreadSplitLagDTOs(offsets []core.CommandPartitionOffset) []hotThreadSplitLagDTO {
	out := make([]hotThreadSplitLagDTO, 0, len(offsets))
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		lag := offset.TailOffset - offset.CommittedOffset
		if lag < 0 {
			lag = 0
		}
		out = append(out, hotThreadSplitLagDTO{
			Kind:            partition.Kind,
			Key:             partition.Key,
			TailOffset:      offset.TailOffset,
			CommittedOffset: offset.CommittedOffset,
			Lag:             lag,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Key < out[j].Key
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
