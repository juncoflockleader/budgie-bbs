package loadmodel

import (
	"reflect"
	"strings"
	"testing"
)

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func TestDefaultCommandLogDrainLoadConfig(t *testing.T) {
	got := DefaultCommandLogDrainLoadConfig()
	if got.Boards != 8 ||
		got.CommandsPerBoard != 50 ||
		got.SubmitConcurrency != 32 ||
		got.Writers != 4 ||
		got.BatchSize != 25 ||
		got.AssignmentMode != CommandLogDrainAssignmentHash ||
		got.ExecutorMode != CommandLogDrainExecutorSQL {
		t.Fatalf("default command-log drain config = %+v", got)
	}
}

func TestNormalizeCommandLogDrainScalarAllocator(t *testing.T) {
	requireStringResults(t, "NormalizeCommandLogDrainScalarAllocator", NormalizeCommandLogDrainScalarAllocator, map[string]string{
		"":                           "",
		"broker":                     CommandLogDrainScalarAllocatorBrokerStreamSequence,
		"broker-stream":              CommandLogDrainScalarAllocatorBrokerStreamSequence,
		"memory":                     CommandLogDrainScalarAllocatorMemoryStreamSequence,
		"postgres-seq":               CommandLogDrainScalarAllocatorPostgresEventSeq,
		"partition-only":             CommandLogDrainScalarAllocatorSQLEventPartitions,
		"sql-event-partition-offset": CommandLogDrainScalarAllocatorSQLEventPartitions,
		"sql-scalar":                 CommandLogDrainScalarAllocatorSQLEventOffsets,
		"sql-event-scalar-offset":    CommandLogDrainScalarAllocatorSQLEventOffsets,
		"global-sequence-service":    "global-sequence-service",
	})
}

func TestNormalizeCommandLogExecutor(t *testing.T) {
	requireStringResults(t, "NormalizeCommandLogExecutor", NormalizeCommandLogExecutor, map[string]string{
		"":                  CommandLogDrainExecutorSQL,
		"Postgres":          CommandLogDrainExecutorSQL,
		"broker-native":     CommandLogDrainExecutorNative,
		"event-transaction": CommandLogDrainExecutorNative,
		"wasm":              "wasm",
	})
}

func TestNormalizeCommandLogDrainLoadConfig(t *testing.T) {
	got := NormalizeCommandLogDrainLoadConfig(CommandLogDrainLoadConfig{
		Boards:               2,
		CommandsPerBoard:     3,
		RepliesPerThread:     4,
		SubmitConcurrency:    99,
		Writers:              99,
		BatchSize:            0,
		PartitionConcurrency: 0,
		AssignmentMode:       "snapshot",
		ExecutorMode:         "broker-native",
	})
	if got.BatchSize != 25 || got.PartitionConcurrency != 1 {
		t.Fatalf("normalized config = %+v, want defaults filled", got)
	}
	if got.SubmitConcurrency != CommandLogDrainLoadTotalCommands(got) {
		t.Fatalf("submit concurrency = %d, want total commands %d", got.SubmitConcurrency, CommandLogDrainLoadTotalCommands(got))
	}
	if got.Writers != CommandLogDrainLoadCommandPartitionLimit(got) {
		t.Fatalf("writers = %d, want partition limit %d", got.Writers, CommandLogDrainLoadCommandPartitionLimit(got))
	}
	if got.AssignmentMode != CommandLogDrainAssignmentSnapshot || got.ExecutorMode != CommandLogDrainExecutorNative {
		t.Fatalf("modes = %q/%q, want snapshot/native", got.AssignmentMode, got.ExecutorMode)
	}
}

