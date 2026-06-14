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
	"time"
)

func TestNormalizePreflightTargetsDetectsDurableEnv(t *testing.T) {
	targets, err := normalizePreflightTargets("", "nats://nats.internal:4222", "redpanda.internal:9092", "postgres://postgres.internal/budgie")
	if err != nil {
		t.Fatalf("normalize targets: %v", err)
	}
	want := []string{targetPostgres, targetNATS, targetKafka}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %+v, want %+v", targets, want)
	}

	targets, err = normalizePreflightTargets("kafka", "", "redpanda.internal:9092", "postgres://postgres.internal/budgie")
	if err != nil {
		t.Fatalf("normalize explicit kafka: %v", err)
	}
	want = []string{targetPostgres, targetKafka}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("explicit kafka targets = %+v, want %+v", targets, want)
	}

	if _, err := normalizePreflightTargets("redis", "", "", ""); err == nil || !strings.Contains(err.Error(), "unsupported preflight target") {
		t.Fatalf("unsupported target err = %v, want validation error", err)
	}
}

func TestValidatePreflightConfigRequiresDurableDependencies(t *testing.T) {
	err := validatePreflightConfig(preflightConfig{
		Targets: []string{targetNATS},
		NATSURL: "nats://nats.internal:4222",
		Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "BUDGIE_POSTGRES_DSN") {
		t.Fatalf("validate nats without postgres err = %v, want postgres requirement", err)
	}

	err = validatePreflightConfig(preflightConfig{
		Targets:            []string{targetKafka},
		KafkaBrokers:       "redpanda.internal:9092",
		PostgresDSN:        "postgres://postgres.internal/budgie",
		KafkaTopicReplicas: 1,
		Timeout:            time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "partition counts") {
		t.Fatalf("validate kafka without partitions err = %v, want partition requirement", err)
	}
}

func TestValidatePreflightConfigRejectsRemoteLoopbackEndpoints(t *testing.T) {
	config := preflightConfig{
		Targets:                []string{targetPostgres, targetNATS, targetKafka},
		RemoteStaging:          true,
		NATSURL:                "nats://127.0.0.1:4222",
		KafkaBrokers:           "redpanda.internal:9092",
		PostgresDSN:            "postgres://postgres.internal/budgie",
		NATSReplicas:           1,
		KafkaCommandPartitions: 32,
		KafkaEventPartitions:   32,
		KafkaTopicReplicas:     1,
		Timeout:                time.Second,
	}
	err := validatePreflightConfig(config)
	if err == nil || !strings.Contains(err.Error(), "BUDGIE_NATS_URL") {
		t.Fatalf("validate loopback nats err = %v, want nats loopback rejection", err)
	}

	config.NATSURL = "nats://nats.internal:4222"
	config.KafkaBrokers = "127.0.0.1:9092"
	err = validatePreflightConfig(config)
	if err == nil || !strings.Contains(err.Error(), "BUDGIE_KAFKA_BROKERS") {
		t.Fatalf("validate loopback kafka err = %v, want kafka loopback rejection", err)
	}

	config.KafkaBrokers = "[::1]:9092"
	err = validatePreflightConfig(config)
	if err == nil || !strings.Contains(err.Error(), "BUDGIE_KAFKA_BROKERS") {
		t.Fatalf("validate ipv6 loopback kafka err = %v, want kafka loopback rejection", err)
	}

	config.KafkaBrokers = "redpanda.internal:9092"
	config.PostgresDSN = "host=localhost port=55432 dbname=budgie_staging"
	err = validatePreflightConfig(config)
	if err == nil || !strings.Contains(err.Error(), "BUDGIE_POSTGRES_DSN") {
		t.Fatalf("validate loopback keyword postgres err = %v, want postgres loopback rejection", err)
	}
}

func TestRunPreflightWithInjectedProbes(t *testing.T) {
	var calls []string
	restore := withInjectedPreflightProbes(t,
		func(context.Context, preflightConfig) error {
			calls = append(calls, targetPostgres)
			return nil
		},
		func(context.Context, preflightConfig) error {
			calls = append(calls, targetNATS)
			return nil
		},
		func(context.Context, preflightConfig) error {
			calls = append(calls, targetKafka)
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
	wantCalls := []string{targetPostgres, targetNATS, targetKafka}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	for _, token := range []string{
		"internet-scale staging preflight targets: postgres nats kafka",
		"Postgres disposable schema create/drop passed",
		"NATS JetStream command/event stream create/delete passed",
		"Kafka command/event topic create/delete passed",
		"internet-scale staging preflight passed",
	} {
		if !strings.Contains(stdout, token) {
			t.Fatalf("stdout missing %q:\n%s", token, stdout)
		}
	}
}

func TestPreflightNamesUsePromotedLoadPrefixes(t *testing.T) {
	id := preflightID("Unit-Test")
	if id != "unit_test" {
		t.Fatalf("preflight id = %q, want sanitized underscore id", id)
	}
	config := preflightConfig{ID: id}
	if schema := preflightPostgresSchema(config); !strings.HasPrefix(schema, "budgie_cmdlog_load_") {
		t.Fatalf("schema = %q, want promoted load schema prefix", schema)
	}
	if topic := preflightKafkaTopics(config)[0]; !strings.HasPrefix(topic, "budgie.commands.load.") {
		t.Fatalf("topic = %q, want promoted command topic prefix", topic)
	}
	if stream := preflightNATSStreams(config)[0]; !strings.HasPrefix(stream, "BUDGIE_COMMAND_LOG_LOAD_") {
		t.Fatalf("stream = %q, want promoted command stream prefix", stream)
	}
}

func TestRunPreflightWritesSanitizedReport(t *testing.T) {
	restore := withInjectedPreflightProbes(t,
		func(context.Context, preflightConfig) error { return nil },
		func(context.Context, preflightConfig) error { return nil },
		func(context.Context, preflightConfig) error { return nil },
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

	var report preflightReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.Passed {
		t.Fatalf("report passed = false, want true")
	}
	if report.Evidence.Tool != "budgie-internet-scale-preflight" {
		t.Fatalf("report tool = %q, want preflight tool", report.Evidence.Tool)
	}
	if !reflect.DeepEqual(report.Config.Targets, []string{targetPostgres, targetNATS, targetKafka}) {
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
	for _, token := range []string{
		"budgie_cmdlog_load_preflight_unit_report",
		"BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_UNIT_REPORT",
		"BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_UNIT_REPORT",
		"budgie.commands.load.preflight.unit_report",
		"budgie.events.load.preflight.unit_report",
	} {
		if !strings.Contains(joinedResources, token) {
			t.Fatalf("report resources missing %q: %+v", token, resources)
		}
	}
}

func withInjectedPreflightProbes(t *testing.T, postgres, nats, kafka func(context.Context, preflightConfig) error) func() {
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
