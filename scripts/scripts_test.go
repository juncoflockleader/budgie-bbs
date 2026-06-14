package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func currentUserNameForTest(t *testing.T) string {
	t.Helper()
	if name := os.Getenv("USER"); strings.TrimSpace(name) != "" {
		return name
	}
	out, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatalf("resolve current user: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestRunSingleNodeScriptPinsSQLiteDeploymentShape(t *testing.T) {
	path := "run-single-node.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_SINGLE_NODE_DATA_DIR",
		"BUDGIE_SINGLE_NODE_DB",
		"BUDGIE_SINGLE_NODE_HTTP",
		"BUDGIE_SINGLE_NODE_SSH",
		"BUDGIE_SINGLE_NODE_HOSTKEY",
		"BUDGIE_SINGLE_NODE_SECRET_FILE",
		"BUDGIE_SINGLE_NODE_WEB",
		"BUDGIE_SINGLE_NODE_BINARY",
		"BUDGIE_SINGLE_NODE_DRY_RUN",
		"artifacts/single-node",
		"generate_secret",
		"openssl rand -base64 48",
		"unset BUDGIE_POSTGRES_DSN",
		"unset BUDGIE_NATS_URL",
		"unset BUDGIE_REDIS_URL",
		"unset BUDGIE_KAFKA_BROKERS",
		"unset BUDGIE_READ_CACHE",
		"-storage sqlite",
		"-db \"$DB_PATH\"",
		"-hostkey \"$HOSTKEY_PATH\"",
		"broker/cache env: disabled for this process",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}

	dataDir := t.TempDir()
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"BUDGIE_SINGLE_NODE_DRY_RUN=1",
		"BUDGIE_SINGLE_NODE_DATA_DIR="+dataDir,
		"BUDGIE_SINGLE_NODE_BINARY=/tmp/budgied-test",
		"BUDGIE_SINGLE_NODE_WEB=off",
		"BUDGIE_POSTGRES_DSN=postgres://should-not-leak",
		"BUDGIE_NATS_URL=nats://should-not-leak",
		"BUDGIE_REDIS_URL=redis://should-not-leak",
		"BUDGIE_KAFKA_BROKERS=should-not-leak:9092",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s dry-run failed: %v\n%s", path, err, out)
	}
	output := string(out)
	for _, want := range []string{
		"launching BudgieBBS single-node SQLite",
		"storage:  sqlite",
		"broker/cache env: disabled for this process",
		"/tmp/budgied-test",
		"-storage sqlite",
		"-db " + filepath.Join(dataDir, "budgie.db"),
		"-hostkey " + filepath.Join(dataDir, "budgie_host_key"),
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("%s dry-run output missing %q\n%s", path, want, output)
		}
	}
	if strings.Contains(output, "should-not-leak") {
		t.Fatalf("%s dry-run output leaked stale multi-node env:\n%s", path, output)
	}
}

func TestInstallSingleNodeLaunchdScriptPinsRestartingServiceShape(t *testing.T) {
	path := "install-single-node-launchd.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_LAUNCHD_LABEL",
		"BUDGIE_LAUNCHD_PLIST",
		"BUDGIE_LAUNCHD_USER",
		"BUDGIE_LAUNCHD_GROUP",
		"BUDGIE_LAUNCHD_LOG_DIR",
		"BUDGIE_LAUNCHD_DRY_RUN",
		"BUDGIE_SINGLE_NODE_DATA_DIR",
		"BUDGIE_SINGLE_NODE_BINARY",
		"com.budgie.bbs",
		"/Library/LaunchDaemons/${LABEL}.plist",
		"RunAtLoad",
		"KeepAlive",
		"ThrottleInterval",
		"launchctl bootstrap system",
		"launchctl enable \"system/$LABEL\"",
		"launchctl kickstart -k \"system/$LABEL\"",
		"launchctl bootout system",
		"left data and logs intact",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}

	dataDir := t.TempDir()
	logDir := t.TempDir()
	plistPath := filepath.Join(t.TempDir(), "com.budgie.test.plist")
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"BUDGIE_LAUNCHD_DRY_RUN=1",
		"BUDGIE_LAUNCHD_LABEL=com.budgie.test",
		"BUDGIE_LAUNCHD_PLIST="+plistPath,
		"BUDGIE_LAUNCHD_USER="+currentUserNameForTest(t),
		"BUDGIE_LAUNCHD_LOG_DIR="+logDir,
		"BUDGIE_SINGLE_NODE_DATA_DIR="+dataDir,
		"BUDGIE_SINGLE_NODE_BINARY=/tmp/budgied-test",
		"BUDGIE_SINGLE_NODE_WEB=off",
		"BUDGIE_SINGLE_NODE_HTTP=:8181",
		"BUDGIE_SINGLE_NODE_SSH=2223",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s dry-run failed: %v\n%s", path, err, out)
	}
	output := string(out)
	for _, want := range []string{
		"launchd install plan",
		"<string>com.budgie.test</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>BUDGIE_SINGLE_NODE_DATA_DIR</key>",
		"<string>" + dataDir + "</string>",
		"<key>BUDGIE_SINGLE_NODE_BINARY</key>",
		"<string>/tmp/budgied-test</string>",
		"<key>BUDGIE_SINGLE_NODE_HTTP</key>",
		"<string>:8181</string>",
		"<key>BUDGIE_SINGLE_NODE_SSH</key>",
		"<string>2223</string>",
		"<key>BUDGIE_SINGLE_NODE_WEB</key>",
		"<string>off</string>",
		"launchctl bootstrap system '" + plistPath + "'",
		"launchctl enable system/com.budgie.test",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("%s dry-run output missing %q\n%s", path, want, output)
		}
	}
	for _, forbidden := range []string{
		"BUDGIE_POSTGRES_DSN",
		"BUDGIE_NATS_URL",
		"BUDGIE_REDIS_URL",
		"BUDGIE_KAFKA_BROKERS",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("%s dry-run output included multi-node env %q\n%s", path, forbidden, output)
		}
	}
}

func TestBuildWebScriptPinsStableNodeResolution(t *testing.T) {
	path := "build-web.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_WEB_NPM",
		"BUDGIE_WEB_INSTALL",
		"/opt/homebrew/bin/npm",
		"/usr/local/bin/npm",
		"export PATH=\"$NPM_DIR:$PATH\"",
		"command -v node",
		"node --version",
		"\"$NPM_CLI\" ci",
		"\"$NPM_CLI\" run build",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
	cmd := exec.Command("bash", "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s syntax check failed: %v\n%s", path, err, out)
	}
}

func TestComposeClusterSmokeScriptPinsComposeShape(t *testing.T) {
	path := "compose-cluster-smoke.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"docker compose version",
		"COMPOSE_API1_PORT",
		"COMPOSE_API2_PORT",
		"deploy/compose.smoke.yml",
		"docker compose -f docker-compose.yml -f deploy/compose.smoke.yml up -d --build",
		"docker compose -f docker-compose.yml -f deploy/compose.smoke.yml down -v",
		"curl -fsS \"${url}/readyz\"",
		"BUDGIE_SMOKE_NODE_A",
		"BUDGIE_SMOKE_NODE_B",
		"./scripts/cluster-smoke.sh",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
	cmd := exec.Command("bash", "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s syntax check failed: %v\n%s", path, err, out)
	}
}

