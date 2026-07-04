package main

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/preflightcheck"
	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

func TestRunAcceptsRemoteDurablePreflightReport(t *testing.T) {
	path := writeReportCheckJSON(t, "preflight-report.json", validPreflightReportForCheck())
	result := runRemotePreflightCheck(t, path)
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "internet-scale preflight report satisfies staging evidence")
}

func TestRunRejectsLoopbackRemoteEvidence(t *testing.T) {
	report := validPreflightReportForCheck()
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	path := writeReportCheckJSON(t, "preflight-report.json", report)
	result := runRemotePreflightCheck(t, path)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "runtime.natsEndpoint", "must be non-local")
}

func TestRunRejectsDirtyOrIncompleteEvidence(t *testing.T) {
	report := validPreflightReportForCheck()
	report.Evidence.GitModified = true
	report.Probes[2].Resources = report.Probes[2].Resources[:1]
	path := writeReportCheckJSON(t, "preflight-report.json", report)
	result := runRemotePreflightCheck(t, path)
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"evidence.gitModified",
		"probes.kafka.resources",
		preflightcheck.KafkaTopics(preflightcheck.Config{ID: report.Config.ID})[1],
	)
}

func TestRunRejectsUnsanitizedEndpointEvidence(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*preflightmodel.Report)
		wantPath  string
		wantToken string
	}{
		{
			name: "postgres userinfo",
			mutate: func(report *preflightmodel.Report) {
				report.Runtime.PostgresEndpoint = "postgres://budgie:secret@postgres.staging.internal:5432/budgie_staging"
			},
			wantPath:  "runtime.postgresEndpoint",
			wantToken: "must not include userinfo or credentials",
		},
		{
			name: "nats query",
			mutate: func(report *preflightmodel.Report) {
				report.Runtime.NATSEndpoint = "nats://nats.staging.internal:4222?token=secret"
			},
			wantPath:  "runtime.natsEndpoint",
			wantToken: "must not include query parameters",
		},
		{
			name: "kafka malformed",
			mutate: func(report *preflightmodel.Report) {
				report.Runtime.KafkaBrokers = []string{"redpanda-a.staging.internal:9092"}
			},
			wantPath:  "runtime.kafkaBrokers[0]",
			wantToken: "must be URL-shaped sanitized endpoint evidence",
		},
		{
			name: "kafka fragment",
			mutate: func(report *preflightmodel.Report) {
				report.Runtime.KafkaBrokers = []string{"kafka://redpanda-a.staging.internal:9092#secret"}
			},
			wantPath:  "runtime.kafkaBrokers[0]",
			wantToken: "must not include URL fragments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validPreflightReportForCheck()
			tt.mutate(&report)
			path := writeReportCheckJSON(t, "preflight-report.json", report)
			result := runRemotePreflightCheck(t, path)
			requireReportCheckExit(t, result, 3)
			requireReportCheckOutputContains(t, "stderr", result.Stderr, tt.wantPath, tt.wantToken)
		})
	}
}

func runRemotePreflightCheck(t *testing.T, path string) reportCheckRunResult {
	t.Helper()
	return runReportCheckForTest(t, runPreflight, []string{
		"-report-file", path,
		"-targets", "nats,kafka",
		"-remote-staging",
	}, nil)
}

func validPreflightReportForCheck() preflightmodel.Report {
	config := preflightcheck.Config{ID: "remote_report"}
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
		Probes:     successfulPreflightProbeReports(config),
	}
}

func successfulPreflightProbeReports(config preflightcheck.Config) []preflightmodel.Probe {
	out := []preflightmodel.Probe{}
	for i, spec := range preflightcheck.ProbeSpecs(config) {
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
