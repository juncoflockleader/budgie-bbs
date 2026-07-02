package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAcceptsRemoteDurablePreflightReport(t *testing.T) {
	path := writePreflightReportForCheck(t, validPreflightReportForCheck())
	var stdout, stderr bytes.Buffer
	code := runPreflight([]string{
		"-report-file", path,
		"-targets", "nats,kafka",
		"-remote-staging",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "internet-scale preflight report satisfies staging evidence") {
		t.Fatalf("stdout missing success line:\n%s", stdout.String())
	}
}

func TestRunRejectsLoopbackRemoteEvidence(t *testing.T) {
	report := validPreflightReportForCheck()
	report.Runtime.NATSEndpoint = "nats://127.0.0.1:4222"
	path := writePreflightReportForCheck(t, report)
	var stdout, stderr bytes.Buffer
	code := runPreflight([]string{
		"-report-file", path,
		"-targets", "nats,kafka",
		"-remote-staging",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, want budget violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "runtime.natsEndpoint") ||
		!strings.Contains(stderr.String(), "must be non-local") {
		t.Fatalf("stderr missing loopback violation:\n%s", stderr.String())
	}
}

func TestRunRejectsDirtyOrIncompleteEvidence(t *testing.T) {
	report := validPreflightReportForCheck()
	report.Evidence.GitModified = true
	report.Probes[2].Resources = report.Probes[2].Resources[:1]
	path := writePreflightReportForCheck(t, report)
	var stdout, stderr bytes.Buffer
	code := runPreflight([]string{
		"-report-file", path,
		"-targets", "nats,kafka",
		"-remote-staging",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, want budget violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, token := range []string{
		"evidence.gitModified",
		"probes.kafka.resources",
		"budgie.events.load.preflight.remote_report",
	} {
		if !strings.Contains(stderr.String(), token) {
			t.Fatalf("stderr missing %q:\n%s", token, stderr.String())
		}
	}
}

func TestRunRejectsUnsanitizedEndpointEvidence(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*preflightReport)
		wantPath  string
		wantToken string
	}{
		{
			name: "postgres userinfo",
			mutate: func(report *preflightReport) {
				report.Runtime.PostgresEndpoint = "postgres://budgie:secret@postgres.staging.internal:5432/budgie_staging"
			},
			wantPath:  "runtime.postgresEndpoint",
			wantToken: "must not include userinfo or credentials",
		},
		{
			name: "nats query",
			mutate: func(report *preflightReport) {
				report.Runtime.NATSEndpoint = "nats://nats.staging.internal:4222?token=secret"
			},
			wantPath:  "runtime.natsEndpoint",
			wantToken: "must not include query parameters",
		},
		{
			name: "kafka malformed",
			mutate: func(report *preflightReport) {
				report.Runtime.KafkaBrokers = []string{"redpanda-a.staging.internal:9092"}
			},
			wantPath:  "runtime.kafkaBrokers[0]",
			wantToken: "must be URL-shaped sanitized endpoint evidence",
		},
		{
			name: "kafka fragment",
			mutate: func(report *preflightReport) {
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
			path := writePreflightReportForCheck(t, report)
			var stdout, stderr bytes.Buffer
			code := runPreflight([]string{
				"-report-file", path,
				"-targets", "nats,kafka",
				"-remote-staging",
			}, strings.NewReader(""), &stdout, &stderr)
			if code != 3 {
				t.Fatalf("run exit = %d, want budget violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantPath) || !strings.Contains(stderr.String(), tt.wantToken) {
				t.Fatalf("stderr missing %q/%q:\n%s", tt.wantPath, tt.wantToken, stderr.String())
			}
		})
	}
}

func validPreflightReportForCheck() preflightReport {
	return preflightReport{
		Config: preflightReportConfig{
			Targets:       []string{targetPostgres, targetNATS, targetKafka},
			RemoteStaging: true,
			ID:            "remote_report",
			TimeoutMS:     45000,
		},
		Runtime: preflightReportRuntime{
			PostgresEndpoint:       "postgres://postgres.staging.internal:5432/budgie_staging",
			NATSEndpoint:           "nats://nats.staging.internal:4222",
			NATSReplicas:           1,
			KafkaBrokers:           []string{"kafka://redpanda-a.staging.internal:9092", "kafka://redpanda-b.staging.internal:9092"},
			KafkaCommandPartitions: 32,
			KafkaEventPartitions:   32,
			KafkaTopicReplicas:     1,
		},
		Evidence: preflightEvidence{
			Tool:        "budgie-internet-scale-preflight",
			GitRevision: "0123456789abcdef",
			GitModified: false,
		},
		StartedAt:  1000,
		FinishedAt: 2000,
		Passed:     true,
		Probes: []preflightProbeReport{
			{
				Target:     targetPostgres,
				Name:       "Postgres disposable schema create/drop",
				Resources:  []string{"budgie_cmdlog_load_preflight_remote_report"},
				StartedAt:  1100,
				FinishedAt: 1200,
				Passed:     true,
			},
			{
				Target: targetNATS,
				Name:   "NATS JetStream command/event stream create/delete",
				Resources: []string{
					"BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_REMOTE_REPORT",
					"BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_REMOTE_REPORT",
				},
				StartedAt:  1200,
				FinishedAt: 1300,
				Passed:     true,
			},
			{
				Target: targetKafka,
				Name:   "Kafka command/event topic create/delete",
				Resources: []string{
					"budgie.commands.load.preflight.remote_report",
					"budgie.events.load.preflight.remote_report",
				},
				StartedAt:  1300,
				FinishedAt: 1400,
				Passed:     true,
			},
		},
	}
}

func writePreflightReportForCheck(t *testing.T, report preflightReport) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	path := filepath.Join(t.TempDir(), "preflight-report.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}
