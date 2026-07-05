package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type EventStoreProjectionWorkerConfig struct {
	Core           *Core
	Store          EventStore
	Partitions     EventPartitionLister
	Source         string
	BatchSize      int
	PartitionLimit int
	Interval       time.Duration
}

type EventStoreProjectionWorker struct {
	core           *Core
	store          EventStore
	partitions     EventPartitionLister
	source         string
	batchSize      int
	partitionLimit int
	interval       time.Duration
}

type EventStorePartitionMaterializationConfig struct {
	Source    string
	Partition LogPartition
	Limit     int
}

type EventStorePartitionMaterializationResult struct {
	Source        string       `json:"source"`
	Partition     LogPartition `json:"partition"`
	StartedOffset int64        `json:"startedOffset"`
	LastOffset    int64        `json:"lastOffset"`
	Applied       int          `json:"applied"`
	Watermark     string       `json:"watermark"`
}

func NewEventStoreProjectionWorker(config EventStoreProjectionWorkerConfig) (*EventStoreProjectionWorker, error) {
	if config.Core == nil {
		return nil, fmt.Errorf("event store projection worker: nil core")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("event store projection worker: nil event store")
	}
	partitions := config.Partitions
	if partitions == nil {
		lister, ok := config.Store.(EventPartitionLister)
		if !ok {
			return nil, fmt.Errorf("event store projection worker: nil partition lister")
		}
		partitions = lister
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	partitionLimit := config.PartitionLimit
	if partitionLimit <= 0 {
		partitionLimit = 100
	}
	interval := config.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &EventStoreProjectionWorker{
		core:           config.Core,
		store:          config.Store,
		partitions:     partitions,
		source:         logmodel.NormalizeEventStoreProjectionSource(config.Source),
		batchSize:      batchSize,
		partitionLimit: partitionLimit,
		interval:       interval,
	}, nil
}

func (c *Core) StartEventStoreProjectionWorker(ctx context.Context, config EventStoreProjectionWorkerConfig) (*EventStoreProjectionWorker, error) {
	config.Core = c
	worker, err := NewEventStoreProjectionWorker(config)
	if err != nil {
		return nil, err
	}
	go worker.Run(ctx)
	return worker, nil
}

func (w *EventStoreProjectionWorker) MaterializeOnce(ctx context.Context) ([]EventStorePartitionMaterializationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("event store projection worker: nil receiver")
	}
	if w.core == nil {
		return nil, fmt.Errorf("event store projection worker: nil core")
	}
	if w.store == nil {
		return nil, fmt.Errorf("event store projection worker: nil event store")
	}
	if w.partitions == nil {
		return nil, fmt.Errorf("event store projection worker: nil partition lister")
	}
	partitions, limited, err := listEventPartitionsWithLimit(ctx, w.partitions, w.partitionLimit)
	if err != nil {
		return nil, err
	}
	if limited {
		return nil, fmt.Errorf("event store projection worker: partition limit %d did not cover every broker event partition", w.partitionLimit)
	}
	results := make([]EventStorePartitionMaterializationResult, 0, len(partitions))
	for _, partition := range partitions {
		result, err := w.core.MaterializeEventStorePartition(ctx, w.store, EventStorePartitionMaterializationConfig{
			Source:    w.source,
			Partition: partition,
			Limit:     w.batchSize,
		})
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (w *EventStoreProjectionWorker) DrainOnce(ctx context.Context) ([]EventStorePartitionMaterializationResult, error) {
	results := []EventStorePartitionMaterializationResult{}
	for {
		batch, err := w.MaterializeOnce(ctx)
		results = append(results, batch...)
		if err != nil {
			return results, err
		}
		if !eventStoreProjectionWorkerShouldContinue(batch, w.batchSize) {
			return results, nil
		}
	}
}

func (w *EventStoreProjectionWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	if _, err := w.DrainOnce(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("event store projection worker failed", "err", err)
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.DrainOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("event store projection worker failed", "err", err)
			}
		}
	}
}

func eventStoreProjectionWorkerShouldContinue(results []EventStorePartitionMaterializationResult, batchSize int) bool {
	if batchSize <= 0 {
		batchSize = 100
	}
	for _, result := range results {
		if result.Applied >= batchSize {
			return true
		}
	}
	return false
}