func TestCommandLogNativeNATSGateScriptPinsDurablePromotionShape(t *testing.T) {
	path := "commandlog-native-nats-gate.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"require_env BUDGIE_NATS_URL",
		"require_env BUDGIE_POSTGRES_DSN",
		"BUDGIE_COMMAND_LOG_LOAD_STREAM",
		"BUDGIE_EVENT_LOG_LOAD_STREAM",
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_REPORT",
		"BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE",
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING",
		"NATS_BIN",
		"resolve_nats_bin",
		"resolve_go_bin",
		"/opt/homebrew/bin/nats",
		"/usr/local/bin/nats",
		"/opt/homebrew/bin/go",
		"/usr/local/go/bin/go",
		"RUN_ID=\"$(date -u +%Y%m%d%H%M%S)_$$\"",
		"artifacts/internet-scale/commandlog-native-nats-report.json",
		"does not accept extra loadgen flags",
		"GO_CLI",
		"LOCAL_PROMOTED_BUDGET_FILE=\"ops/internet-scale-budgets.example.json\"",
		"REMOTE_PROMOTED_BUDGET_FILE=\"ops/internet-scale-remote-staging-budgets.example.json\"",
		"require_promoted_budget_file",
		"require_nonlocal_runtime_inputs",
		"REPORT_PREFIX=\"artifacts/internet-scale/\"",
		"require_report_path",
		"require_min_int BUDGIE_COMMANDLOG_GATE_BOARDS",
		"require_min_int totalCommands",
		"require_prefix BUDGIE_COMMAND_LOG_LOAD_STREAM",
		"require_prefix BUDGIE_EVENT_LOG_LOAD_STREAM",
		"require_clean_git_tree",
		"git status --porcelain",
		"require_nats_subjects_available",
		"\"$NATS_CLI\" --server \"$BUDGIE_NATS_URL\" stream ls --names --subject",
		"budgie.commandlog.>",
		"budgie.commandcommit.>",
		"budgie.eventlog.>",
		"mktemp",
		"cleanup_report_tmp",
		"mv \"$REPORT_TMP\" \"$REPORT_FILE\"",
		"archived verified report",
		"preserved load streams for inspection",
		"BUDGIE_*_LOAD_* streams before rerunning",
		"-command-log-worker-executor native",
		"-command-log-backend nats",
		"-command-log-nats-stream",
		"-command-log-nats-replicas",
		"-event-log-nats-stream",
		"-event-log-nats-replicas",
		"-require-postgres",
		"-authoritative-submit",
		"-assignment-mode snapshot-assignment",
		"-replies-per-thread",
		"-directed-replies",
		"-budget-file",
		"cmd/budgie-commandlog-loadgen",
		"cmd/budgie-commandlog-report-check",
		"\"$GO_CLI\" run ./cmd/budgie-commandlog-loadgen",
		"-report-file",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestCommandLogNativeKafkaGateScriptPinsDurablePromotionShape(t *testing.T) {
	path := "commandlog-native-kafka-gate.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"require_env BUDGIE_KAFKA_BROKERS",
		"require_env BUDGIE_POSTGRES_DSN",
		"BUDGIE_COMMAND_LOG_LOAD_TOPIC",
		"BUDGIE_EVENT_LOG_LOAD_TOPIC",
		"BUDGIE_KAFKA_TLS",
		"BUDGIE_KAFKA_TLS_CA_FILE",
		"BUDGIE_KAFKA_TLS_SERVER_NAME",
		"BUDGIE_KAFKA_SASL_MECHANISM",
		"BUDGIE_KAFKA_SASL_USER",
		"BUDGIE_KAFKA_SASL_PASSWORD",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_CONSUMER_GROUP",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS",
		"BUDGIE_COMMANDLOG_GATE_REPORT",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING",
		"RUN_ID=\"$(date -u +%Y%m%d%H%M%S)_$$\"",
		"COMMAND_TOPIC_PREFIX=\"budgie.commands.load.\"",
		"EVENT_TOPIC_PREFIX=\"budgie.events.load.\"",
		"LOCAL_PROMOTED_BUDGET_FILE=\"ops/internet-scale-kafka-budgets.example.json\"",
		"REMOTE_PROMOTED_BUDGET_FILE=\"ops/internet-scale-kafka-remote-staging-budgets.example.json\"",
		"artifacts/internet-scale/commandlog-native-kafka-report.json",
		"does not accept extra loadgen flags",
		"require_promoted_budget_file",
		"require_nonlocal_runtime_inputs",
		"broker_list_has_localhost",
		"REPORT_PREFIX=\"artifacts/internet-scale/\"",
		"require_report_path",
		"require_min_int BUDGIE_COMMANDLOG_GATE_BOARDS",
		"require_min_int BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS",
		"require_min_int totalCommands",
		"require_prefix BUDGIE_COMMAND_LOG_LOAD_TOPIC",
		"require_prefix BUDGIE_EVENT_LOG_LOAD_TOPIC",
		"require_clean_git_tree",
		"git status --porcelain",
		"resolve_go_bin",
		"/opt/homebrew/bin/go",
		"/usr/local/go/bin/go",
		"mktemp",
		"cleanup_report_tmp",
		"mv \"$REPORT_TMP\" \"$REPORT_FILE\"",
		"archived verified report",
		"preserved Kafka load topics for inspection",
		"-command-log-worker-executor native",
		"-command-log-backend kafka",
		"-kafka-scalar-allocator sql-event-partition-offsets",
		"-kafka-brokers",
		"-kafka-command-topic",
		"-kafka-event-topic",
		"-kafka-consumer-group",
		"-kafka-command-partitions",
		"-kafka-event-partitions",
		"-kafka-topic-replicas",
		"-require-postgres",
		"-authoritative-submit",
		"-assignment-mode snapshot-assignment",
		"-replies-per-thread",
		"-directed-replies",
		"-budget-file",
		"cmd/budgie-commandlog-loadgen",
		"cmd/budgie-commandlog-report-check",
		"\"$GO_CLI\" run ./cmd/budgie-commandlog-loadgen",
		"-report-file",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestGatewayFanoutGateScriptPinsPromotedCapacityShape(t *testing.T) {
	path := "gateway-fanout-gate.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS",
		"BUDGIE_GATEWAY_FANOUT_GATE_IDLE_SUBSCRIBERS",
		"BUDGIE_GATEWAY_FANOUT_GATE_BUFFER_SIZE",
		"BUDGIE_GATEWAY_FANOUT_GATE_EVENTS",
		"BUDGIE_GATEWAY_FANOUT_GATE_TARGET_CONNECTIONS",
		"BUDGIE_GATEWAY_FANOUT_GATE_TIMEOUT",
		"BUDGIE_GATEWAY_FANOUT_GATE_REPORT",
		"BUDGIE_GATEWAY_FANOUT_GATE_ALLOW_OVERWRITE",
		"BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING",
		"does not accept extra loadgen flags",
		"LOCAL_PROMOTED_BUDGET_FILE=\"ops/internet-scale-budgets.example.json\"",
		"REMOTE_PROMOTED_BUDGET_FILE=\"ops/internet-scale-remote-staging-budgets.example.json\"",
		"BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING:-",
		"REPORT_PREFIX=\"artifacts/internet-scale/\"",
		"require_report_path",
		"require_clean_git_tree",
		"git status --porcelain",
		"resolve_go_bin",
		"/opt/homebrew/bin/go",
		"/usr/local/go/bin/go",
		"read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS 10000",
		"read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_IDLE_SUBSCRIBERS 90000",
		"read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_BUFFER_SIZE 2",
		"read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_EVENTS 1",
		"read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_TARGET_CONNECTIONS 1000000",
		"require_min_int BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS",
		"require_min_int totalSubscribers",
		"require_min_int queuedDeliveries",
		"mktemp",
		"cleanup_report_tmp",
		"mv \"$REPORT_TMP\" \"$REPORT_FILE\"",
		"archived verified gateway fanout report",
		"cmd/budgie-gateway-loadgen",
		"cmd/budgie-gateway-report-check",
		"\"$GO_CLI\" run ./cmd/budgie-gateway-loadgen",
		"\"$GO_CLI\" run ./cmd/budgie-gateway-report-check",
		"-hot-subscribers",
		"-idle-subscribers",
		"-buffer-size",
		"-events",
		"-target-connections",
		"-budget-file \"$PROMOTED_BUDGET_FILE\"",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestInternetScaleStagingGateScriptPinsEvidenceBundleShape(t *testing.T) {
	path := "internet-scale-staging-gate.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_INTERNET_SCALE_GATE_TARGETS",
		"BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING",
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX",
		"BUDGIE_GATEWAY_FANOUT_GATE_SCRIPT",
		"BUDGIE_COMMANDLOG_NATS_GATE_SCRIPT",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_SCRIPT",
		"BUDGIE_INTERNET_SCALE_GATE_PREFLIGHT_SCRIPT",
		"BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT",
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_CHECK_SCRIPT",
		"BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK",
		"BUDGIE_INTERNET_SCALE_GATE_MANIFEST",
		"does not accept positional arguments",
		"require_clean_git_tree",
		"git status --porcelain",
		"default_targets",
		"durable_preflight_targets",
		"bundle_report_check_targets",
		"BUDGIE_NATS_URL",
		"BUDGIE_KAFKA_BROKERS",
		"supported targets: gateway,nats,kafka,all",
		"set BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway explicitly",
		"gateway-fanout-report-${REPORT_SUFFIX}.json",
		"commandlog-native-nats-report-${REPORT_SUFFIX}.json",
		"commandlog-native-kafka-report-${REPORT_SUFFIX}.json",
		"preflight-report-${REPORT_SUFFIX}.json",
		"bundle-manifest-${REPORT_SUFFIX}.json",
		"BUDGIE_GATEWAY_FANOUT_GATE_REPORT=\"$GATEWAY_REPORT\"",
		"BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING",
		"BUDGIE_COMMANDLOG_GATE_REPORT=\"$NATS_REPORT\"",
		"BUDGIE_COMMANDLOG_GATE_REPORT=\"$KAFKA_REPORT\"",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS=\"$PREFLIGHT_TARGETS\"",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT=\"$PREFLIGHT_REPORT\"",
		"remote staging create/delete preflight",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=\"$(bundle_report_check_targets)\"",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=\"$REMOTE_STAGING\"",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=\"$REPORT_SUFFIX\"",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST=\"$BUNDLE_MANIFEST\"",
		"archived internet-scale report bundle check",
		"bundle manifest:",
		"preflight report:",
		"internet-scale staging evidence bundle passed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestInternetScaleRemoteStagingPreflightScriptPinsProbeShape(t *testing.T) {
	path := "internet-scale-remote-staging-preflight.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING",
		"BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_TIMEOUT",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT",
		"BUDGIE_NATS_URL",
		"BUDGIE_KAFKA_BROKERS",
		"BUDGIE_POSTGRES_DSN",
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS",
		"BUDGIE_KAFKA_TLS",
		"BUDGIE_KAFKA_SASL_MECHANISM",
		"does not accept positional arguments",
		"resolve_go_bin",
		"/opt/homebrew/bin/go",
		"cmd/budgie-internet-scale-preflight",
		"-remote-staging",
		"-report-file",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestInternetScaleReportCheckScriptPinsArchivedBundleShape(t *testing.T) {
	path := "internet-scale-report-check.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX",
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX",
		"BUDGIE_GATEWAY_FANOUT_REPORT",
		"BUDGIE_COMMANDLOG_NATS_REPORT",
		"BUDGIE_COMMANDLOG_KAFKA_REPORT",
		"BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST",
		"does not accept positional arguments",
		"resolve_go_bin",
		"supported targets: gateway,nats,kafka,all",
		"durable_preflight_targets",
		"bundle_consistency_args",
		"bundle_manifest_verify_args",
		"bundle_consistency_targets",
		"gateway-fanout-report-${suffix}.json",
		"commandlog-native-nats-report-${suffix}.json",
		"commandlog-native-kafka-report-${suffix}.json",
		"preflight-report-${suffix}.json",
		"cmd/budgie-gateway-report-check",
		"cmd/budgie-commandlog-report-check",
		"cmd/budgie-internet-scale-preflight-report-check",
		"cmd/budgie-internet-scale-bundle-report-check",
		"-manifest-file",
		"-verify-manifest",
		"remote staging preflight report",
		"bundle evidence consistency",
		"bundle manifest read-back",
		"-remote-staging",
		"ops/internet-scale-remote-staging-budgets.example.json",
		"ops/internet-scale-kafka-remote-staging-budgets.example.json",
		"internet-scale report bundle satisfies selected budgets",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestSingleRegionFailureDrillPreflightScriptPinsSafeChecks(t *testing.T) {
	path := "single-region-failure-drill-preflight.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, token := range []string{
		"set -euo pipefail",
		"does not accept positional arguments",
		"BUDGIE_API",
		"BUDGIE_REGIONAL_API",
		"BUDGIE_WRITE_REGION_URL",
		"BUDGIE_ADMIN_TOKEN",
		"BUDGIE_USER_TOKEN",
		"BUDGIE_POSTGRES_DSN",
		"BUDGIE_NATS_URL",
		"BUDGIE_ALERT_RULES",
		"BUDGIE_PROMETHEUS_URL",
		"CURL_BIN",
		"JQ_BIN",
		"resolve_jq_bin",
		"/api/v1/alerts",
		"critical Budgie alerts are already firing",
		"Prometheus alert API not configured",
		"BudgieRemoteDeliveryLagHigh",
		"BudgieWriteRegionProxyFailures",
		"BudgieProjectionLagHigh",
		"BudgieCommandLogWriterLagHigh",
		"${BUDGIE_API_BASE}/readyz",
		"${BUDGIE_REGIONAL_API_BASE}/readyz",
		"${BUDGIE_API_BASE}/metrics",
		"${BUDGIE_REGIONAL_API_BASE}/metrics",
		"BUDGIE_SINGLE_REGION_PREFLIGHT_METRIC_DIR",
		"BUDGIE_SINGLE_REGION_PREFLIGHT_CLUSTER_SMOKE",
		"BUDGIE_SINGLE_REGION_PREFLIGHT_SKIP_CLUSTER_SMOKE",
		"single-region drill preflight passed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing token %q", path, token)
		}
	}
}

func TestCommandLogNativeNATSGateRejectsOverlappingNATSLoadStreamsBeforeRun(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	writeFakeCommandLogGateGit(t, dir, "")
	writeFakeCommandLogGateNATS(t, dir, `case "$subject" in
  "budgie.commandlog.>"|"budgie.commandcommit.>")
    echo "BUDGIE_COMMAND_LOG_LOAD_OLD"
    ;;
  "budgie.eventlog.>")
    echo "BUDGIE_EVENT_LOG_LOAD_OLD"
    ;;
esac
`)

	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"NATS_CALL_LOG="+filepath.Join(dir, "nats-calls.log"),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"preflighting NATS stream subject availability",
		"existing NATS command-log load stream BUDGIE_COMMAND_LOG_LOAD_OLD already owns budgie.commandlog.>",
		"existing NATS command-commit load stream BUDGIE_COMMAND_LOG_LOAD_OLD already owns budgie.commandcommit.>",
		"existing NATS event-log load stream BUDGIE_EVENT_LOG_LOAD_OLD already owns budgie.eventlog.>",
		"nats --server \"$BUDGIE_NATS_URL\" stream rm --force BUDGIE_COMMAND_LOG_LOAD_...",
		"do not delete non-load streams",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("script output missing %q:\n%s", token, body)
		}
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting overlapping streams:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateUsesNATSBinOverrideForPreflight(t *testing.T) {
	gitDir := t.TempDir()
	natsDir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	writeFakeCommandLogGateGit(t, gitDir, "")
	fakeNATS := writeFakeCommandLogGateNATS(t, natsDir, `case "$subject" in
  "budgie.commandlog.>")
    echo "BUDGIE_COMMAND_LOG_LOAD_FROM_OVERRIDE"
    ;;
esac
`)

	callLog := filepath.Join(natsDir, "nats-calls.log")
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"NATS_BIN="+fakeNATS,
		"NATS_CALL_LOG="+callLog,
		"PATH="+gitDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "existing NATS command-log load stream BUDGIE_COMMAND_LOG_LOAD_FROM_OVERRIDE") {
		t.Fatalf("script output missing NATS_BIN preflight result:\n%s", body)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read NATS call log: %v", err)
	}
	if !strings.Contains(string(calls), "stream ls --names --subject budgie.commandlog.>") {
		t.Fatalf("NATS_BIN override was not used for stream preflight; calls:\n%s", calls)
	}
}

func TestCommandLogNativeNATSGateRejectsReportOutsideArtifactsBeforeRun(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMANDLOG_GATE_REPORT must be a relative file path under artifacts/internet-scale/") {
		t.Fatalf("script output missing report-path error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting report path:\n%s", body)
	}
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsAlternateBudgetBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_GATE_BUDGET=/tmp/relaxed-budget.json",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMANDLOG_GATE_BUDGET must be ops/internet-scale-budgets.example.json or ops/internet-scale-remote-staging-budgets.example.json") {
		t.Fatalf("script output missing promoted-budget error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting alternate budget:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateUsesRemoteStagingBudget(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.internal:4222",
		"BUDGIE_POSTGRES_DSN=postgres://postgres.internal:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1",
		"CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "budget:         ops/internet-scale-remote-staging-budgets.example.json") {
		t.Fatalf("script output missing remote budget line:\n%s", body)
	}
	if _, err := os.Stat(reportFSPath); err != nil {
		t.Fatalf("remote budget report was not archived: %v", err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if strings.Count(string(calls), "-budget-file ops/internet-scale-remote-staging-budgets.example.json") != 2 {
		t.Fatalf("fake go calls missing remote budget for loadgen and report-check:\n%s", calls)
	}
}

func TestCommandLogNativeNATSGateRejectsRemoteStagingLoopbackEndpointsBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://127.0.0.1:4222",
		"BUDGIE_POSTGRES_DSN=postgres://localhost:55432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "remote staging budget requires non-local BUDGIE_NATS_URL") {
		t.Fatalf("script output missing remote NATS endpoint error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting loopback endpoints:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before remote endpoint failure; stat err=%v", err)
	}
}

