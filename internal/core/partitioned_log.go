package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// SQLEventStore adapts the current SQL events table to the IS4 event-log
// interface. It is useful for shadow/parity code while Postgres remains the
// source of truth.
type SQLEventStore struct {
	db *sql.DB
}

func NewSQLEventStore(db *sql.DB) *SQLEventStore {
	return &SQLEventStore{db: db}
}

func (s *SQLEventStore) Append(ctx context.Context, event EventAppend) (*proto.Event, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sql event store: nil db")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	id := event.ID
	if id == "" {
		id = newID("evt_")
	}
	seq, err := appendEvent(tx, id, event.Kind, event.Scopes, event.Payload)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	events, err := replayEvents(s.db, seq-1, nil, 1)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 || events[0].Seq != seq {
		return nil, fmt.Errorf("sql event store: appended seq %d not replayable", seq)
	}
	return events[0], nil
}

func (s *SQLEventStore) Head(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("sql event store: nil db")
	}
	return headSeq(s.db)
}

func (s *SQLEventStore) Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sql event store: nil db")
	}
	return replayEvents(s.db, after, scopes, limit)
}

func (s *SQLEventStore) ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sql event store: nil db")
	}
	return replayPartitionEvents(s.db, partitionKind, partitionKey, afterOffset, limit)
}

func (s *SQLEventStore) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	offsets, err := s.ListEventPartitionOffsets(ctx, limit)
	if err != nil {
		return nil, err
	}
	partitions := make([]LogPartition, 0, len(offsets))
	for _, offset := range offsets {
		partitions = append(partitions, offset.Partition)
	}
	return partitions, nil
}

func (s *SQLEventStore) ListEventPartitionOffsets(ctx context.Context, limit int) ([]EventPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sql event store: nil db")
	}
	query := `SELECT partition_kind, partition_key, last_offset
	            FROM event_partition_offsets
	           ORDER BY last_offset DESC, partition_kind, partition_key`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := qQuery(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offsets := []EventPartitionOffset{}
	for rows.Next() {
		var kind, key string
		var offset int64
		if err := rows.Scan(&kind, &key, &offset); err != nil {
			return nil, err
		}
		offsets = append(offsets, EventPartitionOffset{
			Partition:  LogPartition{Kind: kind, Key: key}.Normalize(),
			LastOffset: offset,
		})
	}
	return offsets, rows.Err()
}

const (
	EventParityAppendError = "append_error"
	EventParityCoverage    = "coverage_error"
	EventParityMismatch    = "mismatch"
	EventParityReplayError = "replay_error"
)

type EventParityIssue struct {
	Kind          string
	Event         proto.EventKind
	Partition     LogPartition
	PrimarySeq    int64
	ShadowSeq     int64
	PrimaryOffset int64
	ShadowOffset  int64
	Message       string
	Err           string
}

type EventParityReporter interface {
	RecordEventParityIssue(issue EventParityIssue)
}

type EventReplayParityResult struct {
	Partition         LogPartition
	AfterOffset       int64
	Limit             int
	PrimaryCount      int
	ShadowCount       int
	Compared          int
	LastPrimaryOffset int64
	LastShadowOffset  int64
	Issues            []EventParityIssue
}

type EventLogPromotionReadinessConfig struct {
	Primary             EventStore
	Candidate           EventStore
	PrimaryPartitions   EventPartitionLister
	CandidatePartitions EventPartitionLister
	Reporter            EventParityReporter
	ReplayLimit         int
	PartitionLimit      int
}

type EventLogPromotionReadinessReport struct {
	Ready             bool
	ReplayLimit       int
	PartitionLimit    int
	PartitionsChecked int
	WindowsChecked    int
	Compared          int
	Issues            []EventParityIssue
}

type EventPartitionOffset struct {
	Partition  LogPartition
	LastOffset int64
}

