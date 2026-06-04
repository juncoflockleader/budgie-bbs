package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type commandRequest struct {
	CID     string            `json:"cid"`
	Command proto.CommandName `json:"command"`
	Payload json.RawMessage   `json:"payload"`
}

// handleCommand serves POST /api/v1/commands (uniform endpoint).
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "command field required", false)
		return
	}
	cid := req.CID
	if cid == "" {
		cid = r.Header.Get("X-Command-Id")
	}

	reply := s.core.ExecCmd(r.Context(), actor, req.Command, req.Payload, cid)
	writeAck(w, cid, reply)
}

// --- RESTful alias handlers ---

func (s *Server) handleSetPresence(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var p proto.SetPresencePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if p.FromHost == "" {
		p.FromHost = requestHost(r)
	}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetPresence, raw, cid)
	writeAck(w, cid, reply)
}

type guestPresenceRequest struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Location  string `json:"location"`
	FromHost  string `json:"fromHost"`
}

func (s *Server) handleSetGuestPresence(w http.ResponseWriter, r *http.Request) {
	var p guestPresenceRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if strings.TrimSpace(p.SessionID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "sessionId is required", false)
		return
	}
	if p.FromHost == "" {
		p.FromHost = requestHost(r)
	}
	if err := s.core.SetGuestPresence(p.SessionID, p.Status, p.Location, p.FromHost, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeAck(w, r.Header.Get("X-Command-Id"), core.Reply{Result: &proto.AckResult{}})
}

