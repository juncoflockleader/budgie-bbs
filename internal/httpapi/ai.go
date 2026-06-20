package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// Generative-AI HTTP surface. The board api_token is write-only: PATCH accepts
// it, but no GET ever returns it (GetBoardAIConfig reports only tokenSet).

// GET /api/v1/ai/settings — any authenticated user may read the site-wide toggle
// so the UI knows whether AI can be offered.
func (s *Server) handleGetAISettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.core.AISettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// PATCH /api/v1/admin/ai-settings — admin-only site-wide enable/disable.
func (s *Server) handleSetAISettings(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	settings, err := s.core.SetAISettings(req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// GET /api/v1/boards/{board}/ai — board-settings managers read the config
// (token never included; tokenSet reports whether one is stored).
func (s *Server) handleGetBoardAIConfig(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	if !s.core.ActorCanSetBoardSettings(actor, boardID) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board settings permission required", false)
		return
	}
	cfg, err := s.core.BoardAIConfig(boardID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type boardAIConfigRequest struct {
	Enabled     *bool   `json:"enabled"`
	Provider    *string `json:"provider"`
	Model       *string `json:"model"`
	APIToken    *string `json:"apiToken"`
	TriggerRole *string `json:"triggerRole"`
	Mode        *string `json:"mode"`
	ReplyPrompt *string `json:"replyPrompt"`
	MaxTotal    *int    `json:"maxTotal"`
	MaxPerHour  *int    `json:"maxPerHour"`
}

// PATCH /api/v1/boards/{board}/ai — board-settings managers update the config.
// Enabling requires the site-wide toggle to be on and provisions the bot user.
func (s *Server) handleSetBoardAIConfig(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	boardID := r.PathValue("board")
	if !s.core.ActorCanSetBoardSettings(actor, boardID) {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board settings permission required", false)
		return
	}
	if _, err := s.core.GetBoardInfo(boardID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "board not found", false)
		return
	}
	var req boardAIConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	if req.Enabled != nil && *req.Enabled && !s.core.AIEnabled() {
		writeError(w, http.StatusConflict, "conflict", "AI integration is disabled site-wide", false)
		return
	}
	if req.APIToken != nil {
		t := strings.TrimSpace(*req.APIToken)
		req.APIToken = &t
	}
	cfg, err := s.core.SetBoardAIConfig(boardID, core.BoardAIConfigPatch{
		Enabled:     req.Enabled,
		Provider:    req.Provider,
		Model:       req.Model,
		APIToken:    req.APIToken,
		TriggerRole: req.TriggerRole,
		Mode:        req.Mode,
		ReplyPrompt: req.ReplyPrompt,
		MaxTotal:    req.MaxTotal,
		MaxPerHour:  req.MaxPerHour,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	// Provision the bot user when enabled so it can post.
	if cfg.Enabled {
		if _, err := s.core.EnsureBoardAIBot(boardID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
			return
		}
		cfg, _ = s.core.BoardAIConfig(boardID)
	}
	writeJSON(w, http.StatusOK, cfg)
}
