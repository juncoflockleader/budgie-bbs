// budgied is the BudgieBBS server — HTTP, WebSocket, and SSH all in one binary.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/nntp"
	"github.com/juncoflockleader/budgie-bbs/internal/tui"
	"github.com/juncoflockleader/budgie-bbs/internal/wsapi"
)

func main() {
	var (
		dbPath     = flag.String("db", "budgie.db", "SQLite database path")
		httpAddr   = flag.String("http", ":8080", "HTTP/WebSocket listen address")
		sshPort    = flag.Int("ssh", 2222, "SSH listen port")
		hostKey    = flag.String("hostkey", "", "Path to SSH host key (auto-generated if empty)")
		jwtSecret  = flag.String("jwt-secret", "", "JWT signing secret (random if empty)")
		webRoot    = flag.String("web", "", "Path to web/dist directory for SPA serving (optional)")
		nntpAddr   = flag.String("nntp", "", "NNTP listen address (optional, e.g. :1190)")
		nntpDomain = flag.String("nntp-domain", "budgie.local", "Domain used for NNTP Message-ID values")
		nntpPrefix = flag.String("nntp-prefix", "budgie", "NNTP newsgroup prefix")
		migratePG  = flag.Bool("migrate-sqlite-to-postgres", false, "Migrate SQLite source DB into PostgreSQL and exit")
		pgDSN      = flag.String("postgres-dsn", "", "PostgreSQL DSN for migration or future postgres runtime (e.g. postgres://user:pass@127.0.0.1:5432/budgie)")
		rebuild    = flag.Bool("rebuild-projections", false, "Rebuild projection tables from durable events and exit")
		rebuildSeq = flag.Int64("rebuild-from-seq", 0, "Replay durable events with seq > value during projection rebuild")
		autoStats  = flag.Bool("auto-stats", true, "Automatically publish the daily BBSLists stats snapshot")
	)
	flag.Parse()

	// JWT secret: use env var, flag, or a fixed dev default.
	secret := []byte(*jwtSecret)
	if len(secret) == 0 {
		secret = []byte(envOr("BUDGIE_JWT_SECRET", "change-me-in-production"))
	}

	if *migratePG {
		if *pgDSN == "" {
			*pgDSN = envOr("BUDGIE_POSTGRES_DSN", "")
		}
		if *pgDSN == "" {
			slog.Error("postgres DSN required for migration", "flag", "postgres-dsn")
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := core.MigrateSQLiteToPostgres(ctx, *dbPath, *pgDSN); err != nil {
			slog.Error("sqlite->postgres migration failed", "err", err)
			os.Exit(1)
		}
		slog.Info("sqlite->postgres migration completed", "source", *dbPath, "dsn", obfuscateDSN(*pgDSN))
		return
	}

	if *rebuild {
		if *pgDSN != "" {
			c, err := core.NewPostgres(*pgDSN)
			if err != nil {
				slog.Error("core init failed", "err", err)
				os.Exit(1)
			}
			if err := c.RebuildProjectionsFromEventLog(*rebuildSeq); err != nil {
				slog.Error("projection rebuild failed", "err", err)
				os.Exit(1)
			}
			slog.Info("projection rebuild completed", "dsn", obfuscateDSN(*pgDSN), "fromSeq", *rebuildSeq)
			return
		}

		c, err := core.New(*dbPath)
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		if err := c.RebuildProjectionsFromEventLog(*rebuildSeq); err != nil {
			slog.Error("projection rebuild failed", "err", err)
			os.Exit(1)
		}
		slog.Info("projection rebuild completed", "db", *dbPath, "fromSeq", *rebuildSeq)
		return
	}

	if *pgDSN != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		c, err := core.NewPostgres(*pgDSN)
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		go c.Run(ctx)
		if *autoStats {
			startStatsSnapshotScheduler(ctx, c)
		}

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
			slog.Info("HTTP+WS listening (postgres)", "addr", *httpAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP server error", "err", err)
			}
		}()

		hk := hostKeyPath(*hostKey)
		ensureHostKey(hk)
		tuiSrv := tui.New(c, *sshPort, hk)
		go func() {
			if err := tuiSrv.ListenAndServe(ctx); err != nil {
				slog.Error("SSH server error", "err", err)
			}
		}()

		if *nntpAddr != "" {
			nntpSrv := nntp.New(c, *nntpAddr, *nntpDomain, *nntpPrefix)
			go func() {
				if err := nntpSrv.ListenAndServe(ctx); err != nil {
					slog.Error("NNTP server error", "err", err)
				}
			}()
		}

		<-ctx.Done()
		slog.Info("shutting down")
		srv.Shutdown(context.Background())
		return
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
	if *autoStats {
		startStatsSnapshotScheduler(ctx, c)
	}

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

	if *nntpAddr != "" {
		nntpSrv := nntp.New(c, *nntpAddr, *nntpDomain, *nntpPrefix)
		go func() {
			if err := nntpSrv.ListenAndServe(ctx); err != nil {
				slog.Error("NNTP server error", "err", err)
			}
		}()
	}

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

func startStatsSnapshotScheduler(ctx context.Context, c *core.Core) {
	publish := func() {
		result, err := c.PublishDailyStatsSnapshot(ctx, time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("automatic stats snapshot failed", "err", err)
			}
			return
		}
		if result != nil {
			slog.Info("automatic stats snapshot ensured", "thread", result.ID)
		}
	}
	go func() {
		publish()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
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

func obfuscateDSN(dsn string) string {
	if !strings.Contains(dsn, "@") {
		return "[redacted]"
	}
	parts := strings.SplitN(dsn, "@", 2)
	if len(parts) != 2 {
		return "[redacted]"
	}
	prefix := parts[0]
	hostPart := parts[1]
	if idx := strings.Index(prefix, "://"); idx >= 0 {
		prefix = prefix[:idx+3]
	}
	if idx := strings.Index(hostPart, "/"); idx >= 0 {
		hostPart = hostPart[idx:]
	} else {
		hostPart = "/"
	}
	return prefix + "****" + hostPart
}
