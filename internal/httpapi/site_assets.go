package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strconv"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	maxSiteAssetBytes = 3 << 20 // 3 MiB
	// siteAssetCacheSeconds is short so a CDN/browser revalidates often; the
	// ETag (asset updatedAt) makes those revalidations cheap 304s and busts the
	// cache immediately when an admin replaces the image.
	siteAssetCacheSeconds = 300
)

// siteAssetMaxDims returns the max allowed pixel dimensions per asset slot.
func siteAssetMaxDims(name string) (maxW, maxH int) {
	switch name {
	case "banner":
		return 2560, 1024
	default: // logo
		return 1024, 1024
	}
}

// handleGetSiteAsset serves an uploaded site image (logo/banner). Public so the
// login page and header can load it; 404 when unset so the client falls back.
// Cache-friendly (ETag + max-age) so a CDN can sit in front of this route.
func (s *Server) handleGetSiteAsset(w http.ResponseWriter, r *http.Request) {
	data, ct, updatedAt, err := s.core.SiteAsset(r.PathValue("name"))
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
	etag := `"` + strconv.FormatInt(updatedAt, 10) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(siteAssetCacheSeconds))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// handleSetSiteAsset stores an uploaded PNG for a site asset slot (admin only).
// The raw image is sent as the request body. Enforces a byte cap, that the body
// is a real PNG, and per-slot pixel-dimension limits.
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
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "asset must be a valid PNG image", false)
		return
	}
	maxW, maxH := siteAssetMaxDims(name)
	if cfg.Width > maxW || cfg.Height > maxH {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed",
			fmt.Sprintf("image is %dx%d; the max for the %s is %dx%d", cfg.Width, cfg.Height, name, maxW, maxH), false)
		return
	}
	if err := s.core.SetSiteAsset(name, "image/png", data); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "byteSize": len(data), "width": cfg.Width, "height": cfg.Height})
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
