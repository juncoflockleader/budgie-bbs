package httpapi

import (
	"net/http"
	"strconv"
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

func paginate(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return
}