func TestValidateCommandLogDrainLoadConfig(t *testing.T) {
	valid := NormalizeCommandLogDrainLoadConfig(CommandLogDrainLoadConfig{})
	if err := ValidateCommandLogDrainLoadConfig(valid); err != nil {
		t.Fatalf("ValidateCommandLogDrainLoadConfig(valid) error = %v", err)
	}
	invalidAssignment := valid
	invalidAssignment.AssignmentMode = "etcd"
	requireErrorContains(t, ValidateCommandLogDrainLoadConfig(invalidAssignment), `unsupported assignment mode "etcd"`)
	invalidExecutor := valid
	invalidExecutor.ExecutorMode = "wasm"
	requireErrorContains(t, ValidateCommandLogDrainLoadConfig(invalidExecutor), `unsupported executor mode "wasm"`)
}

func TestCommandLogDrainLoadCounts(t *testing.T) {
	config := CommandLogDrainLoadConfig{
		Boards:           2,
		CommandsPerBoard: 3,
		RepliesPerThread: 4,
	}
	requireEqual(t, "CommandLogDrainLoadCreateThreadCommands", CommandLogDrainLoadCreateThreadCommands(config), 6)
	requireEqual(t, "CommandLogDrainLoadAppendPostCommands", CommandLogDrainLoadAppendPostCommands(config), 24)
	requireEqual(t, "CommandLogDrainLoadTotalCommands", CommandLogDrainLoadTotalCommands(config), 30)
	requireEqual(t, "CommandLogDrainLoadExpectedEventProjectionEvents", CommandLogDrainLoadExpectedEventProjectionEvents(config), 36)
	requireEqual(t, "CommandLogDrainLoadCommandPartitionLimit", CommandLogDrainLoadCommandPartitionLimit(config), 8)
	requireEqual(t, "CommandLogDrainLoadEventProjectionSource", CommandLogDrainLoadEventProjectionSource(config), "command-log-drain-load:sql")
}

func TestValidateCommandLogDrainLoadReport(t *testing.T) {
	valid := CommandLogDrainLoadReport{
		Config:        CommandLogDrainLoadConfig{ExecutorMode: CommandLogDrainExecutorSQL},
		TotalCommands: 3,
		Submit:        CommandLogLoadStage{Commands: 3, Succeeded: 3},
		Drain:         CommandLogDrainStage{Commands: 3, Processed: 3, Applied: 3},
		PromotionReadiness: CommandLogPromotionReadinessReport{
			Ready: true,
		},
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete: true,
		},
	}
	if err := ValidateCommandLogDrainLoadReport(valid); err != nil {
		t.Fatalf("ValidateCommandLogDrainLoadReport(valid) error = %v", err)
	}

	tests := map[string]struct {
		mutate func(*CommandLogDrainLoadReport)
		want   string
	}{
		"submit failure": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.Submit.Failed = 1
			},
			want: "command production failed 1/3",
		},
		"drain mismatch": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.Drain.Applied = 2
				report.MaxPartitionLagAfterDrain = 1
			},
			want: "applied 2/3 with max lag 1",
		},
		"drain failures": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.Drain.TerminalFailures = 1
				report.Drain.RetryableFailures = 2
				report.Drain.CommitFailures = 3
			},
			want: "drain failures terminal=1 retryable=2 commit=3",
		},
		"native projection mismatch": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.Config.ExecutorMode = CommandLogDrainExecutorNative
				report.EventProjection.ExpectedEvents = 7
				report.EventProjection.AppliedEvents = 6
			},
			want: "projected 6 broker events, want 7 for 3 native commands",
		},
		"readiness failure": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.PromotionReadiness.Ready = false
				report.PromotionReadiness.LaggingPartitions = 1
				report.PromotionReadiness.TotalLag = 2
				report.MaterializationAudit.MissingMaterialization = 3
				report.MaterializationAudit.RetryingCommitted = 4
				report.MaterializationAudit.MissingRecords = 5
			},
			want: "promotion readiness failed lagging=1 totalLag=2 missing=3 retrying=4 missingRecords=5",
		},
		"audit incomplete": {
			mutate: func(report *CommandLogDrainLoadReport) {
				report.MaterializationAudit.Complete = false
				report.MaterializationAudit.MissingMaterialization = 3
				report.MaterializationAudit.RetryingCommitted = 4
				report.MaterializationAudit.MissingRecords = 5
			},
			want: "materialization audit incomplete missing=3 retrying=4 missingRecords=5",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			report := valid
			tt.mutate(&report)
			requireErrorContains(t, ValidateCommandLogDrainLoadReport(report), tt.want)
		})
	}
}