type EventReplayParityRunnerConfig struct {
	Primary        EventStore
	Shadow         EventStore
	Partitions     EventPartitionLister
	Reporter       EventParityReporter
	ReplayLimit    int
	PartitionLimit int
	Interval       time.Duration
}

type EventReplayParityRunner struct {
	primary        EventStore
	shadow         EventStore
	partitions     EventPartitionLister
	reporter       EventParityReporter
	replayLimit    int
	partitionLimit int
	interval       time.Duration

	mu      sync.Mutex
	offsets map[LogPartition]int64
}

// EventParityRecorder is a small in-memory reporter for tests and local shadow
// runs. Production deployments can provide a reporter that ships issues to logs
// or an audit stream.
type EventParityRecorder struct {
	mu     sync.Mutex
	issues []EventParityIssue
}

func (r *EventParityRecorder) RecordEventParityIssue(issue EventParityIssue) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.issues = append(r.issues, issue)
	r.mu.Unlock()
}

func (r *EventParityRecorder) Issues() []EventParityIssue {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]EventParityIssue(nil), r.issues...)
}

func NewEventReplayParityRunner(config EventReplayParityRunnerConfig) *EventReplayParityRunner {
	partitions := config.Partitions
	if partitions == nil {
		if lister, ok := config.Primary.(EventPartitionLister); ok {
			partitions = lister
		}
	}
	replayLimit := config.ReplayLimit
	if replayLimit <= 0 {
		replayLimit = 100
	}
	partitionLimit := config.PartitionLimit
	if partitionLimit <= 0 {
		partitionLimit = 100
	}
	interval := config.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &EventReplayParityRunner{
		primary:        config.Primary,
		shadow:         config.Shadow,
		partitions:     partitions,
		reporter:       config.Reporter,
		replayLimit:    replayLimit,
		partitionLimit: partitionLimit,
		interval:       interval,
		offsets:        map[LogPartition]int64{},
	}
}

func (r *EventReplayParityRunner) CheckOnce(ctx context.Context) ([]EventReplayParityResult, error) {
	if r == nil {
		return nil, fmt.Errorf("event replay parity runner: nil receiver")
	}
	if r.primary == nil {
		return nil, fmt.Errorf("event replay parity runner: nil primary")
	}
	if r.shadow == nil {
		return nil, fmt.Errorf("event replay parity runner: nil shadow")
	}
	if r.partitions == nil {
		return nil, fmt.Errorf("event replay parity runner: nil partition lister")
	}
	partitions, err := r.partitions.ListEventPartitions(ctx, r.partitionLimit)
	if err != nil {
		return nil, err
	}
	results := make([]EventReplayParityResult, 0, len(partitions))
	for _, partition := range partitions {
		partition = partition.Normalize()
		after := r.offsetFor(partition)
		result, err := CheckEventReplayParity(ctx, r.primary, r.shadow, partition, after, r.replayLimit, r.reporter)
		if err != nil {
			return results, err
		}
		if len(result.Issues) == 0 && result.LastPrimaryOffset > after {
			r.setOffset(partition, result.LastPrimaryOffset)
		}
		results = append(results, result)
	}
	return results, nil
}

func (r *EventReplayParityRunner) Run(ctx context.Context) {
	if r == nil {
		return
	}
	_, _ = r.CheckOnce(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = r.CheckOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *EventReplayParityRunner) SetCheckpoint(partition LogPartition, offset int64) {
	if r == nil {
		return
	}
	r.setOffset(partition, offset)
}

func (r *EventReplayParityRunner) offsetFor(partition LogPartition) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.offsets[partition.Normalize()]
}

func (r *EventReplayParityRunner) setOffset(partition LogPartition, offset int64) {
	r.mu.Lock()
	if offset > r.offsets[partition.Normalize()] {
		r.offsets[partition.Normalize()] = offset
	}
	r.mu.Unlock()
}

