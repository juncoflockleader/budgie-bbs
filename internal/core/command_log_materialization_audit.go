package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type CommandLogMaterializationAuditConfig struct {
	PartitionLimit    int `json:"partitionLimit"`
	BatchSize         int `json:"batchSize"`
	MaxMissingSamples int `json:"maxMissingSamples"`
}

type CommandLogMaterializationAuditReport struct {
	Config                 CommandLogMaterializationAuditConfig     `json:"config"`
	StartedAt              int64                                    `json:"startedAt"`
	FinishedAt             int64                                    `json:"finishedAt"`
	Complete               bool                                     `json:"complete"`
	Partitions             int                                      `json:"partitions"`
	PartitionLimitExceeded bool                                     `json:"partitionLimitExceeded"`
	CommittedCommands      int                                      `json:"committedCommands"`
	AppliedCommands        int                                      `json:"appliedCommands"`
	TerminalFailures       int                                      `json:"terminalFailures"`
	MissingMaterialization int                                      `json:"missingMaterialization"`
	RetryingCommitted      int                                      `json:"retryingCommitted"`
	MissingRecords         int                                      `json:"missingRecords"`
	PartitionReports       []CommandLogMaterializationPartition     `json:"partitionReports,omitempty"`
	MissingSamples         []CommandLogMaterializationMissingSample `json:"missingSamples,omitempty"`
}

type CommandLogMaterializationPartition struct {
	PartitionKind          string `json:"partitionKind"`
	PartitionKey           string `json:"partitionKey"`
	TailOffset             int64  `json:"tailOffset"`
	CommittedOffset        int64  `json:"committedOffset"`
	AppliedCommands        int    `json:"appliedCommands"`
	TerminalFailures       int    `json:"terminalFailures"`
	MissingMaterialization int    `json:"missingMaterialization"`
	RetryingCommitted      int    `json:"retryingCommitted"`
	MissingRecords         int    `json:"missingRecords"`
}

type CommandLogMaterializationMissingSample struct {
	PartitionKind string `json:"partitionKind"`
	PartitionKey  string `json:"partitionKey"`
	Offset        int64  `json:"offset"`
	CommandID     string `json:"commandId,omitempty"`
	ActorID       string `json:"actorId,omitempty"`
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
}

type commandLogMaterializationReceipt struct {
	actorID string
	cid     string
	status  string
}

func DefaultCommandLogMaterializationAuditConfig() CommandLogMaterializationAuditConfig {
	return CommandLogMaterializationAuditConfig{
		PartitionLimit:    100,
		BatchSize:         100,
		MaxMissingSamples: 10,
	}
}

