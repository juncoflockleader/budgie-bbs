#!/usr/bin/env bash
#
# Run the durable native command/event staging gate for IS4 Redpanda/Kafka
# command-log promotion. The script writes an archived JSON report and
# immediately verifies it with the Kafka commandLogDrain budget.
#
# Required env:
#   BUDGIE_KAFKA_BROKERS
#   BUDGIE_POSTGRES_DSN
#
# Optional env:
#   BUDGIE_COMMAND_LOG_LOAD_TOPIC              default generated budgie.commands.load.*
#   BUDGIE_EVENT_LOG_LOAD_TOPIC                default generated budgie.events.load.*
#   BUDGIE_KAFKA_TLS                           enable TLS when 1/true/yes/on
#   BUDGIE_KAFKA_TLS_CA_FILE                   optional PEM CA bundle
#   BUDGIE_KAFKA_TLS_SERVER_NAME               optional TLS server name override
#   BUDGIE_KAFKA_SASL_MECHANISM                plain, scram-sha-256, or scram-sha-512
#   BUDGIE_KAFKA_SASL_USER
#   BUDGIE_KAFKA_SASL_PASSWORD
#   BUDGIE_COMMANDLOG_KAFKA_GATE_CONSUMER_GROUP default generated budgie-writers-load-*
#   BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS default 32
#   BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS   default 32
#   BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS     default 1
#   BUDGIE_COMMANDLOG_GATE_REPORT              default artifacts/internet-scale/commandlog-native-kafka-report.json
#   BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE
#   BUDGIE_COMMANDLOG_GATE_BOARDS              default 8
#   BUDGIE_COMMANDLOG_GATE_COMMANDS            default 100
#   BUDGIE_COMMANDLOG_GATE_REPLIES             default 2
#   BUDGIE_COMMANDLOG_GATE_WRITERS             default 8
#   BUDGIE_COMMANDLOG_GATE_BATCH_SIZE          default 25
#   BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY default 1
#   BUDGIE_COMMANDLOG_GATE_TIMEOUT             default 2m
#   BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: commandlog-native-kafka-gate.sh does not accept extra loadgen flags" >&2
  echo "       use BUDGIE_COMMANDLOG_* env vars or run the expanded command manually for experiments" >&2
  exit 2
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "ERROR: set ${name}" >&2
    exit 2
  fi
}

require_env BUDGIE_KAFKA_BROKERS
require_env BUDGIE_POSTGRES_DSN

read_positive_int() {
  local name="$1"
  local default_value="$2"
  local value="${!name:-$default_value}"
  if [[ ! "$value" =~ ^[0-9]+$ ]]; then
    echo "ERROR: ${name} must be a positive integer; got ${value}" >&2
    exit 2
  fi
  local numeric=$((10#$value))
  if (( numeric < 1 )); then
    echo "ERROR: ${name} must be a positive integer; got ${value}" >&2
    exit 2
  fi
  echo "$numeric"
}

require_min_int() {
  local name="$1"
  local value="$2"
  local min="$3"
  if (( value < min )); then
    echo "ERROR: ${name} must be at least ${min} for promotion evidence; got ${value}" >&2
    exit 2
  fi
}

require_prefix() {
  local name="$1"
  local value="$2"
  local prefix="$3"
  case "$value" in
    "$prefix"*) ;;
    *)
      echo "ERROR: ${name} must start with ${prefix} for promotion evidence; got ${value}" >&2
      exit 2
      ;;
  esac
}

require_promoted_budget_file() {
  local value="$1"
  local local_promoted="$2"
  local remote_promoted="$3"
  if [[ "$value" != "$local_promoted" && "$value" != "$remote_promoted" ]]; then
    echo "ERROR: BUDGIE_COMMANDLOG_GATE_BUDGET must be ${local_promoted} or ${remote_promoted} for promotion evidence; got ${value}" >&2
    echo "       run the expanded loadgen command manually for experimental budgets" >&2
    exit 2
  fi
  if [[ ! -f "$value" ]]; then
    echo "ERROR: promoted budget file not found: ${value}" >&2
    exit 2
  fi
}

