package runbooks_test

import (
	"os"
	"strings"
	"testing"
)

func TestSingleRegionFailureDrillDocumentsIS9RecoveryPaths(t *testing.T) {
	raw, err := os.ReadFile("single-region-failure-drill.md")
	if err != nil {
		t.Fatalf("read single-region drill: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Required Inputs",
		"## Evidence Log",
		"## Preflight",
		"## Phase 1 - Regional Write Route Outage",
		"## Phase 2 - Projection Lag And Read-Your-Writes",
		"## Phase 3 - Live Broker Outage",
		"## Phase 4 - Command Writer Crash Or Rebalance",
		"## Exit Criteria",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"BUDGIE_WRITE_REGION_URL",
		"BUDGIE_NATS_URL",
		"BUDGIE_PROMETHEUS_URL",
		"/api/v1/alerts",
		"No critical Budgie alert is already firing",
		"./scripts/single-region-failure-drill-preflight.sh",
		"./scripts/cluster-smoke.sh",
		"write_region_unavailable",
		"projection_stale",
		"X-Budgie-Min-Seq",
		"X-Budgie-Read-Your-Writes: satisfied",
		"budgie_write_region_proxy_failures_total",
		"budgie_derived_view_lag_events",
		"budgie_events_remote_publish_failures_total",
		"budgie_command_partition_lag",
		"budgie_command_log_assignment_losses_total",
		"-backfill-derived-views rankings.boards",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("runbook missing token %q", token)
		}
	}
	if strings.Count(body, "Expected:") < 4 {
		t.Fatalf("expected at least four explicit Expected blocks")
	}
	if strings.Count(body, "Rollback:") < 4 {
		t.Fatalf("expected at least four explicit Rollback blocks")
	}
}

func TestProjectionSearchRebuildRunbookDocumentsRoutineRepair(t *testing.T) {
	raw, err := os.ReadFile("projection-search-rebuild.md")
	if err != nil {
		t.Fatalf("read projection/search rebuild runbook: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Derived View Groups",
		"## Preflight",
		"## Repair From SQL Event Log",
		"## Repair From NATS Event-Log Shadow",
		"## Repair From Kafka Event Log",
		"## Validation",
		"## Rollback And Escalation",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"`search`",
		"`rankings`",
		"`summaries`",
		"`community`",
		"`feeds`",
		"-backfill-derived-views search",
		"-backfill-derived-views rankings",
		"-backfill-derived-views summaries,feeds",
		"-rebuild-source nats",
		"-rebuild-source kafka",
		"-kafka-event-partitions 32",
		"-event-log-shadow-nats-stream BUDGIE_EVENT_LOG",
		"budgie_derived_view_lag_events",
		"X-Budgie-Min-Seq",
		"X-Budgie-Read-Your-Writes",
		"BudgieProjectionLagHigh",
		"-rebuild-projections",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("projection/search runbook missing token %q", token)
		}
	}
}

func TestCounterStoreRepairRunbookDocumentsCurrentAndFutureRepair(t *testing.T) {
	raw, err := os.ReadFile("counter-store-repair.md")
	if err != nil {
		t.Fatalf("read counter-store repair runbook: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Current Counter Surfaces",
		"## Preflight",
		"## SQL Side-Store Repair",
		"## Projection Rebuild",
		"## Counter-Store Shard Repair Gates",
		"## Validation",
		"## Exit Criteria",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"reaction",
		"poll",
		"post_reactions",
		"poll_votes",
		"user_activity.reactions_recv",
		"counter shard failure",
		"-repair-counter-store-aggregates",
		"budgie_derived_view_lag_events",
		"-backfill-derived-views rankings",
		"-rebuild-projections",
		"budgie_outbox_jobs",
		"BudgieProjectionLagHigh",
		"X-Budgie-Min-Seq",
		"X-Budgie-Read-Your-Writes: satisfied",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("counter-store runbook missing token %q", token)
		}
	}
	if strings.Contains(body, "event log can recreate missing reaction") {
		t.Fatal("runbook must not claim ordered replay can recreate missing unordered rows")
	}
}

