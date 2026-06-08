#!/usr/bin/env bash
#
# cluster-smoke.sh — prove cross-node live delivery for a BudgieBBS Postgres
# cluster (Workstream 8 of milestone-scaling-multiple-servers.md).
#
# It starts two local budgied nodes against the SAME Postgres database, opens a
# live SSE stream on node B, then creates a thread, a reply, and a reaction on
# node A. A pass means node B observed the thread and the reaction over its live
# stream — i.e. the cross-node pg_notify wakeup path delivered events created on
# a different node.
#
# Requirements: bash, curl, jq, a reachable Postgres, and a Go toolchain (to
# build budgied if a binary is not supplied).
#
# Usage:
#   BUDGIE_POSTGRES_DSN="postgres://u:p@localhost:5432/budgie?sslmode=disable" \
#     ./scripts/cluster-smoke.sh
#
# Optional env:
#   BUDGIE_BIN          path to a prebuilt budgied (default: build to a temp dir)
#   NODE_A_HTTP         default :18091
#   NODE_B_HTTP         default :18092
#   PROPAGATE_WAIT_SEC  seconds to wait for cross-node delivery (default 5)
#
set -euo pipefail

DSN="${BUDGIE_POSTGRES_DSN:-}"
if [[ -z "$DSN" ]]; then
  echo "ERROR: set BUDGIE_POSTGRES_DSN to your Postgres DSN." >&2
  echo "  e.g. postgres://budgie:secret@localhost:5432/budgie?sslmode=disable" >&2
  exit 2
fi

for tool in curl jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: '$tool' is required but not installed." >&2
    exit 2
  fi
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE_A_HTTP="${NODE_A_HTTP:-:18091}"
NODE_B_HTTP="${NODE_B_HTTP:-:18092}"
PROPAGATE_WAIT_SEC="${PROPAGATE_WAIT_SEC:-5}"
# Same JWT secret on both nodes so a token minted on A is valid on B.
export BUDGIE_JWT_SECRET="cluster-smoke-shared-secret"
export BUDGIE_POSTGRES_DSN="$DSN"

A_URL="http://localhost${NODE_A_HTTP}"
B_URL="http://localhost${NODE_B_HTTP}"

WORK="$(mktemp -d)"
BIN="${BUDGIE_BIN:-}"
A_PID=""; B_PID=""; SSE_PID=""

cleanup() {
  set +e
  [[ -n "$SSE_PID" ]] && kill "$SSE_PID" 2>/dev/null
  [[ -n "$A_PID" ]] && kill "$A_PID" 2>/dev/null
  [[ -n "$B_PID" ]] && kill "$B_PID" 2>/dev/null
  wait 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> workdir: $WORK"

if [[ -z "$BIN" ]]; then
  echo "==> building budgied"
  BIN="$WORK/budgied"
  ( cd "$ROOT" && go build -o "$BIN" ./cmd/budgied )
fi

start_node() {
  local name="$1" http="$2" ssh="$3"
  "$BIN" -storage postgres \
    -http "$http" -ssh "$ssh" \
    -hostkey "$WORK/${name}_host_key" \
    -auto-stats=false \
    >"$WORK/${name}.log" 2>&1 &
  echo $!
}

wait_ready() {
  local url="$1" name="$2" i
  for i in $(seq 1 30); do
    if curl -fsS "${url}/readyz" >/dev/null 2>&1; then
      echo "==> ${name} ready"
      return 0
    fi
    sleep 0.5
  done
  echo "ERROR: ${name} did not become ready; log:" >&2
  cat "$WORK/${name}.log" >&2
  return 1
}

# Start node A first and let it apply the schema before node B joins. Two nodes
# applying migrations at the exact same moment can race on the system catalogs;
# bringing the cluster up one node at a time avoids that and mirrors real ops.
echo "==> starting node A (${NODE_A_HTTP})"
A_PID="$(start_node nodeA "$NODE_A_HTTP" 12201)"
wait_ready "$A_URL" nodeA
echo "==> starting node B (${NODE_B_HTTP})"
B_PID="$(start_node nodeB "$NODE_B_HTTP" 12202)"
wait_ready "$B_URL" nodeB

USER="smoke_$$_${RANDOM}"
PASS="smokepass123"

echo "==> registering '$USER' on node A"
curl -fsS -X POST "$A_URL/api/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null

TOKEN="$(curl -fsS -X POST "$A_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"$USER\",\"password\":\"$PASS\"}" | jq -r '.token')"
if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "ERROR: login failed on node A" >&2
  exit 1
fi
AUTH=(-H "Authorization: Bearer $TOKEN")

# Current head on node B; the SSE stream starts after this, so anything it
# observes arrived live (its initial replay window is empty).
HEAD="$(curl -fsS "${AUTH[@]}" "$B_URL/api/v1/events?after=0&wait=0&scope=board:general" | jq -r '.head')"
echo "==> node B head before writes: $HEAD"

echo "==> opening live SSE stream on node B (scope=board:general)"
CAP="$WORK/sse_b.txt"
curl -sN "${AUTH[@]}" "$B_URL/api/v1/events/stream?scope=board:general&after=$HEAD" >"$CAP" &
SSE_PID=$!
sleep 1.5  # let the stream connect and drain its (empty) initial replay

echo "==> creating thread on node A"
THREAD_ID="$(curl -fsS -X POST "$A_URL/api/v1/boards/general/threads" "${AUTH[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"title":"cluster smoke thread","body":"hello from node A"}' | jq -r '.result.id')"
echo "    thread: $THREAD_ID"

echo "==> appending a post on node A"
POST_ID="$(curl -fsS -X POST "$A_URL/api/v1/threads/$THREAD_ID/posts" "${AUTH[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"body":"a reply from node A"}' | jq -r '.result.id')"
echo "    post: $POST_ID"

echo "==> reacting (like) on node A"
curl -fsS -X POST "$A_URL/api/v1/posts/$POST_ID/react" "${AUTH[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"emoji":"heart"}' >/dev/null

echo "==> waiting ${PROPAGATE_WAIT_SEC}s for cross-node delivery"
sleep "$PROPAGATE_WAIT_SEC"
kill "$SSE_PID" 2>/dev/null; SSE_PID=""

echo "==> node B live stream captured:"
sed 's/^/    /' "$CAP" || true

fail=0
check() {
  local needle="$1" desc="$2"
  if grep -q "$needle" "$CAP"; then
    echo "PASS: node B observed $desc"
  else
    echo "FAIL: node B did NOT observe $desc (looked for '$needle')" >&2
    fail=1
  fi
}

echo "==> verifying cross-node delivery"
check "thread.new" "the new thread (cross-node post delivery)"
check "post.reacted" "the reaction (cross-node like delivery)"
check "$THREAD_ID" "events referencing the created thread id"

if [[ "$fail" -eq 0 ]]; then
  echo "==> CLUSTER SMOKE TEST PASSED"
else
  echo "==> CLUSTER SMOKE TEST FAILED" >&2
fi
exit "$fail"
