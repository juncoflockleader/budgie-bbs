# Repository Plan

Status: active. Created July 13, 2026 from a repository-wide architecture,
testing, deployment, and production-readiness evaluation.

This is the only active plan for BudgieBBS. Superseded roadmaps, milestone
journals, hardening plans, and status trackers are retained in
[the plan archive](archive/README.md).

## Objective

Make the existing product easier and safer to ship before expanding its feature
surface or promoting the partitioned-log topology. Work is ordered by user and
operator risk:

1. Make every documented deployment safe and complete.
2. Bring frontend verification closer to the backend standard.
3. Remove duplicated authority paths and unnecessary infrastructure choices.
4. Prove sustained behavior before making internet-scale production claims.

The SQLite single-node shape and ordinary Postgres multi-node shape remain the
supported deployment targets. Broker-backed command/event logs remain behind
promotion gates until this plan's architecture and soak criteria are met.

## Current Baseline

- At the July 13 evaluation baseline, the worktree was clean and the Go
  formatter, build, vet, and listener-free test suite passed.
- The web production build and API-client tests passed at the same baseline.
- The command/event architecture, replay model, operational runbooks, and
  backend test coverage are strong.
- The main risks are incomplete Compose packaging, unsafe deployment defaults,
  sparse frontend testing, duplicated SQL/native command decisions, parallel
  broker stacks, and the absence of sustained soak evidence.

## Phase 0 — Deployment Safety

Complete this phase before feature work.

### 0.1 Ship the SPA in the Compose image

- Add a Node build stage to the root `Dockerfile`.
- Copy the built `web/dist` into the runtime image.
- Configure API nodes with an explicit web root.
- Extend the Compose smoke test to verify `/` returns the SPA and a generated
  asset is reachable, in addition to API readiness and cross-node delivery.

Acceptance criteria:

- `docker compose up -d --build` produces the documented self-contained cluster.
- `GET /` returns the browser application through nginx.
- The existing health, API, WebSocket, and cross-node smoke checks still pass.

### 0.2 Remove the known Compose signing secret

- Replace the checked-in JWT signing value with a required environment or
  Docker secret.
- Have smoke tooling generate an ephemeral secret automatically.
- Label the example topology clearly when a setting is development-only.

Acceptance criteria:

- No usable JWT signing secret is tracked in the repository.
- Compose fails clearly when an operator starts it without a secret.
- Automated smoke runs require no manual secret setup.

### 0.3 Pin privileged provisioning inputs

- Stop piping the mutable `main` branch into a root shell from cloud-init and
  manual setup instructions.
- Fetch a tagged or commit-pinned provisioning artifact and verify its checksum,
  or embed the required provisioning commands in the release artifact.
- Add script-contract checks for every provider template.

Acceptance criteria:

- A repository branch update cannot change an already-published cloud-init
  payload.
- Provider setup remains reproducible from a named release.

### 0.4 Finish the remaining browser-session cleanup

- Stop returning a session JWT in browser-login JSON after cookie issuance.
- Preserve an explicit bearer-token flow for programmatic clients.
- Update security documentation and regression tests with the final behavior.

## Phase 1 — Frontend Quality Gate

### 1.1 Enforce existing tests in CI

- Run `npm run test:api-client` in the web CI job.
- Keep type-checking and the production build as required checks.
- Fail CI when the generated client contract and server acknowledgement model
  drift apart.

### 1.2 Cover critical browser workflows

Add focused automated coverage for:

- login, cookie bootstrap, logout, and session expiry;
- command acknowledgement resolution and read-your-writes retries;
- WebSocket/SSE reconnect, cursor replay, and duplicate delivery;
- create thread, reply, edit, reaction, and poll flows;
- privileged moderation and administration actions;
- public/guest browsing and private-board access boundaries.

Acceptance criteria:

- At least one browser-level smoke suite exercises a built SPA against a real
  `budgied` process.
- Authentication, posting, reconnect/replay, and moderation have regression
  coverage before frontend refactoring begins.

