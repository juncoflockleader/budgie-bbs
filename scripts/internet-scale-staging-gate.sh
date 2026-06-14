#!/usr/bin/env bash
#
# Run the internet-scale staging evidence bundle. The bundle gives operators one
# entrypoint for gateway fanout evidence plus the selected durable command-log
# staging gates, while preserving the individual gate scripts as the source of
# truth for each promoted budget.
#
# Throughput signoff should run from the staging host itself or from a
# low-jitter client path colocated with NATS/Kafka, Redis, and Postgres. Remote
# staging mode rejects loopback endpoint evidence, but it cannot distinguish a
# healthy service from a high-latency client path.
#
# Optional env:
#   BUDGIE_INTERNET_SCALE_GATE_TARGETS       default gateway + detected nats/kafka
#   BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING
#   BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX default <short-git-sha>-<utc-ts>
#   BUDGIE_GATEWAY_FANOUT_GATE_SCRIPT        default ./scripts/gateway-fanout-gate.sh
#   BUDGIE_COMMANDLOG_NATS_GATE_SCRIPT       default ./scripts/commandlog-native-nats-gate.sh
#   BUDGIE_COMMANDLOG_KAFKA_GATE_SCRIPT      default ./scripts/commandlog-native-kafka-gate.sh
#   BUDGIE_INTERNET_SCALE_GATE_PREFLIGHT_SCRIPT default ./scripts/internet-scale-remote-staging-preflight.sh
#   BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT   set 1 to skip remote create/delete preflight
#   BUDGIE_INTERNET_SCALE_GATE_REPORT_CHECK_SCRIPT default ./scripts/internet-scale-report-check.sh
#   BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK   set 1 to skip archived bundle report-check
#   BUDGIE_INTERNET_SCALE_GATE_MANIFEST             default artifacts/internet-scale/bundle-manifest-<suffix>.json
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT       set automatically to the shared-suffix preflight report
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: internet-scale-staging-gate.sh does not accept positional arguments" >&2
  echo "       use BUDGIE_INTERNET_SCALE_GATE_* env vars to select targets" >&2
  exit 2
fi

require_clean_git_tree() {
  if ! command -v git >/dev/null 2>&1; then
    echo "ERROR: git is required to verify clean internet-scale staging evidence" >&2
    exit 2
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "ERROR: internet-scale-staging-gate.sh must run inside a git worktree" >&2
    exit 2
  fi
  local git_status
  if ! git_status="$(git status --porcelain)"; then
    echo "ERROR: git status failed while checking internet-scale evidence cleanliness" >&2
    exit 2
  fi
  if [[ -n "$git_status" ]]; then
    echo "ERROR: internet-scale-staging-gate.sh requires a clean git tree for staging evidence" >&2
    echo "$git_status" >&2
    exit 2
  fi
}

git_short_sha() {
  local sha
  if sha="$(git rev-parse --short HEAD 2>/dev/null)"; then
    echo "$sha"
    return 0
  fi
  echo "unknown"
}

default_targets() {
  local out="gateway"
  if [[ -n "${BUDGIE_NATS_URL:-}" ]]; then
    out="${out},nats"
  fi
  if [[ -n "${BUDGIE_KAFKA_BROKERS:-}" ]]; then
    out="${out},kafka"
  fi
  echo "$out"
}

normalize_targets() {
  local raw="$1"
  raw="${raw//,/ }"
  local target
  local out=()
  for target in $raw; do
    case "$target" in
      all)
        out+=(gateway nats kafka)
        ;;
      gateway|nats|kafka)
        out+=("$target")
        ;;
      *)
        echo "ERROR: unsupported internet-scale staging target: $target" >&2
        echo "       supported targets: gateway,nats,kafka,all" >&2
        exit 2
        ;;
    esac
  done
  printf '%s\n' "${out[@]}" | awk 'NF && !seen[$0]++'
}