func TestCommandLogDrainLoadReportLifecycle(t *testing.T) {
	sqlConfig := CommandLogDrainLoadConfig{
		Boards:           2,
		CommandsPerBoard: 3,
		RepliesPerThread: 1,
		ExecutorMode:     CommandLogDrainExecutorSQL,
	}
	report := NewCommandLogDrainLoadReport(sqlConfig, 1000)
	if report.StartedAt != 1000 || report.TotalCommands != 12 || report.Partitions != 2 {
		t.Fatalf("new SQL report = %+v, want started/total/partitions filled", report)
	}
	requireEqual(t, "SQL report expected projection events", report.EventProjection.ExpectedEvents, 0)

	nativeConfig := sqlConfig
	nativeConfig.ExecutorMode = CommandLogDrainExecutorNative
	nativeReport := NewCommandLogDrainLoadReport(nativeConfig, 2000)
	requireEqual(t, "native report expected projection events", nativeReport.EventProjection.ExpectedEvents, 18)

	FinishCommandLogDrainLoadReport(&report, 3000)
	requireEqual(t, "finished report timestamp", report.FinishedAt, int64(3000))
	readiness := CommandLogPromotionReadinessReport{
		Ready: true,
		MaterializationAudit: CommandLogMaterializationAuditReport{
			Complete:        true,
			AppliedCommands: 12,
		},
	}
	FinalizeCommandLogDrainLoadReport(&report, readiness, 4000)
	if !report.PromotionReadiness.Ready || !report.MaterializationAudit.Complete || report.MaterializationAudit.AppliedCommands != 12 || report.FinishedAt != 4000 {
		t.Fatalf("finalized report = %+v, want readiness/audit/timestamp copied", report)
	}
}

func TestCommandLogDrainLoadPhaseRecording(t *testing.T) {
	report := CommandLogDrainLoadReport{}
	RecordCommandLogDrainLoadSubmit(&report, CommandLogLoadStage{Commands: 2, Succeeded: 1, Failed: 1, DurationMS: 1000, SampleErrorText: []string{"one"}})
	RecordCommandLogDrainLoadSubmit(&report, CommandLogLoadStage{Commands: 3, Succeeded: 3, DurationMS: 1000, SampleErrorText: []string{"two"}})
	if report.Submit.Commands != 5 || report.Submit.Succeeded != 4 || report.Submit.Failed != 1 || report.Submit.CommandsPerSec != 2 {
		t.Fatalf("submit report = %+v, want merged stage", report.Submit)
	}
	requireSamples(t, "submit samples", report.Submit.SampleErrorText, "one", "two")

	RecordCommandLogDrainLoadBeforeDrainLag(&report, 3)
	RecordCommandLogDrainLoadBeforeDrainLag(&report, 2)
	requireEqual(t, "max lag before drain", report.MaxPartitionLagBeforeDrain, int64(3))
	RecordCommandLogDrainLoadDrain(&report, CommandLogDrainStage{Commands: 4, Applied: 4, DurationMS: 1000, SampleErrorText: []string{"drain"}})
	RecordCommandLogDrainLoadAfterDrainLag(&report, 1)
	if report.Drain.Commands != 4 || report.Drain.Applied != 4 || report.Drain.CommandsPerSec != 4 || report.MaxPartitionLagAfterDrain != 1 {
		t.Fatalf("drain report = %+v maxLag=%d, want drain stage and lag recorded", report.Drain, report.MaxPartitionLagAfterDrain)
	}

	RecordCommandLogDrainLoadEventProjection(&report, EventStoreProjectionLoadStage{Enabled: true, Partitions: 1, PartitionLimit: 2, AppliedEvents: 2, Rounds: 1, DurationMS: 1000, SampleErrorText: []string{"projection one"}})
	RecordCommandLogDrainLoadEventProjection(&report, EventStoreProjectionLoadStage{Partitions: 4, PartitionLimit: 3, PartitionLimitExceeded: true, AppliedEvents: 3, Rounds: 2, DurationMS: 1000, SampleErrorText: []string{"projection two"}})
	if !report.EventProjection.Enabled || !report.EventProjection.PartitionLimitExceeded ||
		report.EventProjection.Partitions != 4 || report.EventProjection.PartitionLimit != 3 ||
		report.EventProjection.AppliedEvents != 5 || report.EventProjection.Rounds != 3 || report.EventProjection.EventsPerSec != 2.5 {
		t.Fatalf("event projection report = %+v, want merged projection", report.EventProjection)
	}
	requireSamples(t, "projection samples", report.EventProjection.SampleErrorText, "projection one", "projection two")
}

