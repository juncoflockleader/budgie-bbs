package httpapi

// M14 — Node Spy: HTTP admin endpoints for listing, kicking, and messaging
// active SSH sessions. All endpoints require admin role.

import (
	"encoding/json"
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// handleListNodes returns all active SSH sessions (admin only).
func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	nodes := s.core.ListNodes()
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// handleKickNode forcibly closes the SSH session identified by {nodeId}.
func (s *Server) handleKickNode(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	nodeID := r.PathValue("nodeId")
	if nodeID == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "nodeId required", false)
		return
	}
	if err := s.core.KickNode(nodeID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSendNodeMessage enqueues a sysop message for the session {nodeId}.
// Body: {"text": "..."}
func (s *Server) handleSendNodeMessage(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	nodeID := r.PathValue("nodeId")
	if nodeID == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "nodeId required", false)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "text required", false)
		return
	}
	msg := actor.Name + ": " + body.Text
	if err := s.core.SendNodeMessage(nodeID, msg); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
