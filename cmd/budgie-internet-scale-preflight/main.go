package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	nats "github.com/nats-io/nats.go"
)

const (
	targetPostgres = "postgres"
	targetNATS     = "nats"
	targetKafka    = "kafka"
)

var (
	preflightSchemaNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	runPostgresPreflightProbe  = probePostgres
	runNATSPreflightProbe      = probeNATS
	runKafkaPreflightProbe     = probeKafka
)

type preflightConfig struct {
	Targets                []string
	RemoteStaging          bool
	NATSURL                string
	NATSReplicas           int
	KafkaBrokers           string
	KafkaCommandPartitions int32
	KafkaEventPartitions   int32
	KafkaTopicReplicas     int16
	KafkaSecurity          kafkaconn.RuntimeSecurityConfig
	PostgresDSN            string
	ID                     string
	Timeout                time.Duration
	ReportFile             string
}

type preflightReport struct {
	Config     preflightReportConfig  `json:"config"`
	Runtime    preflightReportRuntime `json:"runtime"`
	Evidence   preflightEvidence      `json:"evidence"`
	StartedAt  int64                  `json:"startedAt"`
	FinishedAt int64                  `json:"finishedAt"`
	Passed     bool                   `json:"passed"`
	Probes     []preflightProbeReport `json:"probes"`
}

type preflightReportConfig struct {
	Targets       []string `json:"targets"`
	RemoteStaging bool     `json:"remoteStaging"`
	ID            string   `json:"id"`
	TimeoutMS     int64    `json:"timeoutMs"`
}

type preflightReportRuntime struct {
	PostgresEndpoint       string   `json:"postgresEndpoint,omitempty"`
	NATSEndpoint           string   `json:"natsEndpoint,omitempty"`
	NATSReplicas           int      `json:"natsReplicas,omitempty"`
	KafkaBrokers           []string `json:"kafkaBrokers,omitempty"`
	KafkaTLS               bool     `json:"kafkaTls,omitempty"`
	KafkaSASLMechanism     string   `json:"kafkaSaslMechanism,omitempty"`
	KafkaCommandPartitions int32    `json:"kafkaCommandPartitions,omitempty"`
	KafkaEventPartitions   int32    `json:"kafkaEventPartitions,omitempty"`
	KafkaTopicReplicas     int16    `json:"kafkaTopicReplicas,omitempty"`
}

type preflightEvidence struct {
	Tool        string `json:"tool"`
	GitRevision string `json:"gitRevision,omitempty"`
	GitModified bool   `json:"gitModified"`
}

