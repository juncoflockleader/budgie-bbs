#!/usr/bin/env bash
#
# Dry-run or delete disposable NATS load streams left by
# commandlog-native-nats-gate.sh. The script only deletes streams with the
# promoted load prefixes and reports any non-load stream that owns the same
# broad subjects.
#
# Required env:
#   BUDGIE_NATS_URL
#
# Optional env:
#   NATS_BIN  path to nats CLI
#
# Usage:
#   ./scripts/commandlog-native-nats-cleanup.sh
#   ./scripts/commandlog-native-nats-cleanup.sh --execute
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
      echo "       usage: ./scripts/commandlog-native-nats-cleanup.sh [--dry-run|--execute]" >&2
      exit 2
      ;;
  esac
  shift
done

if [[ -z "${BUDGIE_NATS_URL:-}" ]]; then
  echo "ERROR: set BUDGIE_NATS_URL" >&2
  exit 2
fi

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

if ! NATS_CLI="$(resolve_nats_bin)"; then
  echo "ERROR: nats CLI is required for load-stream cleanup" >&2
  echo "       set NATS_BIN=/path/to/nats if it is not on PATH" >&2
  exit 2
fi

COMMAND_STREAM_PREFIX="BUDGIE_COMMAND_LOG_LOAD_"
EVENT_STREAM_PREFIX="BUDGIE_EVENT_LOG_LOAD_"
LOAD_STREAMS=""
NON_LOAD_STREAMS=""

has_line() {
  local haystack="$1"
  local needle="$2"
  case $'\n'"$haystack" in
    *$'\n'"$needle"$'\n'*) return 0 ;;
    *) return 1 ;;
  esac
}

append_load_stream() {
  local stream="$1"
  if ! has_line "$LOAD_STREAMS" "$stream"; then
    LOAD_STREAMS="${LOAD_STREAMS}${stream}"$'\n'
  fi
}

append_non_load_stream() {
  local stream="$1"
  if ! has_line "$NON_LOAD_STREAMS" "$stream"; then
    NON_LOAD_STREAMS="${NON_LOAD_STREAMS}${stream}"$'\n'
  fi
}

collect_subject_streams() {
  local subject="$1"
  local output
  local stream
  if ! output="$("$NATS_CLI" --server "$BUDGIE_NATS_URL" stream ls --names --subject "$subject" 2>&1)"; then
    echo "ERROR: list NATS streams for ${subject}: ${output}" >&2
    exit 2
  fi
  while IFS= read -r stream; do
    if [[ -z "$stream" ]]; then
      continue
    fi
    case "$stream" in
      "$COMMAND_STREAM_PREFIX"*|"$EVENT_STREAM_PREFIX"*)
        append_load_stream "$stream"
        ;;
      *)
        append_non_load_stream "$stream"
        ;;
    esac
  done <<< "$output"
}

collect_subject_streams "budgie.commandlog.>"
collect_subject_streams "budgie.commandcommit.>"
collect_subject_streams "budgie.eventlog.>"

if [[ -n "$NON_LOAD_STREAMS" ]]; then
  echo "==> non-load streams also own gate subjects and will not be deleted:"
  printf '%s' "$NON_LOAD_STREAMS" | sed 's/^/    /'
  echo "    use a fresh isolated NATS account/domain before rerunning the gate"
fi

if [[ -z "$LOAD_STREAMS" ]]; then
  echo "==> no disposable command/event load streams found"
  exit 0
fi

echo "==> disposable command/event load streams:"
printf '%s' "$LOAD_STREAMS" | sed 's/^/    /'

if (( EXECUTE == 0 )); then
  echo "==> dry run only; pass --execute to delete these load streams"
  exit 0
fi

while IFS= read -r stream; do
  if [[ -z "$stream" ]]; then
    continue
  fi
  echo "==> deleting load stream $stream"
  "$NATS_CLI" --server "$BUDGIE_NATS_URL" stream rm --force "$stream"
done <<< "$LOAD_STREAMS"

echo "==> load stream cleanup complete"