func requestHost(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.TrimSpace(forwarded)
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.CreateThreadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCreateThread, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleAppendPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.AppendPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdAppendPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleRepostPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.RepostPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRepostPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handlePostBoardMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.PostBoardMailPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPostBoardMail, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handlePostThreadMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.PostBoardMailPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPostBoardMail, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleEditPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.EditPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdEditPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSetPostFlag(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.SetPostFlagPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetPostFlag, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleRedactPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.RedactPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.RedactPostPayload{}
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRedactPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleRestorePost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	p := proto.RestorePostPayload{Post: postID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRestorePost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSetThreadTitle(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.SetThreadTitlePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetThreadTitle, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleLockThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.LockThreadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdLockThread, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSendChatLine(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	room := r.PathValue("room")

	var p proto.SendChatLinePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Room = room

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSendChatLine, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/posts/{post}/react
func (s *Server) handleReactPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.ReactPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.ReactPostPayload{}
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdReactPost, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/posts/{post}/react
func (s *Server) handleUnreactPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	p := proto.ReactPostPayload{Post: postID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdUnreactPost, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/polls/{poll}/vote
func (s *Server) handleVotePoll(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	pollID := r.PathValue("poll")

	var p proto.VotePollPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Poll = pollID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdVotePoll, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/polls/{poll}/publish-result
func (s *Server) handlePublishPollResult(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	p := proto.PublishPollResultPayload{Poll: r.PathValue("poll")}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPublishPollResult, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/notifications/{id}/read
func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	id := r.PathValue("id")
	if err := s.core.MarkNotificationRead(id, actor.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/notifications/read-all
func (s *Server) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if err := s.core.MarkAllNotificationsRead(actor.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/v1/threads/{thread}/prefs
func (s *Server) handleSetThreadPref(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.SetThreadPrefPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetThreadPref, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/boards/{board}/favorite
// DELETE /api/v1/boards/{board}/favorite
func (s *Server) handleSetBoardFavorite(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	p := proto.SetBoardFavoritePayload{Board: boardID}
	if r.Method == http.MethodDelete {
		p.Favorite = false
	} else {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
			return
		}
		p.Board = boardID
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetBoardFavorite, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/favorites/import
func (s *Server) handleImportFavoriteTree(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var p proto.ImportFavoriteTreePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdImportFavoriteTree, raw, cid)
	if reply.Err != nil {
		writeAck(w, cid, reply)
		return
	}
	tree, err := s.core.ListFavoriteTree(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusCreated, tree)
}

// PATCH /api/v1/boards/{board}/settings
func (s *Server) handleSetBoardSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.SetBoardSettingsPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetBoardSettings, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/boards/{board}/member-requirements
func (s *Server) handleSetBoardMemberRequirements(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.SetBoardMemberRequirementsPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetBoardMemberRequirements, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/boards/{board}/moderators/{user}
// DELETE /api/v1/boards/{board}/moderators/{user}
func (s *Server) handleSetBoardModerator(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	userRef := r.PathValue("user")

	p := proto.SetBoardModeratorPayload{
		Board:     boardID,
		User:      userRef,
		Moderator: r.Method != http.MethodDelete,
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetBoardModerator, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/boards/{board}/members/{user}
// DELETE /api/v1/boards/{board}/members/{user}
func (s *Server) handleSetBoardMember(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	userRef := r.PathValue("user")

	p := proto.SetBoardMemberPayload{
		Board:  boardID,
		User:   userRef,
		Member: r.Method != http.MethodDelete,
	}
	if r.Method != http.MethodDelete {
		var body struct {
			Title               string `json:"title"`
			Position            *int   `json:"position"`
			CanManageMembers    *bool  `json:"canManageMembers"`
			CanCurate           *bool  `json:"canCurate"`
			CanModeratePosts    *bool  `json:"canModeratePosts"`
			CanModerateThreads  *bool  `json:"canModerateThreads"`
			CanAnnounce         *bool  `json:"canAnnounce"`
			CanManagePolls      *bool  `json:"canManagePolls"`
			CanSetBoardSettings *bool  `json:"canSetBoardSettings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			p.Title = body.Title
			p.Position = body.Position
			p.CanManageMembers = body.CanManageMembers
			p.CanCurate = body.CanCurate
			p.CanModeratePosts = body.CanModeratePosts
			p.CanModerateThreads = body.CanModerateThreads
			p.CanAnnounce = body.CanAnnounce
			p.CanManagePolls = body.CanManagePolls
			p.CanSetBoardSettings = body.CanSetBoardSettings
		}
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetBoardMember, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/member-applications
func (s *Server) handleApplyBoardMembership(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.ApplyBoardMembershipPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.ApplyBoardMembershipPayload{}
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdApplyBoardMembership, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/board-member-applications/{application}/review
func (s *Server) handleReviewBoardMembership(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	applicationID := r.PathValue("application")

	var p proto.ReviewBoardMembershipPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Application = applicationID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdReviewBoardMembership, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/members/leave
func (s *Server) handleLeaveBoardMembership(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	p := proto.LeaveBoardMembershipPayload{Board: boardID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdLeaveBoardMembership, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/posts/{post}/digest
func (s *Server) handleCuratePost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.CuratePostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.CuratePostPayload{}
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCuratePost, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/threads/{thread}/digest
func (s *Server) handleCurateThread(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	var p proto.CurateThreadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.CurateThreadPayload{}
	}
	p.Thread = threadID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCurateThread, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/digest/directories
func (s *Server) handleCreateDigestDirectory(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.CreateDigestDirectoryPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCreateDigestDirectory, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/digest/paths/move
func (s *Server) handleMoveDigestPath(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.MoveDigestPathPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMoveDigestPath, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/digest/paths/copy
func (s *Server) handleCopyDigestPath(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.CopyDigestPathPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCopyDigestPath, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/boards/{board}/digest/paths?kind=&path=
func (s *Server) handleDeleteDigestPath(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	p := proto.DeleteDigestPathPayload{
		Board: r.PathValue("board"),
		Kind:  r.URL.Query().Get("kind"),
		Path:  r.URL.Query().Get("path"),
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdDeleteDigestPath, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/digest/{entry}
func (s *Server) handleRemoveDigestEntry(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")

	p := proto.RemoveDigestEntryPayload{Entry: entryID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRemoveDigestEntry, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/digest/{entry}
func (s *Server) handleUpdateDigestEntry(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")

	var p proto.UpdateDigestEntryPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Entry = entryID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdUpdateDigestEntry, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/digest/{entry}/body
func (s *Server) handleSetDigestEntryBody(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")

	var p proto.SetDigestEntryBodyPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Entry = entryID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetDigestEntryBody, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/digest/{entry}/body
func (s *Server) handleResetDigestEntryBody(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")

	p := proto.SetDigestEntryBodyPayload{Entry: entryID, Reset: true}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetDigestEntryBody, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/digest/{entry}/mail
func (s *Server) handleSendDigestEntryMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")

	var p proto.SendDigestEntryMailPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Entry = entryID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSendDigestEntryMail, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/mail
func (s *Server) handleSendMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var p proto.SendMailPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSendMail, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/mail/groups
// PUT/PATCH /api/v1/mail/groups/{group}
func (s *Server) handleSetMailGroup(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var p proto.SetMailGroupPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Group = r.PathValue("group")

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetMailGroup, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/mail/groups/{group}
func (s *Server) handleDeleteMailGroup(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	p := proto.DeleteMailGroupPayload{Group: r.PathValue("group")}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdDeleteMailGroup, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/mail/{mail}
func (s *Server) handleUpdateMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	mailID := r.PathValue("mail")

	var p proto.UpdateMailPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Mail = mailID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdUpdateMail, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/mail/{mail}
func (s *Server) handleDeleteMail(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	mailID := r.PathValue("mail")

	p := proto.DeleteMailPayload{Mail: mailID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdDeleteMail, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/messages
func (s *Server) handleSendDirectMessage(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var p proto.SendDirectMessagePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSendDirectMessage, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/messages/settings
func (s *Server) handleSetDirectMessageSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var p proto.SetDirectMessageSettingsPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetDirectMessageSettings, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/messages/{message}/read
func (s *Server) handleMarkDirectMessageRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	messageID := r.PathValue("message")

	p := proto.MarkDirectMessageReadPayload{Message: messageID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMarkDirectMessageRead, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/messages/{message}
func (s *Server) handleDeleteDirectMessage(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	messageID := r.PathValue("message")

	p := proto.DeleteDirectMessagePayload{Message: messageID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdDeleteDirectMessage, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/users/{name}/friend
// DELETE /api/v1/users/{name}/friend
// PUT /api/v1/users/{name}/ignore
// DELETE /api/v1/users/{name}/ignore
func (s *Server) handleSetUserRelationship(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")
	kind := r.PathValue("kind")

	p := proto.SetUserRelationshipPayload{
		User:   name,
		Kind:   kind,
		Active: r.Method != http.MethodDelete,
	}
	if r.Method != http.MethodDelete {
		var body proto.SetUserRelationshipPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			p.Note = body.Note
		}
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetUserRelationship, raw, cid)
	writeAck(w, cid, reply)
}

// PUT /api/v1/users/{name}/login-watch
// DELETE /api/v1/users/{name}/login-watch
func (s *Server) handleSetLoginWatch(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")

	p := proto.SetLoginWatchPayload{
		User:   name,
		Active: r.Method != http.MethodDelete,
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetLoginWatch, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/users/{name}/bless
func (s *Server) handleBlessUser(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")

	p := proto.BlessUserPayload{User: name}
	if r.ContentLength != 0 {
		var body proto.BlessUserPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
			return
		}
		p.Message = body.Message
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdBlessUser, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/favorites/folders
func (s *Server) handleCreateFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())

	var p proto.CreateFavoriteFolderPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCreateFavoriteFolder, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/boards/favorites/folders/{folder}
func (s *Server) handleUpdateFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	folderID := r.PathValue("folder")

	var p proto.UpdateFavoriteFolderPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Folder = folderID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdUpdateFavoriteFolder, raw, cid)
	writeAck(w, cid, reply)
}

// DELETE /api/v1/boards/favorites/folders/{folder}
func (s *Server) handleDeleteFavoriteFolder(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	folderID := r.PathValue("folder")

	p := proto.DeleteFavoriteFolderPayload{Folder: folderID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdDeleteFavoriteFolder, raw, cid)
	writeAck(w, cid, reply)
}

// PATCH /api/v1/boards/{board}/favorite
func (s *Server) handleMoveBoardFavorite(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	var p proto.MoveBoardFavoritePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Board = boardID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMoveBoardFavorite, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/read
func (s *Server) handleMarkBoardRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	p := proto.MarkBoardReadPayload{Board: boardID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMarkBoardRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/{board}/read/restore
func (s *Server) handleRestoreBoardRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")

	p := proto.RestoreBoardReadPayload{Board: boardID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRestoreBoardRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/favorites/read
// POST /api/v1/boards/favorites/folders/{folder}/read
func (s *Server) handleMarkFavoriteFolderRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	folderID := r.PathValue("folder")

	p := proto.MarkFavoriteFolderReadPayload{Folder: folderID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMarkFavoriteFolderRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/boards/favorites/read/restore
// POST /api/v1/boards/favorites/folders/{folder}/read/restore
func (s *Server) handleRestoreFavoriteFolderRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	folderID := r.PathValue("folder")

	p := proto.RestoreFavoriteFolderReadPayload{Folder: folderID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRestoreFavoriteFolderRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/threads/{thread}/read
func (s *Server) handleMarkThreadRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	p := proto.MarkThreadReadPayload{Thread: threadID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMarkThreadRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/threads/{thread}/read/restore
func (s *Server) handleRestoreThreadRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	threadID := r.PathValue("thread")

	p := proto.RestoreThreadReadPayload{Thread: threadID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdRestoreThreadRead, raw, cid)
	writeAck(w, cid, reply)
}

// POST /api/v1/posts/{post}/read
func (s *Server) handleMarkPostRead(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	p := proto.MarkPostReadPayload{Post: postID}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdMarkPostRead, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handlePurgePost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.PurgePostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.PurgePostPayload{}
	}
	p.Post = postID

	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPurgePost, raw, cid)
	writeAck(w, cid, reply)
}

type updateProfileRequest struct {
	DisplayName string `json:"displayName"`
	Title       string `json:"title"`
	Bio         string `json:"bio"`
	Avatar      string `json:"avatar"`
	Signature   string `json:"signature"`
	Plan        string `json:"plan"`
	Homepage    string `json:"homepage"`
}

type updatePrivateProfileRequest struct {
	RealName          string `json:"realName"`
	RealEmail         string `json:"realEmail"`
	RegistrationEmail string `json:"registrationEmail"`
	Address           string `json:"address"`
	Phone             string `json:"phone"`
	Mobile            string `json:"mobile"`
	Birthday          string `json:"birthday"`
	School            string `json:"school"`
	ContactNote       string `json:"contactNote"`
}

type accountRegistrationSettingsRequest struct {
	RequireApproval bool `json:"requireApproval"`
}

type accountRegistrationReviewRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type passwordRecoveryReviewRequest struct {
	Decision    string `json:"decision"`
	NewPassword string `json:"newPassword"`
	Note        string `json:"note"`
}

type transferUserIDRequest struct {
	NewName string `json:"newName"`
}

type deleteUserRequest struct {
	Reason string `json:"reason"`
}

type updateCategoryRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ParentID    *string `json:"parentId"`
	Position    *int    `json:"position"`
	Visibility  *string `json:"visibility"`
}

type userPersonalFileRequest struct {
	Body   string `json:"body"`
	Public *bool  `json:"public"`
}

type userSignatureRequest struct {
	Label    string `json:"label"`
	Body     string `json:"body"`
	Position *int   `json:"position"`
	Active   *bool  `json:"active"`
}

type userSignatureSettingsRequest struct {
	SelectedSignatureID string `json:"selectedSignatureId"`
	RandomEnabled       bool   `json:"randomEnabled"`
}

type userLoginACLRuleRequest struct {
	Pattern  string `json:"pattern"`
	Note     string `json:"note"`
	Position *int   `json:"position"`
	Active   *bool  `json:"active"`
}

type userLoginACLSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleUpdateOwnProfile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = actor.Name
	}
	req.Title = strings.TrimSpace(req.Title)
	if len(req.Title) > 80 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "title must be 80 characters or less", false)
		return
	}
	if err := s.core.UpdateUserProfile(actor.ID, req.DisplayName, req.Title, req.Bio, req.Avatar, req.Signature, req.Plan, req.Homepage); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUpdateOwnPrivateProfile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req updatePrivateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if err := s.core.UpdateUserPrivateProfile(&core.UserPrivateProfile{
		UserID:            actor.ID,
		RealName:          req.RealName,
		RealEmail:         req.RealEmail,
		RegistrationEmail: req.RegistrationEmail,
		Address:           req.Address,
		Phone:             req.Phone,
		Mobile:            req.Mobile,
		Birthday:          req.Birthday,
		School:            req.School,
		ContactNote:       req.ContactNote,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetAccountRegistrationSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req accountRegistrationSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	settings, err := s.core.SetAccountRegistrationSettings(req.RequireApproval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleReviewAccountRegistration(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req accountRegistrationReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	review, err := s.core.ReviewAccountRegistration(target.ID, actor.ID, req.Decision, req.Reason)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusConflict, "conflict", "registration is not pending", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registration": review})
}

func (s *Server) handleReviewPasswordRecoveryRequest(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req passwordRecoveryReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	review, err := s.core.ReviewPasswordRecoveryRequest(r.PathValue("request"), actor.ID, req.Decision, req.NewPassword, req.Note)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "password recovery request not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": review})
}

func (s *Server) handleTransferUserID(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req transferUserIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewName == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "newName required", false)
		return
	}
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	user, err := s.core.TransferUserID(target.ID, req.NewName)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": authUser{ID: user.ID, Name: user.Name, Role: user.Role, RegistrationStatus: user.RegistrationStatus}})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req deleteUserRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
			return
		}
	}
	target, err := s.core.UserByName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	if err := s.core.DeleteUser(actor.ID, target.ID, req.Reason); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		case errors.Is(err, core.ErrLastAdminDeletion):
			writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		case errors.Is(err, core.ErrAccountDeleteForbidden):
			writeError(w, http.StatusForbidden, proto.ErrForbidden, err.Error(), false)
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req updateCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	category, err := s.core.UpdateCategory(actor.ID, r.PathValue("category"), core.CategoryUpdate{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		Position:    req.Position,
		Visibility:  req.Visibility,
	})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "not_found", "category not found", false)
		case errors.Is(err, core.ErrAccountDeleteForbidden):
			writeError(w, http.StatusForbidden, proto.ErrForbidden, err.Error(), false)
		default:
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"category": category})
}

func (s *Server) handleRecountOwnSignatures(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	recount, err := s.core.RecountUserSignatures(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recount": recount})
}

func (s *Server) handleSaveOwnPersonalFile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req userPersonalFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	public := true
	if req.Public != nil {
		public = *req.Public
	}
	file, err := s.core.SaveUserPersonalFile(actor.ID, r.PathValue("file"), req.Body, public)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

func (s *Server) handleDeleteOwnPersonalFile(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if err := s.core.DeleteUserPersonalFile(actor.ID, r.PathValue("file")); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "personal file not found", false)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListOwnLoginACL(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	bundle, err := s.core.ListUserLoginACL(actor.ID, requestHost(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleCreateOwnLoginACLRule(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req userLoginACLRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	position := -1
	if req.Position != nil {
		position = *req.Position
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	rule, err := s.core.SaveUserLoginACLRule(actor.ID, "", req.Pattern, req.Note, position, active)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rule": rule})
}

func (s *Server) handleUpdateOwnLoginACLRule(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	ruleID := r.PathValue("rule")
	var req userLoginACLRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	position := -1
	if req.Position != nil {
		position = *req.Position
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	rule, err := s.core.SaveUserLoginACLRule(actor.ID, ruleID, req.Pattern, req.Note, position, active)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "login ACL rule not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": rule})
}

func (s *Server) handleDeleteOwnLoginACLRule(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	ruleID := r.PathValue("rule")
	if err := s.core.DeleteUserLoginACLRule(actor.ID, ruleID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "login ACL rule not found", false)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetOwnLoginACLSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req userLoginACLSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if err := s.core.SetUserLoginACLSettings(actor.ID, req.Enabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListOwnSignatures(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	bundle, err := s.core.ListUserSignatures(actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleCreateOwnSignature(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req userSignatureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	position := -1
	if req.Position != nil {
		position = *req.Position
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	sig, err := s.core.SaveUserSignature(actor.ID, "", req.Label, req.Body, position, active)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"signature": sig})
}

func (s *Server) handleUpdateOwnSignature(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	signatureID := r.PathValue("signature")
	var req userSignatureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	position := -1
	if req.Position != nil {
		position = *req.Position
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	sig, err := s.core.SaveUserSignature(actor.ID, signatureID, req.Label, req.Body, position, active)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not_found", "signature not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signature": sig})
}

func (s *Server) handleDeleteOwnSignature(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	signatureID := r.PathValue("signature")
	if err := s.core.DeleteUserSignature(actor.ID, signatureID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "signature not found", false)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetOwnSignatureSettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req userSignatureSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if err := s.core.SetUserSignatureSettings(actor.ID, req.SelectedSignatureID, req.RandomEnabled); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "signature not found", false)
			return
		}
		writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFlagPost(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")

	var p proto.FlagPostPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		p = proto.FlagPostPayload{}
	}
	p.Post = postID
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdFlagPost, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleResolveReview(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	reviewID := r.PathValue("id")

	var p proto.ResolveReviewPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.Review = reviewID
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdResolveReview, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSanctionUser(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")
	target, err := s.core.UserByName(name)
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}
	var p proto.SanctionUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	p.User = target.ID
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSanctionUser, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleClearUserSanction(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	name := r.PathValue("name")
	target, err := s.core.UserByName(name)
	if err != nil || target == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found", false)
		return
	}

	query := r.URL.Query()
	p := proto.ClearUserSanctionPayload{
		Kind:   strings.TrimSpace(query.Get("kind")),
		Scope:  strings.TrimSpace(query.Get("scope")),
		Reason: strings.TrimSpace(query.Get("reason")),
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
			return
		}
	}
	p.User = target.ID
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdClearUserSanction, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleSetContentFilter(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var p proto.SetContentFilterPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if id := strings.TrimSpace(r.PathValue("filter")); id != "" {
		p.ID = id
	}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdSetContentFilter, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req proto.CreateBoardPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	raw, _ := json.Marshal(req)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdCreateBoard, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handlePublishStatsSnapshot(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var p proto.PublishStatsSnapshotPayload
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
			return
		}
	}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPublishStatsSnapshot, raw, cid)
	writeAck(w, cid, reply)
}

func (s *Server) handlePublishSystemNotice(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var p proto.PublishSystemNoticePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	raw, _ := json.Marshal(p)
	cid := r.Header.Get("X-Command-Id")
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdPublishSystemNotice, raw, cid)
	writeAck(w, cid, reply)
}

// writeAck serialises a handler Reply into the wire ack envelope.
func writeAck(w http.ResponseWriter, cid string, reply core.Reply) {
	type ack struct {
		Kind   string             `json:"kind"`
		CID    string             `json:"cid,omitempty"`
		OK     bool               `json:"ok"`
		Result *proto.AckResult   `json:"result"`
		Error  *proto.ErrorDetail `json:"error,omitempty"`
	}
	a := ack{Kind: "ack", CID: cid, OK: reply.Err == nil, Result: reply.Result, Error: reply.Err}
	status := http.StatusOK
	if reply.Err != nil {
		status = errorCode(reply.Err.Code)
	} else if reply.Result != nil && reply.Result.ID != "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, a)
}
