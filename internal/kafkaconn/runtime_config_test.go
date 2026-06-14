package kafkaconn

import (
	"reflect"
	"strings"
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

func TestRuntimeConfigValidatesCommandEventTransactionTarget(t *testing.T) {
	config := RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "writers")
	if err := config.ValidateCommandEventTransaction(); err != nil {
		t.Fatalf("ValidateCommandEventTransaction: %v", err)
	}
}

func TestRuntimeConfigRequiresBrokers(t *testing.T) {
	err := RuntimeConfigFromFlags("", "", "", "").ValidateCommandLog()
	if err == nil || !strings.Contains(err.Error(), "broker list is required") {
		t.Fatalf("ValidateCommandLog err = %v, want missing broker list", err)
	}
}

func TestRuntimeConfigRequiresDistinctCommandAndEventTopics(t *testing.T) {
	err := RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "writers").ValidateCommandEventTransaction()
	if err == nil || !strings.Contains(err.Error(), "command and event topics must be distinct") {
		t.Fatalf("ValidateCommandEventTransaction err = %v, want distinct topic error", err)
	}
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
	if err == nil || !strings.Contains(err.Error(), "SASL mechanism is required") {
		t.Fatalf("ValidateCommandLog with user/pass but no mechanism err = %v, want mechanism error", err)
	}

	err = RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "oauthbearer",
		SASLUser:      "budgie",
		SASLPassword:  "secret",
	}).ValidateCommandLog()
	if err == nil || !strings.Contains(err.Error(), "unsupported SASL mechanism") {
		t.Fatalf("ValidateCommandLog unsupported mechanism err = %v, want mechanism error", err)
	}

	err = RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "scram-sha-256",
		SASLUser:      "budgie",
	}).ValidateCommandLog()
	if err == nil || !strings.Contains(err.Error(), "SASL password is required") {
		t.Fatalf("ValidateCommandLog missing password err = %v, want password error", err)
	}
}
