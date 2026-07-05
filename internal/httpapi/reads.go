package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type communityStatsResponse struct {
	*projections.CommunityStats
	Meta projectionMeta `json:"meta"`
}

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boards, err := s.core.ListBoards()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) handleGetCommunityStats(w http.ResponseWriter, r *http.Request) {
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewCommunityStats) {
		return
	}
	stats, err := s.core.GetCommunityStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, communityStatsResponse{
		CommunityStats: stats,
		Meta:           s.projectionMetaForView(w, core.DerivedViewCommunityStats),
	})
}

func (s *Server) handleListCommunityStatHistory(w http.ResponseWriter, r *http.Request) {
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewCommunityStatHistory) {
		return
	}
	limit, offset := paginate(r)
	history, err := s.core.ListCommunityStatHistory(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": history, "meta": s.projectionMetaForView(w, core.DerivedViewCommunityStatHistory)})
}

func (s *Server) handleGetAccountRegistrationSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	settings, err := s.core.AccountRegistrationSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleListAccountRegistrations(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	limit, offset := paginate(r)
	status := r.URL.Query().Get("status")
	rows, err := s.core.ListAccountRegistrations(status, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registrations": rows})
}

func (s *Server) handleListPasswordRecoveryRequests(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	limit, offset := paginate(r)
	status := r.URL.Query().Get("status")
	rows, err := s.core.ListPasswordRecoveryRequests(status, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": rows})
}

func (s *Server) handleListBoardRankings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewBoardRankings) {
		return
	}
	limit, offset := paginate(r)
	boards, err := s.core.ListBoardRankings(actor, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards, "meta": s.projectionMetaForView(w, core.DerivedViewBoardRankings)})
}

func (s *Server) handleListRecommendedBoards(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	limit, offset := paginate(r)
	boards, err := s.core.ListRecommendedBoards(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) handleListThreadRankings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	boardID := r.URL.Query().Get("board")
	if boardID != "" {
		if ok, err := s.actorCanReadBoard(actor, boardID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
			return
		} else if !ok {
			writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
			return
		}
	}
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewThreadRankings) {
		return
	}
	threads, err := s.core.ListThreadRankings(actor, boardID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads, "meta": s.projectionMetaForView(w, core.DerivedViewThreadRankings)})
}

func (s *Server) handleListReplyRankings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewReplyRankings) {
		return
	}
	limit, offset := paginate(r)
	replies, err := s.core.ListReplyRankings(actor, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"replies": replies, "meta": s.projectionMetaForView(w, core.DerivedViewReplyRankings)})
}

func (s *Server) handleListUserRankings(w http.ResponseWriter, r *http.Request) {
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewUserRankings) {
		return
	}
	limit, offset := paginate(r)
	users, err := s.core.ListUserRankings(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "meta": s.projectionMetaForView(w, core.DerivedViewUserRankings)})
}

func (s *Server) handleListBlessingRankings(w http.ResponseWriter, r *http.Request) {
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewBlessingRankings) {
		return
	}
	limit, offset := paginate(r)
	blessings, err := s.core.ListBlessingRankings(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blessings": blessings, "meta": s.projectionMetaForView(w, core.DerivedViewBlessingRankings)})
}

