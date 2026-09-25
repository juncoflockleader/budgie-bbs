package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
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
	var (
		kafkaBrokers       = flag.String("kafka-brokers", runconfig.EnvOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers")
		commandTopicPrefix = flag.String("command-topic-prefix", loadmodel.CommandLogLoadKafkaCommandTopicPrefix, "Command load-topic prefix eligible for cleanup")
		eventTopicPrefix   = flag.String("event-topic-prefix", loadmodel.CommandLogLoadKafkaEventTopicPrefix, "Event load-topic prefix eligible for cleanup")
		kafkaSecurityFlags = kafkaconn.RegisterRuntimeSecurityFlags(flag.CommandLine)
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
		Security:           kafkaSecurityFlags.Config(),
		Execute:            *execute,
		Timeout:            *timeout,
	}
	if err := validateKafkaLoadTopicCleanupConfig(config); err != nil {
		log.Print(err)
		return 2
	}

	ctx, cancel := runconfig.InterruptTimeoutContext(context.Background(), config.Timeout)
	defer cancel()
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
	loadTopics, err := loadmodel.SelectCommandLogLoadKafkaTopics(topics, config.CommandTopicPrefix, config.EventTopicPrefix)
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