func TestRecordCommandLogPromotionPartitionOffset(t *testing.T) {
	report := CommandLogPromotionReadinessReport{
		Config: CommandLogPromotionReadinessConfig{MaxLaggingSamples: 2},
	}
	RecordCommandLogPromotionPartitionOffset(&report, "board", "general", 5, 5)
	RecordCommandLogPromotionPartitionOffset(&report, "board", "meta", 7, 3)
	RecordCommandLogPromotionPartitionOffset(&report, "board", "retro", 2, 4)

	if report.TailCommands != 14 || report.CommittedCommands != 12 {
		t.Fatalf("offset totals = tail %d committed %d, want 14/12", report.TailCommands, report.CommittedCommands)
	}
	if report.LaggingPartitions != 2 || report.TotalLag != 4 || report.MaxLag != 4 {
		t.Fatalf("lag totals = partitions %d total %d max %d, want 2/4/4", report.LaggingPartitions, report.TotalLag, report.MaxLag)
	}
	requireEqual(t, "lagging sample count", len(report.LaggingSamples), 2)
	if got := report.LaggingSamples[0]; got.PartitionKey != "meta" || got.TailOffset != 7 || got.CommittedOffset != 3 || got.Lag != 4 {
		t.Fatalf("positive lag sample = %+v", got)
	}
	if got := report.LaggingSamples[1]; got.PartitionKey != "retro" || got.Lag != -2 {
		t.Fatalf("negative lag sample = %+v", got)
	}
}

func TestCommandLogPromotionReadinessReportLifecycle(t *testing.T) {
	report := NewCommandLogPromotionReadinessReport(CommandLogPromotionReadinessConfig{BatchSize: 0}, 1000)
	if report.StartedAt != 1000 || report.Config.BatchSize != defaultCommandLogPromotionReadinessConfig().BatchSize {
		t.Fatalf("new readiness report = %+v, want normalized config and started timestamp", report)
	}
	RecordCommandLogPromotionReadinessCoverage(&report, 3, true)
	if report.Partitions != 3 || !report.PartitionLimitExceeded {
		t.Fatalf("coverage report = %+v, want partition limit coverage recorded", report)
	}
	FinishCommandLogPromotionReadinessReport(&report, 2000)
	requireEqual(t, "finished readiness report timestamp", report.FinishedAt, int64(2000))
}

func TestFinalizeCommandLogPromotionReadinessReport(t *testing.T) {
	tests := map[string]struct {
		report CommandLogPromotionReadinessReport
		audit  CommandLogMaterializationAuditReport
		ready  bool
	}{
		"ready":             {audit: CommandLogMaterializationAuditReport{Complete: true}, ready: true},
		"lagging":           {report: CommandLogPromotionReadinessReport{LaggingPartitions: 1}, audit: CommandLogMaterializationAuditReport{Complete: true}},
		"partition limited": {report: CommandLogPromotionReadinessReport{PartitionLimitExceeded: true}, audit: CommandLogMaterializationAuditReport{Complete: true}},
		"incomplete audit":  {audit: CommandLogMaterializationAuditReport{Complete: false, MissingMaterialization: 1}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			report := tt.report
			FinalizeCommandLogPromotionReadinessReport(&report, tt.audit)
			if report.Ready != tt.ready || !reflect.DeepEqual(report.MaterializationAudit, tt.audit) {
				t.Fatalf("finalized report = %+v, want ready=%v audit=%+v", report, tt.ready, tt.audit)
			}
		})
	}
}

