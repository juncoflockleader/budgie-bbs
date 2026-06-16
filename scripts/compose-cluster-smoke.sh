#!/usr/bin/env bash
#
# compose-cluster-smoke.sh — bring up the docker-compose cluster, prove
# cross-node delivery against it, and tear it down.
#
# It starts the full stack (Postgres + init + 2 API nodes + SSH + worker + LB),
# waits for both API nodes to report ready, then runs cluster-smoke.sh in
# external mode with node A = api1 (direct) and node B = api2 (direct), so a
# write on one API node must reach a live stream on the other.
#
# Requirements: docker (with the compose plugin), curl, jq.
#
# Usage:
#   ./scripts/compose-cluster-smoke.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! docker compose version >/dev/null 2>&1; then
  echo "ERROR: 'docker compose' is required." >&2
  exit 2
fi

# Expose the two API nodes on the host so the smoke test can reach each directly
# (the compose file only publishes the LB; we add per-node ports here).
export COMPOSE_API1_PORT="${COMPOSE_API1_PORT:-18101}"
export COMPOSE_API2_PORT="${COMPOSE_API2_PORT:-18102}"

cleanup() {
  set +e
  docker compose -f docker-compose.yml -f deploy/compose.smoke.yml down -v >/dev/null 2>&1
}
trap cleanup EXIT

echo "==> building and starting the cluster"
docker compose -f docker-compose.yml -f deploy/compose.smoke.yml up -d --build

echo "==> waiting for API nodes"
for url in "http://localhost:${COMPOSE_API1_PORT}" "http://localhost:${COMPOSE_API2_PORT}"; do
  ok=0
  for _ in $(seq 1 60); do
    if curl -fsS "${url}/readyz" >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  if [[ "$ok" -ne 1 ]]; then
    echo "ERROR: ${url} did not become ready" >&2
    docker compose logs --tail=50
    exit 1
  fi
done

echo "==> running cross-node smoke against api1 and api2"
BUDGIE_JWT_SECRET="compose-cluster-shared-secret-demo-0123456789" \
BUDGIE_SMOKE_NODE_A="http://localhost:${COMPOSE_API1_PORT}" \
BUDGIE_SMOKE_NODE_B="http://localhost:${COMPOSE_API2_PORT}" \
  ./scripts/cluster-smoke.sh