func TestPartitionSplitReassignmentRunbookDocumentsSafeOperations(t *testing.T) {
	raw, err := os.ReadFile("partition-split-reassignment.md")
	if err != nil {
		t.Fatalf("read partition split/reassignment runbook: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Signals",
		"## Preflight",
		"## Decision: Split Or Reassign",
		"## Hot-Thread Split",
		"## Reassign A Hot Command Partition",
		"## Roll Back",
		"## Exit Criteria",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"budgie_hot_partition_candidate",
		"budgie_hot_thread_split_shards",
		"budgie_command_partition_lag",
		"budgie_writer_partition_lock_wait",
		"budgie_command_partition_assigned",
		"budgie_command_log_assignment_losses_total",
		"/api/v1/admin/hot-thread-splits",
		"blockingPartitions",
		`"force":true`,
		"?force=1",
		"-command-log-worker-assignment-overrides",
		"-command-log-worker-ownership nats-kv",
		"thread/${THREAD_ID}#reply-0=writer-hot",
		"BudgieCommandLogWriterLagHigh",
		"BudgieHotPartitionCandidate",
		"X-Budgie-Min-Seq",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("partition runbook missing token %q", token)
		}
	}
}

func TestBrokerOperationsRunbookDocumentsBrokerPromotionGates(t *testing.T) {
	raw, err := os.ReadFile("broker-operations.md")
	if err != nil {
		t.Fatalf("read broker operations runbook: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Broker Roles",
		"## Preflight",
		"## Live Delivery NATS Operations",
		"## JetStream Event-Log Shadow",
		"## JetStream Command Log",
		"## Durable Native Command/Event Staging Gate",
		"## NATS KV Assignment",
		"## Redpanda/Kafka Promotion Checklist",
		"## Exit Criteria",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"BUDGIE_NATS_URL",
		"BUDGIE_EVENT_LOG_STREAM",
		"BUDGIE_COMMAND_LOG_STREAM",
		"budgie.events.scope.",
		"budgie.eventlog.>",
		"budgie.commandlog.>",
		"budgie.commandcommit.>",
		"budgie_events_remote_publish_failures_total",
		"budgie_events_remote_decode_failures_total",
		"budgie_event_log_shadow_parity_failures_total",
		"budgie_command_partition_lag",
		"budgie_command_log_assignment_losses_total",
		"-event-log-shadow nats",
		"-command-log-shadow nats",
		"-command-log-authoritative nats",
		"./scripts/commandlog-native-nats-gate.sh",
		"./scripts/commandlog-native-kafka-gate.sh",
		"BUDGIE_COMMANDLOG_GATE_BOARDS",
		"BUDGIE_COMMANDLOG_GATE_COMMANDS",
		"BUDGIE_COMMANDLOG_GATE_REPLIES",
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE=1",
		"weaker-than-budget load",
		"temporary file",
		"final report path is archived only after",
		"one NATS account or domain can host only one active load stream pair",
		"the wrapper preflights",
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1",
		"./scripts/commandlog-native-nats-cleanup.sh",
		"./scripts/commandlog-native-nats-cleanup.sh --execute",
		"nats --server \"$BUDGIE_NATS_URL\" stream rm --force",
		"Do not delete non-load streams",
		"cmd/budgie-commandlog-loadgen",
		"-command-log-worker-executor native",
		"-command-log-backend nats",
		"-command-log-nats-stream",
		"-command-log-nats-replicas",
		"-event-log-nats-stream",
		"-event-log-nats-replicas",
		"-require-postgres",
		"-authoritative-submit",
		"-assignment-mode snapshot-assignment",
		"-replies-per-thread 2",
		"-directed-replies",
		"-budget-file ops/internet-scale-budgets.example.json",
		"ops/internet-scale-kafka-budgets.example.json",
		"ops/internet-scale-kafka-remote-staging-budgets.example.json",
		"budgie.commands.load.*",
		"budgie.events.load.*",
		"-kafka-topic-replicas",
		"auto-create disabled",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS",
		"./scripts/commandlog-native-kafka-cleanup.sh",
		"./scripts/commandlog-native-kafka-cleanup.sh --execute",
		"BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT",
		"BUDGIE_KAFKA_TLS",
		"BUDGIE_KAFKA_TLS_CA_FILE",
		"BUDGIE_KAFKA_SASL_MECHANISM",
		"BUDGIE_KAFKA_SASL_USER",
		"BUDGIE_KAFKA_SASL_PASSWORD",
		"runtime.kafkaTls",
		"runtime.kafkaSaslMechanism",
		"runtime.kafkaBrokers",
		"-kafka-scalar-allocator sql-event-partition-offsets",
		"runtime.scalarCompatibilityAllocator == \"sql-event-partition-offsets\"",
		"scalarCompatibilityAudit.legacySqlScalarOffsetAfter == 0",
		"promoted Kafka gate now opts out of the global SQL scalar allocator",
		"BUDGIE_KAFKA_BROKERS",
		"cmd/budgie-report-check commandlog",
		"-report-file artifacts/internet-scale/commandlog-native-nats-report.json",
		"BUDGIE_POSTGRES_DSN",
		"runtime.commandLogBackend",
		"runtime.eventLogBackend",
		"runtime.materializationStore",
		"runtime.natsEndpoint",
		"runtime.postgresEndpoint",
		"runtime.requirePostgres",
		"runtime.postgresSchema",
		"runtime.keepPostgresSchema",
		"runtime.durableStaging",
		"runtime.commandNatsStream",
		"runtime.eventNatsStream",
		"BUDGIE_COMMAND_LOG_LOAD_",
		"BUDGIE_EVENT_LOG_LOAD_",
		"config.executorMode",
		"config.authoritativeSubmit",
		"config.assignmentMode",
		"config.directedReplies",
		"config.repliesPerThread",
		"config.boards",
		"config.commandsPerBoard",
		"config.writers",
		"config.batchSize",
		"totalCommands",
		"evidence.tool",
		"evidence.budgetFile",
		"requiredReportBudgetFile",
		"evidence.budgetSha256",
		"evidence.gitRevision",
		"evidence.gitModified == false",
		"promotionReadiness.ready",
		"eventProjection.partitionLimitExceeded",
		"materializationAudit.complete",
		"commandLogDrain.requireDurableStaging",
		"requireReportEvidence",
		"-command-log-worker-ownership nats-kv",
		"same-process NATS native writer/projector",
		"-command-log-worker-event-nats-stream",
		"-event-store-projection-nats-stream",
		"Kafka/Redpanda writer and",
		"same event topic and partition count",
		"Nats-Msg-Id",
		"Redpanda/Kafka",
		"BudgieEventLogShadowParityFailure",
		"./scripts/cluster-smoke.sh",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("broker operations runbook missing token %q", token)
		}
	}
}