// ShadowEventStore writes through to a primary EventStore and mirrors appends to
// a shadow EventStore. Shadow errors and mismatches are reported but do not
// change the primary result; this is the promotion-safe mode needed before a
// partitioned log can become authoritative.
type ShadowEventStore struct {
	primary  EventStore
	shadow   EventStore
	reporter EventParityReporter
}

func NewShadowEventStore(primary, shadow EventStore, reporter EventParityReporter) *ShadowEventStore {
	return &ShadowEventStore{
		primary:  primary,
		shadow:   shadow,
		reporter: reporter,
	}
}

func (s *ShadowEventStore) Append(ctx context.Context, event EventAppend) (*proto.Event, error) {
	if s == nil || s.primary == nil {
		return nil, fmt.Errorf("shadow event store: nil primary")
	}
	primary, err := s.primary.Append(ctx, event)
	if err != nil {
		return nil, err
	}
	if s.shadow == nil {
		return primary, nil
	}
	shadowAppend := eventAppendFromEvent(primary)
	shadow, err := s.shadow.Append(ctx, shadowAppend)
	if err != nil {
		recordEventParityIssue(s.reporter, EventParityIssue{
			Kind:          EventParityAppendError,
			Event:         primary.Kind,
			Partition:     logPartitionFromEvent(primary),
			PrimarySeq:    primary.Seq,
			PrimaryOffset: primary.PartitionOffset,
			Message:       "shadow append failed",
			Err:           err.Error(),
		})
		return primary, nil
	}
	if issue, ok := compareEventParity(primary, shadow); ok {
		recordEventParityIssue(s.reporter, issue)
	}
	return primary, nil
}

func (s *ShadowEventStore) Head(ctx context.Context) (int64, error) {
	if s == nil || s.primary == nil {
		return 0, fmt.Errorf("shadow event store: nil primary")
	}
	return s.primary.Head(ctx)
}

func (s *ShadowEventStore) Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error) {
	if s == nil || s.primary == nil {
		return nil, fmt.Errorf("shadow event store: nil primary")
	}
	return s.primary.Replay(ctx, after, scopes, limit)
}

func (s *ShadowEventStore) ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	if s == nil || s.primary == nil {
		return nil, fmt.Errorf("shadow event store: nil primary")
	}
	return s.primary.ReplayPartition(ctx, partitionKind, partitionKey, afterOffset, limit)
}

func recordEventParityIssue(reporter EventParityReporter, issue EventParityIssue) {
	if issue.Kind == EventParityAppendError {
		metrics.EventLogShadowAppendFailures.Inc()
	} else {
		metrics.EventLogShadowParityFailures.Inc()
	}
	if reporter != nil {
		reporter.RecordEventParityIssue(issue)
	}
}

