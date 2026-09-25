package preflightcheck

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	nats "github.com/nats-io/nats.go"
)

type Config struct {
	Targets                []string
	RemoteStaging          bool
	NATSURL                string
	NATSReplicas           int
	KafkaBrokers           string
	KafkaCommandPartitions int32
	KafkaEventPartitions   int32
	KafkaTopicReplicas     int
	KafkaSecurity          kafkaconn.RuntimeSecurityConfig
	PostgresDSN            string
	ID                     string
	Timeout                time.Duration
	Logf                   func(string, ...any)
}

type ProbeSpec struct {
	Target    string
	Name      string
	Resources []string
}

func ValidateConfig(config Config) error {
	targets := runconfig.TargetSet(config.Targets)
	postgresDSN := strings.TrimSpace(config.PostgresDSN)
	natsURL := strings.TrimSpace(config.NATSURL)
	kafkaBrokers := strings.TrimSpace(config.KafkaBrokers)
	if len(config.Targets) == 0 {
		return fmt.Errorf("no preflight targets selected; set BUDGIE_NATS_URL, BUDGIE_KAFKA_BROKERS, BUDGIE_POSTGRES_DSN, or -targets")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if targets[runconfig.PreflightTargetPostgres] && postgresDSN == "" {
		return fmt.Errorf("postgres preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
	}
	if targets[runconfig.PreflightTargetNATS] {
		if natsURL == "" {
			return fmt.Errorf("nats preflight requires -nats or BUDGIE_NATS_URL")
		}
		if postgresDSN == "" {
			return fmt.Errorf("nats staging preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
		}
		if config.NATSReplicas <= 0 {
			return fmt.Errorf("-nats-replicas must be positive")
		}
	}
	if targets[runconfig.PreflightTargetKafka] {
		if kafkaBrokers == "" {
			return fmt.Errorf("kafka preflight requires -kafka-brokers or BUDGIE_KAFKA_BROKERS")
		}
		if postgresDSN == "" {
			return fmt.Errorf("kafka staging preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
		}
		if config.KafkaCommandPartitions <= 0 || config.KafkaEventPartitions <= 0 {
			return fmt.Errorf("kafka preflight requires positive command and event partition counts")
		}
		if _, err := kafkaconn.TopicReplicationFactor(config.KafkaTopicReplicas); err != nil {
			return fmt.Errorf("-kafka-topic-replicas: %w", err)
		}
		if err := kafkaconn.RuntimeConfigFromOptions(config.KafkaBrokers, "", "", "", config.KafkaSecurity).ValidateSecurity(); err != nil {
			return err
		}
	}
	if config.RemoteStaging {
		if targets[runconfig.PreflightTargetPostgres] && runevidence.EndpointHostIsLocal(config.PostgresDSN) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_POSTGRES_DSN evidence; got %s", runevidence.SanitizeEndpoint(config.PostgresDSN, "postgres"))
		}
		if targets[runconfig.PreflightTargetNATS] && runevidence.EndpointHostIsLocal(config.NATSURL) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_NATS_URL evidence; got %s", runevidence.SanitizeEndpoint(config.NATSURL, "nats"))
		}
		if targets[runconfig.PreflightTargetKafka] && runevidence.KafkaBrokerListHasLocal(config.KafkaBrokers) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_KAFKA_BROKERS evidence; got %s", strings.Join(runevidence.SanitizeKafkaBrokers(config.KafkaBrokers), ","))
		}
	}
	return nil
}

func ProbePostgres(ctx context.Context, config Config) error {
	db, err := core.OpenPostgres(config.PostgresDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	schema := PostgresSchema(config)
	if !runconfig.ValidSchemaName(schema) {
		return fmt.Errorf("invalid generated schema %q", schema)
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		return fmt.Errorf("create schema %s: %w", schema, err)
	}
	defer dropSchema(context.Background(), db, schema)
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+schema+".probe (id integer PRIMARY KEY)"); err != nil {
		return fmt.Errorf("create probe table in %s: %w", schema, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO "+schema+".probe (id) VALUES (1)"); err != nil {
		return fmt.Errorf("insert probe row in %s: %w", schema, err)
	}
	if _, err := db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
		return fmt.Errorf("drop schema %s: %w", schema, err)
	}
	return nil
}

func ProbeNATS(ctx context.Context, config Config) error {
	js, cleanup, err := natsconn.OpenJetStreamContext(config.NATSURL, natsconn.JetStreamContextOptions{
		Name:           "budgie-bbs-internet-scale-preflight",
		ConnectTimeout: runconfig.MinDuration(5*time.Second, config.Timeout),
		MaxWait:        runconfig.MinDuration(10*time.Second, config.Timeout),
	})
	if err != nil {
		return err
	}
	defer cleanup()
	streams := NATSStreams(config)
	commandStream := streams[0]
	eventStream := streams[1]
	commandConfig := natsconn.JetStreamCommandLogStreamConfig(natsconn.JetStreamCommandLogOptions{
		Stream:          commandStream,
		Replicas:        config.NATSReplicas,
		DuplicateWindow: time.Hour,
	})
	if err := addStream(ctx, js, &commandConfig); err != nil {
		return fmt.Errorf("create command stream %s: %w", commandStream, err)
	}
	defer deleteStream(context.Background(), js, commandStream, config.Logf)
	eventConfig := natsconn.JetStreamEventLogStreamConfig(natsconn.JetStreamEventLogOptions{
		Stream:          eventStream,
		Replicas:        config.NATSReplicas,
		DuplicateWindow: time.Hour,
	})
	if err := addStream(ctx, js, &eventConfig); err != nil {
		return fmt.Errorf("create event stream %s: %w", eventStream, err)
	}
	defer deleteStream(context.Background(), js, eventStream, config.Logf)
	return nil
}

func ProbeKafka(ctx context.Context, config Config) error {
	topics := KafkaTopics(config)
	commandTopic := topics[0]
	eventTopic := topics[1]
	runtime := kafkaconn.RuntimeConfigFromOptions(
		config.KafkaBrokers,
		commandTopic,
		eventTopic,
		"budgie-writers-preflight-"+config.ID,
		config.KafkaSecurity,
	)
	topicSpecs, err := kafkaconn.CommandEventTopicProvisioningSpecs(
		runtime,
		config.KafkaCommandPartitions,
		config.KafkaEventPartitions,
		config.KafkaTopicReplicas,
	)
	if err != nil {
		return err
	}
	defer func() {
		if err := kafkaconn.DeleteTopics(context.Background(), kafkaconn.TopicDeletionOptions{
			Runtime:       runtime,
			ClientID:      "budgie-internet-scale-preflight-" + config.ID + "-delete",
			Topics:        []string{commandTopic, eventTopic},
			Timeout:       30 * time.Second,
			IgnoreMissing: true,
		}); err != nil && config.Logf != nil {
			config.Logf("cleanup Kafka preflight topics: %v", err)
		}
	}()
	return kafkaconn.EnsureTopics(ctx, kafkaconn.TopicProvisioningOptions{
		Runtime:  runtime,
		ClientID: "budgie-internet-scale-preflight-" + config.ID + "-create",
		Topics:   topicSpecs,
		Timeout:  runconfig.MinDuration(30*time.Second, config.Timeout),
	})
}

func PostgresSchema(config Config) string {
	return loadmodel.CommandLogLoadPostgresSchemaPrefix + "_preflight_" + strings.TrimSpace(config.ID)
}

func NATSStreams(config Config) []string {
	id := strings.ToUpper(strings.TrimSpace(config.ID))
	return []string{
		loadmodel.CommandLogLoadCommandNATSStreamPrefix + "PREFLIGHT_" + id,
		loadmodel.CommandLogLoadEventNATSStreamPrefix + "PREFLIGHT_" + id,
	}
}

func KafkaTopics(config Config) []string {
	id := strings.TrimSpace(config.ID)
	return []string{
		loadmodel.CommandLogLoadKafkaCommandTopicPrefix + "preflight." + id,
		loadmodel.CommandLogLoadKafkaEventTopicPrefix + "preflight." + id,
	}
}

func ProbeSpecs(config Config) []ProbeSpec {
	return []ProbeSpec{
		{Target: runconfig.PreflightTargetPostgres, Name: "Postgres disposable schema create/drop", Resources: []string{PostgresSchema(config)}},
		{Target: runconfig.PreflightTargetNATS, Name: "NATS JetStream command/event stream create/delete", Resources: NATSStreams(config)},
		{Target: runconfig.PreflightTargetKafka, Name: "Kafka command/event topic create/delete", Resources: KafkaTopics(config)},
	}
}

func NewReport(config Config) *preflightmodel.Report {
	targets := runconfig.TargetSet(config.Targets)
	report := &preflightmodel.Report{
		Config: preflightmodel.Config{
			Targets:       append([]string(nil), config.Targets...),
			RemoteStaging: config.RemoteStaging,
			ID:            config.ID,
			TimeoutMS:     config.Timeout.Milliseconds(),
		},
		Evidence:  runevidence.CollectForTool("budgie-internet-scale-preflight", ""),
		StartedAt: time.Now().UnixMilli(),
	}
	if targets[runconfig.PreflightTargetPostgres] {
		report.Runtime.PostgresEndpoint = runevidence.SanitizeEndpoint(config.PostgresDSN, "postgres")
	}
	if targets[runconfig.PreflightTargetNATS] {
		report.Runtime.NATSEndpoint = runevidence.SanitizeEndpoint(config.NATSURL, "nats")
		report.Runtime.NATSReplicas = config.NATSReplicas
	}
	if targets[runconfig.PreflightTargetKafka] {
		report.Runtime.KafkaBrokers = runevidence.SanitizeKafkaBrokers(config.KafkaBrokers)
		report.Runtime.KafkaTLS = config.KafkaSecurity.TLS
		report.Runtime.KafkaSASLMechanism = strings.TrimSpace(config.KafkaSecurity.SASLMechanism)
		report.Runtime.KafkaCommandPartitions = config.KafkaCommandPartitions
		report.Runtime.KafkaEventPartitions = config.KafkaEventPartitions
		if replicas, err := kafkaconn.TopicReplicationFactor(config.KafkaTopicReplicas); err == nil {
			report.Runtime.KafkaTopicReplicas = replicas
		}
	}
	return report
}

func ID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		raw = fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano())
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			if r == '-' {
				b.WriteByte('_')
			} else {
				b.WriteRune(r)
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano())
	}
	return out
}

func dropSchema(ctx context.Context, db *sql.DB, schema string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
}

func addStream(ctx context.Context, js nats.JetStreamContext, cfg *nats.StreamConfig) error {
	if js == nil {
		return fmt.Errorf("nil JetStream context")
	}
	if _, err := js.AddStream(cfg, nats.Context(ctx)); err != nil {
		return err
	}
	if _, err := js.StreamInfo(cfg.Name, nats.Context(ctx)); err != nil {
		return err
	}
	return nil
}

func deleteStream(ctx context.Context, js nats.JetStreamContext, stream string, logf func(string, ...any)) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := js.DeleteStream(stream, nats.Context(ctx)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "stream not found") && logf != nil {
		logf("cleanup NATS stream %s: %v", stream, err)
	}
}