func TestCommandLogNativeKafkaGateRejectsRemoteStagingLoopbackEndpointsBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=127.0.0.1:9092,redpanda.internal:9092",
		"BUDGIE_POSTGRES_DSN=postgres://localhost:55432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "remote staging budget requires non-local BUDGIE_KAFKA_BROKERS") {
		t.Fatalf("script output missing remote Kafka broker error:\n%s", body)
	}
	if strings.Contains(body, "running durable native Kafka command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting loopback endpoints:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before remote endpoint failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsDirtyGitTreeBeforeRun(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	fakeGit := writeFakeCommandLogGateGit(t, dir, " M internal/core/storage.go\n")
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"PATH="+filepath.Dir(fakeGit)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "requires a clean git tree for promotion evidence") ||
		!strings.Contains(body, "internal/core/storage.go") {
		t.Fatalf("script output missing dirty-git error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting dirty git tree:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsExtraArgsBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh", "-command-log-backend", "memory")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "does not accept extra loadgen flags") {
		t.Fatalf("script output missing extra-arg error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting extra args:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeKafkaGateRejectsExtraArgsBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh", "-command-log-backend", "memory")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "does not accept extra loadgen flags") {
		t.Fatalf("script output missing extra-arg error:\n%s", body)
	}
	if strings.Contains(body, "running durable native Kafka command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting extra args:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsWrongStreamPrefixBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMAND_LOG_LOAD_STREAM=PRODUCTION_COMMANDS",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMAND_LOG_LOAD_STREAM must start with BUDGIE_COMMAND_LOG_LOAD_") {
		t.Fatalf("script output missing stream-prefix error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting wrong stream prefix:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeKafkaGateRejectsWrongTopicPrefixBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMAND_LOG_LOAD_TOPIC=budgie.commands.production",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMAND_LOG_LOAD_TOPIC must start with budgie.commands.load.") {
		t.Fatalf("script output missing topic-prefix error:\n%s", body)
	}
	if strings.Contains(body, "running durable native Kafka command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting wrong topic prefix:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsUnderBudgetOverridesBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_GATE_BOARDS=7",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMANDLOG_GATE_BOARDS must be at least 8") {
		t.Fatalf("script output missing under-budget error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting under-budget config:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeKafkaGateRejectsUnderBudgetPartitionsBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS=16",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS must be at least 32") {
		t.Fatalf("script output missing partition budget error:\n%s", body)
	}
	if strings.Contains(body, "running durable native Kafka command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting under-budget partitions:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsExistingReportBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	if err := os.MkdirAll(filepath.Dir(reportFSPath), 0o755); err != nil {
		t.Fatalf("mkdir existing report dir: %v", err)
	}
	if err := os.WriteFile(reportFSPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write existing report: %v", err)
	}
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "report file already exists") ||
		!strings.Contains(body, "BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE=1") {
		t.Fatalf("script output missing overwrite guidance:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting existing report:\n%s", body)
	}
}

