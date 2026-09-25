package core

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
)

// pgLeaderLockKey is the Postgres advisory-lock key used to elect a single
// background-worker leader across the cluster. It is intentionally distinct from
// pgAdvisoryLockKey (per-command write serialization) so the two never collide.
const pgLeaderLockKey = int64(1654893722)

// leaderPollInterval is how often a follower retries to acquire leadership, and
// how often the current leader pings its connection to detect a drop. It is a
// var (not const) so tests can shorten it.
var leaderPollInterval = 5 * time.Second

// StartOutboxWorker runs the outbox worker directly (no leader election). Used
// by the single-process SQLite path and by the test harness, where exactly one
// process owns the database.
func (c *Core) StartOutboxWorker(ctx context.Context) {
	go runOutboxWorker(ctx, c.DB, c.Bus)
}

// StartBackgroundWorker starts the cluster's background jobs (outbox processing
// and, optionally, the daily stats snapshot). In Postgres mode the jobs run only
// while this node holds the leader advisory lock, so multiple worker-role nodes
// are safe — exactly one is active at a time, with automatic failover when the
// leader dies. In SQLite mode the jobs run directly. Returns immediately; work
// happens in background goroutines that stop when ctx is cancelled.
func (c *Core) StartBackgroundWorker(ctx context.Context, autoStats bool) {
	if currentSQLFlavor != postgresFlavor {
		c.StartOutboxWorker(ctx)
		if autoStats {
			go c.runStatsScheduler(ctx)
		}
		return
	}
	go c.runLeaderElectedWorker(ctx, autoStats)
}

// runLeaderElectedWorker contends for the leader lock; whenever it wins, it runs
// the background jobs until it loses the lock connection or ctx is cancelled,
// then re-contends.
func (c *Core) runLeaderElectedWorker(ctx context.Context, autoStats bool) {
	for ctx.Err() == nil {
		conn, err := c.DB.Conn(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("worker: could not get leader-election connection", "err", err)
			}
			if !waitOrDone(ctx, leaderPollInterval) {
				return
			}
			continue
		}

		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, pgLeaderLockKey).Scan(&got); err != nil {
			_ = conn.Close()
			if ctx.Err() == nil {
				slog.Warn("worker: leader lock attempt failed", "err", err)
			}
			if !waitOrDone(ctx, leaderPollInterval) {
				return
			}
			continue
		}
		if !got {
			_ = conn.Close()
			if !waitOrDone(ctx, leaderPollInterval) {
				return
			}
			continue
		}

		// We are the leader. Serve until we lose the connection or shut down.
		c.serveAsLeader(ctx, conn, autoStats)

		// Release the lock (best-effort) and close the dedicated connection.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pgLeaderLockKey)
		_ = conn.Close()
	}
}

// serveAsLeader runs the background jobs while holding leadership and returns
// when ctx is cancelled or the leader connection is lost.
func (c *Core) serveAsLeader(ctx context.Context, conn *sql.Conn, autoStats bool) {
	slog.Info("worker: acquired leadership", "node", c.nodeID)
	c.isLeader.Store(true)
	metrics.WorkerIsLeader.Set(1)
	defer func() {
		c.isLeader.Store(false)
		metrics.WorkerIsLeader.Set(0)
	}()

	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go runOutboxWorker(leaderCtx, c.DB, c.Bus)
	if autoStats {
		go c.runStatsScheduler(leaderCtx)
	}

	ticker := time.NewTicker(leaderPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("worker: releasing leadership (shutdown)", "node", c.nodeID)
			return
		case <-ticker.C:
			if err := conn.PingContext(ctx); err != nil {
				if ctx.Err() == nil {
					slog.Warn("worker: leader connection lost, stepping down", "err", err)
				}
				return
			}
		}
	}
}

// runStatsScheduler publishes the daily stats snapshot on startup and hourly
// thereafter. PublishDailyStatsSnapshot is idempotent (cid auto-stats-<date>),
// so re-runs and brief leadership overlaps are harmless.
func (c *Core) runStatsScheduler(ctx context.Context) {
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
}

// waitOrDone sleeps for d, returning false if ctx is cancelled first.
func waitOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
