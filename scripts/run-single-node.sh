#!/usr/bin/env bash
#
# Launch BudgieBBS as one SQLite-backed process.
#
# Defaults are local and disposable enough for a first run. For a real single
# host, set BUDGIE_SINGLE_NODE_DATA_DIR to a persistent path such as
# /var/lib/budgie before starting the service.
#
# Optional env:
#   BUDGIE_SINGLE_NODE_DATA_DIR    default artifacts/single-node
#   BUDGIE_SINGLE_NODE_DB          default $BUDGIE_SINGLE_NODE_DATA_DIR/budgie.db
#   BUDGIE_SINGLE_NODE_HTTP        default :8080
#   BUDGIE_SINGLE_NODE_SSH         default 2222
#   BUDGIE_SINGLE_NODE_HOSTKEY     default $BUDGIE_SINGLE_NODE_DATA_DIR/budgie_host_key
#   BUDGIE_SINGLE_NODE_SECRET_FILE default $BUDGIE_SINGLE_NODE_DATA_DIR/jwt_secret
#   BUDGIE_SINGLE_NODE_WEB         default auto; set off/none/disabled to skip
#   BUDGIE_SINGLE_NODE_BINARY      default ./budgied, falling back to go run
#   BUDGIE_SINGLE_NODE_DRY_RUN     set 1 to print the resolved command
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if (( $# > 0 )); then
  echo "ERROR: run-single-node.sh does not accept positional arguments" >&2
  echo "       use BUDGIE_SINGLE_NODE_* env vars for launch settings" >&2
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

generate_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 48
    return 0
  fi
  dd if=/dev/urandom bs=48 count=1 2>/dev/null | base64
}

shell_quote_command() {
  local part
  for part in "$@"; do
    printf '%q ' "$part"
  done
  printf '\n'
}

DATA_DIR="${BUDGIE_SINGLE_NODE_DATA_DIR:-artifacts/single-node}"
DB_PATH="${BUDGIE_SINGLE_NODE_DB:-${DATA_DIR}/budgie.db}"
HTTP_ADDR="${BUDGIE_SINGLE_NODE_HTTP:-:8080}"
SSH_PORT="${BUDGIE_SINGLE_NODE_SSH:-2222}"
HOSTKEY_PATH="${BUDGIE_SINGLE_NODE_HOSTKEY:-${DATA_DIR}/budgie_host_key}"
SECRET_FILE="${BUDGIE_SINGLE_NODE_SECRET_FILE:-${DATA_DIR}/jwt_secret}"
WEB_ROOT="${BUDGIE_SINGLE_NODE_WEB:-auto}"
DRY_RUN="${BUDGIE_SINGLE_NODE_DRY_RUN:-0}"

mkdir -p "$DATA_DIR"

if [[ -z "${BUDGIE_JWT_SECRET:-}" ]]; then
  if [[ -f "$SECRET_FILE" ]]; then
    BUDGIE_JWT_SECRET="$(<"$SECRET_FILE")"
  else
    umask 077
    generate_secret >"$SECRET_FILE"
    BUDGIE_JWT_SECRET="$(<"$SECRET_FILE")"
  fi
  export BUDGIE_JWT_SECRET
fi

command_args=()
if [[ -n "${BUDGIE_SINGLE_NODE_BINARY:-}" ]]; then
  command_args=("$BUDGIE_SINGLE_NODE_BINARY")
elif [[ -x "$ROOT/budgied" ]]; then
  command_args=("$ROOT/budgied")
else
  if ! GO_CLI="$(resolve_go_bin)"; then
    echo "ERROR: no budgied binary found and go is not available" >&2
    echo "       run: go build -o budgied ./cmd/budgied" >&2
    exit 2
  fi
  command_args=("$GO_CLI" run ./cmd/budgied)
fi

web_args=()
case "$WEB_ROOT" in
  ""|off|none|disabled)
    ;;
  auto)
    if [[ -d "$ROOT/web/dist" ]]; then
      web_args=(-web "$ROOT/web/dist")
    fi
    ;;
  *)
    web_args=(-web "$WEB_ROOT")
    ;;
esac

# Keep this launcher single-node even if the caller's shell still has staging
# or multi-node environment variables exported.
unset BUDGIE_POSTGRES_DSN
unset BUDGIE_NATS_URL
unset BUDGIE_REDIS_URL
unset BUDGIE_KAFKA_BROKERS
unset BUDGIE_READ_CACHE
unset BUDGIE_READ_CACHE_PREFIX
unset BUDGIE_COMMAND_LOG_INDEX

budgie_args=(
  -storage sqlite
  -db "$DB_PATH"
  -http "$HTTP_ADDR"
  -ssh "$SSH_PORT"
  -hostkey "$HOSTKEY_PATH"
)
if (( ${#web_args[@]} > 0 )); then
  budgie_args+=("${web_args[@]}")
fi

echo "==> launching BudgieBBS single-node SQLite"
echo "    data dir: $DATA_DIR"
echo "    db:       $DB_PATH"
echo "    http:     $HTTP_ADDR"
echo "    ssh:      $SSH_PORT"
echo "    hostkey:  $HOSTKEY_PATH"
if (( ${#web_args[@]} > 0 )); then
  echo "    web:      ${web_args[1]}"
else
  echo "    web:      off/auto-missing"
fi
echo "    storage:  sqlite"
echo "    broker/cache env: disabled for this process"

if [[ "$DRY_RUN" == "1" ]]; then
  shell_quote_command "${command_args[@]}" "${budgie_args[@]}"
  exit 0
fi

exec "${command_args[@]}" "${budgie_args[@]}"