func CheckEventReplayParity(ctx context.Context, primary, shadow EventStore, partition LogPartition, afterOffset int64, limit int, reporter EventParityReporter) (EventReplayParityResult, error) {
	partition = partition.Normalize()
	result := EventReplayParityResult{
		Partition:   partition,
		AfterOffset: afterOffset,
		Limit:       limit,
	}
	if primary == nil {
		return result, fmt.Errorf("event replay parity: nil primary")
	}
	if shadow == nil {
		return result, fmt.Errorf("event replay parity: nil shadow")
	}
	primaryEvents, err := primary.ReplayPartition(ctx, partition.Kind, partition.Key, afterOffset, limit)
	if err != nil {
		return result, fmt.Errorf("event replay parity: primary replay: %w", err)
	}
	shadowEvents, err := shadow.ReplayPartition(ctx, partition.Kind, partition.Key, afterOffset, limit)
	if err != nil {
		issue := EventParityIssue{
			Kind:      EventParityReplayError,
			Partition: partition,
			Message:   "shadow partition replay failed",
			Err:       err.Error(),
		}
		recordEventParityIssue(reporter, issue)
		result.PrimaryCount = len(primaryEvents)
		result.Issues = append(result.Issues, issue)
		return result, nil
	}

	result.PrimaryCount = len(primaryEvents)
	result.ShadowCount = len(shadowEvents)
	shadowByOffset := make(map[int64]*proto.Event, len(shadowEvents))
	for _, evt := range shadowEvents {
		shadowByOffset[evt.PartitionOffset] = evt
		if evt.PartitionOffset > result.LastShadowOffset {
			result.LastShadowOffset = evt.PartitionOffset
		}
	}
	seenOffsets := map[int64]bool{}
	for _, primaryEvt := range primaryEvents {
		if primaryEvt.PartitionOffset > result.LastPrimaryOffset {
			result.LastPrimaryOffset = primaryEvt.PartitionOffset
		}
		seenOffsets[primaryEvt.PartitionOffset] = true
		shadowEvt := shadowByOffset[primaryEvt.PartitionOffset]
		if shadowEvt == nil {
			issue := EventParityIssue{
				Kind:          EventParityMismatch,
				Event:         primaryEvt.Kind,
				Partition:     partition,
				PrimarySeq:    primaryEvt.Seq,
				PrimaryOffset: primaryEvt.PartitionOffset,
				Message:       fmt.Sprintf("shadow missing event at partition offset %d", primaryEvt.PartitionOffset),
			}
			recordEventParityIssue(reporter, issue)
			result.Issues = append(result.Issues, issue)
			continue
		}
		result.Compared++
		if issue, ok := compareEventParity(primaryEvt, shadowEvt); ok {
			recordEventParityIssue(reporter, issue)
			result.Issues = append(result.Issues, issue)
		}
	}
	for _, shadowEvt := range shadowEvents {
		if seenOffsets[shadowEvt.PartitionOffset] {
			continue
		}
		issue := EventParityIssue{
			Kind:         EventParityMismatch,
			Event:        shadowEvt.Kind,
			Partition:    partition,
			ShadowSeq:    shadowEvt.Seq,
			ShadowOffset: shadowEvt.PartitionOffset,
			Message:      fmt.Sprintf("shadow has extra event at partition offset %d", shadowEvt.PartitionOffset),
		}
		recordEventParityIssue(reporter, issue)
		result.Issues = append(result.Issues, issue)
	}
	return result, nil
}

func CheckEventLogPromotionReadiness(ctx context.Context, config EventLogPromotionReadinessConfig) (EventLogPromotionReadinessReport, error) {
	if err := ctx.Err(); err != nil {
		return EventLogPromotionReadinessReport{}, err
	}
	report := EventLogPromotionReadinessReport{
		ReplayLimit:    config.ReplayLimit,
		PartitionLimit: config.PartitionLimit,
	}
	if report.ReplayLimit <= 0 {
		report.ReplayLimit = 100
	}
	if config.Primary == nil {
		return report, fmt.Errorf("event log promotion readiness: nil primary")
	}
	if config.Candidate == nil {
		return report, fmt.Errorf("event log promotion readiness: nil candidate")
	}
	primaryPartitions, err := eventPartitionListerFor(config.Primary, config.PrimaryPartitions, "primary")
	if err != nil {
		return report, err
	}
	candidatePartitions, err := eventPartitionListerFor(config.Candidate, config.CandidatePartitions, "candidate")
	if err != nil {
		return report, err
	}
	partitions, issues, err := promotionReadinessPartitions(ctx, primaryPartitions, candidatePartitions, report.PartitionLimit, config.Reporter)
	if err != nil {
		return report, err
	}
	report.Issues = append(report.Issues, issues...)

	for _, partition := range partitions {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.PartitionsChecked++
		after := int64(0)
		for {
			result, err := CheckEventReplayParity(ctx, config.Primary, config.Candidate, partition, after, report.ReplayLimit, config.Reporter)
			if err != nil {
				return report, err
			}
			report.WindowsChecked++
			report.Compared += result.Compared
			report.Issues = append(report.Issues, result.Issues...)
			if len(result.Issues) > 0 {
				break
			}
			if result.PrimaryCount == 0 && result.ShadowCount == 0 {
				break
			}
			next := result.LastPrimaryOffset
			if next <= after {
				issue := EventParityIssue{
					Kind:      EventParityCoverage,
					Partition: partition,
					Message:   fmt.Sprintf("promotion readiness made no replay progress after partition offset %d", after),
				}
				recordEventParityIssue(config.Reporter, issue)
				report.Issues = append(report.Issues, issue)
				break
			}
			after = next
		}
	}
	report.Ready = len(report.Issues) == 0
	return report, nil
}

