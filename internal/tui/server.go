// Package tui implements the SSH "rendered transport": the server listens for
// SSH connections, authenticates via public key, then spawns a bubbletea TUI
// that acts as a first-class client of the event bus.
package tui

import (
	"context"
	"fmt"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// Server wraps a wish SSH server.
type Server struct {
	core    *core.Core
	port    int
	hostKey string
}

// New creates an SSH server. hostKey is the path to the host private key file.
func New(c *core.Core, port int, hostKey string) *Server {
	return &Server{core: c, port: port, hostKey: hostKey}
}

// ListenAndServe starts the SSH server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf(":%d", s.port)),
		wish.WithHostKeyPath(s.hostKey),
		wish.WithPublicKeyAuth(s.authPublicKey),
		wish.WithMiddleware(
			bubbletea.Middleware(s.tuiHandler),
		),
	)
	if err != nil {
		return fmt.Errorf("create ssh server: %w", err)
	}

	slog.Info("SSH server listening", "port", s.port)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return srv.Close()
	}
}

// authPublicKey authenticates the SSH connection by looking up the key
// fingerprint in the database and attaching the user to the session context.
func (s *Server) authPublicKey(ctx ssh.Context, key ssh.PublicKey) bool {
	// Convert charmbracelet/ssh PublicKey to golang.org/x/crypto/ssh PublicKey
	// to use FingerprintSHA256.
	cryptoKey, err := gossh.ParsePublicKey(key.Marshal())
	if err != nil {
		slog.Warn("ssh: could not parse pubkey", "err", err)
		ctx.SetValue(userKey{}, guestUser())
		return true
	}
	fp := gossh.FingerprintSHA256(cryptoKey)

	user, err := s.core.UserByPubkey(fp)
	if err != nil || user == nil {
		slog.Warn("ssh: unknown pubkey, proceeding as guest", "fp", fp)
		ctx.SetValue(userKey{}, guestUser())
		return true
	}
	ctx.SetValue(userKey{}, user)
	return true
}

// tuiHandler is the bubbletea program factory called once per SSH session.
func (s *Server) tuiHandler(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	user, _ := sess.Context().Value(userKey{}).(*core.User)
	if user == nil {
		user = guestUser()
	}

	pty, _, _ := sess.Pty()
	width := pty.Window.Width
	height := pty.Window.Height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	m := newModel(s.core, user, width, height)
	return m, []tea.ProgramOption{tea.WithAltScreen()}
}

type userKey struct{}

func guestUser() *core.User {
	return &core.User{ID: "guest", Name: "guest", Role: "user"}
}
