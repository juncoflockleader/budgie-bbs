package core

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
)

const gatewayScopeFanoutSampleLimit = 100

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

	metrics.RegisterCollector(func() []metrics.Sample {
		samples, err := commandLogReceiptSamples(c.DB, nowMS())
		if err != nil {
			slog.Warn("metrics: command-log receipt counts failed", "err", err)
			return nil
		}
		return samples
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		samples, err := attachmentBlobStagingSamples(c.DB, nowMS())
		if err != nil {
			slog.Warn("metrics: attachment blob staging counts failed", "err", err)
			return nil
		}
		return samples
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		samples, err := eventPartitionOffsetSamples(c.DB, 100)
		if err != nil {
			slog.Warn("metrics: event partition offsets failed", "err", err)
			return nil
		}
		return samples
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		samples, err := derivedViewWatermarkSamples(c.DB)
		if err != nil {
			slog.Warn("metrics: derived view watermarks failed", "err", err)
			return nil
		}
		return samples
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		return hotThreadSplitSamples(c.HotThreadSplits())
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		provider, ok := c.Bus.(busQueueStatsProvider)
		if !ok {
			return nil
		}
		stats := provider.QueueStats()
		samples := []metrics.Sample{
			{
				Name:   "budgie_gateway_connection_queue_depth",
				Help:   "Current queued live events in local gateway connection buffers.",
				Type:   "gauge",
				Labels: map[string]string{"stat": "total"},
				Value:  float64(stats.QueueDepthTotal),
			},
			{
				Name:   "budgie_gateway_connection_queue_depth",
				Help:   "Current queued live events in local gateway connection buffers.",
				Type:   "gauge",
				Labels: map[string]string{"stat": "max"},
				Value:  float64(stats.QueueDepthMax),
			},
			{
				Name:   "budgie_gateway_connection_queue_capacity",
				Help:   "Configured local gateway connection buffer capacity.",
				Type:   "gauge",
				Labels: map[string]string{"stat": "total"},
				Value:  float64(stats.QueueCapacityTotal),
			},
			{
				Name:   "budgie_gateway_connection_queue_capacity",
				Help:   "Configured local gateway connection buffer capacity.",
				Type:   "gauge",
				Labels: map[string]string{"stat": "max"},
				Value:  float64(stats.QueueCapacityMax),
			},
		}
		samples = append(samples, gatewayScopeFanoutSamples(stats, gatewayScopeFanoutSampleLimit)...)
		return samples
	})

	metrics.RegisterCollector(func() []metrics.Sample {
		return gatewayDropHotPartitionSamples(metrics.GatewayDroppedSendsByScope.Samples())
	})
}

func RegisterCommandLogMetricsCollector(log CommandLog, limit int) {
	if log == nil {
		return
	}
	lister, ok := log.(CommandPartitionOffsetLister)
	if !ok {
		return
	}
	metrics.RegisterCollector(func() []metrics.Sample {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		samples, err := commandPartitionOffsetSamples(ctx, lister, limit)
		if err != nil {
			slog.Warn("metrics: command partition offsets failed", "err", err)
			return nil
		}
		return samples
	})
}

func RegisterCommandAssignmentMetricsCollector(assigner CommandPartitionAssigner, partitions CommandPartitionLister, ownerID string, limit int) {
	if assigner == nil || partitions == nil {
		return
	}
	metrics.RegisterCollector(func() []metrics.Sample {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		samples, err := commandPartitionAssignmentSamples(ctx, assigner, partitions, ownerID, limit)
		if err != nil {
			slog.Warn("metrics: command partition assignments failed", "err", err)
			return nil
		}
		return samples
	})
}