func TestCommandLogNativeNATSGateArchivesReportOnlyAfterVerification(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	checkedLog := filepath.Join(dir, "checked.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  report=""
  prev=""
  for arg in "$@"; do
    if [[ "$prev" == "-report-file" ]]; then
      report="$arg"
      break
    fi
    prev="$arg"
  done
  [[ -f "$report" ]]
  echo "$report" >> "$CHECKED_REPORT_LOG"
  if [[ "$report" == "$FINAL_REPORT" ]]; then
    echo "checked final report before archive" >&2
    exit 7
  fi
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"GO_BIN=/bin/false",
		"CALL_LOG="+callLog,
		"CHECKED_REPORT_LOG="+checkedLog,
		"FINAL_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS=2",
		"BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS=3",
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1",
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	if !strings.Contains(string(out), "archived verified report") {
		t.Fatalf("script output missing archive confirmation:\n%s", out)
	}
	if !strings.Contains(string(out), "preserved load streams for inspection") {
		t.Fatalf("script output missing rerun guidance:\n%s", out)
	}
	report, err := os.ReadFile(reportFSPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(report) != "{\"ok\":true}\n" {
		t.Fatalf("report = %q, want fake loadgen output", report)
	}
	checked, err := os.ReadFile(checkedLog)
	if err != nil {
		t.Fatalf("read checked log: %v", err)
	}
	checkedPath := strings.TrimSpace(string(checked))
	if checkedPath == "" || checkedPath == reportPath || !strings.Contains(filepath.Base(checkedPath), ".report.json.tmp.") {
		t.Fatalf("checked report path = %q, want temporary report", checkedPath)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if strings.Count(string(calls), "./cmd/budgie-commandlog-loadgen") != 1 ||
		strings.Count(string(calls), "./cmd/budgie-commandlog-report-check") != 1 {
		t.Fatalf("unexpected fake go calls:\n%s", calls)
	}
	if !strings.Contains(string(calls), "-command-log-nats-stream BUDGIE_COMMAND_LOG_LOAD_") ||
		!strings.Contains(string(calls), "-event-log-nats-stream BUDGIE_EVENT_LOG_LOAD_") {
		t.Fatalf("fake go calls missing generated stream prefixes:\n%s", calls)
	}
	if !strings.Contains(string(calls), "-command-log-nats-replicas 2") ||
		!strings.Contains(string(calls), "-event-log-nats-replicas 3") {
		t.Fatalf("fake go calls missing replica overrides:\n%s", calls)
	}
	if strings.Contains(string(calls), "BUDGIE_COMMAND_LOG_LOAD_STAGING") ||
		strings.Contains(string(calls), "BUDGIE_EVENT_LOG_LOAD_STAGING") {
		t.Fatalf("fake go calls reused fixed staging streams:\n%s", calls)
	}
}

func TestCommandLogNativeKafkaGateArchivesReportOnlyAfterVerification(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	checkedLog := filepath.Join(dir, "checked.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  report=""
  prev=""
  for arg in "$@"; do
    if [[ "$prev" == "-report-file" ]]; then
      report="$arg"
      break
    fi
    prev="$arg"
  done
  [[ -f "$report" ]]
  echo "$report" >> "$CHECKED_REPORT_LOG"
  if [[ "$report" == "$FINAL_REPORT" ]]; then
    echo "checked final report before archive" >&2
    exit 7
  fi
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"GO_BIN=/bin/false",
		"CALL_LOG="+callLog,
		"CHECKED_REPORT_LOG="+checkedLog,
		"FINAL_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS=48",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS=64",
		"BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS=2",
		"PATH="+dir+string(os.PathListSeparator)+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	if !strings.Contains(string(out), "archived verified report") {
		t.Fatalf("script output missing archive confirmation:\n%s", out)
	}
	if !strings.Contains(string(out), "preserved Kafka load topics for inspection") {
		t.Fatalf("script output missing rerun guidance:\n%s", out)
	}
	report, err := os.ReadFile(reportFSPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(report) != "{\"ok\":true}\n" {
		t.Fatalf("report = %q, want fake loadgen output", report)
	}
	checked, err := os.ReadFile(checkedLog)
	if err != nil {
		t.Fatalf("read checked log: %v", err)
	}
	checkedPath := strings.TrimSpace(string(checked))
	if checkedPath == "" || checkedPath == reportPath || !strings.Contains(filepath.Base(checkedPath), ".report.json.tmp.") {
		t.Fatalf("checked report path = %q, want temporary report", checkedPath)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	if strings.Count(callBody, "./cmd/budgie-commandlog-loadgen") != 1 ||
		strings.Count(callBody, "./cmd/budgie-commandlog-report-check") != 1 {
		t.Fatalf("unexpected fake go calls:\n%s", calls)
	}
	for _, token := range []string{
		"-command-log-backend kafka",
		"-kafka-brokers redpanda.invalid:9092",
		"-kafka-command-topic budgie.commands.load.",
		"-kafka-event-topic budgie.events.load.",
		"-kafka-consumer-group budgie-writers-load-",
		"-kafka-command-partitions 48",
		"-kafka-event-partitions 64",
		"-kafka-topic-replicas 2",
		"-kafka-scalar-allocator sql-event-partition-offsets",
		"-budget-file ops/internet-scale-kafka-budgets.example.json",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("fake go calls missing %q:\n%s", token, calls)
		}
	}
	if strings.Contains(callBody, "budgie.commands.production") ||
		strings.Contains(callBody, "budgie.events.production") {
		t.Fatalf("fake go calls reused production topics:\n%s", calls)
	}
}

func TestCommandLogNativeKafkaGateUsesRemoteStagingBudget(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-kafka-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda-a.internal:9092,redpanda-b.internal:9092",
		"BUDGIE_POSTGRES_DSN=postgres://postgres.internal:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING=1",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "budget:         ops/internet-scale-kafka-remote-staging-budgets.example.json") {
		t.Fatalf("script output missing remote Kafka budget line:\n%s", body)
	}
	if _, err := os.Stat(reportFSPath); err != nil {
		t.Fatalf("remote budget report was not archived: %v", err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if strings.Count(string(calls), "-budget-file ops/internet-scale-kafka-remote-staging-budgets.example.json") != 2 {
		t.Fatalf("fake go calls missing remote Kafka budget for loadgen and report-check:\n%s", calls)
	}
}

func TestGatewayFanoutGateArchivesReportAfterLoadgenPasses(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-gateway-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-gateway-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "gateway-fanout-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_GATEWAY_FANOUT_GATE_REPORT="+reportPath,
		"CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "archived verified gateway fanout report") ||
		!strings.Contains(body, "gateway fanout capacity gate passed") {
		t.Fatalf("script output missing success confirmation:\n%s", body)
	}
	report, err := os.ReadFile(reportFSPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(report) != "{\"ok\":true}\n" {
		t.Fatalf("report = %q, want fake loadgen output", report)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	for _, token := range []string{
		"./cmd/budgie-gateway-loadgen",
		"-hot-subscribers 10000",
		"-idle-subscribers 90000",
		"-buffer-size 2",
		"-events 1",
		"-target-connections 1000000",
		"-budget-file ops/internet-scale-budgets.example.json",
		"./cmd/budgie-gateway-report-check",
		"-report-file artifacts/internet-scale/",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("fake go calls missing %q:\n%s", token, calls)
		}
	}
}

func TestGatewayFanoutGateUsesRemoteStagingBudget(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	reportFSPath := commandLogGateReportFSPath(reportPath)
	callLog := filepath.Join(dir, "calls.log")
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-gateway-loadgen" ]]; then
  printf '{"ok":true}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-gateway-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "gateway-fanout-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING=1",
		"BUDGIE_GATEWAY_FANOUT_GATE_REPORT="+reportPath,
		"CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "budget:             ops/internet-scale-remote-staging-budgets.example.json") {
		t.Fatalf("script output missing remote gateway budget line:\n%s", body)
	}
	if _, err := os.Stat(reportFSPath); err != nil {
		t.Fatalf("remote budget report was not archived: %v", err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if !strings.Contains(string(calls), "-budget-file ops/internet-scale-remote-staging-budgets.example.json") {
		t.Fatalf("fake go calls missing remote gateway budget:\n%s", calls)
	}
	if strings.Count(string(calls), "-budget-file ops/internet-scale-remote-staging-budgets.example.json") != 2 {
		t.Fatalf("fake go calls missing remote gateway budget for loadgen and report-check:\n%s", calls)
	}
}

func TestInternetScaleStagingGateRunsGatewayAndNATSTargets(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeCommandLogGateGit(t, dir, "")
	preflight := writeFakeInternetScaleBundlePreflight(t, dir)
	gatewayGate := writeFakeInternetScaleBundleGate(t, dir, "gateway")
	natsGate := writeFakeInternetScaleBundleGate(t, dir, "nats")
	kafkaGate := writeFakeInternetScaleBundleGate(t, dir, "kafka")
	reportCheck := writeFakeInternetScaleBundleReportCheck(t, dir)

	cmd := exec.Command("bash", "internet-scale-staging-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway,nats",
		"BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX=bundle-test",
		"BUDGIE_NATS_URL=nats://nats.internal:4222",
		"BUDGIE_POSTGRES_DSN=postgres://postgres.internal:5432/budgie",
		"BUDGIE_INTERNET_SCALE_GATE_PREFLIGHT_SCRIPT="+preflight,
		"BUDGIE_GATEWAY_FANOUT_GATE_SCRIPT="+gatewayGate,
		"BUDGIE_COMMANDLOG_NATS_GATE_SCRIPT="+natsGate,
		"BUDGIE_COMMANDLOG_KAFKA_GATE_SCRIPT="+kafkaGate,
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_CHECK_SCRIPT="+reportCheck,
		"CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"internet-scale staging targets: gateway nats",
		"remote staging budgets: enabled",
		"remote staging create/delete preflight passed",
		"archived internet-scale report bundle check passed",
		"preflight report: artifacts/internet-scale/preflight-report-bundle-test.json",
		"gateway report: artifacts/internet-scale/gateway-fanout-report-bundle-test.json",
		"nats report:    artifacts/internet-scale/commandlog-native-nats-report-bundle-test.json",
		"bundle manifest: artifacts/internet-scale/bundle-manifest-bundle-test.json",
		"internet-scale staging evidence bundle passed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("script output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	for _, token := range []string{
		"preflight targets=nats remote=1 report=artifacts/internet-scale/preflight-report-bundle-test.json",
		"gateway gateway=artifacts/internet-scale/gateway-fanout-report-bundle-test.json gateway_remote=1",
		"nats gateway= gateway_remote= commandlog=artifacts/internet-scale/commandlog-native-nats-report-bundle-test.json command_remote=1",
		"report-check targets=gateway,nats remote=1 suffix=bundle-test manifest=artifacts/internet-scale/bundle-manifest-bundle-test.json",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("fake gate calls missing %q:\n%s", token, calls)
		}
	}
	if strings.Contains(callBody, "kafka ") {
		t.Fatalf("kafka gate should not run for gateway,nats targets:\n%s", calls)
	}
	if strings.Contains(callBody, "targets=kafka") {
		t.Fatalf("preflight should not probe Kafka for gateway,nats targets:\n%s", calls)
	}
}

func TestInternetScaleStagingGateRejectsImplicitGatewayOnlyTarget(t *testing.T) {
	dir := t.TempDir()
	writeFakeCommandLogGateGit(t, dir, "")
	cmd := exec.Command("bash", "internet-scale-staging-gate.sh")
	cmd.Env = []string{
		"GIT_STATUS_OUTPUT=",
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "no durable staging target detected") ||
		!strings.Contains(body, "BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway") {
		t.Fatalf("script output missing gateway-only guidance:\n%s", body)
	}
}

func TestInternetScaleReportCheckRunsGatewayAndKafkaTargets(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/gateway-fanout-report-bundle-test.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/commandlog-native-kafka-report-bundle-test.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/preflight-report-bundle-test.json")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-gateway-report-check" || "$2" == "./cmd/budgie-commandlog-report-check" || "$2" == "./cmd/budgie-internet-scale-preflight-report-check" || "$2" == "./cmd/budgie-internet-scale-bundle-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=gateway,kafka",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=bundle-test",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST=artifacts/internet-scale/bundle-manifest-bundle-test.json",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"internet-scale report-check targets: gateway kafka",
		"remote staging budgets: enabled",
		"remote staging preflight report passed",
		"gateway fanout report passed",
		"native Kafka command-log report passed",
		"bundle evidence consistency passed",
		"internet-scale report bundle satisfies selected budgets",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("script output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	for _, token := range []string{
		"./cmd/budgie-internet-scale-preflight-report-check -report-file artifacts/internet-scale/preflight-report-bundle-test.json -targets kafka -remote-staging",
		"./cmd/budgie-gateway-report-check -report-file artifacts/internet-scale/gateway-fanout-report-bundle-test.json -budget-file ops/internet-scale-remote-staging-budgets.example.json",
		"./cmd/budgie-commandlog-report-check -report-file artifacts/internet-scale/commandlog-native-kafka-report-bundle-test.json -budget-file ops/internet-scale-kafka-remote-staging-budgets.example.json",
		"./cmd/budgie-internet-scale-bundle-report-check -targets gateway,kafka -remote-staging -preflight-report artifacts/internet-scale/preflight-report-bundle-test.json -gateway-report artifacts/internet-scale/gateway-fanout-report-bundle-test.json -kafka-report artifacts/internet-scale/commandlog-native-kafka-report-bundle-test.json -manifest-file artifacts/internet-scale/bundle-manifest-bundle-test.json",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("fake go calls missing %q:\n%s", token, calls)
		}
	}
	if strings.Contains(callBody, "commandlog-native-nats") {
		t.Fatalf("NATS report should not be checked for gateway,kafka targets:\n%s", calls)
	}
}

func TestInternetScaleReportCheckVerifiesExistingBundleManifest(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/gateway-fanout-report-bundle-test.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/commandlog-native-kafka-report-bundle-test.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/preflight-report-bundle-test.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/bundle-manifest-bundle-test.json")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-gateway-report-check" || "$2" == "./cmd/budgie-commandlog-report-check" || "$2" == "./cmd/budgie-internet-scale-preflight-report-check" || "$2" == "./cmd/budgie-internet-scale-bundle-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=gateway,kafka",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=bundle-test",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST=artifacts/internet-scale/bundle-manifest-bundle-test.json",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"verify bundle manifest: artifacts/internet-scale/bundle-manifest-bundle-test.json",
		"bundle manifest read-back passed",
		"internet-scale report bundle satisfies selected budgets",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("script output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	if !strings.Contains(callBody, "./cmd/budgie-internet-scale-bundle-report-check -verify-manifest artifacts/internet-scale/bundle-manifest-bundle-test.json -targets gateway,kafka -remote-staging") {
		t.Fatalf("fake go calls missing manifest verify invocation:\n%s", calls)
	}
	if strings.Contains(callBody, "-manifest-file") {
		t.Fatalf("manifest verify mode should not rewrite the manifest:\n%s", calls)
	}
}

func TestInternetScaleReportCheckRemoteDurableRequiresPreflightReport(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/commandlog-native-kafka-report-missing-preflight.json")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `exit 9`)

	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=kafka",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING=1",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=missing-preflight",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "preflight report not found") ||
		!strings.Contains(body, "artifacts/internet-scale/preflight-report-missing-preflight.json") {
		t.Fatalf("script output missing preflight report guidance:\n%s", body)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("go should not run after missing preflight report; stat err=%v", err)
	}
}

func TestInternetScaleReportCheckPropagatesBundleConsistencyFailure(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/gateway-fanout-report-mixed-revision.json")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/commandlog-native-nats-report-mixed-revision.json")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-gateway-report-check" || "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  exit 0
fi
if [[ "$2" == "./cmd/budgie-internet-scale-bundle-report-check" ]]; then
  echo "mixed git revision" >&2
  exit 3
fi
exit 9
`)

	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=gateway,nats",
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX=mixed-revision",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "bundle evidence consistency") ||
		!strings.Contains(body, "mixed git revision") {
		t.Fatalf("script output missing bundle consistency failure:\n%s", body)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if !strings.Contains(string(calls), "./cmd/budgie-internet-scale-bundle-report-check -targets gateway,nats -gateway-report artifacts/internet-scale/gateway-fanout-report-mixed-revision.json -nats-report artifacts/internet-scale/commandlog-native-nats-report-mixed-revision.json") {
		t.Fatalf("fake go calls missing bundle consistency invocation:\n%s", calls)
	}
}

func TestInternetScaleReportCheckAcceptsStagingGateSuffixAlias(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	writeFakeInternetScaleReport(t, "artifacts/internet-scale/commandlog-native-nats-report-gate-suffix.json")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-report-check" || "$2" == "./cmd/budgie-internet-scale-bundle-report-check" ]]; then
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=nats",
		"BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX=gate-suffix",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "report suffix: gate-suffix") ||
		!strings.Contains(body, "native NATS command-log report passed") {
		t.Fatalf("script output missing suffix alias evidence:\n%s", body)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	if !strings.Contains(string(calls), "artifacts/internet-scale/commandlog-native-nats-report-gate-suffix.json") {
		t.Fatalf("fake go calls missing gate suffix report path:\n%s", calls)
	}
}

func TestInternetScaleReportCheckRejectsMissingSuffixOrExplicitReport(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `exit 9`)
	cmd := exec.Command("bash", "internet-scale-report-check.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS=gateway",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "set BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX") {
		t.Fatalf("script output missing suffix guidance:\n%s", body)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("go should not run after missing suffix; stat err=%v", err)
	}
}

func TestSingleRegionFailureDrillPreflightChecksEndpointsAlertsAndSmoke(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	alertRules := filepath.Join(dir, "alerts.yml")
	metricDir := filepath.Join(dir, "metrics")
	if err := os.WriteFile(alertRules, []byte(`groups:
  - name: budgie
    rules:
      - alert: BudgieRemoteDeliveryLagHigh
      - alert: BudgieWriteRegionProxyFailures
      - alert: BudgieProjectionLagHigh
      - alert: BudgieCommandLogWriterLagHigh
`), 0o644); err != nil {
		t.Fatalf("write alert rules: %v", err)
	}
	fakeCurl := writeFakeSingleRegionPreflightCurl(t, dir)
	fakeJQ := writeFakeSingleRegionPreflightJQ(t, dir, `exit 0
`)
	fakeSmoke := writeFakeSingleRegionPreflightSmoke(t, dir)

	cmd := exec.Command("bash", "single-region-failure-drill-preflight.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_API=https://write.example.test/",
		"BUDGIE_REGIONAL_API=https://regional.example.test/",
		"BUDGIE_WRITE_REGION_URL=https://write-region.example.test",
		"BUDGIE_ADMIN_TOKEN=admin-token",
		"BUDGIE_USER_TOKEN=user-token",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@db.internal:5432/budgie?sslmode=require",
		"BUDGIE_NATS_URL=nats://nats.internal:4222",
		"BUDGIE_ALERT_RULES="+alertRules,
		"BUDGIE_PROMETHEUS_URL=https://prom.example.test/",
		"BUDGIE_SINGLE_REGION_PREFLIGHT_METRIC_DIR="+metricDir,
		"BUDGIE_SINGLE_REGION_PREFLIGHT_CLUSTER_SMOKE="+fakeSmoke,
		"CURL_BIN="+fakeCurl,
		"JQ_BIN="+fakeJQ,
		"CALL_LOG="+callLog,
		"DRILL_ID=drill-test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("preflight failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"single-region drill preflight: drill-test",
		"checking IS9 alert rules",
		"checking write endpoint readiness",
		"checking regional endpoint readiness",
		"capturing baseline metrics",
		"checking Prometheus critical Budgie alerts",
		"no critical Budgie alerts are firing",
		"running cluster smoke",
		"single-region drill preflight passed",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("preflight output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	for _, token := range []string{
		"curl -fsS https://write.example.test/readyz",
		"curl -fsS https://regional.example.test/readyz",
		"curl -fsS https://write.example.test/metrics",
		"curl -fsS https://regional.example.test/metrics",
		"curl -fsS https://prom.example.test/api/v1/alerts",
		"jq -r",
		"cluster-smoke postgres=postgres://budgie@db.internal:5432/budgie?sslmode=require nats=nats://nats.internal:4222",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("call log missing %q:\n%s", token, calls)
		}
	}
	for _, path := range []string{
		filepath.Join(metricDir, "api.metrics"),
		filepath.Join(metricDir, "regional.metrics"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read captured metric %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "budgie_write_region_proxy_failures_total") {
			t.Fatalf("metric file %s missing fake metric: %s", path, raw)
		}
	}
	alertsRaw, err := os.ReadFile(filepath.Join(metricDir, "prometheus-alerts.json"))
	if err != nil {
		t.Fatalf("read captured Prometheus alerts: %v", err)
	}
	if !strings.Contains(string(alertsRaw), `"status":"success"`) {
		t.Fatalf("captured Prometheus alerts missing fake response: %s", alertsRaw)
	}
}

func TestSingleRegionFailureDrillPreflightRejectsCriticalPrometheusAlerts(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	alertRules := filepath.Join(dir, "alerts.yml")
	metricDir := filepath.Join(dir, "metrics")
	if err := os.WriteFile(alertRules, []byte(`groups:
  - name: budgie
    rules:
      - alert: BudgieRemoteDeliveryLagHigh
      - alert: BudgieWriteRegionProxyFailures
      - alert: BudgieProjectionLagHigh
      - alert: BudgieCommandLogWriterLagHigh
`), 0o644); err != nil {
		t.Fatalf("write alert rules: %v", err)
	}
	fakeCurl := writeFakeSingleRegionPreflightCurl(t, dir)
	fakeJQ := writeFakeSingleRegionPreflightJQ(t, dir, `echo "BudgieWriteRegionProxyFailures"
`)
	fakeSmoke := writeFakeSingleRegionPreflightSmoke(t, dir)

	cmd := exec.Command("bash", "single-region-failure-drill-preflight.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_API=https://write.example.test",
		"BUDGIE_REGIONAL_API=https://regional.example.test",
		"BUDGIE_WRITE_REGION_URL=https://write-region.example.test",
		"BUDGIE_ADMIN_TOKEN=admin-token",
		"BUDGIE_USER_TOKEN=user-token",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@db.internal:5432/budgie?sslmode=require",
		"BUDGIE_NATS_URL=nats://nats.internal:4222",
		"BUDGIE_ALERT_RULES="+alertRules,
		"BUDGIE_PROMETHEUS_URL=https://prom.example.test",
		"BUDGIE_SINGLE_REGION_PREFLIGHT_METRIC_DIR="+metricDir,
		"BUDGIE_SINGLE_REGION_PREFLIGHT_CLUSTER_SMOKE="+fakeSmoke,
		"CURL_BIN="+fakeCurl,
		"JQ_BIN="+fakeJQ,
		"CALL_LOG="+callLog,
		"DRILL_ID=drill-test",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("preflight unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"critical Budgie alerts are already firing",
		"BudgieWriteRegionProxyFailures",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("preflight output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callBody := string(calls)
	if !strings.Contains(callBody, "curl -fsS https://prom.example.test/api/v1/alerts") {
		t.Fatalf("Prometheus alerts endpoint was not called:\n%s", calls)
	}
	if strings.Contains(callBody, "cluster-smoke") {
		t.Fatalf("cluster smoke should not run after critical alerts:\n%s", calls)
	}
}

func TestSingleRegionFailureDrillPreflightRejectsPlaceholderTokensBeforeCurl(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	fakeCurl := writeFakeSingleRegionPreflightCurl(t, dir)
	cmd := exec.Command("bash", "single-region-failure-drill-preflight.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_API=https://write.example.test",
		"BUDGIE_REGIONAL_API=https://regional.example.test",
		"BUDGIE_WRITE_REGION_URL=https://write-region.example.test",
		"BUDGIE_ADMIN_TOKEN=replace-me",
		"BUDGIE_USER_TOKEN=user-token",
		"CURL_BIN="+fakeCurl,
		"CALL_LOG="+callLog,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("preflight unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_ADMIN_TOKEN still has the runbook placeholder value") {
		t.Fatalf("preflight output missing placeholder guidance:\n%s", body)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("curl should not run after placeholder token; stat err=%v", err)
	}
}

func TestGatewayFanoutGateRejectsUnderBudgetShapeBeforeRun(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	callLog := filepath.Join(dir, "calls.log")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `exit 9`)

	cmd := exec.Command("bash", "gateway-fanout-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_GATEWAY_FANOUT_GATE_REPORT="+reportPath,
		"BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS=9999",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS must be at least 10000") {
		t.Fatalf("script output missing hot subscriber budget error:\n%s", body)
	}
	if strings.Contains(body, "running gateway fanout capacity gate") {
		t.Fatalf("script reached loadgen before rejecting under-budget shape:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("go should not run after under-budget preflight; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateRejectsInvalidReplicaOverridesBeforeRun(t *testing.T) {
	reportPath := commandLogGateReportPath(t)
	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS=0",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS must be a positive integer") {
		t.Fatalf("script output missing replica override error:\n%s", body)
	}
	if strings.Contains(body, "running durable native command/event staging gate") {
		t.Fatalf("script reached staging run before rejecting replica override:\n%s", body)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("report path should not be created before preflight failure; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateDoesNotArchiveReportWhenVerificationFails(t *testing.T) {
	dir := t.TempDir()
	reportPath := commandLogGateReportPath(t)
	writeFakeCommandLogGateGit(t, dir, "")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-commandlog-loadgen" ]]; then
  printf '{"ok":false}\n'
  exit 0
fi
if [[ "$2" == "./cmd/budgie-commandlog-report-check" ]]; then
  exit 6
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-nats-gate.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"BUDGIE_POSTGRES_DSN=postgres://budgie@postgres.invalid:5432/budgie",
		"BUDGIE_COMMANDLOG_GATE_REPORT="+reportPath,
		"GO_BIN=/bin/false",
		"CALL_LOG="+filepath.Join(dir, "calls.log"),
		"BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT=1",
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script unexpectedly succeeded:\n%s", out)
	}
	if _, err := os.Stat(commandLogGateReportFSPath(reportPath)); !os.IsNotExist(err) {
		t.Fatalf("final report should not be archived after failed verification; stat err=%v", err)
	}
}

func TestCommandLogNativeNATSGateArtifactsAreIgnored(t *testing.T) {
	raw, err := os.ReadFile("../.gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(raw), "/artifacts/") {
		t.Fatal(".gitignore must ignore /artifacts/ so staging reports do not dirty VCS evidence")
	}
}

func TestCommandLogNativeNATSCleanupDryRunListsDisposableLoadStreams(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "nats-calls.log")
	writeFakeCommandLogGateNATS(t, dir, fakeCommandLogCleanupNATSBody())
	info, err := os.Stat("commandlog-native-nats-cleanup.sh")
	if err != nil {
		t.Fatalf("stat cleanup script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("commandlog-native-nats-cleanup.sh is not executable")
	}

	cmd := exec.Command("bash", "commandlog-native-nats-cleanup.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"NATS_CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cleanup dry run failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"non-load streams also own gate subjects and will not be deleted",
		"BUDGIE_COMMAND_LOG",
		"disposable command/event load streams",
		"BUDGIE_COMMAND_LOG_LOAD_OLD",
		"BUDGIE_EVENT_LOG_LOAD_OLD",
		"dry run only; pass --execute to delete these load streams",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("cleanup dry-run output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read nats call log: %v", err)
	}
	if strings.Contains(string(calls), "stream rm") {
		t.Fatalf("dry run must not delete streams; calls:\n%s", calls)
	}
}

func TestCommandLogNativeNATSCleanupUsesNATSBinOverride(t *testing.T) {
	dir := t.TempDir()
	natsDir := t.TempDir()
	callLog := filepath.Join(dir, "nats-calls.log")
	fakeNATS := writeFakeCommandLogGateNATS(t, natsDir, fakeCommandLogCleanupNATSBody())

	cmd := exec.Command("bash", "commandlog-native-nats-cleanup.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"NATS_BIN="+fakeNATS,
		"NATS_CALL_LOG="+callLog,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cleanup dry run failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "BUDGIE_COMMAND_LOG_LOAD_OLD") ||
		!strings.Contains(body, "dry run only; pass --execute to delete these load streams") {
		t.Fatalf("cleanup output missing NATS_BIN stream listing:\n%s", body)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read nats call log: %v", err)
	}
	if !strings.Contains(string(calls), "stream ls --names --subject budgie.commandlog.>") {
		t.Fatalf("NATS_BIN override was not used for cleanup listing; calls:\n%s", calls)
	}
}

func TestCommandLogNativeNATSCleanupExecuteDeletesOnlyDisposableLoadStreams(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "nats-calls.log")
	writeFakeCommandLogGateNATS(t, dir, fakeCommandLogCleanupNATSBody())

	cmd := exec.Command("bash", "commandlog-native-nats-cleanup.sh", "--execute")
	cmd.Env = append(os.Environ(),
		"BUDGIE_NATS_URL=nats://nats.invalid:4222",
		"NATS_CALL_LOG="+callLog,
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cleanup execute failed:\n%s", out)
	}
	body := string(out)
	if !strings.Contains(body, "load stream cleanup complete") {
		t.Fatalf("cleanup execute output missing completion:\n%s", body)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read nats call log: %v", err)
	}
	callBody := string(calls)
	if strings.Count(callBody, "stream rm --force BUDGIE_COMMAND_LOG_LOAD_OLD") != 1 {
		t.Fatalf("command load stream should be deleted exactly once; calls:\n%s", calls)
	}
	if strings.Count(callBody, "stream rm --force BUDGIE_EVENT_LOG_LOAD_OLD") != 1 {
		t.Fatalf("event load stream should be deleted exactly once; calls:\n%s", calls)
	}
	if strings.Contains(callBody, "stream rm --force BUDGIE_COMMAND_LOG\n") ||
		strings.Contains(callBody, "stream rm --force BUDGIE_EVENT_LOG\n") {
		t.Fatalf("cleanup must not delete non-load streams; calls:\n%s", calls)
	}
}

func TestCommandLogNativeKafkaCleanupDryRunInvokesGoCleanupCommand(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "go-calls.log")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-kafka-load-topic-cleanup" ]]; then
  echo "==> disposable Kafka load topics"
  echo "    budgie.commands.load.old"
  echo "==> dry run only; pass --execute to delete these load topics"
  exit 0
fi
exit 9
`)
	info, err := os.Stat("commandlog-native-kafka-cleanup.sh")
	if err != nil {
		t.Fatalf("stat Kafka cleanup script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("commandlog-native-kafka-cleanup.sh is not executable")
	}

	cmd := exec.Command("bash", "commandlog-native-kafka-cleanup.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Kafka cleanup dry run failed:\n%s", out)
	}
	body := string(out)
	for _, token := range []string{
		"disposable Kafka load topics",
		"budgie.commands.load.old",
		"dry run only; pass --execute",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("cleanup dry-run output missing %q:\n%s", token, body)
		}
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read go call log: %v", err)
	}
	callBody := string(calls)
	for _, token := range []string{
		"run ./cmd/budgie-kafka-load-topic-cleanup",
		"-kafka-brokers redpanda.invalid:9092",
		"-command-topic-prefix budgie.commands.load.",
		"-event-topic-prefix budgie.events.load.",
		"-timeout 30s",
	} {
		if !strings.Contains(callBody, token) {
			t.Fatalf("go calls missing %q:\n%s", token, calls)
		}
	}
	if strings.Contains(callBody, "-execute") {
		t.Fatalf("dry run must not pass -execute; calls:\n%s", calls)
	}
}

func TestCommandLogNativeKafkaCleanupExecutePassesExecuteFlag(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "go-calls.log")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `if [[ "$2" == "./cmd/budgie-kafka-load-topic-cleanup" ]]; then
  echo "==> Kafka load topic cleanup complete"
  exit 0
fi
exit 9
`)

	cmd := exec.Command("bash", "commandlog-native-kafka-cleanup.sh", "--execute")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=redpanda.invalid:9092",
		"BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT=2m",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Kafka cleanup execute failed:\n%s", out)
	}
	if !strings.Contains(string(out), "Kafka load topic cleanup complete") {
		t.Fatalf("cleanup execute output missing completion:\n%s", out)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read go call log: %v", err)
	}
	callBody := string(calls)
	if !strings.Contains(callBody, "-execute") {
		t.Fatalf("execute cleanup did not pass -execute; calls:\n%s", calls)
	}
	if !strings.Contains(callBody, "-timeout 2m") {
		t.Fatalf("execute cleanup did not pass timeout override; calls:\n%s", calls)
	}
}

func TestCommandLogNativeKafkaCleanupRejectsMissingBrokersBeforeGo(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "go-calls.log")
	fakeGo := writeFakeCommandLogGateGo(t, dir, `exit 9`)

	cmd := exec.Command("bash", "commandlog-native-kafka-cleanup.sh")
	cmd.Env = append(os.Environ(),
		"BUDGIE_KAFKA_BROKERS=",
		"CALL_LOG="+callLog,
		"PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Kafka cleanup unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "set BUDGIE_KAFKA_BROKERS") {
		t.Fatalf("missing broker output = %s, want env error", out)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("go should not run after missing broker env; stat err=%v", err)
	}
}

