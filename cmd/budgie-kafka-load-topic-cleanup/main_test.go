package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

func TestRunKafkaLoadTopicCleanupDryRunDoesNotDelete(t *testing.T) {
	var listed bool
	var deleted bool
	commandTopic := loadmodel.CommandLogLoadKafkaCommandTopicPrefix + "1"
	eventTopic := loadmodel.CommandLogLoadKafkaEventTopicPrefix + "1"
	withKafkaCleanupFakes(t,
		func(_ context.Context, options kafkaconn.TopicListOptions) ([]string, error) {
			listed = true
			if got := options.Runtime.Normalize().Brokers; len(got) != 1 || got[0] != "redpanda:9092" {
				t.Fatalf("list brokers = %+v, want redpanda broker", got)
			}
			runtime := options.Runtime.Normalize()
			if !runtime.TLS || runtime.SASLMechanism != "plain" || runtime.SASLUser != "budgie" || runtime.SASLPassword != "secret" {
				t.Fatalf("list runtime security = %+v, want TLS plus SASL", runtime)
			}
			return []string{commandTopic, eventTopic, "production.topic"}, nil
		},
		func(context.Context, kafkaconn.TopicDeletionOptions) error {
			deleted = true
			return nil
		},
	)

	output := captureStdout(t, func() {
		config := defaultKafkaLoadTopicCleanupConfig()
		config.Security = kafkaconn.RuntimeSecurityConfig{
			TLS:           true,
			SASLMechanism: "plain",
			SASLUser:      "budgie",
			SASLPassword:  "secret",
		}
		if err := runKafkaLoadTopicCleanup(context.Background(), config); err != nil {
			t.Fatalf("cleanup dry run: %v", err)
		}
	})
	if !listed || deleted {
		t.Fatalf("listed=%v deleted=%v, want dry-run list only", listed, deleted)
	}
	for _, token := range []string{
		"disposable Kafka load topics",
		commandTopic,
		eventTopic,
		"dry run only; pass --execute",
	} {
		if !strings.Contains(output, token) {
			t.Fatalf("output missing %q:\n%s", token, output)
		}
	}
	if strings.Contains(output, "production.topic") {
		t.Fatalf("output included production topic:\n%s", output)
	}
}

func TestRunKafkaLoadTopicCleanupExecuteDeletesLoadTopics(t *testing.T) {
	var deletedTopics []string
	commandTopic := loadmodel.CommandLogLoadKafkaCommandTopicPrefix + "1"
	eventTopic := loadmodel.CommandLogLoadKafkaEventTopicPrefix + "1"
	withKafkaCleanupFakes(t,
		func(context.Context, kafkaconn.TopicListOptions) ([]string, error) {
			return []string{eventTopic, commandTopic, "production.topic"}, nil
		},
		func(_ context.Context, options kafkaconn.TopicDeletionOptions) error {
			deletedTopics = append([]string(nil), options.Topics...)
			if !options.IgnoreMissing {
				t.Fatalf("delete IgnoreMissing = false, want true")
			}
			return nil
		},
	)

	output := captureStdout(t, func() {
		config := defaultKafkaLoadTopicCleanupConfig()
		config.Execute = true
		if err := runKafkaLoadTopicCleanup(context.Background(), config); err != nil {
			t.Fatalf("cleanup execute: %v", err)
		}
	})
	want := []string{commandTopic, eventTopic}
	if strings.Join(deletedTopics, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted topics = %+v, want %+v", deletedTopics, want)
	}
	if !strings.Contains(output, "Kafka load topic cleanup complete") {
		t.Fatalf("output missing completion:\n%s", output)
	}
}

func TestRunKafkaLoadTopicCleanupSurfacesListErrors(t *testing.T) {
	withKafkaCleanupFakes(t,
		func(context.Context, kafkaconn.TopicListOptions) ([]string, error) {
			return nil, errors.New("list failed")
		},
		func(context.Context, kafkaconn.TopicDeletionOptions) error {
			t.Fatal("delete should not run after list failure")
			return nil
		},
	)

	err := runKafkaLoadTopicCleanup(context.Background(), defaultKafkaLoadTopicCleanupConfig())
	if err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("cleanup err = %v, want list failure", err)
	}
}

func TestValidateKafkaLoadTopicCleanupConfigRejectsInvalidSecurity(t *testing.T) {
	config := defaultKafkaLoadTopicCleanupConfig()
	config.Security = kafkaconn.RuntimeSecurityConfig{
		SASLMechanism: "plain",
		SASLUser:      "budgie",
	}
	err := validateKafkaLoadTopicCleanupConfig(config)
	if err == nil || !strings.Contains(err.Error(), "SASL password is required") {
		t.Fatalf("validate cleanup invalid security err = %v, want SASL password error", err)
	}
}

func defaultKafkaLoadTopicCleanupConfig() kafkaLoadTopicCleanupConfig {
	return kafkaLoadTopicCleanupConfig{
		Brokers:            "redpanda:9092",
		CommandTopicPrefix: loadmodel.CommandLogLoadKafkaCommandTopicPrefix,
		EventTopicPrefix:   loadmodel.CommandLogLoadKafkaEventTopicPrefix,
		Timeout:            time.Second,
	}
}

func withKafkaCleanupFakes(t *testing.T, list func(context.Context, kafkaconn.TopicListOptions) ([]string, error), delete func(context.Context, kafkaconn.TopicDeletionOptions) error) {
	t.Helper()
	previousList := listKafkaTopics
	previousDelete := deleteKafkaTopics
	listKafkaTopics = list
	deleteKafkaTopics = delete
	t.Cleanup(func() {
		listKafkaTopics = previousList
		deleteKafkaTopics = previousDelete
	})
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = previous
	}()
	fn()
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return buf.String()
}
