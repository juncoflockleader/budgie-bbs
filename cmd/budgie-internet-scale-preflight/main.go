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
	"github.com/juncoflockleader/budgie-bbs/internal/preflightcheck"
	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

var (
	runPostgresPreflightProbe = preflightcheck.ProbePostgres
	runNATSPreflightProbe     = preflightcheck.ProbeNATS
	runKafkaPreflightProbe    = preflightcheck.ProbeKafka
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("budgie-internet-scale-preflight", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	targets := flags.String("targets", runconfig.EnvOr("BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS", ""), "Comma-separated targets to probe: postgres,nats,kafka,all; defaults from env")
	remoteStaging := flags.Bool("remote-staging", runconfig.EnvBool("BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING", runconfig.EnvBool("BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING", false)), "Reject loopback endpoints and use as remote/shared staging preflight")
	natsURL := flags.String("nats", runconfig.EnvOr("BUDGIE_NATS_URL", ""), "NATS URL for JetStream command/event stream create-delete probe")
	natsReplicas := flags.Int("nats-replicas", runconfig.EnvInt("BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS", 1), "NATS stream replica count for preflight streams")
	kafkaBrokers := flags.String("kafka-brokers", runconfig.EnvOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers")
	kafkaCommandPartitions := flags.Int("kafka-command-partitions", runconfig.EnvInt("BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS", 32), "Kafka command topic partitions for preflight")
	kafkaEventPartitions := flags.Int("kafka-event-partitions", runconfig.EnvInt("BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS", 32), "Kafka event topic partitions for preflight")
	kafkaTopicReplicas := flags.Int("kafka-topic-replicas", runconfig.EnvInt("BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS", 1), "Kafka topic replication factor for preflight")
	kafkaSecurityFlags := kafkaconn.RegisterRuntimeSecurityFlags(flags)
	postgresDSN := flags.String("postgres-dsn", runconfig.EnvOr("BUDGIE_POSTGRES_DSN", ""), "Postgres DSN for disposable schema create-delete probe")
	id := flags.String("id", "", "Optional probe id suffix; defaults to pid plus timestamp")
	timeout := flags.Duration("timeout", runconfig.EnvDuration("BUDGIE_INTERNET_SCALE_PREFLIGHT_TIMEOUT", 45*time.Second), "Maximum total preflight duration")
	reportFile := flags.String("report-file", runconfig.EnvOr("BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT", ""), "Optional JSON report path written only after a successful preflight")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		log.Printf("unsupported argument %q; use flags only", flags.Arg(0))
		return 2
	}

	targetList, err := runconfig.InferPreflightTargets(*targets, *natsURL, *kafkaBrokers, *postgresDSN)
	if err != nil {
		log.Print(err)
		return 2
	}
	reportPath := strings.TrimSpace(*reportFile)
	config := preflightcheck.Config{
		Targets:                targetList,
		RemoteStaging:          *remoteStaging,
		NATSURL:                *natsURL,
		NATSReplicas:           *natsReplicas,
		KafkaBrokers:           *kafkaBrokers,
		KafkaCommandPartitions: int32(*kafkaCommandPartitions),
		KafkaEventPartitions:   int32(*kafkaEventPartitions),
		KafkaTopicReplicas:     *kafkaTopicReplicas,
		KafkaSecurity:          kafkaSecurityFlags.Config(),
		PostgresDSN:            strings.TrimSpace(*postgresDSN),
		ID:                     preflightcheck.ID(*id),
		Timeout:                *timeout,
		Logf:                   log.Printf,
	}
	if err := preflightcheck.ValidateConfig(config); err != nil {
		log.Print(err)
		return 2
	}

	ctx, cancel := runconfig.InterruptTimeoutContext(context.Background(), config.Timeout)
	defer cancel()

	report := preflightcheck.NewReport(config)
	targetsByName := runconfig.TargetSet(config.Targets)

	fmt.Printf("==> internet-scale staging preflight targets: %s\n", strings.Join(config.Targets, " "))
	if config.RemoteStaging {
		fmt.Println("    remote staging checks: enabled")
	} else {
		fmt.Println("    remote staging checks: disabled")
	}
	if targetsByName[runconfig.PreflightTargetPostgres] {
		fmt.Printf("    postgres endpoint: %s\n", runevidence.SanitizeEndpoint(config.PostgresDSN, "postgres"))
	}
	if targetsByName[runconfig.PreflightTargetNATS] {
		fmt.Printf("    nats endpoint:     %s\n", runevidence.SanitizeEndpoint(config.NATSURL, "nats"))
	}
	if targetsByName[runconfig.PreflightTargetKafka] {
		fmt.Printf("    kafka brokers:     %s\n", strings.Join(runevidence.SanitizeKafkaBrokers(config.KafkaBrokers), ","))
	}

	probeRunners := map[string]func() error{
		runconfig.PreflightTargetPostgres: func() error {
			return runPostgresPreflightProbe(ctx, config)
		},
		runconfig.PreflightTargetNATS: func() error {
			return runNATSPreflightProbe(ctx, config)
		},
		runconfig.PreflightTargetKafka: func() error {
			return runKafkaPreflightProbe(ctx, config)
		},
	}
	for _, spec := range preflightcheck.ProbeSpecs(config) {
		if !targetsByName[spec.Target] {
			continue
		}
		probe, err := runPreflightStep(spec.Target, spec.Name, spec.Resources, probeRunners[spec.Target])
		report.Probes = append(report.Probes, probe)
		if err != nil {
			report.FinishedAt = nowMS()
			return 1
		}
	}
	report.Passed = true
	report.FinishedAt = nowMS()
	if reportPath != "" {
		if err := runreport.WriteJSONFile(reportPath, report, true); err != nil {
			log.Printf("write preflight report: %v", err)
			return 1
		}
		fmt.Printf("==> archived preflight report at %s\n", reportPath)
	}
	fmt.Println("==> internet-scale staging preflight passed")
	return 0
}

func runPreflightStep(target, label string, resources []string, run func() error) (preflightmodel.Probe, error) {
	report := preflightmodel.Probe{
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

func nowMS() int64 {
	return time.Now().UnixMilli()
}
