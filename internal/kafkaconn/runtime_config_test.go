package kafkaconn

import (
	"flag"
	"reflect"
	"testing"
)

func TestRuntimeConfigFromFlagsNormalizesBrokersAndDefaults(t *testing.T) {
	config := RuntimeConfigFromFlags(" redpanda-a:9092,redpanda-b:9092,redpanda-a:9092 ,, ", "", "", "")
	if !reflect.DeepEqual(config.Brokers, []string{"redpanda-a:9092", "redpanda-b:9092"}) {
		t.Fatalf("brokers = %+v, want normalized unique broker list", config.Brokers)
	}
	if config.CommandTopic != DefaultCommandTopic {
		t.Fatalf("command topic = %q, want default %q", config.CommandTopic, DefaultCommandTopic)
	}
	if config.EventTopic != DefaultEventTopic {
		t.Fatalf("event topic = %q, want default %q", config.EventTopic, DefaultEventTopic)
	}
	if config.ConsumerGroup != DefaultWriterConsumerGroup {
		t.Fatalf("consumer group = %q, want default %q", config.ConsumerGroup, DefaultWriterConsumerGroup)
	}
}

func TestRuntimeSecurityConfigFromEnv(t *testing.T) {
	t.Setenv("BUDGIE_KAFKA_TLS", "1")
	t.Setenv("BUDGIE_KAFKA_TLS_CA_FILE", "/tmp/ca.pem")
	t.Setenv("BUDGIE_KAFKA_TLS_SERVER_NAME", "redpanda.staging.internal")
	t.Setenv("BUDGIE_KAFKA_SASL_MECHANISM", "SCRAM-SHA512")
	t.Setenv("BUDGIE_KAFKA_SASL_USER", " budgie ")
	t.Setenv("BUDGIE_KAFKA_SASL_PASSWORD", "secret")

	security := RuntimeSecurityConfigFromEnv()
	config := RuntimeConfigFromOptions("redpanda:9092", "", "", "", security)
	if !config.TLS || config.TLSCAFile != "/tmp/ca.pem" || config.TLSServerName != "redpanda.staging.internal" {
		t.Fatalf("TLS config = %+v, want env-derived TLS settings", config)
	}
	if config.SASLMechanism != "scram-sha-512" || config.SASLUser != "budgie" || config.SASLPassword != "secret" {
		t.Fatalf("SASL config = mechanism:%q user:%q password:%q, want normalized env-derived SASL settings", config.SASLMechanism, config.SASLUser, config.SASLPassword)
	}
}

func TestRegisterRuntimeSecurityFlags(t *testing.T) {
	t.Setenv("BUDGIE_KAFKA_TLS", "1")
	t.Setenv("BUDGIE_KAFKA_TLS_CA_FILE", "/tmp/env-ca.pem")
	t.Setenv("BUDGIE_KAFKA_SASL_MECHANISM", "plain")
	t.Setenv("BUDGIE_KAFKA_SASL_USER", "env-user")
	t.Setenv("BUDGIE_KAFKA_SASL_PASSWORD", "env-secret")

	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	securityFlags := RegisterRuntimeSecurityFlags(flags)
	if err := flags.Parse([]string{
		"-kafka-tls-ca-file", "/tmp/flag-ca.pem",
		"-kafka-sasl-user", "flag-user",
	}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	security := securityFlags.Config()
	if !security.TLS || security.TLSCAFile != "/tmp/flag-ca.pem" {
		t.Fatalf("TLS security = %+v, want env TLS and flag CA override", security)
	}
	if security.SASLMechanism != "plain" || security.SASLUser != "flag-user" || security.SASLPassword != "env-secret" {
		t.Fatalf("SASL security = %+v, want mixed env/flag values", security)
	}
}

func TestRuntimeConfigValidatesCommandEventTransactionTarget(t *testing.T) {
	config := RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "writers")
	if err := config.ValidateCommandEventTransaction(); err != nil {
		t.Fatalf("ValidateCommandEventTransaction: %v", err)
	}
}

func TestRuntimeConfigRequiresBrokers(t *testing.T) {
	err := RuntimeConfigFromFlags("", "", "", "").ValidateCommandLog()
	requireErrorContains(t, err, "broker list is required")
}

func TestRuntimeConfigRequiresDistinctCommandAndEventTopics(t *testing.T) {
	err := RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "writers").ValidateCommandEventTransaction()
	requireErrorContains(t, err, "command and event topics must be distinct")
}

func TestValidateRuntimePartitions(t *testing.T) {
	if err := ValidateCommandPartitions(1); err != nil {
		t.Fatalf("ValidateCommandPartitions positive: %v", err)
	}
	requireErrorContains(t, ValidateCommandPartitions(0), "-kafka-command-partitions")
	if err := ValidateEventPartitions(1); err != nil {
		t.Fatalf("ValidateEventPartitions positive: %v", err)
	}
	requireErrorContains(t, ValidateEventPartitions(0), "-kafka-event-partitions")

	config := RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "writers")
	if err := config.ValidateCommandLogRuntime(32); err != nil {
		t.Fatalf("ValidateCommandLogRuntime: %v", err)
	}
	requireErrorContains(t, config.ValidateCommandLogRuntime(0), "-kafka-command-partitions")
	if err := config.ValidateEventLogRuntime(32); err != nil {
		t.Fatalf("ValidateEventLogRuntime: %v", err)
	}
	requireErrorContains(t, config.ValidateEventLogRuntime(0), "-kafka-event-partitions")
	if err := config.ValidateCommandEventRuntime(32, 32); err != nil {
		t.Fatalf("ValidateCommandEventRuntime: %v", err)
	}
	requireErrorContains(t, config.ValidateCommandEventRuntime(32, 0), "-kafka-event-partitions")
	requireErrorContains(t, RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "writers").ValidateCommandEventRuntime(32, 32), "command and event topics must be distinct")
}

func TestRuntimeConfigValidatesKafkaSecurity(t *testing.T) {
	config := RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "plain",
		SASLUser:      "budgie",
		SASLPassword:  "secret",
	})
	if err := config.ValidateCommandLog(); err != nil {
		t.Fatalf("ValidateCommandLog with SASL: %v", err)
	}
	if config.SASLMechanism != "plain" {
		t.Fatalf("SASL mechanism = %q, want plain", config.SASLMechanism)
	}

	err := RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLUser:     "budgie",
		SASLPassword: "secret",
	}).ValidateCommandLog()
	requireErrorContains(t, err, "SASL mechanism is required")

	err = RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "oauthbearer",
		SASLUser:      "budgie",
		SASLPassword:  "secret",
	}).ValidateCommandLog()
	requireErrorContains(t, err, "unsupported SASL mechanism")

	err = RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "scram-sha-256",
		SASLUser:      "budgie",
	}).ValidateCommandLog()
	requireErrorContains(t, err, "SASL password is required")
}