type preflightProbeReport struct {
	Target     string   `json:"target"`
	Name       string   `json:"name"`
	Resources  []string `json:"resources,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt"`
	Passed     bool     `json:"passed"`
	Error      string   `json:"error,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	kafkaSecurityDefaults := kafkaconn.RuntimeSecurityConfigFromEnv()
	flags := flag.NewFlagSet("budgie-internet-scale-preflight", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	targets := flags.String("targets", envOr("BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS", ""), "Comma-separated targets to probe: postgres,nats,kafka,all; defaults from env")
	remoteStaging := flags.Bool("remote-staging", envBool("BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING", envBool("BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING", false)), "Reject loopback endpoints and use as remote/shared staging preflight")
	natsURL := flags.String("nats", envOr("BUDGIE_NATS_URL", ""), "NATS URL for JetStream command/event stream create-delete probe")
	natsReplicas := flags.Int("nats-replicas", envInt("BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS", 1), "NATS stream replica count for preflight streams")
	kafkaBrokers := flags.String("kafka-brokers", envOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers")
	kafkaCommandPartitions := flags.Int("kafka-command-partitions", envInt("BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS", 32), "Kafka command topic partitions for preflight")
	kafkaEventPartitions := flags.Int("kafka-event-partitions", envInt("BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS", 32), "Kafka event topic partitions for preflight")
	kafkaTopicReplicas := flags.Int("kafka-topic-replicas", envInt("BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS", 1), "Kafka topic replication factor for preflight")
	kafkaTLS := flags.Bool("kafka-tls", kafkaSecurityDefaults.TLS, "Enable TLS for Kafka/Redpanda connections")
	kafkaTLSCAFile := flags.String("kafka-tls-ca-file", kafkaSecurityDefaults.TLSCAFile, "Optional PEM CA bundle for Kafka/Redpanda TLS")
	kafkaTLSServerName := flags.String("kafka-tls-server-name", kafkaSecurityDefaults.TLSServerName, "Optional TLS server name override for Kafka/Redpanda")
	kafkaSASLMechanism := flags.String("kafka-sasl-mechanism", kafkaSecurityDefaults.SASLMechanism, "Kafka/Redpanda SASL mechanism")
	kafkaSASLUser := flags.String("kafka-sasl-user", kafkaSecurityDefaults.SASLUser, "Kafka/Redpanda SASL user")
	kafkaSASLPassword := flags.String("kafka-sasl-password", kafkaSecurityDefaults.SASLPassword, "Kafka/Redpanda SASL password")
	postgresDSN := flags.String("postgres-dsn", envOr("BUDGIE_POSTGRES_DSN", ""), "Postgres DSN for disposable schema create-delete probe")
	id := flags.String("id", "", "Optional probe id suffix; defaults to pid plus timestamp")
	timeout := flags.Duration("timeout", envDuration("BUDGIE_INTERNET_SCALE_PREFLIGHT_TIMEOUT", 45*time.Second), "Maximum total preflight duration")
	reportFile := flags.String("report-file", envOr("BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT", ""), "Optional JSON report path written only after a successful preflight")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		log.Printf("unsupported argument %q; use flags only", flags.Arg(0))
		return 2
	}

	targetList, err := normalizePreflightTargets(*targets, *natsURL, *kafkaBrokers, *postgresDSN)
	if err != nil {
		log.Print(err)
		return 2
	}
	config := preflightConfig{
		Targets:                targetList,
		RemoteStaging:          *remoteStaging,
		NATSURL:                *natsURL,
		NATSReplicas:           *natsReplicas,
		KafkaBrokers:           *kafkaBrokers,
		KafkaCommandPartitions: int32(*kafkaCommandPartitions),
		KafkaEventPartitions:   int32(*kafkaEventPartitions),
		KafkaTopicReplicas:     int16(*kafkaTopicReplicas),
		KafkaSecurity: kafkaconn.RuntimeSecurityConfig{
			TLS:           *kafkaTLS,
			TLSCAFile:     *kafkaTLSCAFile,
			TLSServerName: *kafkaTLSServerName,
			SASLMechanism: *kafkaSASLMechanism,
			SASLUser:      *kafkaSASLUser,
			SASLPassword:  *kafkaSASLPassword,
		},
		PostgresDSN: strings.TrimSpace(*postgresDSN),
		ID:          preflightID(*id),
		Timeout:     *timeout,
		ReportFile:  strings.TrimSpace(*reportFile),
	}
	if err := validatePreflightConfig(config); err != nil {
		log.Print(err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	report := newPreflightReport(config)

	fmt.Printf("==> internet-scale staging preflight targets: %s\n", strings.Join(config.Targets, " "))
	if config.RemoteStaging {
		fmt.Println("    remote staging checks: enabled")
	} else {
		fmt.Println("    remote staging checks: disabled")
	}
	if hasPreflightTarget(config.Targets, targetPostgres) {
		fmt.Printf("    postgres endpoint: %s\n", sanitizedEndpoint(config.PostgresDSN, "postgres"))
	}
	if hasPreflightTarget(config.Targets, targetNATS) {
		fmt.Printf("    nats endpoint:     %s\n", sanitizedEndpoint(config.NATSURL, "nats"))
	}
	if hasPreflightTarget(config.Targets, targetKafka) {
		fmt.Printf("    kafka brokers:     %s\n", strings.Join(sanitizedKafkaBrokers(config.KafkaBrokers), ","))
	}

	if hasPreflightTarget(config.Targets, targetPostgres) {
		probe, err := runPreflightStep(targetPostgres, "Postgres disposable schema create/drop", []string{preflightPostgresSchema(config)}, func() error {
			return runPostgresPreflightProbe(ctx, config)
		})
		report.Probes = append(report.Probes, probe)
		if err != nil {
			report.FinishedAt = nowMS()
			return 1
		}
	}
	if hasPreflightTarget(config.Targets, targetNATS) {
		probe, err := runPreflightStep(targetNATS, "NATS JetStream command/event stream create/delete", preflightNATSStreams(config), func() error {
			return runNATSPreflightProbe(ctx, config)
		})
		report.Probes = append(report.Probes, probe)
		if err != nil {
			report.FinishedAt = nowMS()
			return 1
		}
	}
	if hasPreflightTarget(config.Targets, targetKafka) {
		probe, err := runPreflightStep(targetKafka, "Kafka command/event topic create/delete", preflightKafkaTopics(config), func() error {
			return runKafkaPreflightProbe(ctx, config)
		})
		report.Probes = append(report.Probes, probe)
		if err != nil {
			report.FinishedAt = nowMS()
			return 1
		}
	}
	report.Passed = true
	report.FinishedAt = nowMS()
	if config.ReportFile != "" {
		if err := writePreflightReport(config.ReportFile, report); err != nil {
			log.Printf("write preflight report: %v", err)
			return 1
		}
		fmt.Printf("==> archived preflight report at %s\n", config.ReportFile)
	}
	fmt.Println("==> internet-scale staging preflight passed")
	return 0
}

func runPreflightStep(target, label string, resources []string, run func() error) (preflightProbeReport, error) {
	report := preflightProbeReport{
		Target:    target,
		Name:      label,
		Resources: resources,
		StartedAt: nowMS(),
	}
	fmt.Printf("==> checking %s\n", label)
	if err := run(); err != nil {
		log.Printf("%s: %v", label, err)
		report.FinishedAt = nowMS()
		report.Error = err.Error()
		return report, err
	}
	report.FinishedAt = nowMS()
	report.Passed = true
	fmt.Printf("==> %s passed\n", label)
	return report, nil
}

func normalizePreflightTargets(raw, natsURL, kafkaBrokers, postgresDSN string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		targets := []string{}
		if strings.TrimSpace(postgresDSN) != "" {
			targets = append(targets, targetPostgres)
		}
		if strings.TrimSpace(natsURL) != "" {
			targets = append(targets, targetPostgres, targetNATS)
		}
		if strings.TrimSpace(kafkaBrokers) != "" {
			targets = append(targets, targetPostgres, targetKafka)
		}
		return dedupePreflightTargets(targets), nil
	}
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	targets := []string{}
	for _, field := range fields {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "all":
			targets = append(targets, targetPostgres, targetNATS, targetKafka)
		case targetPostgres, targetNATS, targetKafka:
			targets = append(targets, strings.ToLower(strings.TrimSpace(field)))
		case "":
		default:
			return nil, fmt.Errorf("unsupported preflight target %q; supported targets: postgres,nats,kafka,all", field)
		}
	}
	targets = dedupePreflightTargets(targets)
	if hasPreflightTarget(targets, targetNATS) || hasPreflightTarget(targets, targetKafka) {
		targets = dedupePreflightTargets(append([]string{targetPostgres}, targets...))
	}
	return targets, nil
}

func dedupePreflightTargets(in []string) []string {
	order := map[string]int{targetPostgres: 0, targetNATS: 1, targetKafka: 2}
	seen := map[string]bool{}
	out := []string{}
	for _, target := range in {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i]] < order[out[j]]
	})
	return out
}

func hasPreflightTarget(targets []string, target string) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}

func validatePreflightConfig(config preflightConfig) error {
	if len(config.Targets) == 0 {
		return fmt.Errorf("no preflight targets selected; set BUDGIE_NATS_URL, BUDGIE_KAFKA_BROKERS, BUDGIE_POSTGRES_DSN, or -targets")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if hasPreflightTarget(config.Targets, targetPostgres) && strings.TrimSpace(config.PostgresDSN) == "" {
		return fmt.Errorf("postgres preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
	}
	if hasPreflightTarget(config.Targets, targetNATS) {
		if strings.TrimSpace(config.NATSURL) == "" {
			return fmt.Errorf("nats preflight requires -nats or BUDGIE_NATS_URL")
		}
		if strings.TrimSpace(config.PostgresDSN) == "" {
			return fmt.Errorf("nats staging preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
		}
		if config.NATSReplicas <= 0 {
			return fmt.Errorf("-nats-replicas must be positive")
		}
	}
	if hasPreflightTarget(config.Targets, targetKafka) {
		if strings.TrimSpace(config.KafkaBrokers) == "" {
			return fmt.Errorf("kafka preflight requires -kafka-brokers or BUDGIE_KAFKA_BROKERS")
		}
		if strings.TrimSpace(config.PostgresDSN) == "" {
			return fmt.Errorf("kafka staging preflight requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
		}
		if config.KafkaCommandPartitions <= 0 || config.KafkaEventPartitions <= 0 {
			return fmt.Errorf("kafka preflight requires positive command and event partition counts")
		}
		if config.KafkaTopicReplicas <= 0 {
			return fmt.Errorf("-kafka-topic-replicas must be positive")
		}
		if err := kafkaconn.RuntimeConfigFromOptions(config.KafkaBrokers, "", "", "", config.KafkaSecurity).ValidateSecurity(); err != nil {
			return err
		}
	}
	if config.RemoteStaging {
		if hasPreflightTarget(config.Targets, targetPostgres) && endpointHostIsLocal(config.PostgresDSN) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_POSTGRES_DSN evidence; got %s", sanitizedEndpoint(config.PostgresDSN, "postgres"))
		}
		if hasPreflightTarget(config.Targets, targetNATS) && endpointHostIsLocal(config.NATSURL) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_NATS_URL evidence; got %s", sanitizedEndpoint(config.NATSURL, "nats"))
		}
		if hasPreflightTarget(config.Targets, targetKafka) && kafkaBrokerListHasLocal(config.KafkaBrokers) {
			return fmt.Errorf("remote staging preflight requires non-local BUDGIE_KAFKA_BROKERS evidence; got %s", strings.Join(sanitizedKafkaBrokers(config.KafkaBrokers), ","))
		}
	}
	return nil
}

func probePostgres(ctx context.Context, config preflightConfig) error {
	db, err := core.OpenPostgres(config.PostgresDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	schema := preflightPostgresSchema(config)
	if !preflightSchemaNamePattern.MatchString(schema) {
		return fmt.Errorf("invalid generated schema %q", schema)
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		return fmt.Errorf("create schema %s: %w", schema, err)
	}
	defer dropPreflightSchema(context.Background(), db, schema)
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

func dropPreflightSchema(ctx context.Context, db *sql.DB, schema string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
}

func probeNATS(ctx context.Context, config preflightConfig) error {
	nc, err := nats.Connect(config.NATSURL, nats.Name("budgie-bbs-internet-scale-preflight"), nats.Timeout(minDuration(5*time.Second, config.Timeout)))
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := nc.JetStream(nats.MaxWait(minDuration(10*time.Second, config.Timeout)))
	if err != nil {
		return err
	}
	streams := preflightNATSStreams(config)
	commandStream := streams[0]
	eventStream := streams[1]
	if err := addPreflightStream(ctx, js, &nats.StreamConfig{
		Name:        commandStream,
		Subjects:    []string{core.BrokerCommandSubjectWildcard(), core.BrokerCommandCommitSubjectWildcard()},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		Replicas:    config.NATSReplicas,
		AllowDirect: true,
		Duplicates:  time.Hour,
	}); err != nil {
		return fmt.Errorf("create command stream %s: %w", commandStream, err)
	}
	defer deletePreflightStream(context.Background(), js, commandStream)
	if err := addPreflightStream(ctx, js, &nats.StreamConfig{
		Name:        eventStream,
		Subjects:    []string{core.BrokerEventSubjectWildcard()},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		Replicas:    config.NATSReplicas,
		AllowDirect: true,
		Duplicates:  time.Hour,
	}); err != nil {
		return fmt.Errorf("create event stream %s: %w", eventStream, err)
	}
	defer deletePreflightStream(context.Background(), js, eventStream)
	return nil
}

func addPreflightStream(ctx context.Context, js nats.JetStreamContext, cfg *nats.StreamConfig) error {
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

func deletePreflightStream(ctx context.Context, js nats.JetStreamContext, stream string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := js.DeleteStream(stream, nats.Context(ctx)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "stream not found") {
		log.Printf("cleanup NATS stream %s: %v", stream, err)
	}
}

func probeKafka(ctx context.Context, config preflightConfig) error {
	topics := preflightKafkaTopics(config)
	commandTopic := topics[0]
	eventTopic := topics[1]
	runtime := kafkaconn.RuntimeConfigFromOptions(
		config.KafkaBrokers,
		commandTopic,
		eventTopic,
		"budgie-writers-preflight-"+config.ID,
		config.KafkaSecurity,
	)
	topicSpecs := []kafkaconn.TopicProvisioningSpec{
		{Topic: commandTopic, Partitions: config.KafkaCommandPartitions, ReplicationFactor: config.KafkaTopicReplicas},
		{Topic: eventTopic, Partitions: config.KafkaEventPartitions, ReplicationFactor: config.KafkaTopicReplicas},
	}
	defer func() {
		if err := kafkaconn.DeleteTopics(context.Background(), kafkaconn.TopicDeletionOptions{
			Runtime:       runtime,
			ClientID:      "budgie-internet-scale-preflight-" + config.ID + "-delete",
			Topics:        []string{commandTopic, eventTopic},
			Timeout:       30 * time.Second,
			IgnoreMissing: true,
		}); err != nil {
			log.Printf("cleanup Kafka preflight topics: %v", err)
		}
	}()
	if err := kafkaconn.EnsureTopics(ctx, kafkaconn.TopicProvisioningOptions{
		Runtime:  runtime,
		ClientID: "budgie-internet-scale-preflight-" + config.ID + "-create",
		Topics:   topicSpecs,
		Timeout:  minDuration(30*time.Second, config.Timeout),
	}); err != nil {
		return err
	}
	return nil
}

func preflightPostgresSchema(config preflightConfig) string {
	return "budgie_cmdlog_load_preflight_" + config.ID
}

func preflightNATSStreams(config preflightConfig) []string {
	id := strings.ToUpper(config.ID)
	return []string{
		"BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_" + id,
		"BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_" + id,
	}
}

func preflightKafkaTopics(config preflightConfig) []string {
	return []string{
		"budgie.commands.load.preflight." + config.ID,
		"budgie.events.load.preflight." + config.ID,
	}
}

func newPreflightReport(config preflightConfig) *preflightReport {
	report := &preflightReport{
		Config: preflightReportConfig{
			Targets:       append([]string(nil), config.Targets...),
			RemoteStaging: config.RemoteStaging,
			ID:            config.ID,
			TimeoutMS:     config.Timeout.Milliseconds(),
		},
		Evidence:  collectPreflightEvidence(),
		StartedAt: nowMS(),
	}
	if hasPreflightTarget(config.Targets, targetPostgres) {
		report.Runtime.PostgresEndpoint = sanitizedEndpoint(config.PostgresDSN, "postgres")
	}
	if hasPreflightTarget(config.Targets, targetNATS) {
		report.Runtime.NATSEndpoint = sanitizedEndpoint(config.NATSURL, "nats")
		report.Runtime.NATSReplicas = config.NATSReplicas
	}
	if hasPreflightTarget(config.Targets, targetKafka) {
		report.Runtime.KafkaBrokers = sanitizedKafkaBrokers(config.KafkaBrokers)
		report.Runtime.KafkaTLS = config.KafkaSecurity.TLS
		report.Runtime.KafkaSASLMechanism = strings.TrimSpace(config.KafkaSecurity.SASLMechanism)
		report.Runtime.KafkaCommandPartitions = config.KafkaCommandPartitions
		report.Runtime.KafkaEventPartitions = config.KafkaEventPartitions
		report.Runtime.KafkaTopicReplicas = config.KafkaTopicReplicas
	}
	return report
}

func collectPreflightEvidence() preflightEvidence {
	evidence := preflightEvidence{Tool: "budgie-internet-scale-preflight"}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				evidence.GitRevision = strings.TrimSpace(setting.Value)
			case "vcs.modified":
				evidence.GitModified = strings.EqualFold(strings.TrimSpace(setting.Value), "true")
			}
		}
	}
	if evidence.GitRevision == "" {
		if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
			evidence.GitRevision = strings.TrimSpace(string(out))
		}
	}
	if out, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		evidence.GitModified = strings.TrimSpace(string(out)) != ""
	}
	return evidence
}

func writePreflightReport(path string, report *preflightReport) error {
	if report == nil {
		return fmt.Errorf("nil preflight report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func preflightID(raw string) string {
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

func endpointHostIsLocal(endpoint string) bool {
	host := endpointHost(endpoint)
	switch host {
	case "", "localhost", "localhost.localdomain", "::1", "[::1]":
		return host != ""
	}
	if strings.HasPrefix(host, "localhost.") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func endpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" {
		return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	}
	fields := dsnFields(endpoint)
	host := strings.TrimSpace(fields["host"])
	if host != "" {
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	}
	if strings.Count(endpoint, ":") == 1 {
		before, _, _ := strings.Cut(endpoint, ":")
		return strings.ToLower(strings.TrimSpace(before))
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(endpoint), "[]"))
}

func kafkaBrokerListHasLocal(brokers string) bool {
	for _, broker := range strings.Split(brokers, ",") {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		if !strings.Contains(broker, "://") {
			broker = "kafka://" + broker
		}
		if endpointHostIsLocal(broker) {
			return true
		}
	}
	return false
}

func sanitizedEndpoint(raw, fallbackScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	fields := dsnFields(raw)
	host := strings.TrimSpace(fields["host"])
	port := strings.TrimSpace(fields["port"])
	dbname := strings.TrimSpace(fields["dbname"])
	if host == "" {
		return strings.TrimSpace(fallbackScheme) + "-endpoint"
	}
	if port != "" && !strings.Contains(host, ":") {
		host += ":" + port
	}
	out := strings.TrimSpace(fallbackScheme) + "://" + host
	if dbname != "" {
		out += "/" + url.PathEscape(dbname)
	}
	return out
}

func sanitizedKafkaBrokers(raw string) []string {
	out := []string{}
	for _, broker := range strings.Split(raw, ",") {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		if !strings.Contains(broker, "://") {
			broker = "kafka://" + broker
		}
		out = append(out, sanitizedEndpoint(broker, "kafka"))
	}
	return out
}

func dsnFields(raw string) map[string]string {
	fields := map[string]string{}
	for _, field := range strings.Fields(raw) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), "'\"")
	}
	return fields
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var out int
	if _, err := fmt.Sscanf(raw, "%d", &out); err != nil || out == 0 {
		return fallback
	}
	return out
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	out, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return out
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}