func (c *Core) AuditCommandLogMaterialization(ctx context.Context, commandLog CommandLog, config CommandLogMaterializationAuditConfig) (CommandLogMaterializationAuditReport, error) {
	report := CommandLogMaterializationAuditReport{
		Config:    normalizeCommandLogMaterializationAuditConfig(config),
		StartedAt: nowMS(),
		Complete:  true,
	}
	if err := ctx.Err(); err != nil {
		report.Complete = false
		report.FinishedAt = nowMS()
		return report, err
	}
	if c == nil || c.DB == nil {
		report.Complete = false
		report.FinishedAt = nowMS()
		return report, fmt.Errorf("command log materialization audit: core is not initialized")
	}
	if commandLog == nil {
		report.Complete = false
		report.FinishedAt = nowMS()
		return report, fmt.Errorf("command log materialization audit: nil command log")
	}
	lister, ok := commandLog.(CommandPartitionOffsetLister)
	if !ok {
		report.Complete = false
		report.FinishedAt = nowMS()
		return report, fmt.Errorf("command log materialization audit: command log does not expose partition offsets")
	}
	offsets, limited, err := listCommandLogPartitionOffsets(ctx, lister, report.Config.PartitionLimit)
	if err != nil {
		report.Complete = false
		report.FinishedAt = nowMS()
		return report, err
	}
	if limited {
		report.PartitionLimitExceeded = true
		report.Complete = false
	}
	report.Partitions = len(offsets)
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		partitionReport := CommandLogMaterializationPartition{
			PartitionKind:   partition.Kind,
			PartitionKey:    partition.Key,
			TailOffset:      offset.TailOffset,
			CommittedOffset: offset.CommittedOffset,
		}
		if offset.CommittedOffset <= 0 {
			report.PartitionReports = append(report.PartitionReports, partitionReport)
			continue
		}
		receipts, err := c.commandLogMaterializationReceipts(partition, offset.CommittedOffset)
		if err != nil {
			report.Complete = false
			report.FinishedAt = nowMS()
			return report, err
		}
		after := int64(0)
		expected := int64(1)
		for expected <= offset.CommittedOffset {
			records, err := commandLog.FetchPartition(ctx, partition, after, report.Config.BatchSize)
			allowCommandLogRebalance(commandLog)
			if err != nil {
				report.Complete = false
				report.FinishedAt = nowMS()
				return report, err
			}
			if len(records) == 0 {
				missing := int(offset.CommittedOffset - expected + 1)
				partitionReport.MissingRecords += missing
				report.MissingRecords += missing
				report.Complete = false
				report.addCommandLogAuditMissingSample(partition, expected, "", "", "missing_record", "committed offset has no command-log record")
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
							partitionReport.MissingRecords++
							report.MissingRecords++
							report.Complete = false
							report.addCommandLogAuditMissingSample(partition, expected, "", "", "missing_record", "committed offset has no command-log record")
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
				commandID, err := EffectiveCommandLogCID(record)
				if err != nil {
					report.Complete = false
					report.FinishedAt = nowMS()
					return report, fmt.Errorf("command log materialization audit: command id: %w", err)
				}
				status, ok := auditCommandLogRecordReceiptStatus(record, commandID, receipts[record.Offset])
				if !ok {
					status, _, err = c.auditCommandLogRecordMaterialization(record)
					if err != nil {
						report.Complete = false
						report.FinishedAt = nowMS()
						return report, err
					}
				}
				report.CommittedCommands++
				switch status {
				case CommandStatusApplied:
					report.AppliedCommands++
					partitionReport.AppliedCommands++
				case CommandStatusFailed:
					report.TerminalFailures++
					partitionReport.TerminalFailures++
				case CommandStatusRetrying:
					report.RetryingCommitted++
					partitionReport.RetryingCommitted++
					report.Complete = false
					report.addCommandLogAuditMissingSample(partition, record.Offset, commandID, record.ActorID, status, "committed command still has retrying receipt")
				default:
					report.MissingMaterialization++
					partitionReport.MissingMaterialization++
					report.Complete = false
					report.addCommandLogAuditMissingSample(partition, record.Offset, commandID, record.ActorID, status, "committed command has no applied result or terminal failure receipt")
				}
				expected = record.Offset + 1
				after = record.Offset
			}
		}
		report.PartitionReports = append(report.PartitionReports, partitionReport)
	}
	report.FinishedAt = nowMS()
	return report, nil
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

func listCommandLogPartitionOffsets(ctx context.Context, lister CommandPartitionOffsetLister, limit int) ([]CommandPartitionOffset, bool, error) {
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
		offsets[i].Partition = offsets[i].Partition.Normalize()
	}
	return offsets, limited, nil
}

func normalizeCommandLogMaterializationAuditConfig(config CommandLogMaterializationAuditConfig) CommandLogMaterializationAuditConfig {
	def := DefaultCommandLogMaterializationAuditConfig()
	if config.PartitionLimit <= 0 {
		config.PartitionLimit = def.PartitionLimit
	}
	if config.BatchSize <= 0 {
		config.BatchSize = def.BatchSize
	}
	if config.MaxMissingSamples <= 0 {
		config.MaxMissingSamples = def.MaxMissingSamples
	}
	return config
}

func (r *CommandLogMaterializationAuditReport) addCommandLogAuditMissingSample(partition LogPartition, offset int64, commandID, actorID, status, detail string) {
	if r == nil || r.Config.MaxMissingSamples == 0 || len(r.MissingSamples) >= r.Config.MaxMissingSamples {
		return
	}
	partition = partition.Normalize()
	r.MissingSamples = append(r.MissingSamples, CommandLogMaterializationMissingSample{
		PartitionKind: partition.Kind,
		PartitionKey:  partition.Key,
		Offset:        offset,
		CommandID:     commandID,
		ActorID:       actorID,
		Status:        status,
		Detail:        detail,
	})
}

func (c *Core) auditCommandLogRecordMaterialization(record CommandLogRecord) (status string, commandID string, err error) {
	commandID, err = EffectiveCommandLogCID(record)
	if err != nil {
		return "", "", fmt.Errorf("command log materialization audit: command id: %w", err)
	}
	partition := record.Partition.Normalize()
	var applied int
	err = qQueryRow(c.DB,
		`SELECT 1
		   FROM processed_commands_v2
		  WHERE partition_kind=? AND partition_key=? AND actor_id=? AND cid=?`,
		partition.Kind, partition.Key, record.ActorID, commandID,
	).Scan(&applied)
	switch {
	case err == nil:
		return CommandStatusApplied, commandID, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", commandID, err
	}
	receiptStatus, _, hasReceipt, err := c.commandLogReceipt(commandID, record.ActorID, partition, record.Offset)
	if err != nil {
		return "", commandID, err
	}
	if !hasReceipt {
		return "missing", commandID, nil
	}
	return receiptStatus, commandID, nil
}