func writeFakeCommandLogGateGo(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "go")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "$@" >> "$CALL_LOG"
` + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	return path
}

func writeFakeInternetScaleBundleGate(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+"-gate")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "` + name + ` gateway=${BUDGIE_GATEWAY_FANOUT_GATE_REPORT:-} gateway_remote=${BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING:-} commandlog=${BUDGIE_COMMANDLOG_GATE_REPORT:-} command_remote=${BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING:-}" >> "$CALL_LOG"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake internet-scale bundle gate: %v", err)
	}
	return path
}

func writeFakeInternetScaleBundlePreflight(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "bundle-preflight")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "preflight targets=${BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS:-} remote=${BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING:-} report=${BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT:-}" >> "$CALL_LOG"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake internet-scale preflight: %v", err)
	}
	return path
}

func writeFakeInternetScaleBundleReportCheck(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "bundle-report-check")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "report-check targets=${BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS:-} remote=${BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING:-} suffix=${BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX:-} manifest=${BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST:-}" >> "$CALL_LOG"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake internet-scale report check: %v", err)
	}
	return path
}

func writeFakeInternetScaleReport(t *testing.T, path string) {
	t.Helper()
	fsPath := commandLogGateReportFSPath(path)
	if err := os.MkdirAll(filepath.Dir(fsPath), 0o755); err != nil {
		t.Fatalf("mkdir fake internet-scale report dir: %v", err)
	}
	if err := os.WriteFile(fsPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fake internet-scale report: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(fsPath)
	})
}

func writeFakeSingleRegionPreflightCurl(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "curl")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "curl $*" >> "$CALL_LOG"
url="${@: -1}"
case "$url" in
  */readyz)
    echo "ok"
    ;;
  */metrics)
    echo "budgie_write_region_proxy_failures_total 0"
    echo "budgie_derived_view_lag_events 0"
    ;;
  */api/v1/alerts)
    echo '{"status":"success","data":{"alerts":[]}}'
    ;;
  *)
    echo "unexpected curl url: $url" >&2
    exit 7
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	return path
}

func writeFakeSingleRegionPreflightJQ(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "jq")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "jq $*" >> "$CALL_LOG"
` + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake jq: %v", err)
	}
	return path
}

