#!/usr/bin/env bash
#
# Cheap create/delete preflight for internet-scale remote/shared staging.
# This validates that the provided staging credentials can create and clean up
# the exact resource families the full gates need, without running the full
# gateway or command-log load.
#
# Optional env:
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS        default detected from env
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING default BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING or 0
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_TIMEOUT        default 45s
#   BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT         optional JSON report path
#   BUDGIE_NATS_URL
#   BUDGIE_KAFKA_BROKERS
#   BUDGIE_POSTGRES_DSN
#   BUDGIE_COMMANDLOG_GATE_COMMAND_REPLICAS
#   BUDGIE_COMMANDLOG_KAFKA_GATE_COMMAND_PARTITIONS
#   BUDGIE_COMMANDLOG_KAFKA_GATE_EVENT_PARTITIONS
#   BUDGIE_COMMANDLOG_KAFKA_GATE_TOPIC_REPLICAS
#   BUDGIE_KAFKA_TLS
#   BUDGIE_KAFKA_TLS_CA_FILE
#   BUDGIE_KAFKA_TLS_SERVER_NAME
#   BUDGIE_KAFKA_SASL_MECHANISM
#   BUDGIE_KAFKA_SASL_USER
#   BUDGIE_KAFKA_SASL_PASSWORD
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: internet-scale-remote-staging-preflight.sh does not accept positional arguments" >&2
  echo "       use BUDGIE_INTERNET_SCALE_PREFLIGHT_* env vars to select targets" >&2
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

if ! GO_CLI="$(resolve_go_bin)"; then
  echo "ERROR: go is required for internet-scale remote staging preflight" >&2
  echo "       install Go on PATH or in /opt/homebrew/bin, /usr/local/go/bin, or /usr/local/bin" >&2
  exit 2
fi

REMOTE_STAGING="${BUDGIE_INTERNET_SCALE_PREFLIGHT_REMOTE_STAGING:-${BUDGIE_INTERNET_SCALE_GATE_REMOTE_STAGING:-0}}"
preflight_args=()
if [[ "$REMOTE_STAGING" == "1" ]]; then
  preflight_args+=(-remote-staging)
fi
if [[ -n "${BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS:-}" ]]; then
  preflight_args+=(-targets "$BUDGIE_INTERNET_SCALE_PREFLIGHT_TARGETS")
fi
if [[ -n "${BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT:-}" ]]; then
  preflight_args+=(-report-file "$BUDGIE_INTERNET_SCALE_PREFLIGHT_REPORT")
fi

"$GO_CLI" run ./cmd/budgie-internet-scale-preflight "${preflight_args[@]}"