func TestCommandLogMaterializationAuditReportLifecycle(t *testing.T) {
	report := NewCommandLogMaterializationAuditReport(CommandLogMaterializationAuditConfig{BatchSize: 0}, 1000)
	if report.StartedAt != 1000 || !report.Complete || report.Config.BatchSize != defaultCommandLogMaterializationAuditConfig().BatchSize {
		t.Fatalf("new materialization audit report = %+v, want normalized config, started timestamp, and complete default", report)
	}
	RecordCommandLogMaterializationAuditCoverage(&report, 2, true)
	if report.Partitions != 2 || !report.PartitionLimitExceeded || report.Complete {
		t.Fatalf("coverage report = %+v, want partition limit marked incomplete", report)
	}
	AppendCommandLogMaterializationPartition(&report, CommandLogMaterializationPartition{PartitionKind: "board", PartitionKey: "general"})
	if len(report.PartitionReports) != 1 || report.PartitionReports[0].PartitionKey != "general" {
		t.Fatalf("partition reports = %+v, want appended partition", report.PartitionReports)
	}
	FinishCommandLogMaterializationAuditReport(&report, 2000)
	requireEqual(t, "finished audit report timestamp", report.FinishedAt, int64(2000))
	healthy := NewCommandLogMaterializationAuditReport(CommandLogMaterializationAuditConfig{}, 3000)
	FailCommandLogMaterializationAuditReport(&healthy, 4000)
	if healthy.Complete || healthy.FinishedAt != 4000 {
		t.Fatalf("failed audit report = %+v, want incomplete with finished timestamp", healthy)
	}
}

func TestRecordCommandLogMaterializationMissingRecords(t *testing.T) {
	report := CommandLogMaterializationAuditReport{
		Config:   CommandLogMaterializationAuditConfig{MaxMissingSamples: 2},
		Complete: true,
	}
	partition := CommandLogMaterializationPartition{
		PartitionKind: "board",
		PartitionKey:  "general",
	}
	RecordCommandLogMaterializationMissingRecords(&report, &partition, "board", "general", 7, 3)

	if report.Complete || report.MissingRecords != 3 || partition.MissingRecords != 3 {
		t.Fatalf("missing record counters = report %+v partition %+v, want incomplete with 3 missing", report, partition)
	}
	requireEqual(t, "missing sample count", len(report.MissingSamples), 1)
	sample := report.MissingSamples[0]
	if sample.PartitionKind != "board" || sample.PartitionKey != "general" || sample.Offset != 7 ||
		sample.Status != "missing_record" || sample.Detail != "committed offset has no command-log record" {
		t.Fatalf("missing sample = %+v", sample)
	}
}