endpoint_host_is_local() {
  local endpoint="$1"
  local lower
  local authority
  local host
  lower="$(printf '%s' "$endpoint" | tr '[:upper:]' '[:lower:]')"
  authority="${lower#*://}"
  authority="${authority%%/*}"
  authority="${authority%%\?*}"
  authority="${authority%%#*}"
  authority="${authority##*@}"
  if [[ "$authority" == \[*\]* ]]; then
    host="${authority#\[}"
    host="${host%%\]*}"
  else
    host="${authority%%:*}"
  fi
  case "$host" in
    localhost|localhost.*|127.*|0.0.0.0|::1)
      return 0
      ;;
  esac
  return 1
}

broker_list_has_localhost() {
  local broker
  local normalized
  IFS=',' read -r -a brokers <<< "$BUDGIE_KAFKA_BROKERS"
  for broker in "${brokers[@]}"; do
    broker="$(printf '%s' "$broker" | xargs)"
    if [[ -z "$broker" ]]; then
      continue
    fi
    normalized="$broker"
    if [[ "$normalized" != *"://"* ]]; then
      normalized="kafka://${normalized}"
    fi
    if endpoint_host_is_local "$normalized"; then
      return 0
    fi
  done
  return 1
}

require_nonlocal_runtime_inputs() {
  if broker_list_has_localhost; then
    echo "ERROR: remote staging budget requires non-local BUDGIE_KAFKA_BROKERS evidence; got ${BUDGIE_KAFKA_BROKERS}" >&2
    exit 2
  fi
  if endpoint_host_is_local "$BUDGIE_POSTGRES_DSN"; then
    echo "ERROR: remote staging budget requires non-local BUDGIE_POSTGRES_DSN evidence; got ${BUDGIE_POSTGRES_DSN}" >&2
    exit 2
  fi
}

require_report_path() {
  local value="$1"
  local prefix="$2"
  case "$value" in
    ""|/*|../*|*/../*|*/..|*/)
      echo "ERROR: BUDGIE_COMMANDLOG_GATE_REPORT must be a relative file path under ${prefix}; got ${value}" >&2
      exit 2
      ;;
  esac
  case "$value" in
    "$prefix"*) ;;
    *)
      echo "ERROR: BUDGIE_COMMANDLOG_GATE_REPORT must be under ${prefix} for promotion evidence; got ${value}" >&2
      exit 2
      ;;
  esac
}

require_clean_git_tree() {
  if ! command -v git >/dev/null 2>&1; then
    echo "ERROR: git is required to verify clean promotion evidence" >&2
    exit 2
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "ERROR: commandlog-native-kafka-gate.sh must run inside a git worktree" >&2
    exit 2
  fi
  local git_status
  if ! git_status="$(git status --porcelain)"; then
    echo "ERROR: git status failed while checking promotion evidence cleanliness" >&2
    exit 2
  fi
  if [[ -n "$git_status" ]]; then
    echo "ERROR: commandlog-native-kafka-gate.sh requires a clean git tree for promotion evidence" >&2
    echo "$git_status" >&2
    exit 2
  fi
}

