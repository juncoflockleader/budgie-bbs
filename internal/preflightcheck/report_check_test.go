package preflightcheck

import (
	"slices"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

func TestEvaluateReportAcceptsRemoteDurablePreflightReport(t *testing.T) {
	report := validPreflightReportForCheck()
	expectedTargets, err := runconfig.NormalizePreflightTargets("nats,kafka", nil)
	if err != nil {
		t.Fatalf("normalize targets: %v", err)
	}

	violations, err := EvaluateReport(report, expectedTargets, true)
	if err != nil {
		t.Fatalf("evaluate report: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %v, want none", violations)
	}
}

func TestEvaluateReportRejectsDirtyOrIncompleteRemoteEvidence(t *testing.T) {
	report := validPreflightReportForCheck()
	report.Evidence.GitModified = true
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	report.Probes[2].Resources = report.Probes[2].Resources[:1]
	expectedTargets, err := runconfig.NormalizePreflightTargets("nats,kafka", nil)
	if err != nil {
		t.Fatalf("normalize targets: %v", err)
	}

	violations, err := EvaluateReport(report, expectedTargets, true)
	if err != nil {
		t.Fatalf("evaluate report: %v", err)
	}
	for _, token := range []string{
		"evidence.gitModified",
		"runtime.natsEndpoint",
		"probes.kafka.resources",
		KafkaTopics(Config{ID: report.Config.ID})[1],
	} {
		if !slices.ContainsFunc(violations, func(violation string) bool {
			return strings.Contains(violation, token)
		}) {
			t.Fatalf("violations missing %q:\n%v", token, violations)
		}
	}
}

func validPreflightReportForCheck() preflightmodel.Report {
	config := Config{ID: "remote_report"}
	return preflightmodel.Report{
		Config: preflightmodel.Config{
			Targets:       []string{runconfig.PreflightTargetPostgres, runconfig.PreflightTargetNATS, runconfig.PreflightTargetKafka},
			RemoteStaging: true,
			ID:            config.ID,
			TimeoutMS:     45000,
		},
		Runtime: preflightmodel.Runtime{
			PostgresEndpoint:       "postgres://postgres.staging.internal:5432/budgie_staging",
			NATSEndpoint:           "nats://nats.staging.internal:4222",
			NATSReplicas:           1,
			KafkaBrokers:           []string{"kafka://redpanda-a.staging.internal:9092", "kafka://redpanda-b.staging.internal:9092"},
			KafkaCommandPartitions: 32,
			KafkaEventPartitions:   32,
			KafkaTopicReplicas:     1,
		},
		Evidence: runevidence.Evidence{
			Tool:        "budgie-internet-scale-preflight",
			GitRevision: "0123456789abcdef",
			GitModified: false,
		},
		StartedAt:  1000,
		FinishedAt: 2000,
		Passed:     true,
		Probes:     successfulProbeReports(config),
	}
}

func successfulProbeReports(config Config) []preflightmodel.Probe {
	out := []preflightmodel.Probe{}
	for i, spec := range ProbeSpecs(config) {
		startedAt := int64(1100 + i*100)
		out = append(out, preflightmodel.Probe{
			Target:     spec.Target,
			Name:       spec.Name,
			Resources:  spec.Resources,
			StartedAt:  startedAt,
			FinishedAt: startedAt + 100,
			Passed:     true,
		})
	}
	return out
}
