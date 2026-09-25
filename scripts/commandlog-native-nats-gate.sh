#!/usr/bin/env bash
#
# Run the durable native command/event staging gate for IS4 command-log
# promotion. The script writes an archived JSON report and immediately verifies
# it with the shared commandLogDrain budget.
#
# Required env:
#   BUDGIE_NATS_URL
#   BUDGIE_POSTGRES_DSN
#
# Optional env:
#   BUDGIE_COMMAND_LOG_LOAD_STREAM       default generated BUDGIE_COMMAND_LOG_LOAD_*
#   BUDGIE_EVENT_LOG_LOAD_STREAM         default generated BUDGIE_EVENT_LOG_LOAD_*
#   BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS default 1
#   BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS   default 1
#   BUDGIE_COMMANDLOG_GATE_REPORT        default artifacts/internet-scale/commandlog-native-nats-report.json
#   BUDGIE_COMMANDLOG_GATE_ALLOW_OVERWRITE
#   BUDGIE_COMMANDLOG_GATE_BOARDS        default 8
#   BUDGIE_COMMANDLOG_GATE_COMMANDS      default 100
#   BUDGIE_COMMANDLOG_GATE_REPLIES       default 2
#   BUDGIE_COMMANDLOG_GATE_WRITERS       default 8
#   BUDGIE_COMMANDLOG_GATE_BATCH_SIZE    default 25
#   BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY default 1
#   BUDGIE_COMMANDLOG_GATE_TIMEOUT       default 2m
#   BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX optional command-log partition index: redis
#   BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX_PREFIX default generated budgie:commandlog-load:*
#   BUDGIE_REDIS_URL                     required when command index is redis
#   BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT
#   BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING
#   NATS_BIN                             optional path to nats CLI
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: commandlog-native-nats-gate.sh does not accept extra loadgen flags" >&2
  echo "       use BUDGIE_COMMANDLOG_GATE_* env vars or run the expanded command manually for experiments" >&2
  exit 2
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "ERROR: set ${name}" >&2
    exit 2
  fi
}

require_env BUDGIE_NATS_URL
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

require_nonlocal_runtime_inputs() {
  if endpoint_host_is_local "$BUDGIE_NATS_URL"; then
    echo "ERROR: remote staging budget requires non-local BUDGIE_NATS_URL evidence; got ${BUDGIE_NATS_URL}" >&2
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
    echo "ERROR: commandlog-native-nats-gate.sh must run inside a git worktree" >&2
    exit 2
  fi
  local git_status
  if ! git_status="$(git status --porcelain)"; then
    echo "ERROR: git status failed while checking promotion evidence cleanliness" >&2
    exit 2
  fi
  if [[ -n "$git_status" ]]; then
    echo "ERROR: commandlog-native-nats-gate.sh requires a clean git tree for promotion evidence" >&2
    echo "$git_status" >&2
    exit 2
  fi
}