func writeFakeSingleRegionPreflightSmoke(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "cluster-smoke.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "cluster-smoke postgres=${BUDGIE_POSTGRES_DSN:-} nats=${BUDGIE_NATS_URL:-}" >> "$CALL_LOG"
echo "==> CLUSTER SMOKE TEST PASSED"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake cluster smoke: %v", err)
	}
	return path
}

func fakeCommandLogCleanupNATSBody() string {
	return `if [[ "${3:-}" == "stream" && "${4:-}" == "rm" ]]; then
  echo "deleted ${6:-}"
  exit 0
fi
case "$subject" in
  "budgie.commandlog.>")
    echo "BUDGIE_COMMAND_LOG_LOAD_OLD"
    echo "BUDGIE_COMMAND_LOG"
    ;;
  "budgie.commandcommit.>")
    echo "BUDGIE_COMMAND_LOG_LOAD_OLD"
    ;;
  "budgie.eventlog.>")
    echo "BUDGIE_EVENT_LOG_LOAD_OLD"
    echo "BUDGIE_EVENT_LOG"
    ;;
esac
`
}

func writeFakeCommandLogGateNATS(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "nats")
	script := `#!/usr/bin/env bash
set -euo pipefail
: "${NATS_CALL_LOG:=/dev/null}"
echo "$@" >> "$NATS_CALL_LOG"
subject=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == "--subject" ]]; then
    subject="$arg"
    break
  fi
  prev="$arg"
done
` + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake nats: %v", err)
	}
	return path
}

func commandLogGateReportPath(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	path := filepath.ToSlash(filepath.Join("artifacts", "internet-scale", name, "report.json"))
	t.Cleanup(func() {
		_ = os.RemoveAll(commandLogGateReportFSPath(path))
		_ = os.Remove(filepath.Dir(commandLogGateReportFSPath(path)))
	})
	return path
}

func commandLogGateReportFSPath(path string) string {
	return filepath.Join("..", filepath.FromSlash(path))
}

func writeFakeCommandLogGateGit(t *testing.T, dir, status string) string {
	t.Helper()
	path := filepath.Join(dir, "git")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "rev-parse" && "${2:-}" == "--is-inside-work-tree" ]]; then
  echo true
  exit 0
fi
if [[ "$1" == "status" && "${2:-}" == "--porcelain" ]]; then
  printf '%s' "$GIT_STATUS_OUTPUT"
  exit 0
fi
exit 9
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("GIT_STATUS_OUTPUT", status)
	return path
}