func (s *Server) handleListArchiveRankings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewArchiveRankings) {
		return
	}
	limit, offset := paginate(r)
	archives, err := s.core.ListArchiveRankings(actor, r.URL.Query().Get("kind"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archives": archives, "meta": s.projectionMetaForView(w, core.DerivedViewArchiveRankings)})
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleListBoardMembers(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": info.Members})
}

func (s *Server) handleListBoardAutomodRules(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	ok, err := s.core.UserCanModerateBoard(actor.ID, actor.Role, boardID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board moderation permission required", false)
		return
	}
	rules, err := s.core.ListBoardAutomodRules(boardID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) handleListBoardAutomodActivity(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	ok, err := s.core.UserCanModerateBoard(actor.ID, actor.Role, boardID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board moderation permission required", false)
		return
	}
	limit, offset := paginate(r)
	activity, err := s.core.ListBoardAutomodActivity(boardID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": activity})
}

func (s *Server) handleListBoardModeratorHistory(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	limit, offset := paginate(r)
	terms, err := s.core.ListBoardModeratorTerms(boardID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"terms": terms})
}

func (s *Server) handleListBoardMemberApplications(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	status := r.URL.Query().Get("status")
	limit, offset := paginate(r)
	userID := actor.ID
	if core.ActorCanManageBoardMembers(actor, info) {
		userID = r.URL.Query().Get("userId")
	}
	apps, err := s.core.ListBoardMemberApplications(boardID, status, userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}

func (s *Server) handleListBoardDeletedPosts(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !core.ActorCanModerateBoardPosts(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board post moderation permission required", false)
		return
	}
	limit, offset := paginate(r)
	posts, err := s.core.ListBoardDeletedPosts(boardID, r.URL.Query().Get("kind"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

func (s *Server) handleListDigestEntries(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	limit, offset := paginate(r)
	kind := r.URL.Query().Get("kind")
	path := r.URL.Query().Get("path")

	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewDigestSearch) {
		return
	}
	entries, err := s.core.ListDigestEntries(boardID, kind, path, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "meta": s.projectionMetaForView(w, core.DerivedViewDigestSearch)})
}

func (s *Server) handleListDigestPathTree(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	kind := r.URL.Query().Get("kind")

	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	nodes, err := s.core.ListDigestPathTree(boardID, kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (s *Server) handleListSiteDigestEntries(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	kind := r.URL.Query().Get("kind")
	path := r.URL.Query().Get("path")

	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewDigestSearch) {
		return
	}
	entries, err := s.core.ListSiteDigestEntries(actor, kind, path, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "meta": s.projectionMetaForView(w, core.DerivedViewDigestSearch)})
}

func (s *Server) handleListSiteAnnouncements(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	path := r.URL.Query().Get("path")

	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewDigestSearch) {
		return
	}
	entries, err := s.core.ListSiteDigestEntries(actor, "announcement", path, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "meta": s.projectionMetaForView(w, core.DerivedViewDigestSearch)})
}

func (s *Server) handleSearchDigestEntries(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "q is required", false)
		return
	}
	limit, offset := paginate(r)
	boardID := r.URL.Query().Get("board")
	if boardID != "" {
		if ok, err := s.actorCanReadBoard(actor, boardID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
			return
		} else if !ok {
			writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
			return
		}
	}
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewDigestSearch) {
		return
	}
	entries, err := s.core.SearchDigestEntries(actor, boardID, r.URL.Query().Get("kind"), r.URL.Query().Get("path"), q, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "meta": s.projectionMetaForView(w, core.DerivedViewDigestSearch)})
}

func (s *Server) handleListMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	limit, offset := paginate(r)
	mailbox := r.URL.Query().Get("mailbox")
	if mailbox == "" {
		mailbox = "inbox"
	}
	unreadOnly := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	items, err := s.core.ListMail(actor.ID, mailbox, limit, offset, unreadOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	unread, _ := s.core.CountUnreadMail(actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{"mail": items, "unreadCount": unread})
}

func (s *Server) handleGetMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	mailID := r.PathValue("mail")
	item, err := s.core.GetMail(actor.ID, mailID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "mail not found", false)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleListMailThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	mailID := r.PathValue("mail")
	if item, err := s.core.GetMail(actor.ID, mailID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "mail not found", false)
		return
	}
	limit, offset := paginate(r)
	items, err := s.core.ListMailThread(actor.ID, mailID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mail": items})
}

func (s *Server) handleListMailByAuthor(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	mailID := r.PathValue("mail")
	if item, err := s.core.GetMail(actor.ID, mailID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "mail not found", false)
		return
	}
	limit, offset := paginate(r)
	items, err := s.core.ListMailByAuthor(actor.ID, mailID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mail": items})
}