resolve_nats_bin() {
  local candidate
  if [[ -n "${NATS_BIN:-}" ]]; then
    if [[ ! -x "$NATS_BIN" ]]; then
      echo "ERROR: NATS_BIN must point to an executable nats CLI; got ${NATS_BIN}" >&2
      exit 2
    fi
    echo "$NATS_BIN"
    return 0
  fi
  if candidate="$(command -v nats 2>/dev/null)"; then
    echo "$candidate"
    return 0
  fi
  for candidate in /opt/homebrew/bin/nats /usr/local/bin/nats; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
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

list_nats_stream_names_for_subject() {
  local subject="$1"
  local output
  if ! output="$("$NATS_CLI" --server "$BUDGIE_NATS_URL" stream ls --names --subject "$subject" 2>&1)"; then
    echo "WARN: optional NATS stream overlap preflight could not list streams for ${subject}; continuing to loadgen validation" >&2
    if [[ -n "$output" ]]; then
      echo "      ${output}" >&2
    fi
    return 0
  fi
  printf '%s\n' "$output"
}

check_nats_subject_available() {
  local label="$1"
  local subject="$2"
  local load_prefix="$3"
  local stream
  while IFS= read -r stream; do
    if [[ -z "$stream" ]]; then
      continue
    fi
    if [[ "$stream" == "$load_prefix"* ]]; then
      echo "ERROR: existing NATS ${label} load stream ${stream} already owns ${subject}" >&2
    else
      echo "ERROR: existing NATS ${label} stream ${stream} already owns ${subject}" >&2
    fi
    NATS_PREFLIGHT_CONFLICT=1
  done < <(list_nats_stream_names_for_subject "$subject")
}

require_nats_subjects_available() {
  if [[ "${BUDGIE_COMMANDLOG_GATE_SKIP_NATS_PREFLIGHT:-}" == "1" ]]; then
    echo "==> skipping optional NATS stream overlap preflight"
    return
  fi
  if ! NATS_CLI="$(resolve_nats_bin)"; then
    echo "==> nats CLI not found; skipping optional NATS stream overlap preflight"
    return
  fi

  echo "==> preflighting NATS stream subject availability"
  NATS_PREFLIGHT_CONFLICT=0
  check_nats_subject_available "command-log" "budgie.commandlog.>" "$COMMAND_STREAM_PREFIX"
  check_nats_subject_available "command-commit" "budgie.commandcommit.>" "$COMMAND_STREAM_PREFIX"
  check_nats_subject_available "event-log" "budgie.eventlog.>" "$EVENT_STREAM_PREFIX"
  if (( NATS_PREFLIGHT_CONFLICT == 0 )); then
    return
  fi

  echo "ERROR: existing NATS streams already cover the gate subjects in this account/domain" >&2
  echo "       archive any needed report evidence, then remove only old load streams before rerunning:" >&2
  echo "       nats --server \"\$BUDGIE_NATS_URL\" stream rm --force BUDGIE_COMMAND_LOG_LOAD_..." >&2
  echo "       nats --server \"\$BUDGIE_NATS_URL\" stream rm --force BUDGIE_EVENT_LOG_LOAD_..." >&2
  echo "       do not delete non-load streams; use a fresh isolated NATS account/domain if non-load streams are present" >&2
  exit 2
}

LOCAL_PROMOTED_BUDGET_FILE="ops/internet-scale-budgets.example.json"
REMOTE_PROMOTED_BUDGET_FILE="ops/internet-scale-remote-staging-budgets.example.json"
DEFAULT_BUDGET_FILE="$LOCAL_PROMOTED_BUDGET_FILE"
if [[ "${BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING:-}" == "1" ]]; then
  DEFAULT_BUDGET_FILE="$REMOTE_PROMOTED_BUDGET_FILE"
fi
BUDGET_FILE="${BUDGIE_COMMANDLOG_GATE_BUDGET:-$DEFAULT_BUDGET_FILE}"
REPORT_FILE="${BUDGIE_COMMANDLOG_GATE_REPORT:-artifacts/internet-scale/commandlog-native-nats-report.json}"
REPORT_PREFIX="artifacts/internet-scale/"
RUN_ID="$(date -u +%Y%m%d%H%M%S)_$$"
COMMAND_STREAM="${BUDGIE_COMMAND_LOG_LOAD_STREAM:-BUDGIE_COMMAND_LOG_LOAD_${RUN_ID}}"
EVENT_STREAM="${BUDGIE_EVENT_LOG_LOAD_STREAM:-BUDGIE_EVENT_LOG_LOAD_${RUN_ID}}"
COMMAND_INDEX="${BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX:-${BUDGIE_COMMAND_LOG_INDEX:-}}"
COMMAND_INDEX_PREFIX="${BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX_PREFIX:-budgie:commandlog-load:${RUN_ID}}"
COMMAND_STREAM_PREFIX="BUDGIE_COMMAND_LOG_LOAD_"
EVENT_STREAM_PREFIX="BUDGIE_EVENT_LOG_LOAD_"
BOARDS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_BOARDS 8)"
COMMANDS_PER_BOARD="$(read_positive_int BUDGIE_COMMANDLOG_GATE_COMMANDS 100)"
REPLIES_PER_THREAD="$(read_positive_int BUDGIE_COMMANDLOG_GATE_REPLIES 2)"
WRITERS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_WRITERS 8)"
BATCH_SIZE="$(read_positive_int BUDGIE_COMMANDLOG_GATE_BATCH_SIZE 25)"
PARTITION_CONCURRENCY="$(read_positive_int BUDGIE_COMMANDLOG_GATE_PARTITION_CONCURRENCY 1)"
COMMAND_REPLICAS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS 1)"
EVENT_REPLICAS="$(read_positive_int BUDGIE_COMMANDLOG_GATE_EVENT_REPLICAS 1)"
TOTAL_COMMANDS=$((BOARDS * COMMANDS_PER_BOARD * (1 + REPLIES_PER_THREAD)))

