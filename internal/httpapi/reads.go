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

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := s.core.ListCategories()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
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

// GET /api/v1/notifications
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	unreadOnly := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	notifs, err := s.core.ListNotifications(actor.ID, limit, offset, unreadOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	unread, _ := s.core.CountUnreadNotifications(actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{"notifications": notifs, "unreadCount": unread})
}

// GET /api/v1/polls/{poll}
func (s *Server) handleGetPoll(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	pollID := r.PathValue("poll")
	poll, err := s.core.GetPoll(pollID, actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if poll == nil {
		writeError(w, http.StatusNotFound, "not_found", "poll not found", false)
		return
	}
	writeJSON(w, http.StatusOK, poll)
}

// GET /api/v1/posts/{post}/poll
func (s *Server) handleGetPollByPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")
	poll, err := s.core.GetPollByPostID(postID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if poll == nil {
		writeError(w, http.StatusNotFound, "not_found", "poll not found", false)
		return
	}
	// Re-fetch with viewer context to get voted state
	fullPoll, err := s.core.GetPoll(poll.ID, actor.ID)
	if err != nil || fullPoll == nil {
		writeJSON(w, http.StatusOK, poll)
		return
	}
	writeJSON(w, http.StatusOK, fullPoll)
}

// GET /api/v1/users/{name}/trust
func (s *Server) handleGetTrust(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u, err := s.core.UserByName(name)
	if err != nil || u == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	info, err := s.core.TrustInfo(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// GET /api/v1/users/{name}
func (s *Server) handleGetUserProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	profile, err := s.core.UserProfileByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// GET /api/v1/mod/reviewables?status=open
func (s *Server) handleListReviewables(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsMod() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "moderator role required", false)
		return
	}
	limit, offset := paginate(r)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	reviews, err := s.core.ListModerationReviews(status, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviewables": reviews})
}

// GET /api/v1/users/{name}/sanctions
func (s *Server) handleListUserSanctions(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")
	target, err := s.core.UserByName(name)
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	if !actor.IsMod() && actor.ID != target.ID {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "moderator role required", false)
		return
	}

	limit, offset := paginate(r)
	rows, err := s.core.ListUserSanctions(target.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sanctions": rows})
}

func paginate(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return
}
