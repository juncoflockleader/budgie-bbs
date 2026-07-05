package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
)

type CommandLogPartitionIndexer interface {
	CommandPartitionLister
	CommandPartitionOffsetLister
	RecordCommandPartitionTail(ctx context.Context, partition LogPartition, offset int64) error
	RecordCommandPartitionCommit(ctx context.Context, partition LogPartition, offset int64) error
}

type IndexedCommandLog struct {
	inner CommandLog
	index CommandLogPartitionIndexer
}

var _ CommandLog = (*IndexedCommandLog)(nil)
var _ CommandPartitionLister = (*IndexedCommandLog)(nil)
var _ CommandPartitionOffsetLister = (*IndexedCommandLog)(nil)
var _ CommandLogRebalanceAllower = (*IndexedCommandLog)(nil)
var _ CommandLogCommitRecorder = (*IndexedCommandLog)(nil)

func NewIndexedCommandLog(inner CommandLog, index CommandLogPartitionIndexer) *IndexedCommandLog {
	return &IndexedCommandLog{inner: inner, index: index}
}

func (l *IndexedCommandLog) Produce(ctx context.Context, record CommandLogRecord) (CommandLogRecord, error) {
	if l == nil || l.inner == nil {
		return CommandLogRecord{}, fmt.Errorf("indexed command log: nil inner log")
	}
	if l.index == nil {
		return CommandLogRecord{}, fmt.Errorf("indexed command log: nil partition index")
	}
	partition := record.Partition.Normalize()
	if err := l.index.RecordCommandPartitionTail(ctx, partition, 0); err != nil {
		return CommandLogRecord{}, err
	}
	produced, err := l.inner.Produce(ctx, record)
	if err != nil {
		return CommandLogRecord{}, err
	}
	if err := l.index.RecordCommandPartitionTail(ctx, produced.Partition, produced.Offset); err != nil {
		return CommandLogRecord{}, err
	}
	return produced, nil
}

func (l *IndexedCommandLog) FetchPartition(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]CommandLogRecord, error) {
	if l == nil || l.inner == nil {
		return nil, fmt.Errorf("indexed command log: nil inner log")
	}
	if l.index == nil {
		return nil, fmt.Errorf("indexed command log: nil partition index")
	}
	records, err := l.inner.FetchPartition(ctx, partition, afterOffset, limit)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if err := l.index.RecordCommandPartitionTail(ctx, record.Partition, record.Offset); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (l *IndexedCommandLog) CommitPartition(ctx context.Context, partition LogPartition, offset int64) error {
	if l == nil || l.inner == nil {
		return fmt.Errorf("indexed command log: nil inner log")
	}
	if l.index == nil {
		return fmt.Errorf("indexed command log: nil partition index")
	}
	if err := l.inner.CommitPartition(ctx, partition, offset); err != nil {
		return err
	}
	return l.index.RecordCommandPartitionCommit(ctx, partition, offset)
}

func (l *IndexedCommandLog) RecordCommandLogCommit(ctx context.Context, partition LogPartition, offset int64) error {
	if l == nil {
		return fmt.Errorf("indexed command log: nil receiver")
	}
	if l.index == nil {
		return fmt.Errorf("indexed command log: nil partition index")
	}
	if recorder, ok := l.inner.(CommandLogCommitRecorder); ok {
		if err := recorder.RecordCommandLogCommit(ctx, partition, offset); err != nil {
			return err
		}
	}
	return l.index.RecordCommandPartitionCommit(ctx, partition, offset)
}

func (l *IndexedCommandLog) CommittedOffset(ctx context.Context, partition LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l == nil {
		return 0, fmt.Errorf("indexed command log: nil receiver")
	}
	if l.inner == nil {
		return 0, fmt.Errorf("indexed command log: nil inner log")
	}
	if l.index == nil {
		return 0, fmt.Errorf("indexed command log: nil partition index")
	}
	partition = partition.Normalize()
	offsets, _, err := listCommandPartitionOffsetsWithLimit(ctx, l.index, 0)
	if err != nil {
		return 0, err
	}
	for _, offset := range offsets {
		if offset.Partition == partition {
			return offset.CommittedOffset, nil
		}
	}
	return 0, nil
}

func CommandPartitionsByTailOffset(offsets []CommandPartitionOffset, limit int) []LogPartition {
	return logmodel.CommandPartitionsByTailOffset(offsets, limit)
}

func SortCommandPartitionOffsetsByLag(offsets []CommandPartitionOffset) {
	logmodel.SortCommandPartitionOffsetsByLag(offsets)
}

func listCommandPartitionOffsetsWithLimit(ctx context.Context, lister CommandPartitionOffsetLister, limit int) ([]CommandPartitionOffset, bool, error) {
	if lister == nil {
		return nil, false, fmt.Errorf("nil command partition offset lister")
	}
	queryLimit := limit
	if limit > 0 {
		queryLimit = limit + 1
	}
	offsets, err := lister.ListCommandPartitionOffsets(ctx, queryLimit)
	if err != nil {
		return nil, false, err
	}
	limited := limit > 0 && len(offsets) > limit
	if limited {
		offsets = offsets[:limit]
	}
	for i := range offsets {
		offsets[i] = offsets[i].Normalize()
	}
	return offsets, limited, nil
}

func requireCommandPartitionOffsetLister(commandLog CommandLog, errMessage string) (CommandPartitionOffsetLister, error) {
	lister, ok := commandLog.(CommandPartitionOffsetLister)
	if !ok {
		return nil, errors.New(errMessage)
	}
	return lister, nil
}

func listCommandLogPartitionOffsetsWithLimit(ctx context.Context, commandLog CommandLog, limit int, errMessage string) ([]CommandPartitionOffset, bool, error) {
	lister, err := requireCommandPartitionOffsetLister(commandLog, errMessage)
	if err != nil {
		return nil, false, err
	}
	return listCommandPartitionOffsetsWithLimit(ctx, lister, limit)
}

func (l *IndexedCommandLog) AllowCommandLogRebalance() {
	if l == nil || l.inner == nil {
		return
	}
	allower, ok := l.inner.(CommandLogRebalanceAllower)
	if !ok {
		return
	}
	allower.AllowCommandLogRebalance()
}

func (l *IndexedCommandLog) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.index == nil {
		return nil, fmt.Errorf("indexed command log: nil partition index")
	}
	return l.index.ListCommandPartitions(ctx, limit)
}

