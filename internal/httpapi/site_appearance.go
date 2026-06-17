package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// handleGetSiteAppearance serves the site branding/appearance settings. Public
// and unauthenticated so the login page and anonymous visitors get the title,
// banner, accent, and default theme.
func (s *Server) handleGetSiteAppearance(w http.ResponseWriter, r *http.Request) {
	a, err := s.core.SiteAppearance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleGetTUIStockArt serves the catalog of built-in ASCII art presets so the
// admin layout editor can offer them with previews. Public (decorative only).
func (s *Server) handleGetTUIStockArt(w http.ResponseWriter, r *http.Request) {
	arts := make([]map[string]string, 0)
	for _, name := range core.StockTUIArtNames() {
		arts = append(arts, map[string]string{"name": name, "art": core.StockTUIArt(name)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"arts": arts})
}

// handleSetSiteAppearance updates the site appearance (admin only).
func (s *Server) handleSetSiteAppearance(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	var req core.SiteAppearance
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "invalid body", false)
		return
	}
	a, err := s.core.SetSiteAppearance(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
