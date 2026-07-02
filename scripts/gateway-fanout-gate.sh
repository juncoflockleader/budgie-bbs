#!/usr/bin/env bash
#
# Run the synthetic IS7/IS9 gateway fanout capacity gate. The wrapper writes an
# archived JSON report under artifacts/internet-scale after cmd/budgie-gateway-
# loadgen accepts the promoted gatewayFanout budget.
#
# Optional env:
#   BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS   default 10000
#   BUDGIE_GATEWAY_FANOUT_GATE_IDLE_SUBSCRIBERS  default 90000
#   BUDGIE_GATEWAY_FANOUT_GATE_BUFFER_SIZE       default 2
#   BUDGIE_GATEWAY_FANOUT_GATE_EVENTS            default 1
#   BUDGIE_GATEWAY_FANOUT_GATE_TARGET_CONNECTIONS default 1000000
#   BUDGIE_GATEWAY_FANOUT_GATE_TIMEOUT           default 30s
#   BUDGIE_GATEWAY_FANOUT_GATE_REPORT            default artifacts/internet-scale/gateway-fanout-report.json
#   BUDGIE_GATEWAY_FANOUT_GATE_ALLOW_OVERWRITE
#   BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: gateway-fanout-gate.sh does not accept extra loadgen flags" >&2
  echo "       use BUDGIE_GATEWAY_FANOUT_GATE_* env vars or run cmd/budgie-gateway-loadgen manually for experiments" >&2
  exit 2
fi

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
    echo "ERROR: ${name} must be at least ${min} for gateway fanout evidence; got ${value}" >&2
    exit 2
  fi
}

require_report_path() {
  local value="$1"
  local prefix="$2"
  case "$value" in
    ""|/*|../*|*/../*|*/..|*/)
      echo "ERROR: BUDGIE_GATEWAY_FANOUT_GATE_REPORT must be a relative file path under ${prefix}; got ${value}" >&2
      exit 2
      ;;
  esac
  case "$value" in
    "$prefix"*) ;;
    *)
      echo "ERROR: BUDGIE_GATEWAY_FANOUT_GATE_REPORT must be under ${prefix} for gateway fanout evidence; got ${value}" >&2
      exit 2
      ;;
  esac
}

require_clean_git_tree() {
  if ! command -v git >/dev/null 2>&1; then
    echo "ERROR: git is required to verify clean gateway fanout evidence" >&2
    exit 2
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "ERROR: gateway-fanout-gate.sh must run inside a git worktree" >&2
    exit 2
  fi
  local git_status
  if ! git_status="$(git status --porcelain)"; then
    echo "ERROR: git status failed while checking gateway fanout evidence cleanliness" >&2
    exit 2
  fi
  if [[ -n "$git_status" ]]; then
    echo "ERROR: gateway-fanout-gate.sh requires a clean git tree for gateway fanout evidence" >&2
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

LOCAL_PROMOTED_BUDGET_FILE="ops/internet-scale-budgets.example.json"
REMOTE_PROMOTED_BUDGET_FILE="ops/internet-scale-remote-staging-budgets.example.json"
PROMOTED_BUDGET_FILE="$LOCAL_PROMOTED_BUDGET_FILE"
if [[ "${BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING:-}" == "1" ]]; then
  PROMOTED_BUDGET_FILE="$REMOTE_PROMOTED_BUDGET_FILE"
fi
REPORT_PREFIX="artifacts/internet-scale/"
REPORT_FILE="${BUDGIE_GATEWAY_FANOUT_GATE_REPORT:-artifacts/internet-scale/gateway-fanout-report.json}"
HOT_SUBSCRIBERS="$(read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS 10000)"
IDLE_SUBSCRIBERS="$(read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_IDLE_SUBSCRIBERS 90000)"
BUFFER_SIZE="$(read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_BUFFER_SIZE 2)"
EVENTS="$(read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_EVENTS 1)"
TARGET_CONNECTIONS="$(read_positive_int BUDGIE_GATEWAY_FANOUT_GATE_TARGET_CONNECTIONS 1000000)"
TOTAL_SUBSCRIBERS=$((HOT_SUBSCRIBERS + IDLE_SUBSCRIBERS))
QUEUED_DELIVERIES=$((HOT_SUBSCRIBERS * BUFFER_SIZE))

require_min_int BUDGIE_GATEWAY_FANOUT_GATE_HOT_SUBSCRIBERS "$HOT_SUBSCRIBERS" 10000
require_min_int totalSubscribers "$TOTAL_SUBSCRIBERS" 100000
require_min_int BUDGIE_GATEWAY_FANOUT_GATE_BUFFER_SIZE "$BUFFER_SIZE" 2
require_min_int BUDGIE_GATEWAY_FANOUT_GATE_EVENTS "$EVENTS" 1
require_min_int queuedDeliveries "$QUEUED_DELIVERIES" 10000
require_min_int BUDGIE_GATEWAY_FANOUT_GATE_TARGET_CONNECTIONS "$TARGET_CONNECTIONS" 1000000
require_report_path "$REPORT_FILE" "$REPORT_PREFIX"

if [[ -e "$REPORT_FILE" && "${BUDGIE_GATEWAY_FANOUT_GATE_ALLOW_OVERWRITE:-}" != "1" ]]; then
  echo "ERROR: report file already exists: $REPORT_FILE" >&2
  echo "       set BUDGIE_GATEWAY_FANOUT_GATE_ALLOW_OVERWRITE=1 to replace it" >&2
  exit 2
fi
if [[ ! -f "$PROMOTED_BUDGET_FILE" ]]; then
  echo "ERROR: promoted budget file not found: ${PROMOTED_BUDGET_FILE}" >&2
  exit 2
fi
require_clean_git_tree
if ! GO_CLI="$(resolve_go_bin)"; then
  echo "ERROR: go is required to run the gateway fanout gate" >&2
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

echo "==> running gateway fanout capacity gate"
echo "    hot subscribers:    $HOT_SUBSCRIBERS"
echo "    idle subscribers:   $IDLE_SUBSCRIBERS"
echo "    total subscribers:  $TOTAL_SUBSCRIBERS"
echo "    buffer size:        $BUFFER_SIZE"
echo "    events:             $EVENTS"
echo "    target connections: $TARGET_CONNECTIONS"
echo "    budget:             $PROMOTED_BUDGET_FILE"
echo "    report:             $REPORT_FILE"
"$GO_CLI" run ./cmd/budgie-gateway-loadgen \
  -hot-subscribers "$HOT_SUBSCRIBERS" \
  -idle-subscribers "$IDLE_SUBSCRIBERS" \
  -buffer-size "$BUFFER_SIZE" \
  -events "$EVENTS" \
  -target-connections "$TARGET_CONNECTIONS" \
  -timeout "${BUDGIE_GATEWAY_FANOUT_GATE_TIMEOUT:-30s}" \
  -budget-file "$PROMOTED_BUDGET_FILE" >"$REPORT_TMP"

echo "==> verifying archived gateway fanout report against gatewayFanout budget"
"$GO_CLI" run ./cmd/budgie-report-check gateway \
  -report-file "$REPORT_TMP" \
  -budget-file "$PROMOTED_BUDGET_FILE"

mv "$REPORT_TMP" "$REPORT_FILE"
REPORT_TMP=""
echo "==> archived verified gateway fanout report at $REPORT_FILE"
echo "==> gateway fanout capacity gate passed"