func (s *Server) handleListMailGroups(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	groups, err := s.core.ListMailGroups(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Server) handleGetMailUsage(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	usage, err := s.core.GetMailUsage(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (s *Server) handleListRelayDeliveries(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if actor == nil || !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	limit, offset := paginate(r)
	items, err := s.core.ListRelayDeliveries(r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": items})
}

func (s *Server) handleListDirectMessageConversations(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	limit, offset := paginate(r)
	conversations, err := s.core.ListDirectMessageConversations(actor.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	unread, _ := s.core.CountUnreadDirectMessages(actor.ID)
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations, "unreadCount": unread})
}

func (s *Server) handleGetDirectMessageSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	settings, err := s.core.GetDirectMessageSettings(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleListDirectMessages(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	name := r.PathValue("name")
	target, err := s.core.UserByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	limit, offset := paginate(r)
	messages, err := s.core.ListDirectMessages(actor.ID, target.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages, "user": target})
}

func (s *Server) handleListSocialUsers(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	list := r.PathValue("list")
	onlineOnly := r.URL.Query().Get("online") == "1" || r.URL.Query().Get("online") == "true"
	if list == "online-friends" {
		list = "friends"
		onlineOnly = true
	}
	users, err := s.core.ListSocialUsers(actor.ID, list, onlineOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	s.maskPrivatePresenceLocations(actor, users)
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleListOnlineUsers(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	users, err := s.core.ListOnlineUsers(actor.ID, "", limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	s.maskPrivatePresenceLocations(actor, users)
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleListChatRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.core.ListChatRooms()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (s *Server) handleListChatLines(w http.ResponseWriter, r *http.Request) {
	limit, _ := paginate(r)
	lines, err := s.core.ListChatLines(r.PathValue("room"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleListChatOnlineUsers(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	limit, offset := paginate(r)
	users, err := s.core.ListChatOnlineUsers(actor.ID, r.PathValue("room"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	s.maskPrivatePresenceLocations(actor, users)
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleListBoardOnlineUsers(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	limit, offset := paginate(r)
	users, err := s.core.ListOnlineUsers(actor.ID, boardID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleListFavoriteBoards(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boards, err := s.core.ListFavoriteBoards(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) handleListFavoriteTree(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	tree, err := s.core.ListFavoriteTree(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": tree.Folders, "boards": tree.Boards})
}

func (s *Server) handleExportFavoriteTree(w http.ResponseWriter, r *http.Request) {
	s.handleListFavoriteTree(w, r)
}

func (s *Server) handleListBoardSummaries(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewBoardSummaries) {
		return
	}
	boards, err := s.core.ListBoardSummaries(actor.ID, false, boardSummaryOptions(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards, "meta": s.projectionMetaForView(w, core.DerivedViewBoardSummaries)})
}

func (s *Server) handleListUnreadBoards(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewBoardSummaries) {
		return
	}
	boards, err := s.core.ListBoardSummaries(actor.ID, true, boardSummaryOptions(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards, "meta": s.projectionMetaForView(w, core.DerivedViewBoardSummaries)})
}

func (s *Server) handleListResidentBoardPosts(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewResidentFeed) {
		return
	}
	limit, offset := paginate(r)
	posts, err := s.core.ListResidentBoardPosts(actor.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts, "meta": s.projectionMetaForView(w, core.DerivedViewResidentFeed)})
}

func (s *Server) handleListLatestFeedPosts(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewLatestFeed) {
		return
	}
	limit, offset := paginate(r)
	posts, err := s.core.ListLatestFeedPosts(actor, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts, "meta": s.projectionMetaForView(w, core.DerivedViewLatestFeed)})
}

func boardSummaryOptions(r *http.Request) core.BoardSummaryOptions {
	q := r.URL.Query()
	newOnly := false
	switch strings.ToLower(strings.TrimSpace(q.Get("new"))) {
	case "1", "true", "yes":
		newOnly = true
	}
	newDays := 30
	if raw := strings.TrimSpace(q.Get("newDays")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			newDays = parsed
		}
	}
	return core.BoardSummaryOptions{
		Search:  q.Get("q"),
		Sort:    q.Get("sort"),
		NewOnly: newOnly,
		NewDays: newDays,
	}
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	categories, err := s.core.ListCategoriesForUser(actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	boardID := r.PathValue("board")
	limit, offset := paginate(r)
	unreadOnly := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	titleQuery := r.URL.Query().Get("q")
	authorQuery := r.URL.Query().Get("author")

	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	if !actorCanReadBoardInfo(actor, info) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	threads, err := s.core.ListThreadSummariesFiltered(actor.ID, boardID, titleQuery, authorQuery, limit, offset, unreadOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
}

func (s *Server) handleListUnreadThreads(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewUnreadThreads) {
		return
	}
	limit, offset := paginate(r)
	favoritesOnly := r.URL.Query().Get("favorites") == "1" || r.URL.Query().Get("favorites") == "true"
	folderID := r.URL.Query().Get("folder")
	threads, err := s.core.ListUnreadThreadSummaries(actor, favoritesOnly, folderID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads, "meta": s.projectionMetaForView(w, core.DerivedViewUnreadThreads)})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	threadID := r.PathValue("thread")
	limit, offset := paginate(r)

	thread, err := s.core.GetThread(threadID)
	if err != nil || thread == nil {
		writeError(w, http.StatusNotFound, "not_found", "thread not found", false)
		return
	}
	if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
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
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	threadID := r.PathValue("thread")
	limit, offset := paginate(r)

	thread, err := s.core.GetThread(threadID)
	if err != nil || thread == nil {
		writeError(w, http.StatusNotFound, "not_found", "thread not found", false)
		return
	}
	if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	posts, err := s.core.ListPosts(threadID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

// GET /api/v1/posts/{post}/reply-tree
func (s *Server) handleListPostReplyTree(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	postID := r.PathValue("post")
	limit, offset := paginate(r)

	root, err := s.core.GetPost(postID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if root == nil {
		writeError(w, http.StatusNotFound, "not_found", "post not found", false)
		return
	}
	thread, err := s.core.GetThread(root.Thread)
	if err != nil || thread == nil {
		writeError(w, http.StatusNotFound, "not_found", "thread not found", false)
		return
	}
	if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	posts, err := s.core.ListReplyTreePosts(postID, limit, offset)
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
	actor := userFromCtx(r.Context())
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "q is required", false)
		return
	}
	board := r.URL.Query().Get("board")
	if board != "" {
		if ok, err := s.actorCanReadBoard(actor, board); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
			return
		} else if !ok {
			writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
			return
		}
	}
	if !s.ensureProjectionFreshForView(w, r, core.DerivedViewPostSearch) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	posts, err := s.core.SearchReadablePosts(actor, q, board, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts, "meta": s.projectionMetaForView(w, core.DerivedViewPostSearch)})
}

func (s *Server) actorCanReadBoard(actor *core.User, boardID string) (bool, error) {
	info, err := s.core.GetBoardInfo(boardID)
	if err != nil || info == nil {
		return false, err
	}
	return actorCanReadBoardInfo(actor, info), nil
}

func (s *Server) maskPrivatePresenceLocations(actor *core.User, users []core.SocialUser) {
	for i := range users {
		if users[i].BoardID == "" {
			continue
		}
		info, err := s.core.GetBoardInfo(users[i].BoardID)
		if err == nil && info != nil && actorCanReadBoardInfo(actor, info) {
			continue
		}
		users[i].BoardID = ""
		users[i].BoardName = ""
		users[i].ThreadID = ""
		users[i].LocationLabel = "private board"
		if users[i].Mode != "" {
			users[i].Status = users[i].Mode
		} else {
			users[i].Status = "online"
		}
	}
}

func actorCanReadBoardInfo(actor *core.User, info *core.BoardInfo) bool {
	// Delegates to the canonical core implementation so every transport (HTTP,
	// NNTP, SSH/TUI) enforces one read-access rule.
	return core.ActorCanReadBoard(actor, info)
}

// GET /api/v1/notifications
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	limit, offset := paginate(r)
	unreadOnly := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	notifs, err := s.core.ListNotifications(actor.ID, limit, offset, unreadOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	unread, _ := s.core.CountUnreadNotifications(actor.ID)
	if notifs == nil {
		// A nil slice marshals to JSON null; return [] so clients can safely
		// iterate (an empty list must not crash the notifications page).
		notifs = []core.Notification{}
	}
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
	// Gate by board read access so guests (and non-members) can't read poll data
	// from a member-only board by post id. GetPost populates the thread (not the
	// board), so resolve the board via the thread.
	if post, err := s.core.GetPost(postID); err == nil && post != nil {
		if thread, err := s.core.GetThread(post.Thread); err == nil && thread != nil {
			if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
				return
			} else if !ok {
				writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
				return
			}
		}
	}
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

// GET /api/v1/threads/{thread}/polls
func (s *Server) handleListThreadPolls(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !s.ensureLocalReadFresh(w, r) {
		return
	}
	threadID := r.PathValue("thread")
	limit, offset := paginate(r)

	if thread, err := s.core.GetThread(threadID); err == nil && thread != nil {
		if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
			return
		} else if !ok {
			writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
			return
		}
	}

	posts, err := s.core.ListPosts(threadID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	postIDs := make([]string, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
	}
	pollsByPost, err := s.core.PollsForPosts(postIDs, actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if pollsByPost == nil {
		pollsByPost = map[string]*core.Poll{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"polls": pollsByPost})
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

// GET /api/v1/users/me/private-profile
func (s *Server) handleGetOwnPrivateProfile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	profile, err := s.core.UserPrivateProfile(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/{name}/private-profile
func (s *Server) handleGetUserPrivateProfile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	name := r.PathValue("name")
	target, err := s.core.UserByName(name)
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	profile, err := s.core.UserPrivateProfile(target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/me/files
func (s *Server) handleListOwnPersonalFiles(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	files, err := s.core.ListUserPersonalFiles(actor.ID, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// GET /api/v1/users/me/files/{file}
func (s *Server) handleGetOwnPersonalFile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	file, err := s.core.GetUserPersonalFile(actor.ID, r.PathValue("file"), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if file == nil {
		writeError(w, http.StatusNotFound, "not_found", "personal file not found", false)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

// GET /api/v1/users/{name}/files
func (s *Server) handleListUserPersonalFiles(w http.ResponseWriter, r *http.Request) {
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	files, err := s.core.ListUserPersonalFiles(target.ID, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// GET /api/v1/users/{name}/files/{file}
func (s *Server) handleGetUserPersonalFile(w http.ResponseWriter, r *http.Request) {
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	file, err := s.core.GetUserPersonalFile(target.ID, r.PathValue("file"), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if file == nil {
		writeError(w, http.StatusNotFound, "not_found", "personal file not found", false)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

// GET /api/v1/users/{name}/posts
func (s *Server) handleListUserPosts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	target, err := s.core.UserByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	limit, offset := paginate(r)
	posts, err := s.core.ListPostsByAuthor(target.Name, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

// GET /api/v1/authors/{name}/posts
func (s *Server) handleListReadableAuthorPosts(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	target, err := s.core.UserByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	limit, offset := paginate(r)
	posts, err := s.core.ListReadablePostsByAuthor(actor, target.Name, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
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

// GET /api/v1/admin/content-filters?scope=&includeInactive=1
func (s *Server) handleListContentFilters(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	limit, offset := paginate(r)
	includeInactive := r.URL.Query().Get("includeInactive") == "1" || strings.EqualFold(r.URL.Query().Get("includeInactive"), "true")
	rows, err := s.core.ListContentFilters(r.URL.Query().Get("scope"), includeInactive, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"filters": rows})
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
	// Clamp offset to a sane range: negative offsets are invalid, and an
	// enormous offset forces the DB to scan/skip huge numbers of rows.
	if offset < 0 {
		offset = 0
	}
	if offset > 1_000_000 {
		offset = 1_000_000
	}
	return
}
