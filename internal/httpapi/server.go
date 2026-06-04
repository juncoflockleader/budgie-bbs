// Package httpapi implements the HTTP transport (Tiers 3 and 4 of the
// transport ladder). All requests are validated here; authority lives in core.
package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// Server wires the HTTP routes to the core.
type Server struct {
	core      *core.Core
	jwtSecret []byte
	// webRoot, if non-empty, serves a SPA from this filesystem path.
	webRoot string
}

// New creates an HTTP server.
func New(c *core.Core, jwtSecret []byte) *Server {
	return &Server{core: c, jwtSecret: jwtSecret}
}

// SetWebRoot configures a directory to serve the web SPA from.
// All non-API requests are served from this directory; unknown paths fall back
// to index.html (client-side routing).
func (s *Server) SetWebRoot(path string) { s.webRoot = path }

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Auth (no middleware required)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	// Authenticated read-only endpoints
	auth := s.requireAuth
	mux.Handle("GET /api/v1/events", auth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/v1/events/stream", auth(http.HandlerFunc(s.handleEventsStream)))
	mux.Handle("GET /api/v1/categories", auth(http.HandlerFunc(s.handleListCategories)))
	mux.Handle("GET /api/v1/boards", auth(http.HandlerFunc(s.handleListBoards)))
	mux.Handle("GET /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleListThreads)))
	mux.Handle("GET /api/v1/threads/{thread}", auth(http.HandlerFunc(s.handleGetThread)))
	mux.Handle("GET /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleListPosts)))
	mux.Handle("GET /api/v1/threads/{thread}/polls", auth(http.HandlerFunc(s.handleListThreadPolls)))
	mux.Handle("GET /api/v1/search", auth(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/v1/audit", auth(http.HandlerFunc(s.handleAuditLog)))
	mux.Handle("GET /api/v1/notifications", auth(http.HandlerFunc(s.handleListNotifications)))
	mux.Handle("GET /api/v1/polls/{poll}", auth(http.HandlerFunc(s.handleGetPoll)))
	mux.Handle("GET /api/v1/posts/{post}/poll", auth(http.HandlerFunc(s.handleGetPollByPost)))
	mux.Handle("GET /api/v1/users/{name}/trust", auth(http.HandlerFunc(s.handleGetTrust)))
	mux.Handle("GET /api/v1/mod/reviewables", auth(http.HandlerFunc(s.handleListReviewables)))
	mux.Handle("GET /api/v1/users/{name}/sanctions", auth(http.HandlerFunc(s.handleListUserSanctions)))

	// Public read-only profile endpoints.
	mux.HandleFunc("GET /api/v1/users/{name}", s.handleGetUserProfile)
	mux.HandleFunc("GET /api/v1/users/{name}/posts", s.handleListUserPosts)

	// Authenticated write endpoints
	mux.Handle("POST /api/v1/auth/pubkey", auth(http.HandlerFunc(s.handleAddPubkey)))
	mux.Handle("PATCH /api/v1/users/me", auth(http.HandlerFunc(s.handleUpdateOwnProfile)))
	mux.Handle("POST /api/v1/boards", auth(http.HandlerFunc(s.handleCreateBoard)))
	mux.Handle("POST /api/v1/commands", auth(http.HandlerFunc(s.handleCommand)))
	mux.Handle("POST /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleCreateThread)))
	mux.Handle("POST /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleAppendPost)))
	mux.Handle("PATCH /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleEditPost)))
	mux.Handle("DELETE /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleRedactPost)))
	mux.Handle("POST /api/v1/posts/{post}/restore", auth(http.HandlerFunc(s.handleRestorePost)))
	mux.Handle("POST /api/v1/posts/{post}/purge", auth(http.HandlerFunc(s.handlePurgePost)))
	mux.Handle("POST /api/v1/posts/{post}/flag", auth(http.HandlerFunc(s.handleFlagPost)))
	mux.Handle("POST /api/v1/posts/{post}/react", auth(http.HandlerFunc(s.handleReactPost)))
	mux.Handle("DELETE /api/v1/posts/{post}/react", auth(http.HandlerFunc(s.handleUnreactPost)))
	mux.Handle("POST /api/v1/polls/{poll}/vote", auth(http.HandlerFunc(s.handleVotePoll)))
	mux.Handle("POST /api/v1/notifications/{id}/read", auth(http.HandlerFunc(s.handleMarkNotificationRead)))
	mux.Handle("POST /api/v1/notifications/read-all", auth(http.HandlerFunc(s.handleMarkAllRead)))
	mux.Handle("PUT /api/v1/threads/{thread}/prefs", auth(http.HandlerFunc(s.handleSetThreadPref)))
	mux.Handle("POST /api/v1/threads/{thread}/lock", auth(http.HandlerFunc(s.handleLockThread)))
	mux.Handle("POST /api/v1/mod/reviewables/{id}/resolve", auth(http.HandlerFunc(s.handleResolveReview)))
	mux.Handle("POST /api/v1/users/{name}/sanctions", auth(http.HandlerFunc(s.handleSanctionUser)))
	mux.Handle("POST /api/v1/chat/{room}/lines", auth(http.HandlerFunc(s.handleSendChatLine)))

	// SPA static files (must come last — catches everything not matched above).
	if s.webRoot != "" {
		mux.Handle("/", spaHandler(s.webRoot))
	}

	return mux
}

// spaHandler serves files from root; requests to unknown paths fall back to
// index.html so that the client-side router can handle them.
func spaHandler(root string) http.Handler {
	dir := os.DirFS(root)
	fileServer := http.FileServer(http.FS(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't intercept API paths (safety net — these are matched first by mux).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// Check if the requested file exists; if not, serve index.html.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dir, path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
