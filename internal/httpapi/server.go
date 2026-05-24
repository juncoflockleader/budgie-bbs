// Package httpapi implements the HTTP transport (Tiers 3 and 4 of the
// transport ladder). All requests are validated here; authority lives in core.
package httpapi

import (
	"net/http"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// Server wires the HTTP routes to the core.
type Server struct {
	core      *core.Core
	jwtSecret []byte
}

// New creates an HTTP server.
func New(c *core.Core, jwtSecret []byte) *Server {
	return &Server{core: c, jwtSecret: jwtSecret}
}

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
	mux.Handle("GET /api/v1/boards", auth(http.HandlerFunc(s.handleListBoards)))
	mux.Handle("GET /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleListThreads)))
	mux.Handle("GET /api/v1/threads/{thread}", auth(http.HandlerFunc(s.handleGetThread)))
	mux.Handle("GET /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleListPosts)))

	// Authenticated write endpoints
	mux.Handle("POST /api/v1/auth/pubkey", auth(http.HandlerFunc(s.handleAddPubkey)))
	mux.Handle("POST /api/v1/commands", auth(http.HandlerFunc(s.handleCommand)))
	mux.Handle("POST /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleCreateThread)))
	mux.Handle("POST /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleAppendPost)))
	mux.Handle("PATCH /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleEditPost)))
	mux.Handle("DELETE /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleRedactPost)))
	mux.Handle("POST /api/v1/posts/{post}/restore", auth(http.HandlerFunc(s.handleRestorePost)))
	mux.Handle("POST /api/v1/threads/{thread}/lock", auth(http.HandlerFunc(s.handleLockThread)))
	mux.Handle("POST /api/v1/chat/{room}/lines", auth(http.HandlerFunc(s.handleSendChatLine)))

	return mux
}
