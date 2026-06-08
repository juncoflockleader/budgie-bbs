// budgied is the BudgieBBS server — HTTP, WebSocket, and SSH all in one binary.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
		storage    = flag.String("storage", "sqlite", "Storage backend: sqlite or postgres")
		httpAddr   = flag.String("http", ":8080", "HTTP/WebSocket listen address")
		sshPort    = flag.Int("ssh", 2222, "SSH listen port")
		hostKey    = flag.String("hostkey", "", "Path to SSH host key (auto-generated if empty)")
		jwtSecret  = flag.String("jwt-secret", "", "JWT signing secret (random if empty)")
		webRoot    = flag.String("web", "", "Path to web/dist directory for SPA serving (optional)")
		nntpAddr   = flag.String("nntp", "", "NNTP listen address (optional, e.g. :1190)")
		nntpDomain = flag.String("nntp-domain", "budgie.local", "Domain used for NNTP Message-ID values")
		nntpPrefix = flag.String("nntp-prefix", "budgie", "NNTP newsgroup prefix")
		migratePG  = flag.Bool("migrate-sqlite-to-postgres", false, "Migrate SQLite source DB into PostgreSQL and exit")
		pgDSN      = flag.String("postgres-dsn", "", "PostgreSQL DSN (also read from BUDGIE_POSTGRES_DSN)")
		rebuild    = flag.Bool("rebuild-projections", false, "Rebuild projection tables from durable events and exit")
		rebuildSeq = flag.Int64("rebuild-from-seq", 0, "Replay durable events with seq > value during projection rebuild")
		autoStats  = flag.Bool("auto-stats", true, "Automatically publish the daily BBSLists stats snapshot")
		doorsConf  = flag.String("doors", "", "Path to doors.json config file for door games (optional)")
	)
	flag.Parse()

	// Resolve storage backend and DSN (reads env var, handles backwards compat).
	*storage, *pgDSN = resolveStorage(*storage, *pgDSN)

	// JWT secret: use env var, flag, or a fixed dev default.
	secret := []byte(*jwtSecret)
	if len(secret) == 0 {
		secret = []byte(envOr("BUDGIE_JWT_SECRET", "change-me-in-production"))
	}

	if *migratePG {
		if *pgDSN == "" {
			slog.Error("postgres DSN required for migration", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
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
		var (
			c   *core.Core
			err error
		)
		if *storage == "postgres" {
			if *pgDSN == "" {
				slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
				os.Exit(1)
			}
			c, err = core.NewPostgres(*pgDSN)
		} else {
			c, err = core.New(*dbPath)
		}
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		if err := c.RebuildProjectionsFromEventLog(*rebuildSeq); err != nil {
			slog.Error("projection rebuild failed", "err", err)
			os.Exit(1)
		}
		if *storage == "postgres" {
			slog.Info("projection rebuild completed", "storage", "postgres", "dsn", obfuscateDSN(*pgDSN), "fromSeq", *rebuildSeq)
		} else {
			slog.Info("projection rebuild completed", "storage", "sqlite", "db", *dbPath, "fromSeq", *rebuildSeq)
		}
		return
	}

	// Open core (single path for both storage backends).
	var (
		c   *core.Core
		err error
	)
	switch *storage {
	case "postgres":
		if *pgDSN == "" {
			slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
			os.Exit(1)
		}
		slog.Info("starting budgied", "storage", "postgres", "dsn", obfuscateDSN(*pgDSN))
		c, err = core.NewPostgres(*pgDSN)
	default:
		slog.Info("starting budgied", "storage", "sqlite", "db", *dbPath)
		c, err = core.New(*dbPath)
	}
	if err != nil {
		slog.Error("core init failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Register scrape-time metrics collectors (SSH sessions, outbox counts).
	c.RegisterMetricsCollectors()

	// Start the single-writer goroutine.
	go c.Run(ctx)
	if *autoStats {
		startStatsSnapshotScheduler(ctx, c)
	}

	// HTTP + WebSocket mux.
	httpSrv := httpapi.New(c, secret)
	if root := resolveWebRoot(*webRoot); root != "" {
		httpSrv.SetWebRoot(root)
	}
	wsSrv := wsapi.New(c, secret)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/ws", wsSrv)
	mux.Handle("/", httpSrv.Handler())

	srv := &http.Server{Addr: *httpAddr, Handler: mux}
	go func() {
		slog.Info("HTTP+WS listening", "addr", *httpAddr, "storage", *storage)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// SSH TUI server.
	hk := hostKeyPath(*hostKey)
	ensureHostKey(hk)
	tuiSrv := tui.New(c, *sshPort, hk)
	if *doorsConf != "" {
		doors, err := core.LoadDoorsConfig(*doorsConf)
		if err != nil {
			slog.Error("doors config load failed", "path", *doorsConf, "err", err)
			os.Exit(1)
		}
		if doors != nil {
			tuiSrv.SetDoors(doors)
			slog.Info("doors loaded", "count", len(doors.Doors), "path", *doorsConf)
		}
	}
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

// resolveStorage returns the effective storage backend and DSN.
// It reads the DSN from the BUDGIE_POSTGRES_DSN env var when pgDSN is empty,
// and infers postgres mode when a DSN is present but storage is still "sqlite"
// (the default), preserving backwards compatibility with -postgres-dsn usage.
func resolveStorage(storage, pgDSN string) (resolvedStorage, resolvedDSN string) {
	if pgDSN == "" {
		pgDSN = envOr("BUDGIE_POSTGRES_DSN", "")
	}
	if pgDSN != "" && storage == "sqlite" {
		storage = "postgres"
	}
	return storage, pgDSN
}

func resolveWebRoot(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	const localDist = "web/dist"
	if hasWebIndex(localDist) {
		slog.Info("serving web SPA", "path", localDist)
		return localDist
	}
	return ""
}

func hasWebIndex(root string) bool {
	if root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, "index.html"))
	return err == nil && !info.IsDir()
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