func eventPartitionListerFor(store EventStore, override EventPartitionLister, role string) (EventPartitionLister, error) {
	if override != nil {
		return override, nil
	}
	lister, ok := store.(EventPartitionLister)
	if !ok {
		return nil, fmt.Errorf("event log promotion readiness: %s does not list event partitions", role)
	}
	return lister, nil
}

func promotionReadinessPartitions(ctx context.Context, primary, candidate EventPartitionLister, partitionLimit int, reporter EventParityReporter) ([]LogPartition, []EventParityIssue, error) {
	primaryPartitions, primaryLimited, err := listPromotionReadinessPartitions(ctx, primary, partitionLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("event log promotion readiness: list primary partitions: %w", err)
	}
	candidatePartitions, candidateLimited, err := listPromotionReadinessPartitions(ctx, candidate, partitionLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("event log promotion readiness: list candidate partitions: %w", err)
	}
	issues := []EventParityIssue{}
	if primaryLimited {
		issue := EventParityIssue{
			Kind:    EventParityCoverage,
			Message: fmt.Sprintf("primary partition count exceeds promotion readiness limit %d", partitionLimit),
		}
		recordEventParityIssue(reporter, issue)
		issues = append(issues, issue)
	}
	if candidateLimited {
		issue := EventParityIssue{
			Kind:    EventParityCoverage,
			Message: fmt.Sprintf("candidate partition count exceeds promotion readiness limit %d", partitionLimit),
		}
		recordEventParityIssue(reporter, issue)
		issues = append(issues, issue)
	}

	seen := map[LogPartition]bool{}
	partitions := make([]LogPartition, 0, len(primaryPartitions)+len(candidatePartitions))
	for _, partition := range primaryPartitions {
		partition = partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		partitions = append(partitions, partition)
	}
	for _, partition := range candidatePartitions {
		partition = partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		partitions = append(partitions, partition)
	}
	sort.Slice(partitions, func(i, j int) bool {
		if partitions[i].Kind == partitions[j].Kind {
			return partitions[i].Key < partitions[j].Key
		}
		return partitions[i].Kind < partitions[j].Kind
	})
	return partitions, issues, nil
}

func listPromotionReadinessPartitions(ctx context.Context, lister EventPartitionLister, limit int) ([]LogPartition, bool, error) {
	if lister == nil {
		return nil, false, fmt.Errorf("nil partition lister")
	}
	queryLimit := limit
	if limit > 0 {
		queryLimit = limit + 1
	}
	partitions, err := lister.ListEventPartitions(ctx, queryLimit)
	if err != nil {
		return nil, false, err
	}
	limited := limit > 0 && len(partitions) > limit
	if limited {
		partitions = partitions[:limit]
	}
	out := make([]LogPartition, 0, len(partitions))
	seen := map[LogPartition]bool{}
	for _, partition := range partitions {
		partition = partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		out = append(out, partition)
	}
	return out, limited, nil
}

// MemoryEventStore is a partition-aware in-memory EventStore for shadow-mode
// tests. It assigns independent partition offsets while preserving a scalar seq
// compatibility view.
type MemoryEventStore struct {
	mu               sync.Mutex
	events           []*proto.Event
	partitionOffsets map[LogPartition]int64
}

func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{
		partitionOffsets: map[LogPartition]int64{},
	}
}