func (l *IndexedCommandLog) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.index == nil {
		return nil, fmt.Errorf("indexed command log: nil partition index")
	}
	return l.index.ListCommandPartitionOffsets(ctx, limit)
}

type SQLCommandLogPartitionIndex struct {
	mu sync.RWMutex
	db *sql.DB
}

var _ CommandLogPartitionIndexer = (*SQLCommandLogPartitionIndex)(nil)

func NewSQLCommandLogPartitionIndex(db *sql.DB) *SQLCommandLogPartitionIndex {
	return &SQLCommandLogPartitionIndex{db: db}
}

func (i *SQLCommandLogPartitionIndex) BindDB(db *sql.DB) {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.db = db
	i.mu.Unlock()
}

func (i *SQLCommandLogPartitionIndex) RecordCommandPartitionTail(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := i.database()
	if err != nil {
		return err
	}
	partition = partition.Normalize()
	if offset < 0 {
		offset = 0
	}
	_, err = db.ExecContext(ctx, rebindPlaceholders(`
INSERT INTO command_log_partition_offsets (
    partition_kind, partition_key, tail_offset, committed_offset, updated_at
) VALUES (?,?,?,?,?)
ON CONFLICT(partition_kind, partition_key)
DO UPDATE SET
    tail_offset=CASE
        WHEN excluded.tail_offset > command_log_partition_offsets.tail_offset THEN excluded.tail_offset
        ELSE command_log_partition_offsets.tail_offset
    END,
    updated_at=excluded.updated_at`),
		partition.Kind, partition.Key, offset, 0, nowMS(),
	)
	if err != nil {
		return fmt.Errorf("command log partition index: record tail %s/%s: %w", partition.Kind, partition.Key, err)
	}
	return nil
}

func (i *SQLCommandLogPartitionIndex) RecordCommandPartitionCommit(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := i.database()
	if err != nil {
		return err
	}
	partition = partition.Normalize()
	if offset < 0 {
		offset = 0
	}
	_, err = db.ExecContext(ctx, rebindPlaceholders(`
INSERT INTO command_log_partition_offsets (
    partition_kind, partition_key, tail_offset, committed_offset, updated_at
) VALUES (?,?,?,?,?)
ON CONFLICT(partition_kind, partition_key)
DO UPDATE SET
    committed_offset=CASE
        WHEN excluded.committed_offset > command_log_partition_offsets.committed_offset THEN excluded.committed_offset
        ELSE command_log_partition_offsets.committed_offset
    END,
    updated_at=excluded.updated_at`),
		partition.Kind, partition.Key, 0, offset, nowMS(),
	)
	if err != nil {
		return fmt.Errorf("command log partition index: record commit %s/%s: %w", partition.Kind, partition.Key, err)
	}
	return nil
}

func (i *SQLCommandLogPartitionIndex) ListCommandPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := i.database()
	if err != nil {
		return nil, err
	}
	query := `
SELECT partition_kind, partition_key
  FROM command_log_partition_offsets
 ORDER BY tail_offset DESC, partition_kind, partition_key`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, rebindPlaceholders(query), args...)
	if err != nil {
		return nil, fmt.Errorf("command log partition index: list partitions: %w", err)
	}
	defer rows.Close()
	partitions := make([]LogPartition, 0)
	for rows.Next() {
		var partition LogPartition
		if err := rows.Scan(&partition.Kind, &partition.Key); err != nil {
			return nil, err
		}
		partitions = append(partitions, partition.Normalize())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return partitions, nil
}

func (i *SQLCommandLogPartitionIndex) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := i.database()
	if err != nil {
		return nil, err
	}
	query := `
SELECT partition_kind,
       partition_key,
       tail_offset,
       CASE
           WHEN committed_offset > tail_offset THEN tail_offset
           ELSE committed_offset
       END AS committed_offset
  FROM command_log_partition_offsets
 ORDER BY (tail_offset - CASE
           WHEN committed_offset > tail_offset THEN tail_offset
           ELSE committed_offset
       END) DESC,
       tail_offset DESC,
       partition_kind,
       partition_key`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, rebindPlaceholders(query), args...)
	if err != nil {
		return nil, fmt.Errorf("command log partition index: list offsets: %w", err)
	}
	defer rows.Close()
	offsets := make([]CommandPartitionOffset, 0)
	for rows.Next() {
		var offset CommandPartitionOffset
		if err := rows.Scan(&offset.Partition.Kind, &offset.Partition.Key, &offset.TailOffset, &offset.CommittedOffset); err != nil {
			return nil, err
		}
		offsets = append(offsets, offset.Normalize())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	logmodel.SortCommandPartitionOffsetsByLag(offsets)
	return offsets, nil
}

func (i *SQLCommandLogPartitionIndex) database() (*sql.DB, error) {
	if i == nil {
		return nil, fmt.Errorf("command log partition index: nil receiver")
	}
	i.mu.RLock()
	db := i.db
	i.mu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("command log partition index: database is not bound")
	}
	return db, nil
}