require_min_int BUDGIE_COMMANDLOG_GATE_BOARDS "$BOARDS" 8
require_min_int BUDGIE_COMMANDLOG_GATE_COMMANDS "$COMMANDS_PER_BOARD" 100
require_min_int BUDGIE_COMMANDLOG_GATE_REPLIES "$REPLIES_PER_THREAD" 2
require_min_int BUDGIE_COMMANDLOG_GATE_WRITERS "$WRITERS" 8
require_min_int BUDGIE_COMMANDLOG_GATE_BATCH_SIZE "$BATCH_SIZE" 25
require_min_int totalCommands "$TOTAL_COMMANDS" 2400
require_prefix BUDGIE_COMMAND_LOG_LOAD_STREAM "$COMMAND_STREAM" "$COMMAND_STREAM_PREFIX"
require_prefix BUDGIE_EVENT_LOG_LOAD_STREAM "$EVENT_STREAM" "$EVENT_STREAM_PREFIX"

if [[ "$COMMAND_STREAM" == "$EVENT_STREAM" ]]; then
  echo "ERROR: BUDGIE_COMMAND_LOG_LOAD_STREAM and BUDGIE_EVENT_LOG_LOAD_STREAM must be distinct" >&2
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
require_nats_subjects_available

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
  -command-log-backend nats
  -nats "$BUDGIE_NATS_URL"
  -command-log-nats-stream "$COMMAND_STREAM"
  -command-log-nats-replicas "$COMMAND_REPLICAS"
  -event-log-nats-stream "$EVENT_STREAM"
  -event-log-nats-replicas "$EVENT_REPLICAS"
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

case "$(printf '%s' "$COMMAND_INDEX" | tr '[:upper:]' '[:lower:]')" in
  ""|none|off|disabled)
    COMMAND_INDEX=""
    ;;
  redis)
    require_env BUDGIE_REDIS_URL
    loadgen_args+=(
      -command-log-index redis
      -command-log-index-prefix "$COMMAND_INDEX_PREFIX"
      -redis "$BUDGIE_REDIS_URL"
    )
    ;;
  *)
    echo "ERROR: unsupported BUDGIE_COMMANDLOG_GATE_COMMAND_INDEX ${COMMAND_INDEX}; supported: redis" >&2
    exit 2
    ;;
esac

echo "==> running durable native command/event staging gate"
echo "    command stream: $COMMAND_STREAM"
echo "    event stream:   $EVENT_STREAM"
echo "    replicas:       command=$COMMAND_REPLICAS event=$EVENT_REPLICAS"
if [[ -n "$COMMAND_INDEX" ]]; then
  echo "    command index:  $COMMAND_INDEX prefix=$COMMAND_INDEX_PREFIX"
fi
echo "    budget:         $BUDGET_FILE"
echo "    report:         $REPORT_FILE"
echo "    load:           boards=$BOARDS commandsPerBoard=$COMMANDS_PER_BOARD repliesPerThread=$REPLIES_PER_THREAD writers=$WRITERS batchSize=$BATCH_SIZE partitionConcurrency=$PARTITION_CONCURRENCY totalCommands=$TOTAL_COMMANDS"
"$GO_CLI" run ./cmd/budgie-commandlog-loadgen "${loadgen_args[@]}" >"$REPORT_TMP"

echo "==> verifying archived report against commandLogDrain budget"
"$GO_CLI" run ./cmd/budgie-report-check commandlog \
  -report-file "$REPORT_TMP" \
  -budget-file "$BUDGET_FILE"

mv "$REPORT_TMP" "$REPORT_FILE"
REPORT_TMP=""
echo "==> archived verified report at $REPORT_FILE"
echo "==> preserved load streams for inspection; use a fresh NATS account/domain or delete prior BUDGIE_*_LOAD_* streams before rerunning"
echo "==> durable native command/event staging gate passed"
