#!/usr/bin/env bash
#
# Dry-run or delete disposable Kafka/Redpanda load topics left by
# commandlog-native-kafka-gate.sh. The cleanup command only deletes topics with
# the promoted load prefixes.
#
# Required env:
#   BUDGIE_KAFKA_BROKERS
#
# Optional env:
#   BUDGIE_KAFKA_TLS
#   BUDGIE_KAFKA_TLS_CA_FILE
#   BUDGIE_KAFKA_TLS_SERVER_NAME
#   BUDGIE_KAFKA_SASL_MECHANISM
#   BUDGIE_KAFKA_SASL_USER
#   BUDGIE_KAFKA_SASL_PASSWORD
#   BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT  default 30s
#
# Usage:
#   ./scripts/commandlog-native-kafka-cleanup.sh
#   ./scripts/commandlog-native-kafka-cleanup.sh --execute
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

EXECUTE=0
while (( $# > 0 )); do
  case "$1" in
    --dry-run)
      EXECUTE=0
      ;;
    --execute)
      EXECUTE=1
      ;;
    *)
      echo "ERROR: unsupported argument: $1" >&2
      echo "       usage: ./scripts/commandlog-native-kafka-cleanup.sh [--dry-run|--execute]" >&2
      exit 2
      ;;
  esac
  shift
done

if [[ -z "${BUDGIE_KAFKA_BROKERS:-}" ]]; then
  echo "ERROR: set BUDGIE_KAFKA_BROKERS" >&2
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
  echo "ERROR: go is required for Kafka load-topic cleanup" >&2
  echo "       install Go on PATH or in /opt/homebrew/bin, /usr/local/go/bin, or /usr/local/bin" >&2
  exit 2
fi

cleanup_args=(
  -kafka-brokers "$BUDGIE_KAFKA_BROKERS"
  -command-topic-prefix "budgie.commands.load."
  -event-topic-prefix "budgie.events.load."
  -timeout "${BUDGIE_COMMANDLOG_KAFKA_CLEANUP_TIMEOUT:-30s}"
)

if (( EXECUTE == 1 )); then
  cleanup_args+=(-execute)
fi

"$GO_CLI" run ./cmd/budgie-kafka-load-topic-cleanup "${cleanup_args[@]}"
