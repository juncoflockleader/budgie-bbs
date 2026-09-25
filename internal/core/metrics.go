package core

import (
	"log/slog"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
)

// RegisterMetricsCollectors wires scrape-time gauges that cannot be tracked
// incrementally — open SSH sessions (from the in-memory node registry) and
// outbox job counts by status (from a GROUP BY query). Call once at startup.
func (c *Core) RegisterMetricsCollectors() {
	metrics.RegisterCollector(func() []metrics.Sample {
		n := 0
		if c.Nodes != nil {
			n = len(c.Nodes.List())
		}
		return []metrics.Sample{{
			Name:  "budgie_ssh_sessions",
			Help:  "Open SSH TUI sessions on this node.",
			Type:  "gauge",
			Value: float64(n),
		}}
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		counts, err := outboxStatusCounts(c.DB)
		if err != nil {
			slog.Warn("metrics: outbox status count failed", "err", err)
			return nil
		}
		// Always report the operationally interesting statuses so the series
		// exist even when zero.
		for _, st := range []string{"pending", "running", "done", "dead"} {
			if _, ok := counts[st]; !ok {
				counts[st] = 0
			}
		}
		samples := make([]metrics.Sample, 0, len(counts))
		for status, n := range counts {
			samples = append(samples, metrics.Sample{
				Name:   "budgie_outbox_jobs",
				Help:   "Outbox jobs by status.",
				Type:   "gauge",
				Labels: map[string]string{"status": status},
				Value:  float64(n),
			})
		}
		return samples
	})
}