func (s *MemoryEventStore) Append(ctx context.Context, event EventAppend) (*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("memory event store: nil receiver")
	}
	payload, err := cloneEventPayload(event.Kind, event.Payload)
	if err != nil {
		return nil, err
	}
	partition := logPartitionFromEventPartition(eventPartitionFor(event.Kind, event.Scopes))

	s.mu.Lock()
	defer s.mu.Unlock()
	offset := s.partitionOffsets[partition] + 1
	if event.PartitionOffset > 0 {
		if event.PartitionOffset != offset {
			return nil, fmt.Errorf("memory event store: partition offset %d for %s/%s must follow current tail %d",
				event.PartitionOffset, partition.Kind, partition.Key, s.partitionOffsets[partition])
		}
		offset = event.PartitionOffset
	}
	s.partitionOffsets[partition] = offset
	seq := int64(len(s.events) + 1)
	if event.CompatibilitySeq > 0 {
		seq = event.CompatibilitySeq
	}
	id := event.ID
	if id == "" {
		id = newID("evt_")
	}
	evt := &proto.Event{
		ID:              id,
		Kind:            event.Kind,
		Seq:             seq,
		Payload:         payload,
		TS:              eventAppendTS(event.TS),
		PartitionKind:   partition.Kind,
		PartitionKey:    partition.Key,
		PartitionOffset: offset,
		Scopes:          append([]string(nil), event.Scopes...),
	}
	s.events = append(s.events, cloneEvent(evt))
	return cloneEvent(evt), nil
}

func (s *MemoryEventStore) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, fmt.Errorf("memory event store: nil receiver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.events)), nil
}

func (s *MemoryEventStore) Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("memory event store: nil receiver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*proto.Event, 0, len(s.events))
	for _, evt := range s.events {
		if evt.Seq <= after {
			continue
		}
		if scopes != nil && !scopesOverlap(evt.Scopes, scopes) {
			continue
		}
		out = append(out, cloneEvent(evt))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryEventStore) ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("memory event store: nil receiver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*proto.Event, 0, len(s.events))
	for _, evt := range s.events {
		if evt.PartitionKind != partitionKind || evt.PartitionKey != partitionKey || evt.PartitionOffset <= afterOffset {
			continue
		}
		out = append(out, cloneEvent(evt))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryEventStore) SeedEventPartitionOffset(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("memory event store: nil receiver")
	}
	if offset < 0 {
		offset = 0
	}
	partition = partition.Normalize()
	s.mu.Lock()
	if offset > s.partitionOffsets[partition] {
		s.partitionOffsets[partition] = offset
	}
	s.mu.Unlock()
	return nil
}

func (s *MemoryEventStore) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("memory event store: nil receiver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	partitions := make([]LogPartition, 0, len(s.partitionOffsets))
	for partition := range s.partitionOffsets {
		partitions = append(partitions, partition.Normalize())
	}
	sort.Slice(partitions, func(i, j int) bool {
		oi := s.partitionOffsets[partitions[i]]
		oj := s.partitionOffsets[partitions[j]]
		if oi == oj {
			if partitions[i].Kind == partitions[j].Kind {
				return partitions[i].Key < partitions[j].Key
			}
			return partitions[i].Kind < partitions[j].Kind
		}
		return oi > oj
	})
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions, nil
}

// MemoryCommandLog is a deterministic in-process CommandLog for tests and
// shadow-mode fixtures. It mirrors broker partition semantics: offsets are
// independent per partition and fetches never cross partition boundaries.
type MemoryCommandLog struct {
	mu        sync.Mutex
	records   map[LogPartition][]CommandLogRecord
	byReceipt map[commandReceiptKey]CommandLogRecord
	committed map[LogPartition]int64
}

func NewMemoryCommandLog() *MemoryCommandLog {
	return &MemoryCommandLog{
		records:   map[LogPartition][]CommandLogRecord{},
		byReceipt: map[commandReceiptKey]CommandLogRecord{},
		committed: map[LogPartition]int64{},
	}
}

func (l *MemoryCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return CommandLogRecord{}, err
	}
	if l == nil {
		return CommandLogRecord{}, fmt.Errorf("memory command log: nil receiver")
	}
	record.Partition = record.Partition.Normalize()
	record.Payload = append([]byte(nil), record.Payload...)
	if strings.TrimSpace(record.CID) != "" && record.EnqueuedAt <= 0 {
		return CommandLogRecord{}, fmt.Errorf("memory command log: enqueue time is required when command receipt is set")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if key, ok := newCommandReceiptKey(record.Partition, record.ActorID, record.CID); ok {
		if existing, ok := l.byReceipt[key]; ok {
			if !sameCommandLogRecordIdentity(existing, record) {
				return CommandLogRecord{}, fmt.Errorf("memory command log: duplicate command receipt %q has different content", record.CID)
			}
			return cloneCommandLogRecord(existing), nil
		}
	}
	record.Offset = int64(len(l.records[record.Partition]) + 1)
	if record.CID == "" {
		record.CID = SyntheticCommandLogCID(record.Partition, record.Offset)
	}
	l.records[record.Partition] = append(l.records[record.Partition], record)
	if key, ok := newCommandReceiptKey(record.Partition, record.ActorID, record.CID); ok {
		l.byReceipt[key] = cloneCommandLogRecord(record)
	}
	return cloneCommandLogRecord(record), nil
}

func (l *MemoryCommandLog) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("memory command log: nil receiver")
	}
	partition = partition.Normalize()

	l.mu.Lock()
	defer l.mu.Unlock()
	source := l.records[partition]
	out := make([]CommandLogRecord, 0, len(source))
	for _, record := range source {
		if record.Offset <= afterOffset {
			continue
		}
		out = append(out, cloneCommandLogRecord(record))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (l *MemoryCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil {
		return fmt.Errorf("memory command log: nil receiver")
	}
	partition = partition.Normalize()

	l.mu.Lock()
	if offset > l.committed[partition] {
		l.committed[partition] = offset
	}
	l.mu.Unlock()
	return nil
}

func (l *MemoryCommandLog) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l == nil {
		return 0, fmt.Errorf("memory command log: nil receiver")
	}
	partition = partition.Normalize()

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.committed[partition], nil
}

