package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

type commandLogMaterializationReceipt struct {
	actorID string
	cid     string
	status  string
}

func (c *Core) AuditCommandLogMaterialization(ctx context.Context, commandLog CommandLog, config loadmodel.CommandLogMaterializationAuditConfig) (loadmodel.CommandLogMaterializationAuditReport, error) {
	report := loadmodel.NewCommandLogMaterializationAuditReport(config, nowMS())
	if err := c.validateCommandLogMaterializationAudit(ctx, commandLog); err != nil {
		return commandLogMaterializationAuditError(report, err)
	}
	offsets, limited, err := listCommandLogPartitionOffsetsWithLimit(ctx, commandLog, report.Config.PartitionLimit, "command log materialization audit: command log does not expose partition offsets")
	if err != nil {
		return commandLogMaterializationAuditError(report, err)
	}
	return c.auditCommandLogMaterializationOffsets(ctx, commandLog, report, offsets, limited)
}

func (c *Core) auditCommandLogMaterializationFromOffsets(ctx context.Context, commandLog CommandLog, config loadmodel.CommandLogMaterializationAuditConfig, offsets []logmodel.CommandPartitionOffset, limited bool) (loadmodel.CommandLogMaterializationAuditReport, error) {
	report := loadmodel.NewCommandLogMaterializationAuditReport(config, nowMS())
	if err := c.validateCommandLogMaterializationAudit(ctx, commandLog); err != nil {
		return commandLogMaterializationAuditError(report, err)
	}
	return c.auditCommandLogMaterializationOffsets(ctx, commandLog, report, offsets, limited)
}

func (c *Core) validateCommandLogMaterializationAudit(ctx context.Context, commandLog CommandLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.DB == nil {
		return fmt.Errorf("command log materialization audit: core is not initialized")
	}
	if commandLog == nil {
		return fmt.Errorf("command log materialization audit: nil command log")
	}
	return nil
}

func (c *Core) auditCommandLogMaterializationOffsets(ctx context.Context, commandLog CommandLog, report loadmodel.CommandLogMaterializationAuditReport, offsets []logmodel.CommandPartitionOffset, limited bool) (loadmodel.CommandLogMaterializationAuditReport, error) {
	loadmodel.RecordCommandLogMaterializationAuditCoverage(&report, len(offsets), limited)
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		partitionReport := loadmodel.CommandLogMaterializationPartition{
			PartitionKind:   partition.Kind,
			PartitionKey:    partition.Key,
			TailOffset:      offset.TailOffset,
			CommittedOffset: offset.CommittedOffset,
		}
		if offset.CommittedOffset <= 0 {
			loadmodel.AppendCommandLogMaterializationPartition(&report, partitionReport)
			continue
		}
		receipts, err := c.commandLogMaterializationReceipts(partition, offset.CommittedOffset)
		if err != nil {
			return commandLogMaterializationAuditError(report, err)
		}
		after := int64(0)
		expected := int64(1)
		for expected <= offset.CommittedOffset {
			records, err := commandLog.FetchPartition(ctx, partition, after, report.Config.BatchSize)
			allowCommandLogRebalance(commandLog)
			if err != nil {
				return commandLogMaterializationAuditError(report, err)
			}
			if len(records) == 0 {
				missing := int(offset.CommittedOffset - expected + 1)
				loadmodel.RecordCommandLogMaterializationMissingRecords(&report, &partitionReport, partition.Kind, partition.Key, expected, missing)
				break
			}
			for _, record := range records {
				if record.Offset > offset.CommittedOffset {
					expected = offset.CommittedOffset + 1
					break
				}
				if expected < record.Offset {
					if record.SourcePosition.IsZero() {
						for expected < record.Offset && expected <= offset.CommittedOffset {
							loadmodel.RecordCommandLogMaterializationMissingRecords(&report, &partitionReport, partition.Kind, partition.Key, expected, 1)
							expected++
						}
					} else {
						expected = record.Offset
					}
				}
				if record.Offset != expected {
					after = record.Offset
					continue
				}
				commandID, err := logmodel.EffectiveCommandLogCID(record)
				if err != nil {
					return commandLogMaterializationAuditError(report, fmt.Errorf("command log materialization audit: command id: %w", err))
				}
				status, ok := auditCommandLogRecordReceiptStatus(record, commandID, receipts[record.Offset])
				if !ok {
					status, err = c.auditCommandLogRecordMaterialization(record, commandID)
					if err != nil {
						return commandLogMaterializationAuditError(report, err)
					}
				}
				switch status {
				case CommandStatusApplied:
					loadmodel.RecordCommandLogMaterializationApplied(&report, &partitionReport)
				case CommandStatusFailed:
					loadmodel.RecordCommandLogMaterializationTerminalFailure(&report, &partitionReport)
				case CommandStatusRetrying:
					loadmodel.RecordCommandLogMaterializationIncomplete(&report, &partitionReport, partition.Kind, partition.Key, record.Offset, commandID, record.ActorID, status, true)
				default:
					loadmodel.RecordCommandLogMaterializationIncomplete(&report, &partitionReport, partition.Kind, partition.Key, record.Offset, commandID, record.ActorID, status, false)
				}
				expected = record.Offset + 1
				after = record.Offset
			}
		}
		loadmodel.AppendCommandLogMaterializationPartition(&report, partitionReport)
	}
	loadmodel.FinishCommandLogMaterializationAuditReport(&report, nowMS())
	return report, nil
}

func commandLogMaterializationAuditError(report loadmodel.CommandLogMaterializationAuditReport, err error) (loadmodel.CommandLogMaterializationAuditReport, error) {
	loadmodel.FailCommandLogMaterializationAuditReport(&report, nowMS())
	return report, err
}

func (c *Core) commandLogMaterializationReceipts(partition LogPartition, maxOffset int64) (map[int64]commandLogMaterializationReceipt, error) {
	receipts := map[int64]commandLogMaterializationReceipt{}
	if c == nil || c.DB == nil || maxOffset <= 0 {
		return receipts, nil
	}
	partition = partition.Normalize()
	rows, err := qQuery(c.DB,
		`SELECT command_offset, actor_id, cid, status
		   FROM command_log_receipts
		  WHERE partition_kind=? AND partition_key=? AND command_offset > 0 AND command_offset <= ?`,
		partition.Kind, partition.Key, maxOffset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var offset int64
		var receipt commandLogMaterializationReceipt
		if err := rows.Scan(&offset, &receipt.actorID, &receipt.cid, &receipt.status); err != nil {
			return nil, err
		}
		if offset > 0 {
			receipts[offset] = receipt
		}
	}
	return receipts, rows.Err()
}

func auditCommandLogRecordReceiptStatus(record CommandLogRecord, commandID string, receipt commandLogMaterializationReceipt) (string, bool) {
	if receipt.status == "" || receipt.actorID != record.ActorID || receipt.cid != commandID {
		return "", false
	}
	return receipt.status, true
}

func (c *Core) auditCommandLogRecordMaterialization(record CommandLogRecord, commandID string) (string, error) {
	partition := record.Partition.Normalize()
	var applied int
	err := qQueryRow(c.DB,
		`SELECT 1
		   FROM processed_commands_v2
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=?`,
		partition.Kind, partition.Key, record.ActorID, commandID,
	).Scan(&applied)
	switch {
	case err == nil:
		return CommandStatusApplied, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", err
	}
	receiptStatus, _, hasReceipt, err := c.commandLogReceipt(commandID, record.ActorID, partition, record.Offset)
	if err != nil {
		return "", err
	}
	if !hasReceipt {
		return "missing", nil
	}
	return receiptStatus, nil
}
