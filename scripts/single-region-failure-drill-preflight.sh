#!/usr/bin/env bash
#
# Validate the safe preflight for the IS9 single-region failure drill.
# This script performs only readiness/metrics checks itself, then delegates the
# runbook's cluster smoke step to scripts/cluster-smoke.sh.
#
# Required env:
#   BUDGIE_API
#   BUDGIE_REGIONAL_API
#   BUDGIE_WRITE_REGION_URL
#   BUDGIE_ADMIN_TOKEN
#   BUDGIE_USER_TOKEN
#   BUDGIE_POSTGRES_DSN
#   BUDGIE_NATS_URL
#   BUDGIE_ALERT_RULES
#
# Optional env:
#   DRILL_ID
#   BUDGIE_SINGLE_REGION_PREFLIGHT_METRIC_DIR
#   BUDGIE_SINGLE_REGION_PREFLIGHT_CLUSTER_SMOKE
#   BUDGIE_SINGLE_REGION_PREFLIGHT_SKIP_CLUSTER_SMOKE
#   BUDGIE_PROMETHEUS_URL
#   CURL_BIN
#   JQ_BIN
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: single-region-failure-drill-preflight.sh does not accept positional arguments" >&2
  echo "       set BUDGIE_* env vars as documented in ops/runbooks/single-region-failure-drill.md" >&2
  exit 2
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "ERROR: set ${name}" >&2
    exit 2
  fi
}

require_url_env() {
  local name="$1"
  require_env "$name"
  case "${!name}" in
    http://*|https://*) ;;
    *)
      echo "ERROR: ${name} must be an http(s) URL; got ${!name}" >&2
      exit 2
      ;;
  esac
}

require_token_env() {
  local name="$1"
  require_env "$name"
  if [[ "${!name}" == "replace-me" ]]; then
    echo "ERROR: ${name} still has the runbook placeholder value" >&2
    exit 2
  fi
}

resolve_curl_bin() {
  local candidate
  if [[ -n "${CURL_BIN:-}" ]]; then
    if [[ ! -x "$CURL_BIN" ]]; then
      echo "ERROR: CURL_BIN must point to an executable curl; got ${CURL_BIN}" >&2
      exit 2
    fi
    echo "$CURL_BIN"
    return 0
  fi
  if candidate="$(command -v curl 2>/dev/null)"; then
    echo "$candidate"
    return 0
  fi
  return 1
}

resolve_jq_bin() {
  local candidate
  if [[ -n "${JQ_BIN:-}" ]]; then
    if [[ ! -x "$JQ_BIN" ]]; then
      echo "ERROR: JQ_BIN must point to an executable jq; got ${JQ_BIN}" >&2
      exit 2
    fi
    echo "$JQ_BIN"
    return 0
  fi
  if candidate="$(command -v jq 2>/dev/null)"; then
    echo "$candidate"
    return 0
  fi
  return 1
}

check_alert_rule() {
  local alert="$1"
  if ! grep -q "alert: ${alert}" "$BUDGIE_ALERT_RULES"; then
    echo "ERROR: alert rules missing ${alert}: ${BUDGIE_ALERT_RULES}" >&2
    exit 2
  fi
}

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do
    value="${value%/}"
  done
  echo "$value"
}

