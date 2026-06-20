package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// sitemapCache holds the most recently generated sitemap and serves it with a
// stale-while-revalidate policy: a cold cache generates synchronously, a fresh
// cache is returned as-is, and a stale cache is served immediately while a
// single background goroutine regenerates it. Generation runs outside the lock
// so a slow DB scan never blocks readers.
type sitemapCache struct {
	mu          sync.Mutex
	data        []byte
	baseURL     string
	generatedAt time.Time
	refreshing  bool
}

// serve returns sitemap bytes for base, regenerating via gen as needed. interval
// is the freshness window (cache TTL); interval<=0 means "always considered
// stale" (still served from cache while a background refresh runs).
func (sc *sitemapCache) serve(gen func(string) []byte, base string, interval time.Duration) []byte {
	sc.mu.Lock()
	hit := sc.data != nil && sc.baseURL == base
	fresh := hit && interval > 0 && time.Since(sc.generatedAt) < interval
	switch {
	case fresh:
		d := sc.data
		sc.mu.Unlock()
		return d
	case hit:
		// Stale but usable: serve the old copy now, refresh once in the
		// background so the slow path never blocks a request.
		d := sc.data
		if !sc.refreshing {
			sc.refreshing = true
			go func() { sc.store(base, gen(base)) }()
		}
		sc.mu.Unlock()
		return d
	default:
		// Cold cache, or the base URL changed (e.g. first request, or a
		// different host): generate synchronously.
		sc.mu.Unlock()
		data := gen(base)
		sc.store(base, data)
		return data
	}
}

// store records a freshly generated sitemap, or just clears the refreshing flag
// when generation failed (keeping any previous copy).
func (sc *sitemapCache) store(base string, data []byte) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.refreshing = false
	if data == nil {
		return
	}
	sc.data = data
	sc.baseURL = base
	sc.generatedAt = time.Now()
}

// sitemapTTL returns the configured regeneration interval, defaulting when unset.
func (s *Server) sitemapTTL() time.Duration {
	if s.sitemapInterval > 0 {
		return s.sitemapInterval
	}
	return core.DefaultSitemapInterval
}

// resolveBaseURL returns the configured public base URL, or derives one from the
// incoming request (honoring a reverse proxy's X-Forwarded-Proto/Host).
func (s *Server) resolveBaseURL(r *http.Request) string {
	if s.seoBaseURL != "" {
		return s.seoBaseURL
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + host
}

// handleRobotsTxt serves robots.txt, advertising the sitemap and keeping the
// JSON API out of the index.
func (s *Server) handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	body := core.GenerateRobotsTxt(s.resolveBaseURL(r))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

// handleSitemap serves the cached sitemap of the guest-readable site.
func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	base := s.resolveBaseURL(r)
	body := s.sitemap.serve(s.generateSitemap, base, s.sitemapTTL())
	if len(body) == 0 {
		body = core.MinimalSitemap(base) // generation failed on a cold cache
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

// generateSitemap is the cache's generation callback. On error it returns nil so
// the cache keeps any previous copy; the very first cold failure falls back to a
// minimal valid sitemap (just the homepage) so the endpoint never 500s.
func (s *Server) generateSitemap(base string) []byte {
	data, stats, err := s.core.GenerateSitemap(base)
	if err != nil {
		slog.Error("sitemap generation failed", "err", err)
		return nil
	}
	if stats.Truncated {
		slog.Warn("sitemap truncated at URL cap", "boards", stats.Boards, "threads", stats.Threads)
	}
	return data
}