// MaterializeEventStorePartition applies broker-source events for one logical
// partition into the current SQL projections and advances a partition-scoped
// watermark in the same transaction.
func (c *Core) MaterializeEventStorePartition(ctx context.Context, store EventStore, config EventStorePartitionMaterializationConfig) (EventStorePartitionMaterializationResult, error) {
	result := EventStorePartitionMaterializationResult{
		Source:    logmodel.NormalizeEventStoreProjectionSource(config.Source),
		Partition: config.Partition.Normalize(),
	}
	result.Watermark = logmodel.EventStoreProjectionWatermarkName(result.Source, result.Partition)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if c == nil || c.DB == nil {
		return result, fmt.Errorf("event store materializer: core is not initialized")
	}
	if store == nil {
		return result, fmt.Errorf("event store materializer: nil event store")
	}
	limit := config.Limit
	if limit <= 0 {
		limit = 100
	}
	applied, found, err := projections.LookupProjectionWatermarkAppliedSeq(c.DB, result.Watermark)
	if err != nil {
		return result, err
	}
	if !found {
		applied = 0
	}
	result.StartedOffset = applied
	result.LastOffset = applied

	events, err := store.ReplayPartition(ctx, result.Partition.Kind, result.Partition.Key, applied, limit)
	if err != nil {
		return result, err
	}
	if len(events) == 0 {
		return result, nil
	}

	eventsToApply := make([]*proto.Event, 0, len(events))
	last := applied
	for _, evt := range events {
		if evt == nil {
			continue
		}
		partition := LogPartition{Kind: evt.PartitionKind, Key: evt.PartitionKey}.Normalize()
		if partition != result.Partition {
			return result, fmt.Errorf("event store materializer: replay returned wrong partition %s/%s for %s/%s",
				partition.Kind, partition.Key, result.Partition.Kind, result.Partition.Key)
		}
		if evt.PartitionOffset <= last {
			continue
		}
		if evt.PartitionOffset != last+1 {
			return result, fmt.Errorf("event store materializer: offset gap in %s/%s: got %d after %d",
				result.Partition.Kind, result.Partition.Key, evt.PartitionOffset, last)
		}
		eventsToApply = append(eventsToApply, evt)
		last = evt.PartitionOffset
	}
	if len(eventsToApply) == 0 {
		return result, nil
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint:errcheck

	var compatibilitySeqs map[string]int64
	if currentSQLFlavor == postgresFlavor {
		compatibilitySeqs, err = indexEventStoreProjectionCompatibilityEventsTx(tx, eventsToApply)
		if err != nil {
			return result, fmt.Errorf("event store materializer: index compatibility events for %s/%s: %w",
				result.Partition.Kind, result.Partition.Key, err)
		}
	}

	applyCtx := &projectionApplyContext{}
	if currentSQLFlavor == postgresFlavor {
		applied, lastOffset, ok, err := materializeHotNativeProjectionBatchTx(tx, applyCtx, compatibilitySeqs, eventsToApply)
		if err != nil {
			return result, fmt.Errorf("event store materializer: apply hot native batch for %s/%s: %w",
				result.Partition.Kind, result.Partition.Key, err)
		}
		if ok {
			result.Applied = applied
			result.LastOffset = lastOffset
			if result.Applied == 0 {
				return result, nil
			}
			if err := recordEventStoreProjectionWatermarkTx(tx, result.Watermark, result.LastOffset); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			return result, nil
		}
	}

	last = applied
	for _, evt := range eventsToApply {
		seq := evt.Seq
		if seq <= 0 {
			if compatibilitySeqs != nil {
				var ok bool
				seq, ok = compatibilitySeqs[strings.TrimSpace(evt.ID)]
				if !ok {
					return result, fmt.Errorf("event store materializer: missing compatibility seq for %s/%s offset %d",
						result.Partition.Kind, result.Partition.Key, evt.PartitionOffset)
				}
			} else {
				seq, err = indexEventStoreProjectionCompatibilityEventTx(tx, evt)
				if err != nil {
					return result, fmt.Errorf("event store materializer: index compatibility event %s/%s offset %d: %w",
						result.Partition.Kind, result.Partition.Key, evt.PartitionOffset, err)
				}
			}
		}
		if err := rebuildProjectionEventWithContext(tx, applyCtx, evt.ID, seq, evt.Payload, evt.Scopes); err != nil {
			return result, fmt.Errorf("event store materializer: apply %s/%s offset %d: %w",
				result.Partition.Kind, result.Partition.Key, evt.PartitionOffset, err)
		}
		if err := enqueueEventStoreProjectionSideEffects(tx, seq, evt.Payload, evt.Scopes); err != nil {
			return result, fmt.Errorf("event store materializer: side effects for %s/%s offset %d: %w",
				result.Partition.Kind, result.Partition.Key, evt.PartitionOffset, err)
		}
		last = evt.PartitionOffset
		result.LastOffset = last
		result.Applied++
	}
	if result.Applied == 0 {
		return result, nil
	}
	if err := recordEventStoreProjectionWatermarkTx(tx, result.Watermark, result.LastOffset); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Core) seedEventStoreProjectionWatermarksFromEventPartitionOffsets(ctx context.Context, source string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil || c.DB == nil {
		return 0, fmt.Errorf("event store materializer: core is not initialized")
	}
	rows, err := qQuery(c.DB,
		`SELECT partition_kind, partition_key, last_offset
		   FROM event_partition_offsets
		  WHERE last_offset > 0`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type seed struct {
		partition LogPartition
		offset    int64
	}
	seeds := []seed{}
	for rows.Next() {
		var kind, key string
		var offset int64
		if err := rows.Scan(&kind, &key, &offset); err != nil {
			return 0, err
		}
		if offset <= 0 {
			continue
		}
		seeds = append(seeds, seed{
			partition: LogPartition{Kind: kind, Key: key}.Normalize(),
			offset:    offset,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(seeds) == 0 {
		return 0, nil
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, seed := range seeds {
		watermark := logmodel.EventStoreProjectionWatermarkName(source, seed.partition)
		if err := projections.RecordProjectionWatermarkAppliedMax(tx, watermark, seed.offset, nowMS()); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(seeds), nil
}

type eventStoreProjectionCompatibilityIndexCandidate struct {
	id              string
	kind            string
	rawPayload      string
	ts              int64
	partition       LogPartition
	partitionOffset int64
	scopes          []string
}

func indexEventStoreProjectionCompatibilityEventTx(tx *sql.Tx, evt *proto.Event) (int64, error) {
	if tx == nil {
		return 0, fmt.Errorf("nil transaction")
	}
	if evt == nil {
		return 0, fmt.Errorf("nil event")
	}
	if evt.Seq > 0 {
		return evt.Seq, nil
	}
	id := strings.TrimSpace(evt.ID)
	if id == "" {
		return 0, fmt.Errorf("event id is required")
	}
	if evt.Kind == "" {
		return 0, fmt.Errorf("event kind is required")
	}
	partition := LogPartition{Kind: evt.PartitionKind, Key: evt.PartitionKey}.Normalize()
	if partition.Kind == "" || partition.Key == "" {
		return 0, fmt.Errorf("event partition is required")
	}
	if evt.PartitionOffset <= 0 {
		return 0, fmt.Errorf("event partition offset is required")
	}
	raw, err := json.Marshal(evt.Payload)
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return 0, fmt.Errorf("event payload is required")
	}
	ts := evt.TS
	if ts <= 0 {
		return 0, fmt.Errorf("event timestamp is required")
	}

	if currentSQLFlavor == postgresFlavor {
		_, err = qExec(tx,
			`INSERT INTO events (id, kind, payload, created_at, partition_kind, partition_key, partition_offset)
			 VALUES (?, ?, CAST(? AS JSONB), ?, ?, ?, ?)
			 ON CONFLICT (id) DO NOTHING`,
			id, string(evt.Kind), string(raw), ts, partition.Kind, partition.Key, evt.PartitionOffset)
	} else {
		_, err = qExec(tx,
			`INSERT INTO events (id, kind, scopes, payload, ts, partition_kind, partition_key, partition_offset)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (id) DO NOTHING`,
			id, string(evt.Kind), strings.Join(evt.Scopes, ","), string(raw), ts, partition.Kind, partition.Key, evt.PartitionOffset)
	}
	if err != nil {
		return 0, err
	}

	var (
		seq             int64
		kind            string
		partitionKind   string
		partitionKey    string
		partitionOffset int64
	)
	if err := qQueryRow(tx,
		`SELECT seq, kind, partition_kind, partition_key, partition_offset
		   FROM events
		  WHERE id=?`,
		id,
	).Scan(&seq, &kind, &partitionKind, &partitionKey, &partitionOffset); err != nil {
		return 0, err
	}
	if kind != string(evt.Kind) {
		return 0, fmt.Errorf("event id %q already indexed with kind %q, want %q", id, kind, evt.Kind)
	}
	indexedPartition := LogPartition{Kind: partitionKind, Key: partitionKey}.Normalize()
	if indexedPartition != partition || partitionOffset != evt.PartitionOffset {
		return 0, fmt.Errorf("event id %q already indexed at %s/%s offset %d, want %s/%s offset %d",
			id, indexedPartition.Kind, indexedPartition.Key, partitionOffset, partition.Kind, partition.Key, evt.PartitionOffset)
	}
	if _, err := qExec(tx,
		`INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
		 VALUES (?, ?, ?)
		 ON CONFLICT(partition_kind, partition_key) DO UPDATE
		       SET last_offset=CASE
		             WHEN event_partition_offsets.last_offset > excluded.last_offset
		             THEN event_partition_offsets.last_offset
		             ELSE excluded.last_offset
		           END`,
		partition.Kind, partition.Key, evt.PartitionOffset,
	); err != nil {
		return 0, err
	}
	for _, scope := range evt.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, err := qExec(tx,
			`INSERT INTO event_scopes (seq, scope) VALUES (?, ?)
			 ON CONFLICT (seq, scope) DO NOTHING`,
			seq, scope,
		); err != nil {
			return 0, err
		}
	}
	return seq, nil
}

func indexEventStoreProjectionCompatibilityEventsTx(tx *sql.Tx, events []*proto.Event) (map[string]int64, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil transaction")
	}
	if len(events) == 0 {
		return nil, nil
	}
	if currentSQLFlavor != postgresFlavor {
		seqs := make(map[string]int64, len(events))
		for _, evt := range events {
			seq, err := indexEventStoreProjectionCompatibilityEventTx(tx, evt)
			if err != nil {
				return nil, err
			}
			if evt != nil && evt.Seq <= 0 {
				seqs[strings.TrimSpace(evt.ID)] = seq
			}
		}
		return seqs, nil
	}

	candidates := make([]eventStoreProjectionCompatibilityIndexCandidate, 0, len(events))
	byID := make(map[string]eventStoreProjectionCompatibilityIndexCandidate, len(events))
	partitionOffsets := map[LogPartition]int64{}
	for _, evt := range events {
		if evt == nil || evt.Seq > 0 {
			continue
		}
		candidate, err := eventStoreProjectionCompatibilityIndexCandidateForEvent(evt)
		if err != nil {
			return nil, err
		}
		if _, exists := byID[candidate.id]; exists {
			return nil, fmt.Errorf("duplicate event id %q in compatibility batch", candidate.id)
		}
		byID[candidate.id] = candidate
		candidates = append(candidates, candidate)
		if candidate.partitionOffset > partitionOffsets[candidate.partition] {
			partitionOffsets[candidate.partition] = candidate.partitionOffset
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	var insert strings.Builder
	insert.WriteString(`INSERT INTO events (id, kind, payload, created_at, partition_kind, partition_key, partition_offset) VALUES `)
	args := make([]any, 0, len(candidates)*7)
	for i, candidate := range candidates {
		if i > 0 {
			insert.WriteByte(',')
		}
		insert.WriteString(`(?, ?, CAST(? AS JSONB), ?, ?, ?, ?)`)
		args = append(args,
			candidate.id,
			candidate.kind,
			candidate.rawPayload,
			candidate.ts,
			candidate.partition.Kind,
			candidate.partition.Key,
			candidate.partitionOffset,
		)
	}
	insert.WriteString(` ON CONFLICT (id) DO NOTHING RETURNING id, seq`)
	seqs := make(map[string]int64, len(candidates))
	rows, err := qQuery(tx, insert.String(), args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			rows.Close()
			return nil, err
		}
		seqs[id] = seq
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	ids := make([]string, 0, len(candidates))
	selectArgs := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
		selectArgs = append(selectArgs, candidate.id)
	}
	rows, err = qQuery(tx,
		`SELECT id, seq, kind, partition_kind, partition_key, partition_offset
		   FROM events
		  WHERE id IN (`+queryPlaceholders(len(ids))+`)`,
		selectArgs...,
	)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(candidates))
	for rows.Next() {
		var (
			id              string
			seq             int64
			kind            string
			partitionKind   string
			partitionKey    string
			partitionOffset int64
		)
		if err := rows.Scan(&id, &seq, &kind, &partitionKind, &partitionKey, &partitionOffset); err != nil {
			rows.Close()
			return nil, err
		}
		candidate, ok := byID[id]
		if !ok {
			rows.Close()
			return nil, fmt.Errorf("unexpected indexed event id %q", id)
		}
		if kind != candidate.kind {
			rows.Close()
			return nil, fmt.Errorf("event id %q already indexed with kind %q, want %q", id, kind, candidate.kind)
		}
		indexedPartition := LogPartition{Kind: partitionKind, Key: partitionKey}.Normalize()
		if indexedPartition != candidate.partition || partitionOffset != candidate.partitionOffset {
			rows.Close()
			return nil, fmt.Errorf("event id %q already indexed at %s/%s offset %d, want %s/%s offset %d",
				id, indexedPartition.Kind, indexedPartition.Key, partitionOffset,
				candidate.partition.Kind, candidate.partition.Key, candidate.partitionOffset)
		}
		seen[id] = struct{}{}
		seqs[id] = seq
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, candidate := range candidates {
		if _, ok := seen[candidate.id]; !ok {
			return nil, fmt.Errorf("event id %q was not indexed", candidate.id)
		}
	}

	if err := insertEventStoreProjectionCompatibilityScopesTx(tx, candidates, seqs); err != nil {
		return nil, err
	}
	for partition, offset := range partitionOffsets {
		if _, err := qExec(tx,
			`INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
			 VALUES (?, ?, ?)
			 ON CONFLICT(partition_kind, partition_key) DO UPDATE
			       SET last_offset=CASE
			             WHEN event_partition_offsets.last_offset > excluded.last_offset
			             THEN event_partition_offsets.last_offset
			             ELSE excluded.last_offset
			           END`,
			partition.Kind, partition.Key, offset,
		); err != nil {
			return nil, err
		}
	}
	return seqs, nil
}

func eventStoreProjectionCompatibilityIndexCandidateForEvent(evt *proto.Event) (eventStoreProjectionCompatibilityIndexCandidate, error) {
	if evt == nil {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("nil event")
	}
	id := strings.TrimSpace(evt.ID)
	if id == "" {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event id is required")
	}
	if evt.Kind == "" {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event kind is required")
	}
	partition := LogPartition{Kind: evt.PartitionKind, Key: evt.PartitionKey}.Normalize()
	if partition.Kind == "" || partition.Key == "" {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event partition is required")
	}
	if evt.PartitionOffset <= 0 {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event partition offset is required")
	}
	raw, err := json.Marshal(evt.Payload)
	if err != nil {
		return eventStoreProjectionCompatibilityIndexCandidate{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event payload is required")
	}
	ts := evt.TS
	if ts <= 0 {
		return eventStoreProjectionCompatibilityIndexCandidate{}, fmt.Errorf("event timestamp is required")
	}
	return eventStoreProjectionCompatibilityIndexCandidate{
		id:              id,
		kind:            string(evt.Kind),
		rawPayload:      string(raw),
		ts:              ts,
		partition:       partition,
		partitionOffset: evt.PartitionOffset,
		scopes:          evt.Scopes,
	}, nil
}

func insertEventStoreProjectionCompatibilityScopesTx(tx *sql.Tx, candidates []eventStoreProjectionCompatibilityIndexCandidate, seqs map[string]int64) error {
	type scopeRow struct {
		seq   int64
		scope string
	}
	rows := make([]scopeRow, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		seq := seqs[candidate.id]
		if seq <= 0 {
			return fmt.Errorf("missing seq for event id %q", candidate.id)
		}
		for _, scope := range candidate.scopes {
			scope = strings.TrimSpace(scope)
			if scope == "" {
				continue
			}
			key := fmt.Sprintf("%d\x00%s", seq, scope)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			rows = append(rows, scopeRow{seq: seq, scope: scope})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO event_scopes (seq, scope) VALUES `)
	args := make([]any, 0, len(rows)*2)
	for i, row := range rows {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?)`)
		args = append(args, row.seq, row.scope)
	}
	query.WriteString(` ON CONFLICT (seq, scope) DO NOTHING`)
	_, err := qExec(tx, query.String(), args...)
	return err
}

type hotNativeProjectionThread struct {
	id       string
	boardID  string
	author   string
	authorID string
	title    string
	seq      int64
	ts       int64
}

type hotNativeProjectionPost struct {
	id              string
	threadID        string
	boardID         string
	author          string
	authorID        string
	body            string
	signature       string
	contentType     string
	replyTo         string
	tex             bool
	mailBack        bool
	sourcePost      string
	sourceThread    string
	sourceBoard     string
	sourceAuthor    string
	sourceAuthorID  string
	sourceTitle     string
	seq             int64
	ts              int64
	activityActorID string
	outboxActorName string
	outboxBody      string
}

type hotNativeProjectionThreadMeta struct {
	count     int
	lastSeq   int64
	updatedAt int64
}

func materializeHotNativeProjectionBatchTx(tx *sql.Tx, applyCtx *projectionApplyContext, compatibilitySeqs map[string]int64, events []*proto.Event) (int, int64, bool, error) {
	if currentSQLFlavor != postgresFlavor || len(events) == 0 {
		return 0, 0, false, nil
	}
	threads := make([]hotNativeProjectionThread, 0, len(events))
	posts := make([]hotNativeProjectionPost, 0, len(events))
	lastOffset := int64(0)
	for _, evt := range events {
		if evt == nil {
			continue
		}
		seq, err := eventStoreProjectionBatchSeq(evt, compatibilitySeqs)
		if err != nil {
			return 0, 0, false, err
		}
		switch payload := evt.Payload.(type) {
		case *proto.ThreadNewPayload:
			thread, ok := hotNativeProjectionThreadCandidate(payload, seq)
			if !ok {
				return 0, 0, false, nil
			}
			threads = append(threads, thread)
		case *proto.PostAppendedPayload:
			post, ok, err := hotNativeProjectionPostCandidate(tx, applyCtx, payload, evt.Scopes, seq)
			if err != nil {
				return 0, 0, false, err
			}
			if !ok {
				return 0, 0, false, nil
			}
			posts = append(posts, post)
		default:
			return 0, 0, false, nil
		}
		lastOffset = evt.PartitionOffset
	}
	if len(threads) == 0 && len(posts) == 0 {
		return 0, 0, false, nil
	}
	if err := insertHotNativeProjectionThreadsTx(tx, threads); err != nil {
		return 0, 0, false, err
	}
	if err := insertHotNativeProjectionPostsTx(tx, posts); err != nil {
		return 0, 0, false, err
	}
	if err := updateHotNativeProjectionThreadMetaTx(tx, posts); err != nil {
		return 0, 0, false, err
	}
	if err := insertHotNativeProjectionFeedsTx(tx, "resident_feed_posts", posts); err != nil {
		return 0, 0, false, err
	}
	if err := insertHotNativeProjectionFeedsTx(tx, "latest_feed_posts", posts); err != nil {
		return 0, 0, false, err
	}
	if err := insertHotNativeProjectionFTSTx(tx, posts); err != nil {
		return 0, 0, false, err
	}
	if err := upsertHotNativeProjectionActivityTx(tx, posts); err != nil {
		return 0, 0, false, err
	}
	if err := insertHotNativeProjectionOutboxTx(tx, posts); err != nil {
		return 0, 0, false, err
	}
	return len(threads) + len(posts), lastOffset, true, nil
}

func eventStoreProjectionBatchSeq(evt *proto.Event, compatibilitySeqs map[string]int64) (int64, error) {
	if evt == nil {
		return 0, fmt.Errorf("nil event")
	}
	if evt.Seq > 0 {
		return evt.Seq, nil
	}
	seq, ok := compatibilitySeqs[strings.TrimSpace(evt.ID)]
	if !ok || seq <= 0 {
		return 0, fmt.Errorf("missing compatibility seq for event id %q", strings.TrimSpace(evt.ID))
	}
	return seq, nil
}

func hotNativeProjectionThreadCandidate(evt *proto.ThreadNewPayload, seq int64) (hotNativeProjectionThread, bool) {
	if evt == nil {
		return hotNativeProjectionThread{}, false
	}
	id := strings.TrimSpace(evt.ID)
	boardID := strings.TrimSpace(evt.Board)
	authorID := strings.TrimSpace(evt.AuthorID)
	if id == "" || boardID == "" || authorID == "" || seq <= 0 || evt.TS <= 0 {
		return hotNativeProjectionThread{}, false
	}
	return hotNativeProjectionThread{
		id:       id,
		boardID:  boardID,
		author:   strings.TrimSpace(evt.Author),
		authorID: authorID,
		title:    evt.Title,
		seq:      seq,
		ts:       evt.TS,
	}, true
}

func hotNativeProjectionPostCandidate(tx *sql.Tx, applyCtx *projectionApplyContext, evt *proto.PostAppendedPayload, scopes []string, seq int64) (hotNativeProjectionPost, bool, error) {
	if evt == nil {
		return hotNativeProjectionPost{}, false, nil
	}
	if len(evt.Attachments) != 0 {
		return hotNativeProjectionPost{}, false, nil
	}
	id := strings.TrimSpace(evt.ID)
	threadID := strings.TrimSpace(evt.Thread)
	boardID := projectionBoardFromScopes(scopes)
	authorID := strings.TrimSpace(evt.AuthorID)
	if id == "" || threadID == "" || boardID == "" || authorID == "" || seq <= 0 || evt.TS <= 0 {
		return hotNativeProjectionPost{}, false, nil
	}
	relayEnabled, err := projectedBoardRelayEnabled(tx, applyCtx, boardID)
	if err != nil {
		return hotNativeProjectionPost{}, false, err
	}
	if relayEnabled {
		return hotNativeProjectionPost{}, false, nil
	}
	sourceBody := evt.Body
	if strings.TrimSpace(evt.RawBody) != "" {
		sourceBody = evt.RawBody
	}
	pollBlock, _ := extractPoll(sourceBody)
	if pollBlock != nil {
		return hotNativeProjectionPost{}, false, nil
	}
	activityActorID := strings.TrimSpace(evt.PostCommitActorID)
	if activityActorID == "" {
		activityActorID = authorID
	}
	if activityActorID == "" {
		return hotNativeProjectionPost{}, false, nil
	}
	return hotNativeProjectionPost{
		id:              id,
		threadID:        threadID,
		boardID:         boardID,
		author:          strings.TrimSpace(evt.Author),
		authorID:        authorID,
		body:            evt.Body,
		signature:       strings.TrimSpace(evt.Signature),
		contentType:     evt.ContentType,
		replyTo:         evt.ReplyTo,
		tex:             evt.TeX,
		mailBack:        evt.MailBack,
		sourcePost:      strings.TrimSpace(evt.SourcePost),
		sourceThread:    strings.TrimSpace(evt.SourceThread),
		sourceBoard:     strings.TrimSpace(evt.SourceBoard),
		sourceAuthor:    strings.TrimSpace(evt.SourceAuthor),
		sourceAuthorID:  strings.TrimSpace(evt.SourceAuthorID),
		sourceTitle:     strings.TrimSpace(evt.SourceTitle),
		seq:             seq,
		ts:              evt.TS,
		activityActorID: activityActorID,
		outboxActorName: eventStoreProjectionPostCommittedActorName(evt),
		outboxBody:      eventStoreProjectionPostCommittedBody(evt),
	}, true, nil
}

func insertHotNativeProjectionThreadsTx(tx *sql.Tx, threads []hotNativeProjectionThread) error {
	if len(threads) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO threads (id, board, author, author_id, title, locked, post_count, last_seq, created_ts, created_at, updated_at) VALUES `)
	args := make([]any, 0, len(threads)*11)
	for i, thread := range threads {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?)`)
		args = append(args, thread.id, thread.boardID, thread.author, thread.authorID, thread.title, thread.seq, thread.ts, thread.ts, thread.ts)
	}
	_, err := qExec(tx, query.String(), args...)
	return err
}

func insertHotNativeProjectionPostsTx(tx *sql.Tx, posts []hotNativeProjectionPost) error {
	if len(posts) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO posts (id, thread, author, author_id, body, signature, content_type, reply_to, version, redacted,
	        tex, mail_back,
	        source_post, source_thread, source_board, source_author, source_author_id, source_title,
	        created_seq, updated_seq, created_at, updated_at) VALUES `)
	args := make([]any, 0, len(posts)*22)
	for i, post := range posts {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		args = append(args,
			post.id,
			post.threadID,
			post.author,
			post.authorID,
			post.body,
			post.signature,
			post.contentType,
			eventStoreProjectionNullStr(post.replyTo),
			eventStoreProjectionBoolInt(post.tex),
			eventStoreProjectionBoolInt(post.mailBack),
			post.sourcePost,
			post.sourceThread,
			post.sourceBoard,
			post.sourceAuthor,
			post.sourceAuthorID,
			post.sourceTitle,
			post.seq,
			post.seq,
			post.ts,
			post.ts,
		)
	}
	_, err := qExec(tx, query.String(), args...)
	return err
}

func updateHotNativeProjectionThreadMetaTx(tx *sql.Tx, posts []hotNativeProjectionPost) error {
	if len(posts) == 0 {
		return nil
	}
	byThread := make(map[string]hotNativeProjectionThreadMeta, len(posts))
	order := make([]string, 0, len(posts))
	for _, post := range posts {
		meta, ok := byThread[post.threadID]
		if !ok {
			order = append(order, post.threadID)
		}
		meta.count++
		if post.seq >= meta.lastSeq {
			meta.lastSeq = post.seq
			meta.updatedAt = post.ts
		}
		byThread[post.threadID] = meta
	}
	var query strings.Builder
	query.WriteString(`UPDATE threads AS t
	   SET post_count=t.post_count + v.post_count_delta,
	       last_seq=v.last_seq,
	       updated_at=v.updated_at
	  FROM (VALUES `)
	args := make([]any, 0, len(order)*4)
	for i, threadID := range order {
		if i > 0 {
			query.WriteByte(',')
		}
		meta := byThread[threadID]
		query.WriteString(`(CAST(? AS TEXT), CAST(? AS INTEGER), CAST(? AS BIGINT), CAST(? AS BIGINT))`)
		args = append(args, threadID, meta.count, meta.lastSeq, meta.updatedAt)
	}
	query.WriteString(`) AS v(thread_id, post_count_delta, last_seq, updated_at)
	 WHERE t.id=v.thread_id`)
	_, err := qExec(tx, query.String(), args...)
	return err
}

func insertHotNativeProjectionFeedsTx(tx *sql.Tx, table string, posts []hotNativeProjectionPost) error {
	if len(posts) == 0 {
		return nil
	}
	if table != "resident_feed_posts" && table != "latest_feed_posts" {
		return fmt.Errorf("unsupported feed table %q", table)
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO ` + table + ` (post_id, thread_id, board_id, created_seq, updated_seq) VALUES `)
	args := make([]any, 0, len(posts)*5)
	for i, post := range posts {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, ?, ?)`)
		args = append(args, post.id, post.threadID, post.boardID, post.seq, post.seq)
	}
	query.WriteString(` ON CONFLICT(post_id) DO UPDATE SET
	    thread_id=excluded.thread_id,
	    board_id=excluded.board_id,
	    created_seq=excluded.created_seq,
	    updated_seq=excluded.updated_seq`)
	_, err := qExec(tx, query.String(), args...)
	return err
}

func insertHotNativeProjectionFTSTx(tx *sql.Tx, posts []hotNativeProjectionPost) error {
	if len(posts) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO posts_fts (post_id, thread_id, board_id, author, body) VALUES `)
	args := make([]any, 0, len(posts)*5)
	for i, post := range posts {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, ?, ?)`)
		args = append(args, post.id, post.threadID, post.boardID, post.author, post.body)
	}
	_, err := qExec(tx, query.String(), args...)
	return err
}

func upsertHotNativeProjectionActivityTx(tx *sql.Tx, posts []hotNativeProjectionPost) error {
	for _, post := range posts {
		if err := recordPostActivityFromEvent(tx, post.activityActorID, post.seq, post.ts); err != nil {
			return err
		}
	}
	return nil
}

func insertHotNativeProjectionOutboxTx(tx *sql.Tx, posts []hotNativeProjectionPost) error {
	if len(posts) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO outbox_jobs (id, kind, payload, status, attempts, next_run_at, created_at, updated_at) VALUES `)
	args := make([]any, 0, len(posts)*8)
	for i, post := range posts {
		raw, err := json.Marshal(postCommittedJob{
			ActorID:   post.activityActorID,
			ActorName: post.outboxActorName,
			PostID:    post.id,
			ThreadID:  post.threadID,
			BoardID:   post.boardID,
			Body:      post.outboxBody,
			ReplyTo:   strings.TrimSpace(post.replyTo),
			TS:        post.ts,
			Seq:       post.seq,
		})
		if err != nil {
			return err
		}
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, 'pending', 0, ?, ?, ?)`)
		args = append(args, newID("job_"), outboxPostCommitted, string(raw), post.ts, post.ts, post.ts)
	}
	_, err := qExec(tx, query.String(), args...)
	return err
}

func eventStoreProjectionBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func eventStoreProjectionNullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func enqueueEventStoreProjectionSideEffects(tx *sql.Tx, seq int64, payload any, scopes []string) error {
	post, ok := payload.(*proto.PostAppendedPayload)
	if !ok || post == nil {
		return nil
	}
	actorID := postCommittedActorIDFromEvent(tx, post)
	actorName := eventStoreProjectionPostCommittedActorName(post)
	boardID := projectionBoardFromScopes(scopes)
	if boardID == "" {
		var err error
		boardID, err = eventStoreProjectionThreadBoard(tx, post.Thread)
		if err != nil {
			return err
		}
	}
	body := eventStoreProjectionPostCommittedBody(post)
	return enqueueOutboxJob(tx, outboxPostCommitted, postCommittedJob{
		ActorID:   actorID,
		ActorName: actorName,
		PostID:    strings.TrimSpace(post.ID),
		ThreadID:  strings.TrimSpace(post.Thread),
		BoardID:   boardID,
		Body:      body,
		ReplyTo:   strings.TrimSpace(post.ReplyTo),
		TS:        post.TS,
		Seq:       seq,
	}, post.TS)
}

func eventStoreProjectionPostCommittedActorName(post *proto.PostAppendedPayload) string {
	if post == nil {
		return ""
	}
	if actorName := strings.TrimSpace(post.PostCommitActorName); actorName != "" {
		return actorName
	}
	return strings.TrimSpace(post.Author)
}

func eventStoreProjectionThreadBoard(tx *sql.Tx, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", nil
	}
	var boardID string
	err := qQueryRow(tx, `SELECT board FROM threads WHERE id=?`, threadID).Scan(&boardID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return boardID, err
}

func eventStoreProjectionPostCommittedBody(post *proto.PostAppendedPayload) string {
	if post == nil {
		return ""
	}
	if post.PostCommitBody != nil {
		return *post.PostCommitBody
	}
	sourceBody := post.Body
	if strings.TrimSpace(post.RawBody) != "" {
		sourceBody = post.RawBody
	}
	if pollBlock, cleanBody := extractPoll(sourceBody); pollBlock != nil && cleanBody != sourceBody {
		return cleanBody
	}
	return post.Body
}

func recordEventStoreProjectionWatermarkTx(tx *sql.Tx, watermark string, offset int64) error {
	return projections.RecordProjectionWatermarkApplied(tx, watermark, offset, nowMS())
}