func (l *MemoryCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("memory command log: nil receiver")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	partitions := make([]LogPartition, 0, len(l.records))
	for partition, records := range l.records {
		if len(records) == 0 {
			continue
		}
		partitions = append(partitions, partition.Normalize())
	}
	sort.Slice(partitions, func(i, j int) bool {
		oi := int64(len(l.records[partitions[i]]))
		oj := int64(len(l.records[partitions[j]]))
		if oi == oj {
			if partitions[i].Kind == partitions[j].Kind {
				return partitions[i].Key < partitions[j].Key
			}
			return partitions[i].Kind < partitions[j].Kind
		}
		return oi > oj
	})
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions, nil
}

func (l *MemoryCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("memory command log: nil receiver")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	offsets := make([]CommandPartitionOffset, 0, len(l.records))
	for partition, records := range l.records {
		if len(records) == 0 {
			continue
		}
		partition = partition.Normalize()
		offsets = append(offsets, CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      int64(len(records)),
			CommittedOffset: l.committed[partition],
		})
	}
	sort.Slice(offsets, func(i, j int) bool {
		li := offsets[i].TailOffset - offsets[i].CommittedOffset
		lj := offsets[j].TailOffset - offsets[j].CommittedOffset
		if li == lj {
			if offsets[i].TailOffset == offsets[j].TailOffset {
				if offsets[i].Partition.Kind == offsets[j].Partition.Kind {
					return offsets[i].Partition.Key < offsets[j].Partition.Key
				}
				return offsets[i].Partition.Kind < offsets[j].Partition.Kind
			}
			return offsets[i].TailOffset > offsets[j].TailOffset
		}
		return li > lj
	})
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func cloneCommandLogRecord(record CommandLogRecord) CommandLogRecord {
	record.Partition = record.Partition.Normalize()
	record.Payload = append([]byte(nil), record.Payload...)
	return record
}