func TestRecordCommandLogMaterializationIncomplete(t *testing.T) {
	report := CommandLogMaterializationAuditReport{
		Config:   CommandLogMaterializationAuditConfig{MaxMissingSamples: 2},
		Complete: true,
	}
	partition := CommandLogMaterializationPartition{
		PartitionKind: "thread",
		PartitionKey:  "thr_1",
	}
	RecordCommandLogMaterializationIncomplete(&report, &partition, "thread", "thr_1", 4, "cid-1", "usr-1", "missing", false)
	RecordCommandLogMaterializationIncomplete(&report, &partition, "thread", "thr_1", 5, "cid-2", "usr-2", "retrying", true)

	if report.Complete || report.CommittedCommands != 2 || report.MissingMaterialization != 1 || report.RetryingCommitted != 1 {
		t.Fatalf("incomplete report = %+v, want two committed with one missing and one retrying", report)
	}
	if partition.MissingMaterialization != 1 || partition.RetryingCommitted != 1 {
		t.Fatalf("incomplete partition = %+v, want one missing and one retrying", partition)
	}
	requireEqual(t, "missing sample count", len(report.MissingSamples), 2)
	if got := report.MissingSamples[0]; got.CommandID != "cid-1" || got.ActorID != "usr-1" ||
		got.Status != "missing" || got.Detail != "committed command has no applied result or terminal failure receipt" {
		t.Fatalf("missing materialization sample = %+v", got)
	}
	if got := report.MissingSamples[1]; got.CommandID != "cid-2" || got.ActorID != "usr-2" ||
		got.Status != "retrying" || got.Detail != "committed command still has retrying receipt" {
		t.Fatalf("retrying materialization sample = %+v", got)
	}
}

func TestRecordCommandLogMaterializationAppliedAndTerminal(t *testing.T) {
	report := CommandLogMaterializationAuditReport{Complete: true}
	partition := CommandLogMaterializationPartition{}

	RecordCommandLogMaterializationApplied(&report, &partition)
	RecordCommandLogMaterializationTerminalFailure(&report, &partition)

	if !report.Complete || report.CommittedCommands != 2 || report.AppliedCommands != 1 || report.TerminalFailures != 1 {
		t.Fatalf("materialization counters = report %+v, want one applied and one terminal", report)
	}
	if partition.AppliedCommands != 1 || partition.TerminalFailures != 1 {
		t.Fatalf("partition counters = %+v, want one applied and one terminal", partition)
	}
}

func TestAddLoadSamplesCapsAndDeduplicates(t *testing.T) {
	stage := CommandLogDrainStage{SampleErrorText: []string{"one"}}
	seen := map[string]bool{"one": true}
	for _, sample := range []string{"one", "two", "three", "four", "five", "six"} {
		AddCommandLogDrainSample(&stage, seen, sample)
	}
	requireSamples(t, "capped drain samples", stage.SampleErrorText, "one", "two", "three", "four", "five")
}

func TestAddCommandLogDrainPartitionSample(t *testing.T) {
	stage := CommandLogDrainStage{}
	seen := map[string]bool{}
	AddCommandLogDrainPartitionSample(&stage, seen, "board", "general", "terminal", "boom")
	AddCommandLogDrainPartitionSample(&stage, seen, "board", "general", "assignment lost", "")
	AddCommandLogDrainPartitionSample(&stage, seen, "board", "general", "terminal", "boom")
	requireSamples(t, "partition drain samples", stage.SampleErrorText,
		"board/general terminal: boom",
		"board/general assignment lost",
	)
}

func TestAddLoadSampleWithoutSeenMapDeduplicates(t *testing.T) {
	samples := []string{"one"}
	for _, sample := range []string{"one", "two", "three", "four", "five", "six"} {
		AddLoadSample(&samples, nil, sample)
	}
	requireSamples(t, "capped load samples", samples, "one", "two", "three", "four", "five")
}

func TestAddLoadSampleWithUnprimedSeenMapDeduplicates(t *testing.T) {
	samples := []string{"one"}
	seen := map[string]bool{}
	AddLoadSample(&samples, seen, "one")
	AddLoadSample(&samples, seen, "two")
	requireSamples(t, "unprimed seen samples", samples, "one", "two")
	if !seen["one"] || !seen["two"] {
		t.Fatalf("seen = %+v, want observed samples tracked", seen)
	}
}

func requireStringResults(t *testing.T, name string, normalize func(string) string, tests map[string]string) {
	t.Helper()
	for raw, want := range tests {
		requireEqual(t, name+"("+raw+")", normalize(raw), want)
	}
}

func requireEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func requireSamples(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v, want %+v", name, got, want)
	}
}