func eventPartitionOffsetSamples(db *sql.DB, limit int) ([]metrics.Sample, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := qQuery(db,
		`SELECT partition_kind, partition_key, last_offset
		   FROM event_partition_offsets
		  ORDER BY last_offset DESC, partition_kind, partition_key
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	samples := []metrics.Sample{}
	for rows.Next() {
		var kind, key string
		var offset int64
		if err := rows.Scan(&kind, &key, &offset); err != nil {
			return nil, err
		}
		samples = append(samples, metrics.Sample{
			Name: "budgie_event_partition_offset",
			Help: "Latest durable event offset by write-ordering partition.",
			Type: "gauge",
			Labels: map[string]string{
				"kind": kind,
				"key":  key,
			},
			Value: float64(offset),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var count, minOffset, maxOffset int64
	if err := qQueryRow(db,
		`SELECT COUNT(*), COALESCE(MIN(last_offset), 0), COALESCE(MAX(last_offset), 0)
		   FROM event_partition_offsets`,
	).Scan(&count, &minOffset, &maxOffset); err != nil {
		return nil, err
	}
	samples = append(samples,
		metrics.Sample{
			Name:  "budgie_event_partition_count",
			Help:  "Number of write-ordering partitions with durable events.",
			Type:  "gauge",
			Value: float64(count),
		},
		metrics.Sample{
			Name:  "budgie_event_partition_offset_skew",
			Help:  "Difference between the hottest and coolest durable event partition offsets.",
			Type:  "gauge",
			Value: float64(maxOffset - minOffset),
		},
	)
	return samples, nil
}

func commandLogReceiptSamples(db *sql.DB, now int64) ([]metrics.Sample, error) {
	rows, err := qQuery(db,
		`SELECT status, COUNT(*), COALESCE(MIN(updated_at), 0)
		   FROM command_log_receipts
		  GROUP BY status`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type receiptStats struct {
		count    int64
		oldestAt int64
	}
	statsByStatus := map[string]receiptStats{
		CommandStatusApplied:  {},
		CommandStatusRetrying: {},
		CommandStatusFailed:   {},
	}
	for rows.Next() {
		var status string
		var stats receiptStats
		if err := rows.Scan(&status, &stats.count, &stats.oldestAt); err != nil {
			return nil, err
		}
		statsByStatus[status] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	statuses := make([]string, 0, len(statsByStatus))
	for status := range statsByStatus {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)

	samples := make([]metrics.Sample, 0, len(statuses)*2)
	for _, status := range statuses {
		stats := statsByStatus[status]
		age := int64(0)
		if stats.count > 0 && stats.oldestAt < now {
			age = now - stats.oldestAt
		}
		labels := map[string]string{"status": status}
		samples = append(samples,
			metrics.Sample{
				Name:   "budgie_command_log_receipts",
				Help:   "Command-log receipt rows by status.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(stats.count),
			},
			metrics.Sample{
				Name:   "budgie_command_log_receipt_oldest_age_ms",
				Help:   "Age of the oldest command-log receipt row by status.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(age),
			},
		)
	}
	return samples, nil
}

func attachmentBlobStagingSamples(db *sql.DB, now int64) ([]metrics.Sample, error) {
	rows, err := qQuery(db,
		`SELECT kind,
		        COUNT(*),
		        COALESCE(SUM(size_bytes), 0),
		        COALESCE(MIN(created_at), 0),
		        COALESCE(SUM(CASE WHEN expires_at > 0 AND expires_at <= ? THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN expires_at > 0 AND expires_at <= ? THEN size_bytes ELSE 0 END), 0)
		   FROM attachment_blob_staging
		  GROUP BY kind`,
		now, now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type stagingStats struct {
		count        int64
		bytes        int64
		oldestAt     int64
		expiredCount int64
		expiredBytes int64
	}
	statsByKind := map[string]stagingStats{
		projections.StagedBlobPostAttachment: {},
		projections.StagedBlobMailAttachment: {},
	}
	for rows.Next() {
		var kind string
		var stats stagingStats
		if err := rows.Scan(&kind, &stats.count, &stats.bytes, &stats.oldestAt, &stats.expiredCount, &stats.expiredBytes); err != nil {
			return nil, err
		}
		statsByKind[kind] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	kinds := make([]string, 0, len(statsByKind))
	for kind := range statsByKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	samples := make([]metrics.Sample, 0, len(kinds)*5)
	for _, kind := range kinds {
		stats := statsByKind[kind]
		oldestAge := int64(0)
		if stats.count > 0 && stats.oldestAt < now {
			oldestAge = now - stats.oldestAt
		}
		totalLabels := map[string]string{"kind": kind, "state": "total"}
		expiredLabels := map[string]string{"kind": kind, "state": "expired"}
		samples = append(samples,
			metrics.Sample{
				Name:   "budgie_attachment_blob_staging_blobs",
				Help:   "Staged attachment blob rows waiting for writer promotion or cleanup.",
				Type:   "gauge",
				Labels: totalLabels,
				Value:  float64(stats.count),
			},
			metrics.Sample{
				Name:   "budgie_attachment_blob_staging_blobs",
				Help:   "Staged attachment blob rows waiting for writer promotion or cleanup.",
				Type:   "gauge",
				Labels: expiredLabels,
				Value:  float64(stats.expiredCount),
			},
			metrics.Sample{
				Name:   "budgie_attachment_blob_staging_bytes",
				Help:   "Bytes held in attachment blob staging by state.",
				Type:   "gauge",
				Labels: totalLabels,
				Value:  float64(stats.bytes),
			},
			metrics.Sample{
				Name:   "budgie_attachment_blob_staging_bytes",
				Help:   "Bytes held in attachment blob staging by state.",
				Type:   "gauge",
				Labels: expiredLabels,
				Value:  float64(stats.expiredBytes),
			},
			metrics.Sample{
				Name:   "budgie_attachment_blob_staging_oldest_age_ms",
				Help:   "Age of the oldest staged attachment blob row.",
				Type:   "gauge",
				Labels: map[string]string{"kind": kind},
				Value:  float64(oldestAge),
			},
		)
	}
	return samples, nil
}

func derivedViewWatermarkSamples(db *sql.DB) ([]metrics.Sample, error) {
	head, err := headSeq(db)
	if err != nil {
		return nil, err
	}
	rows, err := qQuery(db,
		`SELECT view_name, applied_seq
		   FROM derived_view_watermarks
		  ORDER BY view_name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	appliedByView := map[string]int64{}
	for rows.Next() {
		var view string
		var applied int64
		if err := rows.Scan(&view, &applied); err != nil {
			return nil, err
		}
		appliedByView[view] = applied
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	samples := make([]metrics.Sample, 0, len(knownDerivedViews)*2+len(appliedByView)*2)
	for _, view := range knownDerivedViews {
		applied, ok := appliedByView[view]
		if !ok {
			applied = head
		}
		samples = appendDerivedViewSamples(samples, view, head, applied)
		seen[view] = true
	}
	for view, applied := range appliedByView {
		if seen[view] {
			continue
		}
		samples = appendDerivedViewSamples(samples, view, head, applied)
	}
	return samples, nil
}

func appendDerivedViewSamples(samples []metrics.Sample, view string, head, applied int64) []metrics.Sample {
	lag := head - applied
	if lag < 0 {
		lag = 0
	}
	labels := map[string]string{"view": view}
	return append(samples,
		metrics.Sample{
			Name:   "budgie_derived_view_applied_seq",
			Help:   "Durable event seq applied by an asynchronous derived view.",
			Type:   "gauge",
			Labels: labels,
			Value:  float64(applied),
		},
		metrics.Sample{
			Name:   "budgie_derived_view_lag_events",
			Help:   "Durable event lag for an asynchronous derived view.",
			Type:   "gauge",
			Labels: labels,
			Value:  float64(lag),
		},
	)
}

func commandPartitionAssignmentSamples(ctx context.Context, assigner CommandPartitionAssigner, partitions CommandPartitionLister, ownerID string, limit int) ([]metrics.Sample, error) {
	if limit <= 0 {
		limit = 100
	}
	list, err := partitions.ListCommandPartitions(ctx, limit)
	if err != nil {
		return nil, err
	}
	samples := make([]metrics.Sample, 0, len(list)*2+1)
	assignedCount := 0
	for _, partition := range list {
		partition = partition.Normalize()
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			return nil, err
		}
		assignment.Partition = assignment.Partition.Normalize()
		if assigned {
			assignedCount++
		}
		labels := map[string]string{
			"kind":     partition.Kind,
			"key":      partition.Key,
			"owner_id": assignment.OwnerID,
		}
		samples = append(samples,
			metrics.Sample{
				Name:   "budgie_command_partition_assigned",
				Help:   "1 when this command-log writer owns the sampled partition assignment, else 0.",
				Type:   "gauge",
				Labels: labels,
				Value:  boolFloat(assigned),
			},
			metrics.Sample{
				Name:   "budgie_command_partition_assignment_generation",
				Help:   "Broker assignment generation observed for a sampled command-log partition.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(assignment.Generation),
			},
		)
	}
	samples = append(samples, metrics.Sample{
		Name:  "budgie_command_partition_assigned_count",
		Help:  "Number of sampled command-log partitions currently assigned to this writer.",
		Type:  "gauge",
		Value: float64(assignedCount),
	})
	return samples, nil
}

func commandPartitionOffsetSamples(ctx context.Context, lister CommandPartitionOffsetLister, limit int) ([]metrics.Sample, error) {
	if limit <= 0 {
		limit = 100
	}
	offsets, err := lister.ListCommandPartitionOffsets(ctx, limit)
	if err != nil {
		return nil, err
	}
	samples := make([]metrics.Sample, 0, len(offsets)*4+4)
	var totalLag, maxLag, minLag int64
	for i, offset := range offsets {
		partition := offset.Partition.Normalize()
		lag := offset.TailOffset - offset.CommittedOffset
		if lag < 0 {
			lag = 0
		}
		if i == 0 || lag < minLag {
			minLag = lag
		}
		if lag > maxLag {
			maxLag = lag
		}
		totalLag += lag
		labels := map[string]string{
			"kind": partition.Kind,
			"key":  partition.Key,
		}
		samples = append(samples,
			metrics.Sample{
				Name:   "budgie_command_partition_tail_offset",
				Help:   "Latest produced command-log offset by write-ordering partition.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(offset.TailOffset),
			},
			metrics.Sample{
				Name:   "budgie_command_partition_committed_offset",
				Help:   "Latest writer-committed command-log offset by write-ordering partition.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(offset.CommittedOffset),
			},
			metrics.Sample{
				Name:   "budgie_command_partition_lag",
				Help:   "Produced minus writer-committed command-log offsets by write-ordering partition.",
				Type:   "gauge",
				Labels: labels,
				Value:  float64(lag),
			},
		)
		if lag > 0 {
			samples = append(samples, metrics.Sample{
				Name: "budgie_hot_partition_candidate",
				Help: "Hot write-ordering partition candidate, with value set to the signal magnitude.",
				Type: "gauge",
				Labels: map[string]string{
					"kind":   partition.Kind,
					"key":    partition.Key,
					"signal": "command_lag",
				},
				Value: float64(lag),
			})
		}
	}
	samples = append(samples,
		metrics.Sample{
			Name:  "budgie_command_partition_count",
			Help:  "Number of command-log partitions sampled for writer lag metrics.",
			Type:  "gauge",
			Value: float64(len(offsets)),
		},
		metrics.Sample{
			Name:  "budgie_command_partition_lag_total",
			Help:  "Total sampled command-log writer lag across write-ordering partitions.",
			Type:  "gauge",
			Value: float64(totalLag),
		},
		metrics.Sample{
			Name:  "budgie_command_partition_lag_max",
			Help:  "Largest sampled command-log writer lag on one write-ordering partition.",
			Type:  "gauge",
			Value: float64(maxLag),
		},
		metrics.Sample{
			Name:  "budgie_command_partition_lag_skew",
			Help:  "Difference between largest and smallest sampled command-log writer lag.",
			Type:  "gauge",
			Value: float64(maxLag - minLag),
		},
	)
	return samples, nil
}

func gatewayDropHotPartitionSamples(dropSamples []metrics.Sample) []metrics.Sample {
	samples := make([]metrics.Sample, 0, len(dropSamples))
	for _, drop := range dropSamples {
		if drop.Value <= 0 {
			continue
		}
		scope := drop.Labels["scope"]
		partition, ok := partitionFromScopes([]string{scope})
		if !ok {
			continue
		}
		samples = append(samples, metrics.Sample{
			Name: "budgie_hot_partition_candidate",
			Help: "Hot write-ordering partition candidate, with value set to the signal magnitude.",
			Type: "gauge",
			Labels: map[string]string{
				"kind":   partition.Kind,
				"key":    partition.Key,
				"signal": "gateway_drops",
			},
			Value: drop.Value,
		})
	}
	return samples
}

func hotThreadSplitSamples(splits map[string]int) []metrics.Sample {
	if len(splits) == 0 {
		return nil
	}
	threadIDs := make([]string, 0, len(splits))
	for threadID, shards := range splits {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" || shards <= 1 {
			continue
		}
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	samples := make([]metrics.Sample, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		samples = append(samples, metrics.Sample{
			Name:   "budgie_hot_thread_split_shards",
			Help:   "Configured hot-thread reply split shard count by thread.",
			Type:   "gauge",
			Labels: map[string]string{"thread_id": threadID},
			Value:  float64(splits[threadID]),
		})
	}
	return samples
}

func gatewayScopeFanoutSamples(stats BusQueueStats, limit int) []metrics.Sample {
	if limit <= 0 {
		limit = gatewayScopeFanoutSampleLimit
	}
	scopes := append([]BusScopeQueueStats(nil), stats.Scopes...)
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Subscribers == scopes[j].Subscribers {
			if scopes[i].QueueDepthTotal == scopes[j].QueueDepthTotal {
				return scopes[i].Scope < scopes[j].Scope
			}
			return scopes[i].QueueDepthTotal > scopes[j].QueueDepthTotal
		}
		return scopes[i].Subscribers > scopes[j].Subscribers
	})
	if len(scopes) > limit {
		scopes = scopes[:limit]
	}

	samples := make([]metrics.Sample, 0, len(scopes)*2)
	for _, scope := range scopes {
		labels := map[string]string{"scope": scope.Scope}
		samples = append(samples, metrics.Sample{
			Name:   "budgie_gateway_scope_subscribers",
			Help:   "Local gateway subscribers by event scope, capped to the hottest sampled scopes.",
			Type:   "gauge",
			Labels: labels,
			Value:  float64(scope.Subscribers),
		})
		partition, ok := partitionFromScopes([]string{scope.Scope})
		if !ok || scope.Subscribers <= 0 {
			continue
		}
		samples = append(samples, metrics.Sample{
			Name: "budgie_hot_partition_candidate",
			Help: "Hot write-ordering partition candidate, with value set to the signal magnitude.",
			Type: "gauge",
			Labels: map[string]string{
				"kind":   partition.Kind,
				"key":    partition.Key,
				"signal": "gateway_subscribers",
			},
			Value: float64(scope.Subscribers),
		})
	}
	return samples
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
