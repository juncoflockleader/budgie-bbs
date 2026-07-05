package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

var ErrCommandStatusNotFound = errors.New("command status not found")

const (
	CommandStatusPending   = "pending"
	CommandStatusApplied   = "applied"
	CommandStatusRetrying  = "retrying"
	CommandStatusFailed    = "failed"
	CommandStatusCommitted = "committed"
)

type CommandStatus struct {
	CommandID            string             `json:"commandId"`
	Status               string             `json:"status"`
	CommandPartitionKind string             `json:"commandPartitionKind"`
	CommandPartitionKey  string             `json:"commandPartitionKey"`
	CommandOffset        int64              `json:"commandOffset,omitempty"`
	CommittedOffset      int64              `json:"committedOffset,omitempty"`
	Result               *proto.AckResult   `json:"result,omitempty"`
	Error                *proto.ErrorDetail `json:"error,omitempty"`
}

func (c *Core) CommandStatus(ctx context.Context, actor *projections.User, commandID string, partition LogPartition, offset int64) (*CommandStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.DB == nil {
		return nil, fmt.Errorf("command status: core is not initialized")
	}
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return nil, ErrCommandStatusNotFound
	}
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	partition = partition.Normalize()
	status := &CommandStatus{
		CommandID:            commandID,
		Status:               CommandStatusPending,
		CommandPartitionKind: partition.Kind,
		CommandPartitionKey:  partition.Key,
		CommandOffset:        offset,
	}

	if offset <= 0 || c.commandLogAuthoritative == nil {
		return nil, ErrCommandStatusNotFound
	}
	records, err := c.commandLogAuthoritative.FetchPartition(ctx, partition, offset-1, 1)
	if err != nil {
		return nil, fmt.Errorf("command status: fetch command log: %w", err)
	}
	if len(records) == 0 || records[0].Offset != offset || records[0].ActorID != actorID {
		return nil, ErrCommandStatusNotFound
	}
	recordID, err := logmodel.EffectiveCommandLogCID(records[0])
	if err != nil {
		return nil, fmt.Errorf("command status: command log record id: %w", err)
	}
	if recordID != commandID {
		return nil, ErrCommandStatusNotFound
	}

	var raw string
	err = qQueryRow(c.DB,
		`SELECT result_json
		   FROM processed_commands_v2
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=?`,
		partition.Kind, partition.Key, actorID, commandID,
	).Scan(&raw)
	switch {
	case err == nil:
		var result proto.AckResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, fmt.Errorf("command status: decode processed result: %w", err)
		}
		committed, err := c.commandLogAuthoritative.CommittedOffset(ctx, partition)
		if err != nil {
			return nil, fmt.Errorf("command status: command log committed offset: %w", err)
		}
		status.CommittedOffset = committed
		status.Status = CommandStatusApplied
		status.Result = &result
		return status, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}

	receiptStatus, receiptErr, hasReceipt, err := c.commandLogReceipt(commandID, actorID, partition, offset)
	if err != nil {
		return nil, err
	}

	committed, err := c.commandLogAuthoritative.CommittedOffset(ctx, partition)
	if err != nil {
		return nil, fmt.Errorf("command status: command log committed offset: %w", err)
	}
	status.CommittedOffset = committed
	if hasReceipt && receiptStatus == CommandStatusFailed && committed >= offset {
		status.Status = CommandStatusFailed
		status.Error = receiptErr
		return status, nil
	}
	if committed >= offset {
		status.Status = CommandStatusCommitted
		return status, nil
	}
	if hasReceipt && receiptStatus == CommandStatusRetrying {
		status.Status = CommandStatusRetrying
		status.Error = receiptErr
	}
	return status, nil
}

func (c *Core) RecordCommandLogTerminalFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
	return c.recordCommandLogReceipt(ctx, record, CommandStatusFailed, errDetail)
}

func (c *Core) RecordCommandLogRetryableFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error {
	return c.recordCommandLogReceipt(ctx, record, CommandStatusRetrying, errDetail)
}