func compareEventParity(primary, shadow *proto.Event) (EventParityIssue, bool) {
	if primary == nil || shadow == nil {
		return EventParityIssue{
			Kind:      EventParityMismatch,
			Partition: logPartitionFromEvent(primary),
			Message:   "primary or shadow event was nil",
		}, true
	}
	issue := EventParityIssue{
		Kind:          EventParityMismatch,
		Event:         primary.Kind,
		Partition:     logPartitionFromEvent(primary),
		PrimarySeq:    primary.Seq,
		ShadowSeq:     shadow.Seq,
		PrimaryOffset: primary.PartitionOffset,
		ShadowOffset:  shadow.PartitionOffset,
	}
	switch {
	case primary.Kind != shadow.Kind:
		issue.Message = fmt.Sprintf("event kind mismatch: primary=%s shadow=%s", primary.Kind, shadow.Kind)
	case primary.PartitionKind != shadow.PartitionKind || primary.PartitionKey != shadow.PartitionKey:
		issue.Message = fmt.Sprintf("partition mismatch: primary=%s/%s shadow=%s/%s", primary.PartitionKind, primary.PartitionKey, shadow.PartitionKind, shadow.PartitionKey)
	case primary.PartitionOffset != shadow.PartitionOffset:
		issue.Message = fmt.Sprintf("partition offset mismatch: primary=%d shadow=%d", primary.PartitionOffset, shadow.PartitionOffset)
	case primary.Seq != shadow.Seq:
		issue.Message = fmt.Sprintf("seq mismatch: primary=%d shadow=%d", primary.Seq, shadow.Seq)
	case !sameScopes(primary.Scopes, shadow.Scopes):
		issue.Message = fmt.Sprintf("scope mismatch: primary=%v shadow=%v", primary.Scopes, shadow.Scopes)
	case !samePayload(primary.Payload, shadow.Payload):
		issue.Message = "payload mismatch"
	default:
		return EventParityIssue{}, false
	}
	return issue, true
}

func logPartitionFromEvent(evt *proto.Event) LogPartition {
	if evt == nil {
		return LogPartition{}.Normalize()
	}
	return LogPartition{Kind: evt.PartitionKind, Key: evt.PartitionKey}.Normalize()
}

func eventAppendFromEvent(evt *proto.Event) EventAppend {
	if evt == nil {
		return EventAppend{}
	}
	return EventAppend{
		ID:               evt.ID,
		Kind:             evt.Kind,
		Scopes:           append([]string(nil), evt.Scopes...),
		Payload:          evt.Payload,
		CompatibilitySeq: evt.Seq,
		PartitionOffset:  evt.PartitionOffset,
		TS:               evt.TS,
	}
}

func cloneEvent(evt *proto.Event) *proto.Event {
	if evt == nil {
		return nil
	}
	payload, err := cloneEventPayload(evt.Kind, evt.Payload)
	if err != nil {
		payload = evt.Payload
	}
	return &proto.Event{
		ID:              evt.ID,
		Kind:            evt.Kind,
		Seq:             evt.Seq,
		ESeq:            evt.ESeq,
		Payload:         payload,
		TS:              evt.TS,
		PartitionKind:   evt.PartitionKind,
		PartitionKey:    evt.PartitionKey,
		PartitionOffset: evt.PartitionOffset,
		Scopes:          append([]string(nil), evt.Scopes...),
	}
}

func cloneEventPayload(kind proto.EventKind, payload any) (any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return unmarshalPayload(kind, raw)
}

func sameScopes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	return reflect.DeepEqual(aa, bb)
}

func samePayload(a, b any) bool {
	aa, err := canonicalJSON(a)
	if err != nil {
		return false
	}
	bb, err := canonicalJSON(b)
	if err != nil {
		return false
	}
	return string(aa) == string(bb)
}

func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}
