package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// handleGetSiteAppearance serves the site branding/appearance settings. Public
// and unauthenticated so the login page and anonymous visitors get the title,
// banner, accent, and default theme.
type siteAppearanceResponse struct {
	*core.SiteAppearance
	// AssetBaseURL is the external base (CDN/bucket) for uploaded images, or ""
	// when the app serves them. AssetVersions is each asset's version (0 = unset)
	// so the client can build cache-busting / CDN URLs.
	AssetBaseURL  string           `json:"assetBaseURL"`
	AssetVersions map[string]int64 `json:"assetVersions"`
}

func (s *Server) handleGetSiteAppearance(w http.ResponseWriter, r *http.Request) {
	a, err := s.core.SiteAppearance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, siteAppearanceResponse{
		SiteAppearance: a,
		AssetBaseURL:   s.core.AssetPublicBaseURL(),
		AssetVersions:  s.core.SiteAssetVersions(),
	})
}

// handleGetTUIStockArt serves the catalog of built-in ASCII art presets so the
// admin layout editor can offer them with previews. Public (decorative only).
func (s *Server) handleGetTUIStockArt(w http.ResponseWriter, r *http.Request) {
	arts := make([]map[string]string, 0)
	for _, name := range sitemodel.StockTUIArtNames() {
		arts = append(arts, map[string]string{"name": name, "art": sitemodel.StockTUIArt(name)})
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