resolve_go_bin() {
  local candidate
  if candidate="$(command -v go 2>/dev/null)"; then
    echo "$candidate"
    return 0
  fi
  for candidate in /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

LOCAL_PROMOTED_BUDGET_FILE="ops/internet-scale-kafka-budgets.example.json"
REMOTE_PROMOTED_BUDGET_FILE="ops/internet-scale-kafka-remote-staging-budgets.example.json"
DEFAULT_BUDGET_FILE="$LOCAL_PROMOTED_BUDGET_FILE"
if [[ "${BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING:-}" == "1" ]]; then
  DEFAULT_BUDGET_FILE="$REMOTE_PROMOTED_BUDGET_FILE"
fi
BUDGET_FILE="${BUDGIE_COMMANDLOG_GATE_BUDGET:-$DEFAULT_BUDGET_FILE}"
REPORT_FILE="${BUDGIE_COMMANDLOG_GATE_REPORT:-artifacts/internet-scale/commandlog-native-kafka-report.json}"
REPORT_PREFIX="artifacts/internet-scale/"
RUN_ID="$(date -u +%Y%m%d%H%M%S)_$$"
COMMAND_TOPIC_PREFIX="budgie.commands.load."
EVENT_TOPIC_PREFIX="budgie.events.load."
COMMAND_TOPIC="${BUDGIE_COMMAND_LOG_LOAD_TOPIC:-${COMMAND_TOPIC_PREFIX}${RUN_ID}}"
EVENT_TOPIC="${BUDGIE_EVENT_LOG_LOAD_TOPIC:-${EVENT_TOPIC_PREFIX}${RUN_ID}}"
CONSUMER_GROUP="${BUDGIE_COMMANDLOG_KAFKA_GATE_CONSUMER_GROUP:-budgie-writers-load-${RUN_ID}}"
BOARDS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_BOARDS 8)"
COMMANDS_PER_BOARD="$(read_positive_int BUDGIE_COMMANDLOG_GATE_COMMANDS 100)"
REPLIES_PER_THREAD="$(read_positive_int BUDGIE_COMMANDLOG_GATE_REPLIES 2)"
WRITERS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_WRITERS 8)"
BATCH_SIZE="$(read_positive_int BUDGIE_COMMANDLOG_GATE_BATCH_SIZE 25)"
PARTITION_CONCURRENCY="$(read_positive_int BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY 1)"
COMMAND_PARTITIONS="$(read_positive_int BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS 32)"
EVENT_PARTITIONS="$(read_positive_int BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS 32)"
TOPIC_REPLICAS="$(read_positive_int BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS 1)"
TOTAL_COMMANDS=$((BOARDS * COMMANDS_PER_BOARD * (1 + REPLIES_PER_THREAD)))

require_min_int BUDGIE_COMMANDLOG_GATE_BOARDS "$BOARDS" 8
require_min_int BUDGIE_COMMANDLOG_GATE_COMMANDS "$COMMANDS_PER_BOARD" 100
require_min_int BUDGIE_COMMANDLOG_GATE_REPLIES "$REPLIES_PER_THREAD" 2
require_min_int BUDGIE_COMMANDLOG_GATE_WRITERS "$WRITERS" 8
require_min_int BUDGIE_COMMANDLOG_GATE_BATCH_SIZE "$BATCH_SIZE" 25
require_min_int BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS "$COMMAND_PARTITIONS" 32
require_min_int BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS "$EVENT_PARTITIONS" 32
require_min_int totalCommands "$TOTAL_COMMANDS" 2400
require_prefix BUDGIE_COMMAND_LOG_LOAD_TOPIC "$COMMAND_TOPIC" "$COMMAND_TOPIC_PREFIX"
require_prefix BUDGIE_EVENT_LOG_LOAD_TOPIC "$EVENT_TOPIC" "$EVENT_TOPIC_PREFIX"

if [[ "$COMMAND_TOPIC" == "$EVENT_TOPIC" ]]; then
  echo "ERROR: BUDGIE_COMMAND_LOG_LOAD_TOPIC and BUDGIE_EVENT_LOG_LOAD_TOPIC must be distinct" >&2
  exit 2
fi

require_report_path "$REPORT_FILE" "$REPORT_PREFIX"

if [[ -e "$REPORT_FILE" && "${BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE:-}" != "1" ]]; then
  echo "ERROR: report file already exists: $REPORT_FILE" >&2
  echo "       set BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE=1 to replace it" >&2
  exit 2
fi