check_prometheus_critical_alerts() {
  if [[ -z "${BUDGIE_PROMETHEUS_URL:-}" ]]; then
    echo "==> Prometheus alert API not configured; verify critical alerts manually"
    return
  fi
  require_url_env BUDGIE_PROMETHEUS_URL
  local jq_cli
  if ! jq_cli="$(resolve_jq_bin)"; then
    echo "ERROR: jq is required when BUDGIE_PROMETHEUS_URL is set" >&2
    exit 2
  fi
  local prometheus_base alerts_file critical
  prometheus_base="$(trim_trailing_slash "$BUDGIE_PROMETHEUS_URL")"
  alerts_file="${METRIC_DIR}/prometheus-alerts.json"
  echo "==> checking Prometheus critical Budgie alerts"
  "$CURL_CLI" -fsS "${prometheus_base}/api/v1/alerts" > "$alerts_file"
  if ! critical="$("$jq_cli" -r '.data.alerts[]? | select(.state=="firing") | select(.labels.severity=="critical") | select((.labels.alertname // "") | startswith("Budgie")) | .labels.alertname' "$alerts_file")"; then
    echo "ERROR: failed to parse Prometheus alerts from ${alerts_file}" >&2
    exit 2
  fi
  if [[ -n "$critical" ]]; then
    echo "ERROR: critical Budgie alerts are already firing:" >&2
    printf '%s\n' "$critical" | sed 's/^/  - /' >&2
    exit 2
  fi
  echo "==> no critical Budgie alerts are firing"
}

require_url_env BUDGIE_API
require_url_env BUDGIE_REGIONAL_API
require_url_env BUDGIE_WRITE_REGION_URL
require_token_env BUDGIE_ADMIN_TOKEN
require_token_env BUDGIE_USER_TOKEN
require_env BUDGIE_POSTGRES_DSN
require_env BUDGIE_NATS_URL
require_env BUDGIE_ALERT_RULES

if [[ ! -f "$BUDGIE_ALERT_RULES" ]]; then
  echo "ERROR: BUDGIE_ALERT_RULES file not found: $BUDGIE_ALERT_RULES" >&2
  exit 2
fi

if ! CURL_CLI="$(resolve_curl_bin)"; then
  echo "ERROR: curl is required for the single-region drill preflight" >&2
  exit 2
fi

BUDGIE_API_BASE="$(trim_trailing_slash "$BUDGIE_API")"
BUDGIE_REGIONAL_API_BASE="$(trim_trailing_slash "$BUDGIE_REGIONAL_API")"
DRILL_ID="${DRILL_ID:-budgie-is9-preflight-$(date -u +%Y%m%dT%H%M%SZ)}"
METRIC_DIR="${BUDGIE_SINGLE_REGION_PREFLIGHT_METRIC_DIR:-/tmp/${DRILL_ID}-metrics}"
CLUSTER_SMOKE="${BUDGIE_SINGLE_REGION_PREFLIGHT_CLUSTER_SMOKE:-./scripts/cluster-smoke.sh}"

echo "==> single-region drill preflight: ${DRILL_ID}"
mkdir -p "$METRIC_DIR"

echo "==> checking IS9 alert rules"
check_alert_rule BudgieRemoteDeliveryLagHigh
check_alert_rule BudgieWriteRegionProxyFailures
check_alert_rule BudgieProjectionLagHigh
check_alert_rule BudgieCommandLogWriterLagHigh

check_prometheus_critical_alerts

echo "==> checking write endpoint readiness"
"$CURL_CLI" -fsS "${BUDGIE_API_BASE}/readyz" >/dev/null

echo "==> checking regional endpoint readiness"
"$CURL_CLI" -fsS "${BUDGIE_REGIONAL_API_BASE}/readyz" >/dev/null

echo "==> capturing baseline metrics"
"$CURL_CLI" -fsS "${BUDGIE_API_BASE}/metrics" > "${METRIC_DIR}/api.metrics"
"$CURL_CLI" -fsS "${BUDGIE_REGIONAL_API_BASE}/metrics" > "${METRIC_DIR}/regional.metrics"
echo "    metrics: ${METRIC_DIR}"

if [[ "${BUDGIE_SINGLE_REGION_PREFLIGHT_SKIP_CLUSTER_SMOKE:-}" == "1" ]]; then
  echo "==> skipping cluster smoke by request"
else
  if [[ ! -x "$CLUSTER_SMOKE" ]]; then
    echo "ERROR: cluster smoke script is not executable: $CLUSTER_SMOKE" >&2
    exit 2
  fi
  echo "==> running cluster smoke"
  "$CLUSTER_SMOKE"
fi

echo "==> single-region drill preflight passed"