target_enabled() {
  local needle="$1"
  local target
  for target in "${TARGETS[@]}"; do
    if [[ "$target" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

require_env_for_target() {
  local target="$1"
  local name="$2"
  if [[ -z "${!name:-}" ]]; then
    echo "ERROR: $target target requires $name" >&2
    exit 2
  fi
}

run_gate() {
  local label="$1"
  shift
  echo "==> running $label"
  "$@"
  echo "==> $label passed"
}

durable_preflight_targets() {
  local out=()
  if target_enabled nats; then
    out+=(nats)
  fi
  if target_enabled kafka; then
    out+=(kafka)
  fi
  local IFS=,
  echo "${out[*]}"
}

bundle_report_check_targets() {
  local IFS=,
  echo "${TARGETS[*]}"
}

require_clean_git_tree

REPORT_SUFFIX="${BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX:-$(git_short_sha)-$(date -u +%Y%m%d%H%M%S)}"
TARGETS_RAW="${BUDGIE_INTERNET_SCALE_GATE_TARGETS:-$(default_targets)}"
TARGETS=()
while IFS= read -r target; do
  TARGETS+=("$target")
done < <(normalize_targets "$TARGETS_RAW")

if [[ "${#TARGETS[@]}" -eq 1 && "${TARGETS[0]}" == "gateway" && -z "${BUDGIE_INTERNET_SCALE_GATE_TARGETS:-}" ]]; then
  echo "ERROR: no durable staging target detected; set BUDGIE_NATS_URL or BUDGIE_KAFKA_BROKERS" >&2
  echo "       for fanout-only evidence, set BUDGIE_INTERNET_SCALE_GATE_TARGETS=gateway explicitly" >&2
  exit 2
fi

REMOTE_STAGING="${BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING:-0}"
GATEWAY_SCRIPT="${BUDGIE_GATEWAY_FANOUT_GATE_SCRIPT:-./scripts/gateway-fanout-gate.sh}"
NATS_SCRIPT="${BUDGIE_COMMANDLOG_NATS_GATE_SCRIPT:-./scripts/commandlog-native-nats-gate.sh}"
KAFKA_SCRIPT="${BUDGIE_COMMANDLOG_KAFKA_GATE_SCRIPT:-./scripts/commandlog-native-kafka-gate.sh}"
PREFLIGHT_SCRIPT="${BUDGIE_INTERNET_SCALE_GATE_PREFLIGHT_SCRIPT:-./scripts/internet-scale-remote-staging-preflight.sh}"
REPORT_CHECK_SCRIPT="${BUDGIE_INTERNET_SCALE_GATE_REPORT_CHECK_SCRIPT:-./scripts/internet-scale-report-check.sh}"

GATEWAY_REPORT="artifacts/internet-scale/gateway-fanout-report-${REPORT_SUFFIX}.json"
NATS_REPORT="artifacts/internet-scale/commandlog-native-nats-report-${REPORT_SUFFIX}.json"
KAFKA_REPORT="artifacts/internet-scale/commandlog-native-kafka-report-${REPORT_SUFFIX}.json"
PREFLIGHT_REPORT="artifacts/internet-scale/preflight-report-${REPORT_SUFFIX}.json"
BUNDLE_MANIFEST="${BUDGIE_INTERNET_SCALE_GATE_MANIFEST:-artifacts/internet-scale/bundle-manifest-${REPORT_SUFFIX}.json}"
PREFLIGHT_RAN=0

if target_enabled nats; then
  require_env_for_target nats BUDGIE_NATS_URL
  require_env_for_target nats BUDGIE_POSTGRES_DSN
fi
if target_enabled kafka; then
  require_env_for_target kafka BUDGIE_KAFKA_BROKERS
  require_env_for_target kafka BUDGIE_POSTGRES_DSN
fi

echo "==> internet-scale staging targets: ${TARGETS[*]}"
echo "    report suffix: $REPORT_SUFFIX"
if [[ "$REMOTE_STAGING" == "1" ]]; then
  echo "    remote staging budgets: enabled"
else
  echo "    remote staging budgets: disabled"
fi

if [[ "$REMOTE_STAGING" == "1" && "${BUDGIE_INTERNET_SCALE_GATE_SKIP_PREFLIGHT:-0}" != "1" ]]; then
  PREFLIGHT_TARGETS="$(durable_preflight_targets)"
  if [[ -n "$PREFLIGHT_TARGETS" ]]; then
    run_gate "remote staging create/delete preflight" env \
      BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS="$PREFLIGHT_TARGETS" \
      BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING=1 \
      BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT="$PREFLIGHT_REPORT" \
      "$PREFLIGHT_SCRIPT"
    PREFLIGHT_RAN=1
  fi
elif [[ "$REMOTE_STAGING" == "1" ]]; then
  echo "==> skipping remote staging create/delete preflight"
fi

if target_enabled gateway; then
  run_gate "gateway fanout gate" env \
    BUDGIE_GATEWAY_FANOUT_GATE_REPORT="$GATEWAY_REPORT" \
    BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING="${BUDGIE_GATEWAY_FANOUT_GATE_REMOTE_STAGING:-$REMOTE_STAGING}" \
    "$GATEWAY_SCRIPT"
fi

if target_enabled nats; then
  run_gate "native NATS command-log gate" env \
    BUDGIE_COMMANDLOG_GATE_REPORT="$NATS_REPORT" \
    BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING="${BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING:-$REMOTE_STAGING}" \
    "$NATS_SCRIPT"
fi

if target_enabled kafka; then
  run_gate "native Kafka command-log gate" env \
    BUDGIE_COMMANDLOG_GATE_REPORT="$KAFKA_REPORT" \
    BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING="${BUDGIE_COMMANDLOG_GATE_REMOTE_STAGING:-$REMOTE_STAGING}" \
    "$KAFKA_SCRIPT"
fi

if [[ "${BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK:-0}" != "1" ]]; then
  run_gate "archived internet-scale report bundle check" env \
    BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS="$(bundle_report_check_targets)" \
    BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING="$REMOTE_STAGING" \
    BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX="$REPORT_SUFFIX" \
    BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST="$BUNDLE_MANIFEST" \
    "$REPORT_CHECK_SCRIPT"
else
  echo "==> skipping archived internet-scale report bundle check"
fi

echo "==> internet-scale staging evidence bundle passed"
if [[ "$PREFLIGHT_RAN" == "1" ]]; then
  echo "    preflight report: $PREFLIGHT_REPORT"
fi
echo "    gateway report: $GATEWAY_REPORT"
if target_enabled nats; then
  echo "    nats report:    $NATS_REPORT"
fi
if target_enabled kafka; then
  echo "    kafka report:   $KAFKA_REPORT"
fi
if [[ "${BUDGIE_INTERNET_SCALE_GATE_SKIP_REPORT_CHECK:-0}" != "1" ]]; then
  echo "    bundle manifest: $BUNDLE_MANIFEST"
fi