func TestInternetScaleStagingHandoffDocumentsExternalEnvironment(t *testing.T) {
	raw, err := os.ReadFile("internet-scale-staging-handoff.md")
	if err != nil {
		t.Fatalf("read internet-scale staging handoff: %v", err)
	}
	body := string(raw)
	for _, section := range []string{
		"## Goal",
		"## Required Outputs",
		"## Localhost Exception",
		"## NATS Requirements",
		"## Rerun And Cleanup",
		"## Postgres Requirements",
		"## Connectivity Check",
		"## What To Run",
		"## Success Criteria",
		"## Handoff Back",
	} {
		if !strings.Contains(body, section) {
			t.Fatalf("missing section %q", section)
		}
	}
	for _, token := range []string{
		"BUDGIE_NATS_URL",
		"BUDGIE_POSTGRES_DSN",
		"artifacts/internet-scale/staging.env",
		"nats://127.0.0.1:4222",
		"local validation evidence",
		"staging-scoped credentials",
		"./scripts/internet-scale-remote-staging-preflight.sh",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT",
		"preflight-report-<shared-report-suffix>.json",
		"BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT=1",
		"budgie_cmdlog_load_preflight_*",
		"BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_*",
		"BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_*",
		"budgie.commands.load.preflight.*",
		"budgie.events.load.preflight.*",
		"./scripts/commandlog-native-nats-gate.sh",
		"./scripts/commandlog-native-kafka-gate.sh",
		"./scripts/gateway-fanout-gate.sh",
		"./scripts/internet-scale-staging-gate.sh",
		"BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK=1",
		"BUDGIE_INTERNET_SCALE_GATE_TARGETS",
		"BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway",
		"Supported values are `gateway`, `nats`, `kafka`",
		"ops/internet-scale-budgets.example.json",
		"ops/internet-scale-remote-staging-budgets.example.json",
		"ops/internet-scale-kafka-budgets.example.json",
		"ops/internet-scale-kafka-remote-staging-budgets.example.json",
		"BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING=1",
		"BUDGIE_KAFKA_BROKERS",
		"BUDGIE_COMMAND_LOG_LOAD_*",
		"BUDGIE_EVENT_LOG_LOAD_*",
		"budgie.commands.load.",
		"budgie.events.load.",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS",
		"auto-create disabled",
		"./scripts/commandlog-native-kafka-cleanup.sh",
		"./scripts/commandlog-native-kafka-cleanup.sh --execute",
		"BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT",
		"BUDGIE_KAFKA_TLS",
		"BUDGIE_KAFKA_SASL_MECHANISM",
		"BUDGIE_KAFKA_SASL_USER",
		"BUDGIE_KAFKA_SASL_PASSWORD",
		"runtime.kafkaTls",
		"runtime.kafkaSaslMechanism",
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS",
		"budgie.commandlog.>",
		"budgie.commandcommit.>",
		"budgie.eventlog.>",
		"one NATS account/domain can host only one active load stream pair",
		"preflights those subjects",
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1",
		"./scripts/commandlog-native-nats-cleanup.sh",
		"./scripts/commandlog-native-nats-cleanup.sh --execute",
		"nats --server \"$BUDGIE_NATS_URL\" stream rm --force",
		"Do not delete non-load streams",
		"budgie_cmdlog_load_*",
		"runtime.commandNatsReplicas >= 1",
		"runtime.eventNatsReplicas >= 1",
		"runtime.commandLogBackend == \"kafka\"",
		"runtime.kafkaCommandPartitions >= 32",
		"evidence.tool == \"budgie-gateway-loadgen\"",
		"evidence.budgetFile",
		"evidence.budgetSha256",
		"subscribers >= 100000",
		"hotScopeSubscribers >= 10000",
		"queuedDeliveries >= 10000",
		"targetConnections >= 1000000",
		"gatewayNodesForTarget <= 20",
		"go run ./cmd/budgie-report-check gateway",
		"-report-file artifacts/internet-scale/gateway-fanout-report.json",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=<shared-report-suffix>",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST",
		"./scripts/internet-scale-report-check.sh",
		"cmd/budgie-report-check preflight",
		"cmd/budgie-report-check bundle",
		"-verify-manifest",
		"bundle-manifest-<shared-report-suffix>.json",
		"target set",
		"remote/local staging mode",
		"SHA-256 hashes",
		"different git revisions",
		"failed probes",
		"unsanitized endpoint evidence",
		"loopback endpoints",
		"evidence.gitModified == false",
		"Do not send production credentials",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("handoff missing token %q", token)
		}
	}
}
