package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const maxSiteAssetBytes = 3 << 20 // 3 MiB

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// handleGetSiteAsset serves an uploaded site image (logo/banner). Public so the
// login page and header can load it; 404 when unset so the client falls back.
func (s *Server) handleGetSiteAsset(w http.ResponseWriter, r *http.Request) {
	data, ct, _, err := s.core.SiteAsset(r.PathValue("name"))
	if errors.Is(err, sql.ErrNoRows) || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// handleSetSiteAsset stores an uploaded PNG for a site asset slot (admin only).
// The raw image is sent as the request body.
func (s *Server) handleSetSiteAsset(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	name := r.PathValue("name")
	if !core.ValidSiteAsset(name) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "unknown site asset", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "image too large (max 3 MiB)", false)
		return
	}
	if len(data) < len(pngMagic) || !bytes.Equal(data[:len(pngMagic)], pngMagic) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "asset must be a PNG image", false)
		return
	}
	if err := s.core.SetSiteAsset(name, "image/png", data); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "byteSize": len(data)})
}

// handleDeleteSiteAsset clears an uploaded site asset (admin only).
func (s *Server) handleDeleteSiteAsset(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	if !actor.IsAdmin() {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "admin role required", false)
		return
	}
	name := r.PathValue("name")
	if !core.ValidSiteAsset(name) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "unknown site asset", false)
		return
	}
	if err := s.core.DeleteSiteAsset(name); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
