package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/preflightcheck"
	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

func TestRunPreflightWithInjectedProbes(t *testing.T) {
	var calls []string
	restore := withInjectedPreflightProbes(t,
		func(context.Context, preflightcheck.Config) error {
			calls = append(calls, runconfig.PreflightTargetPostgres)
			return nil
		},
		func(context.Context, preflightcheck.Config) error {
			calls = append(calls, runconfig.PreflightTargetNATS)
			return nil
		},
		func(context.Context, preflightcheck.Config) error {
			calls = append(calls, runconfig.PreflightTargetKafka)
			return nil
		},
	)
	defer restore()

	stdout := captureStdout(t, func() {
		code := run([]string{
			"-targets", "all",
			"-postgres-dsn", "postgres://postgres.internal/budgie",
			"-nats", "nats://nats.internal:4222",
			"-kafka-brokers", "redpanda.internal:9092",
			"-id", "unit-test",
			"-timeout", "1s",
		})
		if code != 0 {
			t.Fatalf("run exit = %d, want success", code)
		}
	})
	wantCalls := []string{runconfig.PreflightTargetPostgres, runconfig.PreflightTargetNATS, runconfig.PreflightTargetKafka}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	wantTokens := []string{
		"internet-scale staging preflight targets: postgres nats kafka",
		"internet-scale staging preflight passed",
	}
	for _, spec := range preflightcheck.ProbeSpecs(preflightcheck.Config{ID: "unit_test"}) {
		wantTokens = append(wantTokens, spec.Name+" passed")
	}
	for _, token := range wantTokens {
		if !strings.Contains(stdout, token) {
			t.Fatalf("stdout missing %q:\n%s", token, stdout)
		}
	}
}

func TestRunPreflightWritesSanitizedReport(t *testing.T) {
	restore := withInjectedPreflightProbes(t,
		func(context.Context, preflightcheck.Config) error { return nil },
		func(context.Context, preflightcheck.Config) error { return nil },
		func(context.Context, preflightcheck.Config) error { return nil },
	)
	defer restore()

	reportPath := filepath.Join(t.TempDir(), "preflight-report.json")
	stdout := captureStdout(t, func() {
		code := run([]string{
			"-targets", "all",
			"-postgres-dsn", "postgres://budgie:secret@postgres.internal:5432/budgie?sslmode=require",
			"-nats", "nats://user:secret@nats.internal:4222?token=secret",
			"-kafka-brokers", "redpanda-a.internal:9092,redpanda-b.internal:9092",
			"-kafka-tls",
			"-kafka-sasl-mechanism", "scram-sha-512",
			"-kafka-sasl-user", "budgie",
			"-kafka-sasl-password", "secret",
			"-id", "unit-report",
			"-timeout", "1s",
			"-report-file", reportPath,
		})
		if code != 0 {
			t.Fatalf("run exit = %d, want success", code)
		}
	})
	if !strings.Contains(stdout, "archived preflight report at "+reportPath) {
		t.Fatalf("stdout missing archived report path:\n%s", stdout)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	reportBody := string(raw)
	for _, secret := range []string{"secret", "budgie:secret", "user:secret", "sslmode=require", "token=secret"} {
		if strings.Contains(reportBody, secret) {
			t.Fatalf("report leaked secret token %q:\n%s", secret, reportBody)
		}
	}

	var report preflightmodel.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report passed = false, want true")
	}
	if report.Evidence.Tool != "budgie-internet-scale-preflight" {
		t.Fatalf("report tool = %q, want preflight tool", report.Evidence.Tool)
	}
	if !reflect.DeepEqual(report.Config.Targets, []string{runconfig.PreflightTargetPostgres, runconfig.PreflightTargetNATS, runconfig.PreflightTargetKafka}) {
		t.Fatalf("report targets = %+v", report.Config.Targets)
	}
	if report.Config.ID != "unit_report" {
		t.Fatalf("report id = %q, want sanitized id", report.Config.ID)
	}
	if report.Runtime.PostgresEndpoint != "postgres://postgres.internal:5432/budgie" {
		t.Fatalf("postgres endpoint = %q", report.Runtime.PostgresEndpoint)
	}
	if report.Runtime.NATSEndpoint != "nats://nats.internal:4222" {
		t.Fatalf("nats endpoint = %q", report.Runtime.NATSEndpoint)
	}
	if !reflect.DeepEqual(report.Runtime.KafkaBrokers, []string{"kafka://redpanda-a.internal:9092", "kafka://redpanda-b.internal:9092"}) {
		t.Fatalf("kafka brokers = %+v", report.Runtime.KafkaBrokers)
	}
	if !report.Runtime.KafkaTLS || report.Runtime.KafkaSASLMechanism != "scram-sha-512" {
		t.Fatalf("kafka security evidence = tls:%v sasl:%q", report.Runtime.KafkaTLS, report.Runtime.KafkaSASLMechanism)
	}
	if len(report.Probes) != 3 {
		t.Fatalf("probe count = %d, want 3", len(report.Probes))
	}
	resources := []string{}
	for _, probe := range report.Probes {
		if !probe.Passed {
			t.Fatalf("probe %+v did not pass", probe)
		}
		resources = append(resources, probe.Resources...)
	}
	joinedResources := strings.Join(resources, " ")
	resourceConfig := preflightcheck.Config{ID: report.Config.ID}
	wantResources := []string{preflightcheck.PostgresSchema(resourceConfig)}
	wantResources = append(wantResources, preflightcheck.NATSStreams(resourceConfig)...)
	wantResources = append(wantResources, preflightcheck.KafkaTopics(resourceConfig)...)
	for _, token := range wantResources {
		if !strings.Contains(joinedResources, token) {
			t.Fatalf("report resources missing %q: %+v", token, resources)
		}
	}
}

func withInjectedPreflightProbes(t *testing.T, postgres, nats, kafka func(context.Context, preflightcheck.Config) error) func() {
	t.Helper()
	previousPostgres := runPostgresPreflightProbe
	previousNATS := runNATSPreflightProbe
	previousKafka := runKafkaPreflightProbe
	runPostgresPreflightProbe = postgres
	runNATSPreflightProbe = nats
	runKafkaPreflightProbe = kafka
	return func() {
		runPostgresPreflightProbe = previousPostgres
		runNATSPreflightProbe = previousNATS
		runKafkaPreflightProbe = previousKafka
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	previous := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	os.Stdout = previous
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return buf.String()
}
