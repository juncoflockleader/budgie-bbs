package loadmodel

import (
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

type CommandLogDrainLoadReport struct {
	Config                     CommandLogDrainLoadConfig            `json:"config"`
	Runtime                    CommandLogDrainLoadRuntime           `json:"runtime"`
	Evidence                   runevidence.Evidence                 `json:"evidence"`
	StartedAt                  int64                                `json:"startedAt"`
	FinishedAt                 int64                                `json:"finishedAt"`
	TotalCommands              int                                  `json:"totalCommands"`
	Partitions                 int                                  `json:"partitions"`
	Submit                     CommandLogLoadStage                  `json:"submit"`
	Drain                      CommandLogDrainStage                 `json:"drain"`
	EventProjection            EventStoreProjectionLoadStage        `json:"eventProjection"`
	MaxPartitionLagBeforeDrain int64                                `json:"maxPartitionLagBeforeDrain"`
	MaxPartitionLagAfterDrain  int64                                `json:"maxPartitionLagAfterDrain"`
	PromotionReadiness         CommandLogPromotionReadinessReport   `json:"promotionReadiness"`
	MaterializationAudit       CommandLogMaterializationAuditReport `json:"materializationAudit"`
	ScalarCompatibilityAudit   CommandLogScalarCompatibilityAudit   `json:"scalarCompatibilityAudit"`
}

func NewCommandLogDrainLoadReport(config CommandLogDrainLoadConfig, startedAt int64) CommandLogDrainLoadReport {
	report := CommandLogDrainLoadReport{
		Config:        config,
		StartedAt:     startedAt,
		TotalCommands: CommandLogDrainLoadTotalCommands(config),
		Partitions:    config.Boards,
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative {
		report.EventProjection.ExpectedEvents = CommandLogDrainLoadExpectedEventProjectionEvents(config)
	}
	return report
}

func FinishCommandLogDrainLoadReport(report *CommandLogDrainLoadReport, finishedAt int64) {
	if report != nil {
		report.FinishedAt = finishedAt
	}
}

func FinalizeCommandLogDrainLoadReport(report *CommandLogDrainLoadReport, readiness CommandLogPromotionReadinessReport, finishedAt int64) {
	if report != nil {
		report.PromotionReadiness = readiness
		report.MaterializationAudit = readiness.MaterializationAudit
		FinishCommandLogDrainLoadReport(report, finishedAt)
	}
}

func RecordCommandLogDrainLoadSubmit(report *CommandLogDrainLoadReport, submit CommandLogLoadStage) {
	if report == nil {
		return
	}
	report.Submit.Commands += submit.Commands
	report.Submit.Succeeded += submit.Succeeded
	report.Submit.Failed += submit.Failed
	report.Submit.DurationMS += submit.DurationMS
	report.Submit.SampleErrorText = mergeLoadSamples(report.Submit.SampleErrorText, submit.SampleErrorText)
	report.Submit.CommandsPerSec = mergedPerSecond(report.Submit.Succeeded, report.Submit.DurationMS, report.Submit.CommandsPerSec, submit.CommandsPerSec)
}

func RecordCommandLogDrainLoadBeforeDrainLag(report *CommandLogDrainLoadReport, lag int64) {
	if report != nil {
		report.MaxPartitionLagBeforeDrain = max(report.MaxPartitionLagBeforeDrain, lag)
	}
}

func RecordCommandLogDrainLoadDrain(report *CommandLogDrainLoadReport, drain CommandLogDrainStage) {
	if report == nil {
		return
	}
	report.Drain.Commands += drain.Commands
	report.Drain.Processed += drain.Processed
	report.Drain.Applied += drain.Applied
	report.Drain.TerminalFailures += drain.TerminalFailures
	report.Drain.RetryableFailures += drain.RetryableFailures
	report.Drain.CommitFailures += drain.CommitFailures
	report.Drain.AssignmentLosses += drain.AssignmentLosses
	report.Drain.ClaimLosses += drain.ClaimLosses
	report.Drain.Rounds += drain.Rounds
	report.Drain.DurationMS += drain.DurationMS
	report.Drain.SampleErrorText = mergeLoadSamples(report.Drain.SampleErrorText, drain.SampleErrorText)
	report.Drain.CommandsPerSec = mergedPerSecond(report.Drain.Applied, report.Drain.DurationMS, report.Drain.CommandsPerSec, drain.CommandsPerSec)
}

func RecordCommandLogDrainLoadAfterDrainLag(report *CommandLogDrainLoadReport, lag int64) {
	if report != nil {
		report.MaxPartitionLagAfterDrain = lag
	}
}

func RecordCommandLogDrainLoadEventProjection(report *CommandLogDrainLoadReport, projection EventStoreProjectionLoadStage) {
	if report == nil {
		return
	}
	report.EventProjection.Enabled = report.EventProjection.Enabled || projection.Enabled
	report.EventProjection.Partitions = max(report.EventProjection.Partitions, projection.Partitions)
	report.EventProjection.PartitionLimit = max(report.EventProjection.PartitionLimit, projection.PartitionLimit)
	report.EventProjection.PartitionLimitExceeded = report.EventProjection.PartitionLimitExceeded || projection.PartitionLimitExceeded
	report.EventProjection.AppliedEvents += projection.AppliedEvents
	report.EventProjection.Rounds += projection.Rounds
	report.EventProjection.DurationMS += projection.DurationMS
	report.EventProjection.SampleErrorText = mergeLoadSamples(report.EventProjection.SampleErrorText, projection.SampleErrorText)
	report.EventProjection.EventsPerSec = mergedPerSecond(report.EventProjection.AppliedEvents, report.EventProjection.DurationMS, report.EventProjection.EventsPerSec, projection.EventsPerSec)
}

func ValidateCommandLogDrainLoadReport(report CommandLogDrainLoadReport) error {
	if report.Submit.Failed > 0 {
		return fmt.Errorf("command log drain load: command production failed %d/%d", report.Submit.Failed, report.Submit.Commands)
	}
	if report.Drain.Applied != report.TotalCommands || report.MaxPartitionLagAfterDrain != 0 {
		return fmt.Errorf("command log drain load: applied %d/%d with max lag %d",
			report.Drain.Applied, report.TotalCommands, report.MaxPartitionLagAfterDrain)
	}
	if report.Drain.TerminalFailures+report.Drain.RetryableFailures+report.Drain.CommitFailures > 0 {
		return fmt.Errorf("command log drain load: drain failures terminal=%d retryable=%d commit=%d",
			report.Drain.TerminalFailures, report.Drain.RetryableFailures, report.Drain.CommitFailures)
	}
	if report.Config.ExecutorMode == CommandLogDrainExecutorNative && report.EventProjection.AppliedEvents != report.EventProjection.ExpectedEvents {
		return fmt.Errorf("command log drain load: projected %d broker events, want %d for %d native commands",
			report.EventProjection.AppliedEvents, report.EventProjection.ExpectedEvents, report.TotalCommands)
	}
	if !report.PromotionReadiness.Ready {
		return fmt.Errorf("command log drain load: promotion readiness failed lagging=%d totalLag=%d missing=%d retrying=%d missingRecords=%d",
			report.PromotionReadiness.LaggingPartitions,
			report.PromotionReadiness.TotalLag,
			report.MaterializationAudit.MissingMaterialization,
			report.MaterializationAudit.RetryingCommitted,
			report.MaterializationAudit.MissingRecords)
	}
	if !report.MaterializationAudit.Complete {
		return fmt.Errorf("command log drain load: materialization audit incomplete missing=%d retrying=%d missingRecords=%d",
			report.MaterializationAudit.MissingMaterialization,
			report.MaterializationAudit.RetryingCommitted,
			report.MaterializationAudit.MissingRecords)
	}
	return nil
}

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

func defaultCommandLogPromotionReadinessConfig() CommandLogPromotionReadinessConfig {
	return CommandLogPromotionReadinessConfig{
		PartitionLimit:    100,
		BatchSize:         100,
		MaxLaggingSamples: 10,
		MaxMissingSamples: 10,
	}
}

func NewCommandLogPromotionReadinessReport(config CommandLogPromotionReadinessConfig, startedAt int64) CommandLogPromotionReadinessReport {
	def := defaultCommandLogPromotionReadinessConfig()
	config.PartitionLimit = positiveOrDefault(config.PartitionLimit, def.PartitionLimit)
	config.BatchSize = positiveOrDefault(config.BatchSize, def.BatchSize)
	config.MaxLaggingSamples = positiveOrDefault(config.MaxLaggingSamples, def.MaxLaggingSamples)
	config.MaxMissingSamples = positiveOrDefault(config.MaxMissingSamples, def.MaxMissingSamples)
	return CommandLogPromotionReadinessReport{
		Config:    config,
		StartedAt: startedAt,
	}
}

func RecordCommandLogPromotionReadinessCoverage(report *CommandLogPromotionReadinessReport, partitions int, limited bool) {
	if report != nil {
		report.Partitions = partitions
		report.PartitionLimitExceeded = limited
	}
}

func RecordCommandLogPromotionPartitionOffset(report *CommandLogPromotionReadinessReport, partitionKind, partitionKey string, tailOffset, committedOffset int64) {
	if report == nil {
		return
	}
	report.TailCommands += tailOffset
	report.CommittedCommands += committedOffset
	lag := tailOffset - committedOffset
	if lag == 0 {
		return
	}
	report.LaggingPartitions++
	if lag > 0 {
		report.TotalLag += lag
		report.MaxLag = max(report.MaxLag, lag)
	}
	if report.Config.MaxLaggingSamples != 0 && len(report.LaggingSamples) < report.Config.MaxLaggingSamples {
		report.LaggingSamples = append(report.LaggingSamples, CommandLogPromotionLagSample{
			PartitionKind:   partitionKind,
			PartitionKey:    partitionKey,
			TailOffset:      tailOffset,
			CommittedOffset: committedOffset,
			Lag:             lag,
		})
	}
}

func FinalizeCommandLogPromotionReadinessReport(report *CommandLogPromotionReadinessReport, audit CommandLogMaterializationAuditReport) {
	if report != nil {
		report.MaterializationAudit = audit
		report.Ready = !report.PartitionLimitExceeded && report.LaggingPartitions == 0 && audit.Complete
	}
}

func FinishCommandLogPromotionReadinessReport(report *CommandLogPromotionReadinessReport, finishedAt int64) {
	if report != nil {
		report.FinishedAt = finishedAt
	}
}

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

func defaultCommandLogMaterializationAuditConfig() CommandLogMaterializationAuditConfig {
	return CommandLogMaterializationAuditConfig{
		PartitionLimit:    100,
		BatchSize:         100,
		MaxMissingSamples: 10,
	}
}

func NewCommandLogMaterializationAuditReport(config CommandLogMaterializationAuditConfig, startedAt int64) CommandLogMaterializationAuditReport {
	def := defaultCommandLogMaterializationAuditConfig()
	config.PartitionLimit = positiveOrDefault(config.PartitionLimit, def.PartitionLimit)
	config.BatchSize = positiveOrDefault(config.BatchSize, def.BatchSize)
	config.MaxMissingSamples = positiveOrDefault(config.MaxMissingSamples, def.MaxMissingSamples)
	return CommandLogMaterializationAuditReport{
		Config:    config,
		StartedAt: startedAt,
		Complete:  true,
	}
}

func RecordCommandLogMaterializationAuditCoverage(report *CommandLogMaterializationAuditReport, partitions int, limited bool) {
	if report == nil {
		return
	}
	report.Partitions = partitions
	report.PartitionLimitExceeded = limited
	if limited {
		report.Complete = false
	}
}

func AppendCommandLogMaterializationPartition(report *CommandLogMaterializationAuditReport, partitionReport CommandLogMaterializationPartition) {
	if report != nil {
		report.PartitionReports = append(report.PartitionReports, partitionReport)
	}
}

func FinishCommandLogMaterializationAuditReport(report *CommandLogMaterializationAuditReport, finishedAt int64) {
	if report != nil {
		report.FinishedAt = finishedAt
	}
}

func FailCommandLogMaterializationAuditReport(report *CommandLogMaterializationAuditReport, finishedAt int64) {
	if report != nil {
		report.Complete = false
		FinishCommandLogMaterializationAuditReport(report, finishedAt)
	}
}

func addCommandLogMaterializationMissingSample(report *CommandLogMaterializationAuditReport, partitionKind, partitionKey string, offset int64, commandID, actorID, status, detail string) {
	if report == nil || report.Config.MaxMissingSamples == 0 || len(report.MissingSamples) >= report.Config.MaxMissingSamples {
		return
	}
	report.MissingSamples = append(report.MissingSamples, CommandLogMaterializationMissingSample{
		PartitionKind: partitionKind,
		PartitionKey:  partitionKey,
		Offset:        offset,
		CommandID:     commandID,
		ActorID:       actorID,
		Status:        status,
		Detail:        detail,
	})
}

func RecordCommandLogMaterializationMissingRecords(report *CommandLogMaterializationAuditReport, partitionReport *CommandLogMaterializationPartition, partitionKind, partitionKey string, offset int64, count int) {
	if report == nil || partitionReport == nil || count <= 0 {
		return
	}
	partitionReport.MissingRecords += count
	report.MissingRecords += count
	report.Complete = false
	addCommandLogMaterializationMissingSample(report, partitionKind, partitionKey, offset, "", "", "missing_record", "committed offset has no command-log record")
}

func RecordCommandLogMaterializationApplied(report *CommandLogMaterializationAuditReport, partitionReport *CommandLogMaterializationPartition) {
	if report == nil || partitionReport == nil {
		return
	}
	report.CommittedCommands++
	report.AppliedCommands++
	partitionReport.AppliedCommands++
}

func RecordCommandLogMaterializationTerminalFailure(report *CommandLogMaterializationAuditReport, partitionReport *CommandLogMaterializationPartition) {
	if report == nil || partitionReport == nil {
		return
	}
	report.CommittedCommands++
	report.TerminalFailures++
	partitionReport.TerminalFailures++
}

func RecordCommandLogMaterializationIncomplete(report *CommandLogMaterializationAuditReport, partitionReport *CommandLogMaterializationPartition, partitionKind, partitionKey string, offset int64, commandID, actorID, status string, retrying bool) {
	if report == nil || partitionReport == nil {
		return
	}
	report.CommittedCommands++
	detail := "committed command has no applied result or terminal failure receipt"
	if retrying {
		report.RetryingCommitted++
		partitionReport.RetryingCommitted++
		detail = "committed command still has retrying receipt"
	} else {
		report.MissingMaterialization++
		partitionReport.MissingMaterialization++
	}
	report.Complete = false
	addCommandLogMaterializationMissingSample(report, partitionKind, partitionKey, offset, commandID, actorID, status, detail)
}

func CommandLogDrainLoadExpectedEventProjectionEvents(config CommandLogDrainLoadConfig) int {
	return CommandLogDrainLoadCreateThreadCommands(config)*2 + CommandLogDrainLoadAppendPostCommands(config)
}

func CommandLogDrainLoadCreateThreadCommands(config CommandLogDrainLoadConfig) int {
	return config.Boards * config.CommandsPerBoard
}

func CommandLogDrainLoadAppendPostCommands(config CommandLogDrainLoadConfig) int {
	return CommandLogDrainLoadCreateThreadCommands(config) * config.RepliesPerThread
}

func CommandLogDrainLoadTotalCommands(config CommandLogDrainLoadConfig) int {
	return CommandLogDrainLoadCreateThreadCommands(config) + CommandLogDrainLoadAppendPostCommands(config)
}

func CommandLogDrainLoadCommandPartitionLimit(config CommandLogDrainLoadConfig) int {
	partitions := config.Boards
	if config.RepliesPerThread > 0 {
		partitions += CommandLogDrainLoadCreateThreadCommands(config)
	}
	if partitions <= 0 {
		return 1
	}
	return partitions
}

func CommandLogDrainLoadEventProjectionSource(config CommandLogDrainLoadConfig) string {
	return "command-log-drain-load:" + NormalizeCommandLogExecutor(config.ExecutorMode)
}

func mergedPerSecond(count int, durationMS int64, current, incoming float64) float64 {
	if durationMS > 0 {
		return float64(count) / (float64(durationMS) / 1000)
	}
	return max(current, incoming)
}

const maxLoadSamples = 5

func mergeLoadSamples(dst, src []string) []string {
	if len(src) == 0 || len(dst) >= maxLoadSamples {
		return dst
	}
	seen := map[string]bool{}
	for _, text := range dst {
		seen[text] = true
	}
	for _, text := range src {
		AddLoadSample(&dst, seen, text)
	}
	return dst
}

func AddLoadSample(samples *[]string, seen map[string]bool, text string) {
	if samples == nil || text == "" || len(*samples) >= maxLoadSamples {
		return
	}
	if seen != nil && seen[text] {
		return
	}
	for _, existing := range *samples {
		if existing == text {
			if seen != nil {
				seen[text] = true
			}
			return
		}
	}
	*samples = append(*samples, text)
	if seen != nil {
		seen[text] = true
	}
}

func AddCommandLogDrainSample(stage *CommandLogDrainStage, seen map[string]bool, text string) {
	if stage == nil {
		return
	}
	AddLoadSample(&stage.SampleErrorText, seen, text)
}

func AddCommandLogDrainPartitionSample(stage *CommandLogDrainStage, seen map[string]bool, partitionKind, partitionKey, reason, detail string) {
	text := fmt.Sprintf("%s/%s %s", partitionKind, partitionKey, reason)
	if detail != "" {
		text = fmt.Sprintf("%s: %s", text, detail)
	}
	AddCommandLogDrainSample(stage, seen, text)
}
