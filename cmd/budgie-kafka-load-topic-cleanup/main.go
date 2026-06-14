package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
)

const (
	defaultCommandLoadTopicPrefix = "budgie.commands.load."
	defaultEventLoadTopicPrefix   = "budgie.events.load."
)

var (
	listKafkaTopics = func(ctx context.Context, options kafkaconn.TopicListOptions) ([]string, error) {
		return kafkaconn.ListTopics(ctx, options)
	}
	deleteKafkaTopics = func(ctx context.Context, options kafkaconn.TopicDeletionOptions) error {
		return kafkaconn.DeleteTopics(ctx, options)
	}
)

func main() {
	os.Exit(run())
}

func run() int {
	kafkaSecurityDefaults := kafkaconn.RuntimeSecurityConfigFromEnv()
	var (
		kafkaBrokers       = flag.String("kafka-brokers", envOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers")
		commandTopicPrefix = flag.String("command-topic-prefix", defaultCommandLoadTopicPrefix, "Command load-topic prefix eligible for cleanup")
		eventTopicPrefix   = flag.String("event-topic-prefix", defaultEventLoadTopicPrefix, "Event load-topic prefix eligible for cleanup")
		kafkaTLS           = flag.Bool("kafka-tls", kafkaSecurityDefaults.TLS, "Enable TLS for Kafka/Redpanda connections (also read from BUDGIE_KAFKA_TLS)")
		kafkaTLSCAFile     = flag.String("kafka-tls-ca-file", kafkaSecurityDefaults.TLSCAFile, "Optional PEM CA bundle for Kafka/Redpanda TLS (also read from BUDGIE_KAFKA_TLS_CA_FILE)")
		kafkaTLSServerName = flag.String("kafka-tls-server-name", kafkaSecurityDefaults.TLSServerName, "Optional TLS server name override for Kafka/Redpanda (also read from BUDGIE_KAFKA_TLS_SERVER_NAME)")
		kafkaSASLMechanism = flag.String("kafka-sasl-mechanism", kafkaSecurityDefaults.SASLMechanism, "Kafka/Redpanda SASL mechanism: plain, scram-sha-256, or scram-sha-512 (also read from BUDGIE_KAFKA_SASL_MECHANISM)")
		kafkaSASLUser      = flag.String("kafka-sasl-user", kafkaSecurityDefaults.SASLUser, "Kafka/Redpanda SASL user (also read from BUDGIE_KAFKA_SASL_USER)")
		kafkaSASLPassword  = flag.String("kafka-sasl-password", kafkaSecurityDefaults.SASLPassword, "Kafka/Redpanda SASL password (also read from BUDGIE_KAFKA_SASL_PASSWORD)")
		execute            = flag.Bool("execute", false, "Delete matching load topics; defaults to dry-run only")
		timeout            = flag.Duration("timeout", 30*time.Second, "Maximum duration for listing or deleting topics")
	)
	flag.Parse()
	if flag.NArg() != 0 {
		log.Printf("unsupported argument %q; use flags only", flag.Arg(0))
		return 2
	}
	config := kafkaLoadTopicCleanupConfig{
		Brokers:            *kafkaBrokers,
		CommandTopicPrefix: *commandTopicPrefix,
		EventTopicPrefix:   *eventTopicPrefix,
		Security: kafkaconn.RuntimeSecurityConfig{
			TLS:           *kafkaTLS,
			TLSCAFile:     *kafkaTLSCAFile,
			TLSServerName: *kafkaTLSServerName,
			SASLMechanism: *kafkaSASLMechanism,
			SASLUser:      *kafkaSASLUser,
			SASLPassword:  *kafkaSASLPassword,
		},
		Execute: *execute,
		Timeout: *timeout,
	}
	if err := validateKafkaLoadTopicCleanupConfig(config); err != nil {
		log.Print(err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runKafkaLoadTopicCleanup(ctx, config); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

type kafkaLoadTopicCleanupConfig struct {
	Brokers            string
	CommandTopicPrefix string
	EventTopicPrefix   string
	Security           kafkaconn.RuntimeSecurityConfig
	Execute            bool
	Timeout            time.Duration
}

func validateKafkaLoadTopicCleanupConfig(config kafkaLoadTopicCleanupConfig) error {
	if strings.TrimSpace(config.Brokers) == "" {
		return fmt.Errorf("-kafka-brokers or BUDGIE_KAFKA_BROKERS is required")
	}
	if strings.TrimSpace(config.CommandTopicPrefix) == "" {
		return fmt.Errorf("-command-topic-prefix is required")
	}
	if strings.TrimSpace(config.EventTopicPrefix) == "" {
		return fmt.Errorf("-event-topic-prefix is required")
	}
	if strings.TrimSpace(config.CommandTopicPrefix) == strings.TrimSpace(config.EventTopicPrefix) {
		return fmt.Errorf("command and event topic cleanup prefixes must be distinct")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if err := kafkaconn.RuntimeConfigFromOptions(config.Brokers, "", "", "", config.Security).ValidateSecurity(); err != nil {
		return err
	}
	return nil
}

func runKafkaLoadTopicCleanup(ctx context.Context, config kafkaLoadTopicCleanupConfig) error {
	runtime := kafkaconn.RuntimeConfigFromOptions(config.Brokers, "", "", "", config.Security)
	topics, err := listKafkaTopics(ctx, kafkaconn.TopicListOptions{
		Runtime:  runtime,
		ClientID: fmt.Sprintf("budgie-kafka-load-topic-cleanup-%d-list", os.Getpid()),
		Timeout:  config.Timeout,
	})
	if err != nil {
		return err
	}
	loadTopics, err := selectKafkaLoadTopics(topics, config.CommandTopicPrefix, config.EventTopicPrefix)
	if err != nil {
		return err
	}
	if len(loadTopics) == 0 {
		fmt.Println("==> no disposable Kafka load topics found")
		return nil
	}

	fmt.Println("==> disposable Kafka load topics:")
	for _, topic := range loadTopics {
		fmt.Printf("    %s\n", topic)
	}
	if !config.Execute {
		fmt.Println("==> dry run only; pass --execute to delete these load topics")
		return nil
	}
	fmt.Println("==> deleting Kafka load topics")
	if err := deleteKafkaTopics(ctx, kafkaconn.TopicDeletionOptions{
		Runtime:       runtime,
		ClientID:      fmt.Sprintf("budgie-kafka-load-topic-cleanup-%d-delete", os.Getpid()),
		Topics:        loadTopics,
		Timeout:       config.Timeout,
		IgnoreMissing: true,
	}); err != nil {
		return err
	}
	fmt.Println("==> Kafka load topic cleanup complete")
	return nil
}

func selectKafkaLoadTopics(topics []string, commandPrefix, eventPrefix string) ([]string, error) {
	commandPrefix = strings.TrimSpace(commandPrefix)
	eventPrefix = strings.TrimSpace(eventPrefix)
	if commandPrefix == "" || eventPrefix == "" {
		return nil, fmt.Errorf("Kafka load-topic cleanup prefixes must be non-empty")
	}
	if commandPrefix == eventPrefix {
		return nil, fmt.Errorf("Kafka load-topic cleanup prefixes must be distinct")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" || seen[topic] {
			continue
		}
		if strings.HasPrefix(topic, commandPrefix) || strings.HasPrefix(topic, eventPrefix) {
			seen[topic] = true
			out = append(out, topic)
		}
	}
	sort.Strings(out)
	return out, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
