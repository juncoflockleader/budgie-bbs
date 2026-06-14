#!/usr/bin/env bash
#
# Re-check archived internet-scale staging evidence without rerunning load.
# This is the handoff counterpart to internet-scale-staging-gate.sh.
#
# Optional env:
#   BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS       default gateway,nats,kafka
#   BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING
#   BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX        report suffix from the bundle run
#   BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX         accepted alias from internet-scale-staging-gate.sh
#   BUDGIE_GATEWAY_FANOUT_REPORT                     explicit gateway report path
#   BUDGIE_COMMANDLOG_NATS_REPORT                    explicit NATS report path
#   BUDGIE_COMMANDLOG_KAFKA_REPORT                   explicit Kafka report path
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT           explicit remote preflight report path
#   BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST      optional bundle manifest output path
#   BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST optional existing bundle manifest path to verify
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: internet-scale-report-check.sh does not accept positional arguments" >&2
  echo "       use BUDGIE_INTERNET_SCALE_REPORT_CHECK_* env vars to select reports" >&2
  exit 2
fi

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
        echo "ERROR: unsupported internet-scale report-check target: $target" >&2
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

report_from_suffix() {
  local kind="$1"
  local suffix="$2"
  if [[ -z "$suffix" ]]; then
    echo "ERROR: set BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX or explicit report path env vars" >&2
    exit 2
  fi
  case "$kind" in
    gateway)
      echo "artifacts/internet-scale/gateway-fanout-report-${suffix}.json"
      ;;
    nats)
      echo "artifacts/internet-scale/commandlog-native-nats-report-${suffix}.json"
      ;;
    kafka)
      echo "artifacts/internet-scale/commandlog-native-kafka-report-${suffix}.json"
      ;;
    preflight)
      echo "artifacts/internet-scale/preflight-report-${suffix}.json"
      ;;
    *)
      echo "ERROR: unsupported report kind: $kind" >&2
      exit 2
      ;;
  esac
}

require_report() {
  local label="$1"
  local path="$2"
  if [[ ! -f "$path" ]]; then
    echo "ERROR: ${label} report not found: $path" >&2
    exit 2
  fi
}

run_check() {
  local label="$1"
  shift
  echo "==> checking $label"
  "$@"
  echo "==> $label passed"
}

bundle_consistency_args() {
  local out=()
  out+=(-targets "$(bundle_consistency_targets)")
  if [[ "$REMOTE_STAGING" == "1" ]]; then
    out+=(-remote-staging)
  fi
  if [[ "$REMOTE_STAGING" == "1" && -n "$PREFLIGHT_TARGETS" ]]; then
    out+=(-preflight-report "$PREFLIGHT_REPORT")
  fi
  if target_enabled gateway; then
    out+=(-gateway-report "$GATEWAY_REPORT")
  fi
  if target_enabled nats; then
    out+=(-nats-report "$NATS_REPORT")
  fi
  if target_enabled kafka; then
    out+=(-kafka-report "$KAFKA_REPORT")
  fi
  if [[ -n "$BUNDLE_MANIFEST" ]]; then
    out+=(-manifest-file "$BUNDLE_MANIFEST")
  fi
  printf '%s\n' "${out[@]}"
}

bundle_manifest_verify_args() {
  local out=()
  out+=(-verify-manifest "$VERIFY_BUNDLE_MANIFEST")
  out+=(-targets "$(bundle_consistency_targets)")
  if [[ "$REMOTE_STAGING" == "1" ]]; then
    out+=(-remote-staging)
  fi
  printf '%s\n' "${out[@]}"
}

