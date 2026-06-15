// Package tui implements the SSH "rendered transport": the server listens for
// SSH connections, authenticates via public key, then spawns a bubbletea TUI
// that acts as a first-class client of the event bus.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// Server wraps a wish SSH server.
type Server struct {
	core              *core.Core
	port              int
	hostKey           string
	doors             *core.DoorsConfig // optional; nil means doors are disabled
	allowRegistration bool              // opt-in guest self-registration over SSH
}

// New creates an SSH server. hostKey is the path to the host private key file.
func New(c *core.Core, port int, hostKey string) *Server {
	return &Server{core: c, port: port, hostKey: hostKey}
}

// SetDoors configures the door games available to SSH sessions.
func (s *Server) SetDoors(cfg *core.DoorsConfig) { s.doors = cfg }

// SetAllowRegistration enables the guest "create account" flow in the TUI.
func (s *Server) SetAllowRegistration(v bool) { s.allowRegistration = v }

// ListenAndServe starts the SSH server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf(":%d", s.port)),
		wish.WithHostKeyPath(s.hostKey),
		wish.WithPublicKeyAuth(s.authPublicKey),
		wish.WithPasswordAuth(s.authPassword),
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
		if isGuestSSHUser(ctx.User()) {
			slog.Warn("ssh: unknown pubkey, proceeding as guest", "fp", fp)
			ctx.SetValue(userKey{}, guestUser())
			return true
		}
		slog.Warn("ssh: unknown pubkey", "user", ctx.User(), "fp", fp)
		return false
	}
	ctx.SetValue(userKey{}, user)
	return true
}

// authPassword authenticates SSH password logins against the same account
// credentials and login host ACLs used by the HTTP login endpoint.
func (s *Server) authPassword(ctx ssh.Context, password string) bool {
	username := strings.TrimSpace(ctx.User())
	if username == "" || isGuestSSHUser(username) {
		return false
	}
	user, err := s.core.AuthenticateUserFromHost(username, password, remoteHost(ctx.RemoteAddr()))
	if err != nil || user == nil {
		slog.Warn("ssh: password auth failed", "user", username, "err", err)
		return false
	}
	if err := s.core.RecordLogin(user.ID); err != nil {
		slog.Error("ssh: could not record login", "user", username, "err", err)
		return false
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

	// M14: register this SSH session with the node registry so sysops can see
	// it, kick it, or send it a message.
	nodeID := core.NewSessionNodeID()
	entry := core.NodeEntry{
		NodeID:    nodeID,
		UserID:    user.ID,
		Username:  user.Name,
		RemoteIP:  remoteHost(sess.RemoteAddr()),
		Location:  "main-menu",
		LoginTime: time.Now(),
	}
	// Kick closes the SSH session; the deferred Unregister below then fires.
	msgCh := s.core.Nodes.Register(entry, func() { _ = sess.Close() })

	// Unregister when the SSH session ends (normal disconnect or kick).
	go func() {
		<-sess.Context().Done()
		s.core.Nodes.Unregister(nodeID)
	}()

	caps := terminalProfileFromEnviron(sess.Environ())
	var doors []core.DoorConfig
	if s.doors != nil {
		doors = s.doors.Doors
	}
	m := newModel(s.core, user, width, height, caps.supportsANSI, caps.locale, nodeID, msgCh, doors, caps.termName, s.allowRegistration)
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithEnvironment(sess.Environ()),
	}
	if caps.baudDelay > 0 {
		opts = append(opts, tea.WithOutput(newBaudWriter(sess, caps.baudDelay)))
	}
	return m, opts
}

type userKey struct{}

func guestUser() *core.User {
	return &core.User{ID: "guest", Name: "guest", Role: "user"}
}

func isGuestSSHUser(username string) bool {
	return strings.EqualFold(strings.TrimSpace(username), "guest")
}

func remoteHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}
