package core

import (
	"context"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

func (c *Core) CheckCommandLogPromotionReadiness(ctx context.Context, commandLog CommandLog, config loadmodel.CommandLogPromotionReadinessConfig) (loadmodel.CommandLogPromotionReadinessReport, error) {
	report := loadmodel.NewCommandLogPromotionReadinessReport(config, nowMS())
	if err := ctx.Err(); err != nil {
		return commandLogPromotionReadinessError(report, err)
	}
	if commandLog == nil {
		return commandLogPromotionReadinessError(report, fmt.Errorf("command log promotion readiness: nil command log"))
	}
	offsets, limited, err := listCommandLogPartitionOffsetsWithLimit(ctx, commandLog, report.Config.PartitionLimit, "command log promotion readiness: command log does not expose partition offsets")
	if err != nil {
		return commandLogPromotionReadinessError(report, err)
	}
	loadmodel.RecordCommandLogPromotionReadinessCoverage(&report, len(offsets), limited)
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		loadmodel.RecordCommandLogPromotionPartitionOffset(&report, partition.Kind, partition.Key, offset.TailOffset, offset.CommittedOffset)
	}
	audit, err := c.auditCommandLogMaterializationFromOffsets(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{
		PartitionLimit:    report.Config.PartitionLimit,
		BatchSize:         report.Config.BatchSize,
		MaxMissingSamples: report.Config.MaxMissingSamples,
	}, offsets, limited)
	if err != nil {
		return commandLogPromotionReadinessError(report, err)
	}
	loadmodel.FinalizeCommandLogPromotionReadinessReport(&report, audit)
	loadmodel.FinishCommandLogPromotionReadinessReport(&report, nowMS())
	return report, nil
}

func commandLogPromotionReadinessError(report loadmodel.CommandLogPromotionReadinessReport, err error) (loadmodel.CommandLogPromotionReadinessReport, error) {
	loadmodel.FinishCommandLogPromotionReadinessReport(&report, nowMS())
	return report, err
}
