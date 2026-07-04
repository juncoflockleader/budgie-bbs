package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultCommandPartitionClaimTTL = 30 * time.Second

// SQLCommandPartitionClaimer stores short-lived command-partition leases in the
// SQL database. It is an interim ownership boundary while IS4 still drains a
// broker command log through the SQL-backed executor.
type SQLCommandPartitionClaimer struct {
	db  *sql.DB
	now func() int64
}

func NewSQLCommandPartitionClaimer(db *sql.DB) *SQLCommandPartitionClaimer {
	return &SQLCommandPartitionClaimer{
		db:  db,
		now: nowMS,
	}
}

func (c *SQLCommandPartitionClaimer) ClaimCommandPartition(ctx context.Context, ownerID string, partition LogPartition, ttl time.Duration) (CommandPartitionClaim, bool, error) {
	if err := ctx.Err(); err != nil {
		return CommandPartitionClaim{}, false, err
	}
	if c == nil || c.db == nil {
		return CommandPartitionClaim{}, false, fmt.Errorf("sql command partition claimer: nil db")
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return CommandPartitionClaim{}, false, fmt.Errorf("sql command partition claimer: missing owner id")
	}
	if ttl <= 0 {
		ttl = defaultCommandPartitionClaimTTL
	}
	ttlMS := ttl.Milliseconds()
	if ttlMS <= 0 {
		ttlMS = 1
	}
	now := nowMS()
	if c.now != nil {
		now = c.now()
	}
	expiresAt := now + ttlMS
	partition = partition.Normalize()

	query := `
INSERT INTO command_partition_leases (
    partition_kind, partition_key, owner_id, claimed_at, expires_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (partition_kind, partition_key) DO UPDATE
   SET owner_id=excluded.owner_id,
       claimed_at=excluded.claimed_at,
       expires_at=excluded.expires_at
 WHERE command_partition_leases.owner_id=excluded.owner_id
    OR command_partition_leases.expires_at <= excluded.claimed_at`
	if _, err := c.db.ExecContext(ctx, rebindPlaceholders(query), partition.Kind, partition.Key, ownerID, now, expiresAt); err != nil {
		return CommandPartitionClaim{}, false, err
	}

	var claimOwner string
	var claimExpiresAt int64
	if err := c.db.QueryRowContext(ctx, rebindPlaceholders(`
SELECT owner_id, expires_at
  FROM command_partition_leases
 WHERE partition_kind=? AND partition_key=?`), partition.Kind, partition.Key).Scan(&claimOwner, &claimExpiresAt); err != nil {
		return CommandPartitionClaim{}, false, err
	}
	claim := commandPartitionClaimForOwner(partition, claimOwner, claimExpiresAt)
	return claim, claim.OwnerID == ownerID && claim.ExpiresAt >= expiresAt, nil
}

func commandPartitionClaimForOwner(partition LogPartition, ownerID string, expiresAt int64) CommandPartitionClaim {
	return CommandPartitionClaim{
		Partition: partition.Normalize(),
		OwnerID:   strings.TrimSpace(ownerID),
		ExpiresAt: expiresAt,
	}
}
