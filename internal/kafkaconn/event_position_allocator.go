package kafkaconn

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

const brokerEventScalarOffsetID = "broker_event_log"

type SQLEventPositionAllocatorOptions struct {
	Flavor                  int
	DisableCompatibilitySeq bool
}

// SQLEventPositionAllocator reserves logical event positions in SQL before a
// Kafka/Redpanda transaction appends the corresponding broker records.
type SQLEventPositionAllocator struct {
	db      *sql.DB
	options SQLEventPositionAllocatorOptions
}

var _ EventPositionAllocator = (*SQLEventPositionAllocator)(nil)
var _ core.EventPartitionLister = (*SQLEventPositionAllocator)(nil)
var _ core.EventPartitionOffsetLister = (*SQLEventPositionAllocator)(nil)
var _ EventLogHeadReader = (*SQLEventPositionAllocator)(nil)

func NewSQLEventPositionAllocator(db *sql.DB, options SQLEventPositionAllocatorOptions) *SQLEventPositionAllocator {
	if options.Flavor == 0 {
		options.Flavor = core.SQLFlavor()
	}
	return &SQLEventPositionAllocator{db: db, options: options}
}

func (a *SQLEventPositionAllocator) AllocateEventPositions(ctx context.Context, records []core.BrokerEventRecord) ([]EventPositionAllocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("sql event position allocator: nil db")
	}
	if len(records) == 0 {
		return nil, nil
	}

	normalized := make([]core.LogPartition, 0, len(records))
	counts := map[core.LogPartition]int{}
	for _, record := range records {
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		normalized = append(normalized, partition)
		counts[partition]++
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var seqBase int64
	if !a.options.DisableCompatibilitySeq {
		seqBase, err = a.reserveScalarOffsets(ctx, tx, len(records))
		if err != nil {
			return nil, err
		}
	}
	partitionBases := make(map[core.LogPartition]int64, len(counts))
	for partition, count := range counts {
		base, err := a.reservePartitionOffsets(ctx, tx, partition, count)
		if err != nil {
			return nil, err
		}
		partitionBases[partition] = base
	}

	seen := map[core.LogPartition]int64{}
	allocations := make([]EventPositionAllocation, 0, len(records))
	for i, record := range records {
		partition := normalized[i]
		seen[partition]++
		var compatibilitySeq int64
		if a.options.DisableCompatibilitySeq {
			if record.CompatibilitySeq > 0 {
				return nil, fmt.Errorf("sql event position allocator: requested event %d scalar sequence %d while scalar compatibility allocation is disabled",
					i, record.CompatibilitySeq)
			}
		} else {
			compatibilitySeq = seqBase + int64(i) + 1
			if record.CompatibilitySeq > 0 && record.CompatibilitySeq != compatibilitySeq {
				return nil, fmt.Errorf("sql event position allocator: requested event %d scalar sequence %d does not match next reserved sequence %d",
					i, record.CompatibilitySeq, compatibilitySeq)
			}
		}
		allocations = append(allocations, EventPositionAllocation{
			Partition:        partition,
			PartitionOffset:  partitionBases[partition] + seen[partition],
			CompatibilitySeq: compatibilitySeq,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return allocations, nil
}

func (a *SQLEventPositionAllocator) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("sql event position allocator: nil db")
	}
	if a.options.DisableCompatibilitySeq {
		return 0, fmt.Errorf("sql event position allocator: scalar head disabled")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := a.seedScalarOffset(ctx, tx); err != nil {
		return 0, err
	}
	var head int64
	if err := a.queryRow(ctx, tx,
		`SELECT last_seq
		   FROM event_scalar_offsets
		  WHERE id=?`,
		brokerEventScalarOffsetID,
	).Scan(&head); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return head, nil
}

func (a *SQLEventPositionAllocator) ListEventPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	offsets, err := a.ListEventPartitionOffsets(ctx, limit)
	if err != nil {
		return nil, err
	}
	partitions := make([]core.LogPartition, 0, len(offsets))
	for _, offset := range offsets {
		partitions = append(partitions, offset.Partition)
	}
	return partitions, nil
}

func (a *SQLEventPositionAllocator) ListEventPartitionOffsets(ctx context.Context, limit int) ([]core.EventPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("sql event position allocator: nil db")
	}
	query := `SELECT partition_kind, partition_key, last_offset
	            FROM event_partition_offsets
	           WHERE last_offset > 0
	           ORDER BY last_offset DESC, partition_kind, partition_key`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := a.db.QueryContext(ctx, a.rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offsets := []core.EventPartitionOffset{}
	for rows.Next() {
		var kind, key string
		var offset int64
		if err := rows.Scan(&kind, &key, &offset); err != nil {
			return nil, err
		}
		offsets = append(offsets, core.EventPartitionOffset{
			Partition:  core.LogPartition{Kind: kind, Key: key}.Normalize(),
			LastOffset: offset,
		})
	}
	return offsets, rows.Err()
}

func (a *SQLEventPositionAllocator) reserveScalarOffsets(ctx context.Context, tx *sql.Tx, count int) (int64, error) {
	if count <= 0 {
		return 0, nil
	}
	if err := a.seedScalarOffset(ctx, tx); err != nil {
		return 0, err
	}
	if _, err := a.exec(ctx, tx,
		`UPDATE event_scalar_offsets
		    SET last_seq=last_seq+?
		  WHERE id=?`,
		count, brokerEventScalarOffsetID,
	); err != nil {
		return 0, err
	}
	var final int64
	if err := a.queryRow(ctx, tx,
		`SELECT last_seq
		   FROM event_scalar_offsets
		  WHERE id=?`,
		brokerEventScalarOffsetID,
	).Scan(&final); err != nil {
		return 0, err
	}
	return final - int64(count), nil
}

func (a *SQLEventPositionAllocator) seedScalarOffset(ctx context.Context, tx *sql.Tx) error {
	if a.isPostgres() {
		if _, err := a.exec(ctx, tx,
			`INSERT INTO event_scalar_offsets (id, last_seq)
			   SELECT ?, COALESCE(MAX(seq), 0) FROM events
			  ON CONFLICT (id) DO NOTHING`,
			brokerEventScalarOffsetID,
		); err != nil {
			return err
		}
		_, err := a.exec(ctx, tx,
			`UPDATE event_scalar_offsets
			    SET last_seq=(SELECT COALESCE(MAX(seq), 0) FROM events)
			  WHERE id=? AND last_seq < (SELECT COALESCE(MAX(seq), 0) FROM events)`,
			brokerEventScalarOffsetID,
		)
		return err
	}
	if _, err := a.exec(ctx, tx,
		`INSERT OR IGNORE INTO event_scalar_offsets (id, last_seq)
		   SELECT ?, COALESCE(MAX(seq), 0) FROM events`,
		brokerEventScalarOffsetID,
	); err != nil {
		return err
	}
	_, err := a.exec(ctx, tx,
		`UPDATE event_scalar_offsets
		    SET last_seq=(SELECT COALESCE(MAX(seq), 0) FROM events)
		  WHERE id=? AND last_seq < (SELECT COALESCE(MAX(seq), 0) FROM events)`,
		brokerEventScalarOffsetID,
	)
	return err
}

func (a *SQLEventPositionAllocator) reservePartitionOffsets(ctx context.Context, tx *sql.Tx, partition core.LogPartition, count int) (int64, error) {
	if count <= 0 {
		return 0, nil
	}
	partition = partition.Normalize()
	if err := a.seedPartitionOffset(ctx, tx, partition); err != nil {
		return 0, err
	}
	if _, err := a.exec(ctx, tx,
		`UPDATE event_partition_offsets
		    SET last_offset=last_offset+?
		  WHERE partition_kind=? AND partition_key=?`,
		count, partition.Kind, partition.Key,
	); err != nil {
		return 0, err
	}
	var final int64
	if err := a.queryRow(ctx, tx,
		`SELECT last_offset
		   FROM event_partition_offsets
		  WHERE partition_kind=? AND partition_key=?`,
		partition.Kind, partition.Key,
	).Scan(&final); err != nil {
		return 0, err
	}
	return final - int64(count), nil
}

func (a *SQLEventPositionAllocator) seedPartitionOffset(ctx context.Context, tx *sql.Tx, partition core.LogPartition) error {
	if a.isPostgres() {
		_, err := a.exec(ctx, tx,
			`INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
			 VALUES (?,?,0)
			 ON CONFLICT (partition_kind, partition_key) DO NOTHING`,
			partition.Kind, partition.Key,
		)
		return err
	}
	_, err := a.exec(ctx, tx,
		`INSERT OR IGNORE INTO event_partition_offsets (partition_kind, partition_key, last_offset)
		 VALUES (?,?,0)`,
		partition.Kind, partition.Key,
	)
	return err
}

func (a *SQLEventPositionAllocator) exec(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, a.rebind(query), args...)
}

func (a *SQLEventPositionAllocator) queryRow(ctx context.Context, tx *sql.Tx, query string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, a.rebind(query), args...)
}

func (a *SQLEventPositionAllocator) rebind(query string) string {
	if !a.isPostgres() {
		return query
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteString("$")
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func (a *SQLEventPositionAllocator) isPostgres() bool {
	return a.options.Flavor == core.PostgresFlavor()
}