require_promoted_budget_file "$BUDGET_FILE" "$LOCAL_PROMOTED_BUDGET_FILE" "$REMOTE_PROMOTED_BUDGET_FILE"
if [[ "$BUDGET_FILE" == "$REMOTE_PROMOTED_BUDGET_FILE" ]]; then
  require_nonlocal_runtime_inputs
fi
require_clean_git_tree
if ! GO_CLI="$(resolve_go_bin)"; then
  echo "ERROR: go is required to run the durable staging gate" >&2
  echo "       install Go on PATH or in /opt/homebrew/bin, /usr/local/go/bin, or /usr/local/bin" >&2
  exit 2
fi

REPORT_DIR="$(dirname "$REPORT_FILE")"
REPORT_BASE="$(basename "$REPORT_FILE")"
mkdir -p "$REPORT_DIR"
REPORT_TMP="$(mktemp "${REPORT_DIR}/.${REPORT_BASE}.tmp.XXXXXX")"

cleanup_report_tmp() {
  if [[ -n "${REPORT_TMP:-}" && -e "$REPORT_TMP" ]]; then
    rm -f "$REPORT_TMP"
  fi
}
trap cleanup_report_tmp EXIT

loadgen_args=(
  -command-log-worker-executor native
  -command-log-backend kafka
  -kafka-scalar-allocator sql-event-partition-offsets
  -kafka-brokers "$BUDGIE_KAFKA_BROKERS"
  -kafka-command-topic "$COMMAND_TOPIC"
  -kafka-event-topic "$EVENT_TOPIC"
  -kafka-consumer-group "$CONSUMER_GROUP"
  -kafka-command-partitions "$COMMAND_PARTITIONS"
  -kafka-event-partitions "$EVENT_PARTITIONS"
  -kafka-topic-replicas "$TOPIC_REPLICAS"
  -require-postgres
  -authoritative-submit
  -assignment-mode snapshot-assignment
  -replies-per-thread "$REPLIES_PER_THREAD"
  -directed-replies
  -boards "$BOARDS"
  -commands-per-board "$COMMANDS_PER_BOARD"
  -writers "$WRITERS"
  -batch-size "$BATCH_SIZE"
  -partition-concurrency "$PARTITION_CONCURRENCY"
  -timeout "${BUDGIE_COMMANDLOG_GATE_TIMEOUT:-2m}"
  -budget-file "$BUDGET_FILE"
)

echo "==> running durable native Kafka command/event staging gate"
echo "    command topic:  $COMMAND_TOPIC"
echo "    event topic:    $EVENT_TOPIC"
echo "    partitions:     command=$COMMAND_PARTITIONS event=$EVENT_PARTITIONS"
echo "    topic replicas: $TOPIC_REPLICAS"
echo "    consumer group: $CONSUMER_GROUP"
echo "    budget:         $BUDGET_FILE"
echo "    report:         $REPORT_FILE"
echo "    load:           boards=$BOARDS commandsPerBoard=$COMMANDS_PER_BOARD repliesPerThread=$REPLIES_PER_THREAD writers=$WRITERS batchSize=$BATCH_SIZE partitionConcurrency=$PARTITION_CONCURRENCY totalCommands=$TOTAL_COMMANDS"
"$GO_CLI" run ./cmd/budgie-commandlog-loadgen "${loadgen_args[@]}" >"$REPORT_TMP"

echo "==> verifying archived report against Kafka commandLogDrain budget"
"$GO_CLI" run ./cmd/budgie-commandlog-report-check \
  -report-file "$REPORT_TMP" \
  -budget-file "$BUDGET_FILE"

mv "$REPORT_TMP" "$REPORT_FILE"
REPORT_TMP=""
echo "==> archived verified report at $REPORT_FILE"
echo "==> preserved Kafka load topics for inspection; use fresh topics or delete prior ${COMMAND_TOPIC_PREFIX}* and ${EVENT_TOPIC_PREFIX}* topics before rerunning"
echo "==> durable native Kafka command/event staging gate passed"
