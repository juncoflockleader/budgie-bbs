// budgied is the BudgieBBS server — HTTP, WebSocket, and SSH all in one binary.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/tui"
	"github.com/juncoflockleader/budgie-bbs/internal/wsapi"
)

func main() {
	var (
		dbPath    = flag.String("db", "budgie.db", "SQLite database path")
		httpAddr  = flag.String("http", ":8080", "HTTP/WebSocket listen address")
		sshPort   = flag.Int("ssh", 2222, "SSH listen port")
		hostKey   = flag.String("hostkey", "", "Path to SSH host key (auto-generated if empty)")
		jwtSecret = flag.String("jwt-secret", "", "JWT signing secret (random if empty)")
		webRoot   = flag.String("web", "", "Path to web/dist directory for SPA serving (optional)")
	)
	flag.Parse()

	// JWT secret: use env var, flag, or a fixed dev default.
	secret := []byte(*jwtSecret)
	if len(secret) == 0 {
		secret = []byte(envOr("BUDGIE_JWT_SECRET", "change-me-in-production"))
	}

	// Open the core.
	c, err := core.New(*dbPath)
	if err != nil {
		slog.Error("core init failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the single-writer goroutine.
	go c.Run(ctx)

	// HTTP + WebSocket mux.
	httpSrv := httpapi.New(c, secret)
	if *webRoot != "" {
		httpSrv.SetWebRoot(*webRoot)
	}
	wsSrv := wsapi.New(c, secret)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/ws", wsSrv)
	mux.Handle("/", httpSrv.Handler())

	srv := &http.Server{Addr: *httpAddr, Handler: mux}
	go func() {
		slog.Info("HTTP+WS listening", "addr", *httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// SSH TUI server.
	hk := hostKeyPath(*hostKey)
	ensureHostKey(hk)
	tuiSrv := tui.New(c, *sshPort, hk)
	go func() {
		if err := tuiSrv.ListenAndServe(ctx); err != nil {
			slog.Error("SSH server error", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	srv.Shutdown(context.Background())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hostKeyPath(path string) string {
	if path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "budgie_host_key"
	}
	return home + "/.ssh/budgie_host_key"
}

func ensureHostKey(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	slog.Info("generating SSH host key", "path", path)
	if err := generateHostKey(path); err != nil {
		slog.Warn("could not generate host key, SSH may fail", "err", err)
	}
}

func generateHostKey(path string) error {
	return tui.GenerateHostKey(path)
}
