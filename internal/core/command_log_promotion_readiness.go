package core

import (
	"context"
	"fmt"
)

type CommandLogPromotionReadinessConfig struct {
	PartitionLimit    int `json:"partitionLimit"`
	BatchSize         int `json:"batchSize"`
	MaxLaggingSamples int `json:"maxLaggingSamples"`
	MaxMissingSamples int `json:"maxMissingSamples"`
}

type CommandLogPromotionReadinessReport struct {
	Config                 CommandLogPromotionReadinessConfig   `json:"config"`
	StartedAt              int64                                `json:"startedAt"`
	FinishedAt             int64                                `json:"finishedAt"`
	Ready                  bool                                 `json:"ready"`
	Partitions             int                                  `json:"partitions"`
	PartitionLimitExceeded bool                                 `json:"partitionLimitExceeded"`
	TailCommands           int64                                `json:"tailCommands"`
	CommittedCommands      int64                                `json:"committedCommands"`
	LaggingPartitions      int                                  `json:"laggingPartitions"`
	TotalLag               int64                                `json:"totalLag"`
	MaxLag                 int64                                `json:"maxLag"`
	LaggingSamples         []CommandLogPromotionLagSample       `json:"laggingSamples,omitempty"`
	MaterializationAudit   CommandLogMaterializationAuditReport `json:"materializationAudit"`
}

type CommandLogPromotionLagSample struct {
	PartitionKind   string `json:"partitionKind"`
	PartitionKey    string `json:"partitionKey"`
	TailOffset      int64  `json:"tailOffset"`
	CommittedOffset int64  `json:"committedOffset"`
	Lag             int64  `json:"lag"`
}

func DefaultCommandLogPromotionReadinessConfig() CommandLogPromotionReadinessConfig {
	return CommandLogPromotionReadinessConfig{
		PartitionLimit:    100,
		BatchSize:         100,
		MaxLaggingSamples: 10,
		MaxMissingSamples: 10,
	}
}

func (c *Core) CheckCommandLogPromotionReadiness(ctx context.Context, commandLog CommandLog, config CommandLogPromotionReadinessConfig) (CommandLogPromotionReadinessReport, error) {
	report := CommandLogPromotionReadinessReport{
		Config:    normalizeCommandLogPromotionReadinessConfig(config),
		StartedAt: nowMS(),
	}
	if err := ctx.Err(); err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	if commandLog == nil {
		report.FinishedAt = nowMS()
		return report, fmt.Errorf("command log promotion readiness: nil command log")
	}
	lister, ok := commandLog.(CommandPartitionOffsetLister)
	if !ok {
		report.FinishedAt = nowMS()
		return report, fmt.Errorf("command log promotion readiness: command log does not expose partition offsets")
	}
	offsets, limited, err := listCommandLogPartitionOffsets(ctx, lister, report.Config.PartitionLimit)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.PartitionLimitExceeded = limited
	report.Partitions = len(offsets)
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		report.TailCommands += offset.TailOffset
		report.CommittedCommands += offset.CommittedOffset
		lag := offset.TailOffset - offset.CommittedOffset
		if lag == 0 {
			continue
		}
		report.LaggingPartitions++
		if lag > 0 {
			report.TotalLag += lag
			if lag > report.MaxLag {
				report.MaxLag = lag
			}
		}
		report.addCommandLogPromotionLagSample(partition, offset.TailOffset, offset.CommittedOffset, lag)
	}
	report.MaterializationAudit, err = c.AuditCommandLogMaterialization(ctx, commandLog, CommandLogMaterializationAuditConfig{
		PartitionLimit:    report.Config.PartitionLimit,
		BatchSize:         report.Config.BatchSize,
		MaxMissingSamples: report.Config.MaxMissingSamples,
	})
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.Ready = !report.PartitionLimitExceeded && report.LaggingPartitions == 0 && report.MaterializationAudit.Complete
	report.FinishedAt = nowMS()
	return report, nil
}

func normalizeCommandLogPromotionReadinessConfig(config CommandLogPromotionReadinessConfig) CommandLogPromotionReadinessConfig {
	def := DefaultCommandLogPromotionReadinessConfig()
	if config.PartitionLimit <= 0 {
		config.PartitionLimit = def.PartitionLimit
	}
	if config.BatchSize <= 0 {
		config.BatchSize = def.BatchSize
	}
	if config.MaxLaggingSamples <= 0 {
		config.MaxLaggingSamples = def.MaxLaggingSamples
	}
	if config.MaxMissingSamples <= 0 {
		config.MaxMissingSamples = def.MaxMissingSamples
	}
	return config
}

func (r *CommandLogPromotionReadinessReport) addCommandLogPromotionLagSample(partition LogPartition, tailOffset, committedOffset, lag int64) {
	if r == nil || r.Config.MaxLaggingSamples == 0 || len(r.LaggingSamples) >= r.Config.MaxLaggingSamples {
		return
	}
	partition = partition.Normalize()
	r.LaggingSamples = append(r.LaggingSamples, CommandLogPromotionLagSample{
		PartitionKind:   partition.Kind,
		PartitionKey:    partition.Key,
		TailOffset:      tailOffset,
		CommittedOffset: committedOffset,
		Lag:             lag,
	})
}
