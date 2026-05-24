package httpapi

import (
	"net/http"
	"strconv"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := s.core.ListBoards()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("board")
	limit, offset := paginate(r)

	board, err := s.core.GetBoard(boardID)
	if err != nil || board == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	threads, err := s.core.ListThreads(boardID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread")
	limit, offset := paginate(r)

	thread, err := s.core.GetThread(threadID)
	if err != nil || thread == nil {
		writeError(w, http.StatusNotFound, "not_found", "thread not found", false)
		return
	}
	posts, err := s.core.ListPosts(threadID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "posts": posts})
}

func (s *Server) handleListPosts(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread")
	limit, offset := paginate(r)

	posts, err := s.core.ListPosts(threadID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

// handleAuditLog returns recent durable events for mod/admin inspection.
// GET /api/v1/audit?after=0&limit=100
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsMod() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "moderator role required", false)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	events, err := s.core.AuditLog(after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "q is required", false)
		return
	}
	board := r.URL.Query().Get("board")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	posts, err := s.core.SearchPosts(q, board, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

func paginate(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return
}