bundle_consistency_targets() {
  local IFS=,
  echo "${TARGETS[*]}"
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

if ! GO_CLI="$(resolve_go_bin)"; then
  echo "ERROR: go is required to re-check internet-scale reports" >&2
  echo "       install Go on PATH or in /opt/homebrew/bin, /usr/local/go/bin, or /usr/local/bin" >&2
  exit 2
fi

TARGETS_RAW="${BUDGIE_INTERNET_SCALE_REPORT_CHECK_TARGETS:-gateway,nats,kafka}"
TARGETS=()
while IFS= read -r target; do
  TARGETS+=("$target")
done < <(normalize_targets "$TARGETS_RAW")

REMOTE_STAGING="${BUDGIE_INTERNET_SCALE_REPORT_CHECK_REMOTE_STAGING:-0}"
REPORT_SUFFIX="${BUDGIE_INTERNET_SCALE_REPORT_CHECK_SUFFIX:-${BUDGIE_INTERNET_SCALE_GATE_REPORT_SUFFIX:-}}"
GATEWAY_REPORT="${BUDGIE_GATEWAY_FANOUT_REPORT:-}"
NATS_REPORT="${BUDGIE_COMMANDLOG_NATS_REPORT:-}"
KAFKA_REPORT="${BUDGIE_COMMANDLOG_KAFKA_REPORT:-}"
PREFLIGHT_REPORT="${BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT:-}"
BUNDLE_MANIFEST="${BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST:-}"
VERIFY_BUNDLE_MANIFEST="${BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST:-}"
if [[ -n "$BUNDLE_MANIFEST" && -n "$VERIFY_BUNDLE_MANIFEST" ]]; then
  echo "ERROR: set only one of BUDGIE_INTERNET_SCALE_REPORT_CHECK_MANIFEST or BUDGIE_INTERNET_SCALE_REPORT_CHECK_VERIFY_MANIFEST" >&2
  exit 2
fi
if target_enabled gateway && [[ -z "$GATEWAY_REPORT" ]]; then
  GATEWAY_REPORT="$(report_from_suffix gateway "$REPORT_SUFFIX")"
fi
if target_enabled nats && [[ -z "$NATS_REPORT" ]]; then
  NATS_REPORT="$(report_from_suffix nats "$REPORT_SUFFIX")"
fi
if target_enabled kafka && [[ -z "$KAFKA_REPORT" ]]; then
  KAFKA_REPORT="$(report_from_suffix kafka "$REPORT_SUFFIX")"
fi
PREFLIGHT_TARGETS="$(durable_preflight_targets)"
if [[ "$REMOTE_STAGING" == "1" && -n "$PREFLIGHT_TARGETS" && -z "$PREFLIGHT_REPORT" ]]; then
  PREFLIGHT_REPORT="$(report_from_suffix preflight "$REPORT_SUFFIX")"
fi

GATEWAY_BUDGET="ops/internet-scale-budgets.example.json"
NATS_BUDGET="ops/internet-scale-budgets.example.json"
KAFKA_BUDGET="ops/internet-scale-kafka-budgets.example.json"
if [[ "$REMOTE_STAGING" == "1" ]]; then
  GATEWAY_BUDGET="ops/internet-scale-remote-staging-budgets.example.json"
  NATS_BUDGET="ops/internet-scale-remote-staging-budgets.example.json"
  KAFKA_BUDGET="ops/internet-scale-kafka-remote-staging-budgets.example.json"
fi

echo "==> internet-scale report-check targets: ${TARGETS[*]}"
if [[ -n "$REPORT_SUFFIX" ]]; then
  echo "    report suffix: $REPORT_SUFFIX"
fi
if [[ "$REMOTE_STAGING" == "1" ]]; then
  echo "    remote staging budgets: enabled"
else
  echo "    remote staging budgets: disabled"
fi
if [[ -n "$VERIFY_BUNDLE_MANIFEST" ]]; then
  echo "    verify bundle manifest: $VERIFY_BUNDLE_MANIFEST"
fi

if [[ "$REMOTE_STAGING" == "1" && -n "$PREFLIGHT_TARGETS" ]]; then
  require_report preflight "$PREFLIGHT_REPORT"
  run_check "remote staging preflight report" "$GO_CLI" run ./cmd/budgie-internet-scale-preflight-report-check \
    -report-file "$PREFLIGHT_REPORT" \
    -targets "$PREFLIGHT_TARGETS" \
    -remote-staging
fi

if target_enabled gateway; then
  require_report gateway "$GATEWAY_REPORT"
  run_check "gateway fanout report" "$GO_CLI" run ./cmd/budgie-gateway-report-check \
    -report-file "$GATEWAY_REPORT" \
    -budget-file "$GATEWAY_BUDGET"
fi

if target_enabled nats; then
  require_report nats "$NATS_REPORT"
  run_check "native NATS command-log report" "$GO_CLI" run ./cmd/budgie-commandlog-report-check \
    -report-file "$NATS_REPORT" \
    -budget-file "$NATS_BUDGET"
fi

if target_enabled kafka; then
  require_report kafka "$KAFKA_REPORT"
  run_check "native Kafka command-log report" "$GO_CLI" run ./cmd/budgie-commandlog-report-check \
    -report-file "$KAFKA_REPORT" \
    -budget-file "$KAFKA_BUDGET"
fi

if [[ -n "$VERIFY_BUNDLE_MANIFEST" ]]; then
  require_report "bundle manifest" "$VERIFY_BUNDLE_MANIFEST"
  BUNDLE_VERIFY_ARGS=()
  while IFS= read -r arg; do
    BUNDLE_VERIFY_ARGS+=("$arg")
  done < <(bundle_manifest_verify_args)
  run_check "bundle manifest read-back" "$GO_CLI" run ./cmd/budgie-internet-scale-bundle-report-check "${BUNDLE_VERIFY_ARGS[@]}"
else
  BUNDLE_CONSISTENCY_ARGS=()
  while IFS= read -r arg; do
    BUNDLE_CONSISTENCY_ARGS+=("$arg")
  done < <(bundle_consistency_args)
  if [[ "${#BUNDLE_CONSISTENCY_ARGS[@]}" -gt 0 ]]; then
    run_check "bundle evidence consistency" "$GO_CLI" run ./cmd/budgie-internet-scale-bundle-report-check "${BUNDLE_CONSISTENCY_ARGS[@]}"
  fi
fi

echo "==> internet-scale report bundle satisfies selected budgets"