func (c *Core) RecordCommandLogApplied(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error {
	if result == nil {
		return nil
	}
	return c.recordCommandLogReceipt(ctx, record, CommandStatusApplied, nil)
}

func (c *Core) RecordCommandLogAppliedBatch(ctx context.Context, records []CommandLogRecord, results []*proto.AckResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.DB == nil {
		return fmt.Errorf("command log receipt: core is not initialized")
	}
	if len(records) != len(results) {
		return fmt.Errorf("command log receipt: %d records for %d results", len(records), len(results))
	}
	type receipt struct {
		partition LogPartition
		actorID   string
		commandID string
		offset    int64
	}
	receipts := make([]receipt, 0, len(records))
	for i, record := range records {
		if results[i] == nil {
			continue
		}
		commandID, err := logmodel.EffectiveCommandLogCID(record)
		if err != nil {
			return fmt.Errorf("command log receipt: command id: %w", err)
		}
		receipts = append(receipts, receipt{
			partition: record.Partition.Normalize(),
			actorID:   record.ActorID,
			commandID: commandID,
			offset:    record.Offset,
		})
	}
	if len(receipts) == 0 {
		return nil
	}
	updatedAt := nowMS()
	var query strings.Builder
	query.WriteString(`INSERT INTO command_log_receipts (
	    partition_kind, partition_key, actor_id, cid, command_offset, status, error_json, updated_at
	) VALUES `)
	args := make([]any, 0, len(receipts)*8)
	for i, receipt := range receipts {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(`(?, ?, ?, ?, ?, ?, ?, ?)`)
		args = append(args,
			receipt.partition.Kind,
			receipt.partition.Key,
			receipt.actorID,
			receipt.commandID,
			receipt.offset,
			CommandStatusApplied,
			"",
			updatedAt,
		)
	}
	query.WriteString(` ON CONFLICT (partition_kind, partition_key, actor_id, cid) DO UPDATE SET
	   command_offset=EXCLUDED.command_offset,
	   status=EXCLUDED.status,
	   error_json=EXCLUDED.error_json,
	   updated_at=EXCLUDED.updated_at`)
	_, err := qExec(c.DB, query.String(), args...)
	return err
}

func (c *Core) recordCommandLogReceipt(ctx context.Context, record CommandLogRecord, status string, errDetail *proto.ErrorDetail) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.DB == nil {
		return fmt.Errorf("command log receipt: core is not initialized")
	}
	if errDetail == nil && status != CommandStatusApplied {
		return nil
	}
	commandID, err := logmodel.EffectiveCommandLogCID(record)
	if err != nil {
		return fmt.Errorf("command log receipt: command id: %w", err)
	}
	partition := record.Partition.Normalize()
	raw := []byte{}
	if errDetail != nil {
		raw, err = json.Marshal(errDetail)
		if err != nil {
			return fmt.Errorf("command log receipt: encode error: %w", err)
		}
	}
	_, err = qExec(c.DB,
		`INSERT INTO command_log_receipts (
		    partition_kind, partition_key, actor_id, cid, command_offset, status, error_json, updated_at
		) VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT (partition_kind, partition_key, actor_id, cid) DO UPDATE SET
		   command_offset=EXCLUDED.command_offset,
		   status=EXCLUDED.status,
		   error_json=EXCLUDED.error_json,
		   updated_at=EXCLUDED.updated_at`,
		partition.Kind, partition.Key, record.ActorID, commandID, record.Offset, status, string(raw), nowMS(),
	)
	return err
}

func (c *Core) commandLogReceipt(commandID, actorID string, partition LogPartition, offset int64) (string, *proto.ErrorDetail, bool, error) {
	if c == nil || c.DB == nil || offset <= 0 {
		return "", nil, false, nil
	}
	var status, raw string
	err := qQueryRow(c.DB,
		`SELECT status, error_json
		   FROM command_log_receipts
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=? AND command_offset=?`,
		partition.Kind, partition.Key, actorID, commandID, offset,
	).Scan(&status, &raw)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return "", nil, false, nil
	default:
		return "", nil, false, err
	}
	if raw == "" {
		return status, nil, true, nil
	}
	var errDetail proto.ErrorDetail
	if err := json.Unmarshal([]byte(raw), &errDetail); err != nil {
		return "", nil, false, fmt.Errorf("command status: decode receipt: %w", err)
	}
	return status, &errDetail, true, nil
}
