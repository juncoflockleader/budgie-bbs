package preflightcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

func TestValidateConfigRequiresDurableDependencies(t *testing.T) {
	requireConfigError(t, Config{
		Targets: []string{runconfig.PreflightTargetNATS},
		NATSURL: "nats://nats.internal:4222",
		Timeout: time.Second,
	}, "BUDGIE_POSTGRES_DSN")

	requireConfigError(t, Config{
		Targets:            []string{runconfig.PreflightTargetKafka},
		KafkaBrokers:       "redpanda.internal:9092",
		PostgresDSN:        "postgres://postgres.internal/budgie",
		KafkaTopicReplicas: 1,
		Timeout:            time.Second,
	}, "partition counts")

	requireConfigError(t, Config{
		Targets:                []string{runconfig.PreflightTargetKafka},
		KafkaBrokers:           "redpanda.internal:9092",
		PostgresDSN:            "postgres://postgres.internal/budgie",
		KafkaCommandPartitions: 32,
		KafkaEventPartitions:   32,
		KafkaTopicReplicas:     32768,
		Timeout:                time.Second,
	}, "fit int16")
}

func TestValidateConfigRejectsRemoteLoopbackEndpoints(t *testing.T) {
	config := Config{
		Targets:                []string{runconfig.PreflightTargetPostgres, runconfig.PreflightTargetNATS, runconfig.PreflightTargetKafka},
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
	requireConfigError(t, config, "BUDGIE_NATS_URL")

	config.NATSURL = "nats://nats.internal:4222"
	config.KafkaBrokers = "127.0.0.1:9092"
	requireConfigError(t, config, "BUDGIE_KAFKA_BROKERS")

	config.KafkaBrokers = "[::1]:9092"
	requireConfigError(t, config, "BUDGIE_KAFKA_BROKERS")

	config.KafkaBrokers = "redpanda.internal:9092"
	config.PostgresDSN = "host=localhost port=55432 dbname=budgie_staging"
	requireConfigError(t, config, "BUDGIE_POSTGRES_DSN")
}

func requireConfigError(t *testing.T, config Config, want string) {
	t.Helper()
	err := ValidateConfig(config)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateConfig err = %v, want containing %q", err, want)
	}
}

func TestNamesUsePromotedLoadPrefixes(t *testing.T) {
	id := ID("Unit-Test")
	if id != "unit_test" {
		t.Fatalf("preflight id = %q, want sanitized underscore id", id)
	}
	config := Config{ID: id}
	if schema := PostgresSchema(config); !strings.HasPrefix(schema, loadmodel.CommandLogLoadPostgresSchemaPrefix+"_") {
		t.Fatalf("schema = %q, want promoted load schema prefix", schema)
	}
	if topic := KafkaTopics(config)[0]; !strings.HasPrefix(topic, loadmodel.CommandLogLoadKafkaCommandTopicPrefix) {
		t.Fatalf("topic = %q, want promoted command topic prefix", topic)
	}
	if stream := NATSStreams(config)[0]; !strings.HasPrefix(stream, loadmodel.CommandLogLoadCommandNATSStreamPrefix) {
		t.Fatalf("stream = %q, want promoted command stream prefix", stream)
	}
}