### 1.3 Reduce frontend hotspots incrementally

- Split the API client by capability while keeping one transport/error policy.
- Extract page-level state and effects from the largest React components.
- Split the global stylesheet by layout or feature only when tests protect the
  affected surface.

Line-count reduction is not itself a goal; clearer ownership and safer changes
are the goal.

## Phase 2 — Architecture Simplification

### 2.1 Eliminate duplicate command decisions

- Continue extracting pure decision functions shared by the SQL handler and
  native command-log executor.
- Keep state reads and effect application behind path-specific adapters.
- Convert native-decider parity tests into tests of the shared decision contract.

Acceptance criteria:

- A validation or authorization rule is implemented once for both write paths.
- Replaying an accepted command cannot produce a different event decision from
  direct execution.
- The native path is not promoted while duplicated authority logic remains.

### 2.2 Choose one durable-log strategy

- Record an architecture decision choosing NATS JetStream or Kafka/Redpanda for
  durable command/event logs.
- Treat NATS KV presence/chat/counter use separately from the durable-log
  decision.
- Preserve comparable gate evidence during migration, then remove the rejected
  durable-log adapter, flags, cleanup scripts, and duplicated documentation.

Acceptance criteria:

- One broker owns each architectural responsibility.
- New command/event protocol work does not require two durable-log
  implementations.

### 2.3 Break up coordination hotspots

- Move flag groups and backend construction out of `cmd/budgied/main.go` into
  typed configuration and runtime assembly packages.
- Split projection readers and mutations by domain without changing their SQL
  contracts.
- Keep ordinary SQLite and Postgres paths easy to locate and understand.

Each extraction must be behavior-preserving and land with focused tests.

## Phase 3 — Sustained Scale Proof

Start this phase after the durable-log decision and shared command decisions are
complete.

### 3.1 Implement the soak harness

- Add duration and target-rate modes to the load tooling.
- Drive mixed writes, reads, live subscribers, reconnect churn, and slow
  consumers against at least two API nodes, a worker, Postgres, and the selected
  broker.
- Capture process memory, goroutines, latency, broker lag, projection freshness,
  database resources, reconnects, replay repairs, and bounded drops.
- Retain the detailed historical design in the
  [archived soak profile](archive/internet-scale-soak-profile.md).

### 3.2 Establish promotion tiers

- Smoke: 30 minutes on release candidates.
- Standard: 4 hours before topology promotion.
- Endurance: 24 hours before major releases and periodically thereafter.

Add worker failover, broker reconnect, and slow-consumer injection after a clean
baseline passes.

Acceptance criteria:

- Memory and goroutine trends remain bounded.
- Command and projection lag recover to zero.
- Reconnect replay repairs every intentional interruption without durable loss.
- A clean, reproducible evidence bundle passes the standard tier before the
  partitioned-log topology is described as production-ready.

## Phase 4 — Ongoing Security And Maintenance

- Add automated dependency-vulnerability and update checks for Go, npm, GitHub
  Actions, and container bases.
- Pin release-critical actions and images to immutable versions or digests.
- Keep `SECURITY.md` limited to current behavior and open limitations.
- Keep this file as the single active plan. Move superseded implementation
  journals to `doc/archive/` and update the archive index.
- Review large status and deployment documents periodically; preserve durable
  design and operational facts while removing progress-log narration.

## Deferred Product Work

New parity features, retro gateways, federation, games, and broad UI expansion
are deferred until Phases 0 and 1 are complete. Small fixes and accessibility
work may proceed when they do not delay those gates.

## Definition Of Done

An item is complete only when:

- SQLite behavior remains correct unless the item is explicitly cluster-only.
- Postgres and cross-node implications are tested and documented.
- Security-sensitive defaults fail closed with actionable errors.
- Relevant Go, web, script, deployment, and smoke checks pass.
- Operational documentation changes with the implementation.
- Temporary compatibility paths and superseded plan text are removed or
  archived deliberately.
