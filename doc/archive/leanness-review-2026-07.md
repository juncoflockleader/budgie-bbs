# Leanness & Architecture Review (July 2026)

> Archived on July 13, 2026. Its unfinished recommendations were consolidated
> into [the current roadmap](../roadmap.md).

Historical status: in progress. This document records the findings of a repo-wide leanness
review (2026-07-02) and the agreed directions for shrinking the codebase
without losing capability. Fold completed items into
[the archived roadmap](roadmap-2026-07-13.md) and delete their sections here per
[repo maintenance guidance](../repo-maintenance.md).

## Summary

The architecture is fundamentally sound: a command/event core with one
authority path (`core.ExecCmd`) and four thin frontends (web, SSH TUI,
WebSocket, NNTP) that leak no business logic. The weight is concentrated in
three places:

1. The internet-scale migration left a ~10k-line hand-copied reimplementation
   of the command handlers
   ([command_log_native_decider.go](../../internal/core/command_log_native_decider.go)),
   plus ~5k lines of flag-gated scaffolding inside `internal/core`.
2. Two full broker stacks (Kafka ~7.3k lines, NATS ~6.4k lines) are carried
   for the same architectural role (command/event log).
3. Mechanical boilerplate: 234 hand-written HTTP handlers and 9 auxiliary
   `cmd/` binaries that re-implement each other's setup code.

A realistic pass removes **~8–12k lines of Go** (plus a large share of the
15k-line decider test) with no capability loss.

### Baseline (2026-07-02)

- ~172k lines of Go total; `internal/core` is ~110k across 160 files.
- Production (default flags) exercises exactly **one** write path:
  SQL handler → event append → projections. Three more write paths (shadow
  log, broker+SQL hybrid, broker+native) exist behind flags as staging steps
  for the internet-scale epic.
- The read side is clean (core → bridge → `projections/readers.go`, no
  duplicate queries). The sqlite/postgres schema split is honest per-dialect
  DDL, not waste.
- All four frontends route writes through `core.ExecCmd` — including NNTP.

## Leanness directions, ranked

### 1. Deduplicate the native decider (highest value; fixes a correctness risk)

[command_log_native_decider.go](../../internal/core/command_log_native_decider.go)
re-implements handler validation at 70–95% overlap per command. Example:
`createThread` is ~95% identical between
[handler/commands_threads.go](../../internal/core/handler/commands_threads.go)
(~line 21) and the decider (~line 271). Every rule change (sanctions, poll
trust levels, automod, content filters) must be made twice; divergence means a
replayed command produces different events — a data-corruption class of bug.

**Direction:** extract each command into a **pure decide function**
(state in → validated events out) that both the SQL handler and the native
executor call. The paths differ only in how they *apply* effects. Expected
outcome: decider shrinks ~10k → ~6k lines, its 15k-line test collapses with
it, and new commands get internet-scale support for free instead of a second
implementation.

**This should land before the native path is promoted to production.**

- Effort: high (~12 commands to extract). Risk: medium — mitigated by the
  existing parity tests, which become the safety net for the refactor.
- **Started 2026-07-03:** post body length validation now lives with the
  command payload model in `internal/proto`; SQL handlers and native
  create/reply, board-mail, repost, edit/redact/restore/purge-post, post-range,
  clear-board-junk, board favorite/zap, move-thread, and post-flag payload
  validation and system-notice decisions share the same cap,
  thread-title/update and system-notice title validation share the same
  required/length rule,
  system-notice source validation and publish-system-notice payload
  normalization share one rule, post/mail
  attachment metadata normalization/validation, SQL/native count/staging readers,
  promoted blob idempotency checks, inline ID stamping, and count limits share helpers,
  post content-type canonicalization and mail quota size
  calculation, anonymous post identity, post signature trimming, and quoted
  reply prefix, repost, article mail-back, system notice, and blessing body
  formatting plus post-author mail, sysmail, syssecurity, and sanction system body
  formatting, forward/post-to-board mail bodies, content-filter review body
  formatting, and poll-result body formatting share helpers,
  sanction payload normalization, sanction/automod reason normalization, board
  recommendation payload normalization, and board membership
  note/application/leave/review-payload normalization share their rules,
  automod rule create/delete payload normalization plus set/delete target
  validation, create-board payload validation, content-filter
  normalization/validation, and command
  slug validation are shared across SQL/native execution, user-ref lookup,
  user relationship existence readers, login-watch payload normalization, and
  presence status/online predicates,
  mail attachment filename extraction and all-recipient lookup,
  blessing payload normalization and existence readers,
  direct-message policy/target normalization/readers, mailbox normalization,
  mail-group ID/member/deletion lookup, mail-copy
  projection readers, restore-size/count readers, and quota checks,
  post/mail range ID normalization, and board-junk ID/thread lookup share one rule,
  board settings payload, audit-line formatting, guest-access canonicalization,
  and read-marker payload,
  board/thread existence/name lookups, poll-by-post lookup,
  thread root reply guard/post lookup, thread/post read-marker payload,
  digest curation target/field payload,
  digest entry maintenance payload, board member title/mode/status,
  moderator/member payload, delegated-permission change detection and board
  member permission readers plus board member final-state calculation,
  requirements-payload normalization, membership admission checks, and
  counter-backed admission score readers plus membership application lookup
  and auto-approval note constants, board-member permission-policy checks,
  moderation
  review target/resolution payload normalization, moderation audit-log
  board identity/formatting, content-filter generated-review board
  identity/IDs, publish-poll-result target normalization, and vote-board
  result identity/IDs are shared across SQL/native execution,
  favorite-folder name/payload/state readers and import-favorite-tree structure
  validation share one rule, favorite-tree import projection paths share one planner,
  stats snapshot date normalization and stats system-post idempotency prelude,
  system-notice board normalization, command slug validation messages, and
  digest kind/path normalization plus digest path mutation board/kind/source
  path normalization and digest permission/scoping helpers share one rule,
  mail-group payload normalization plus friend-list aliases,
  delete-mail-group, attach-post, and
  attach/update/delete/forward-mail payload normalization plus post-mail-to-board
  source/target validation, direct-message target/send payload normalization
  plus conversation ID canonicalization,
  send-mail content, mail-post-author, and
  digest-entry mail payload normalization and
  post/forward/mail-author/digest-mail subject normalization share one rule,
  and native create/reply
  decisions now fail closed on overlong bodies before state reads.

### 2. Pick one broker (biggest strategic fork)

Kafka and NATS are both fully wired production paths for the command/event
log, each with its own connector, gate script
(`scripts/commandlog-native-kafka-gate.sh` /
`scripts/commandlog-native-nats-gate.sh`), preflight coverage, and doc
sections. Every future change to the log protocol is made twice.

Considerations:

- NATS already covers roles Kafka cannot in this deployment (presence, chat,
  counter KV stores) and NATS KV requires Postgres.
- [design-internet-scale-writes.md](../../design-internet-scale-writes.md)
  targets Kafka partitioning for the scale epic.
- A defensible split is Kafka-for-logs + NATS-for-KV — but then delete the
  NATS *log* path, or vice versa. Carrying both log paths indefinitely is the
  single largest sustained maintenance cost in the repo.

**Redis** (`internal/redisconn`, ~800 lines) is a purely optional cache/index
with SQL fallbacks. Drop it or gate it behind a build tag.

- Effort: decision first, then medium mechanical removal. Risk: low once
  decided (paths are flag-isolated).

### 3. Quarantine or delete internet-scale scaffolding from core

~1.5–2k lines of test fixtures and load harness live inside the production
package:

- Memory-backed chat/counter/presence stores — only tests instantiate them;
  production uses SQL or NATS-KV.
- `command_log_drain_load.go` (~2k lines) — used only by
  `cmd/budgie-commandlog-loadgen`.
- One-shot promotion-gate code (`command_log_promotion_readiness.go`,
  `event_log_shadow.go`).

**Direction:** move load harness and memory fixtures to a dedicated
harness/testfixtures package (or delete where an in-memory SQLite covers the
test). Keeping flag-gated *paths* is fine while the epic is live; keeping load
generators inside `internal/core` is not.

- Effort: low (1–2 days). Risk: low.
- **Partially done 2026-07-02:** memory stores are now test-only fixture
  files. Gateway fanout and partition-write load/report/budget code now live
  in `internal/loadtest`, with the loadgen/report-check commands consuming
  that package. The command-log drain load config/runtime/stage model now lives
  in `internal/loadmodel`; tooling consumes that package directly, and
  `internal/core` no longer re-exports those model types. Remaining: the
  tighter core-coupled `command_log_drain_load.go` runner and one-shot
  promotion-gate code still live in `internal/core`, though the runner's
  create/reply submit+drain phase skeleton, sample accounting,
  authoritative/non-authoritative submit branching, pending-receipt validation,
  projection-error bookkeeping, native batch pending transaction/failure/result
  bookkeeping plus pending record envelope/queue helpers and pending-flush
  workflow, worker assignment/claim result construction, drain-load report
  finalization, local SQL `IN` helper construction, and one-shot report
  finalization now share small helpers; the submit worker pool lives in
  `internal/loadutil`; drain-load
  config validity, create/reply phase planning, report
  construction/finalization/phase aggregation, load/drain/projection stage
  construction, final drain-load report validation, promotion-readiness
  construction/coverage/finalization plus lag/ready bookkeeping, and
  materialization-audit
  construction/coverage/finalization plus missing/incomplete bookkeeping live
  with the report model in `internal/loadmodel`.

### 4. Collapse the tooling binaries

Nine of ten `cmd/` binaries are loadgen/report-check/preflight tools that each
re-implement Kafka TLS/SASL config, NATS dialing, Postgres opening, and flag
parsing (~700 duplicated lines).

**Direction:** one `budgie-test` binary with subcommands
(`commandlog`, `gateway`, `preflight`, `report-check --type=...`) plus a
shared connection/config helper package. Gate scripts in `scripts/` change
invocations only; update `scripts/scripts_test.go` in the same change.

- Effort: low–medium. Risk: low (tooling only, pinned by script tests).
- **Partially done 2026-07-02:** the four report-check binaries are now
  `cmd/budgie-report-check` subcommands. Load/preflight report evidence
  collection (budget hash + git revision/dirty state) and runtime endpoint
  redaction/sanitization plus sanitized-endpoint validation now live in
  `internal/runevidence` instead of being reimplemented by each command;
  command-log and gateway report-check subcommands share the same
  flag/budget/report/hash/violation runner;
  report-check and manifest hashing now reuse the same evidence SHA, report
  evidence envelope decoding, and evidence violation formatting helpers,
  bundle manifest target/tool/evidence validation now lives in
  `internal/runbundle` instead of the command package,
  budget-hash comparison is enforced through `internal/scalebudget`,
  command-log and gateway budget checks now use the same report-evidence,
  endpoint-locality, sensitivity, and budget-path helpers, and command-log,
  gateway, preflight, and bundle checks now share one evidence type and one
  no-budget tool/git/clean-tree evidence validator. Report structs now use
  the shared evidence type directly instead of per-report aliases, and
  loadgen commands call the shared evidence collector directly. Preflight
  generation/checking share the same report schema model, and preflight
  config validation, promoted resource naming, sanitized report generation,
  report writing, report-check validation, and live Postgres/NATS/Kafka probes now live in
  `internal/preflightcheck` instead of the command package. Shared
  env parsing, disposable Postgres
  schema naming/validation/provisioning, and search-path DSN wiring now live in
  `internal/runconfig` instead of being copied across `budgied`, loadgen,
  preflight, and cleanup tools; backend/executor alias normalization,
  command-log executor and worker-ownership support checks/messages,
  projection rebuild/promotion source aliases, optional broker backend
  support checks/messages, side-store SQL/NATS-KV aliases,
  read-cache and post-search index backend aliases,
  and preflight/bundle target-list parsing,
  endpoint-based preflight target inference, and membership checks are shared
  there too. Kafka TLS/SASL flag
  registration and topic replication-factor validation are now shared in
  `internal/kafkaconn` instead of being copied across tool binaries. Shared
  NATS JetStream open/cleanup helpers now live in `internal/natsconn`, covering
  command-log loadgen and one-shot `budgied` event-store probes while leaving
  the main server's long-lived NATS connection explicit; preflight now reuses
  the same NATS connection/security setup instead of calling `nats.Connect`
  directly. `budgied` also uses the NATS package's canonical command/event
  stream defaults instead of local string copies, and JetStream option
  defaulting, command/event stream config builders, and stream
  ensure/validation merging are shared across the NATS adapters and preflight
  tooling.
  Shared synthetic load body/ID helpers now live in
  `internal/loadutil`, and repeated pretty-JSON/stdout plus atomic report-file
  output plus strict/lenient report JSON decoding now go through
  `internal/runreport`; report-check, bundle manifest, and scale-budget reads
  use shared helpers instead of local copies, and Postgres
  integration-test schema setup is shared too. Flag-value-before-env precedence,
  duration env parsing, and SQL/NATS-KV side-store support checks now share
  `internal/runconfig`, and Kafka command/event runtime open helpers live in
  `internal/kafkaconn` so commands no longer rebuild franz clients locally;
  SQL-positioned Kafka event-store setup is shared there too, covering both
  scalar-compatible and partition-only event offset modes. Command-log scalar
  allocator aliases are normalized once in `internal/loadmodel`, so budget
  checks and loadgen runtime evidence no longer carry separate alias tables;
  command-log load Postgres schema, NATS stream, and Kafka topic naming now
  share the same `internal/loadmodel` constants/helpers, including Kafka
  cleanup topic selection.
  Kafka command/event runtime-plus-partition validation is also shared in
  `internal/kafkaconn` and reused by `budgied` and command-log loadgen.
  Command-log load runtime validation, sanitized runtime reporting, Kafka load
  topic specs, and scalar compatibility audit attachment now live in
  `internal/loadtest` instead of the loadgen command package.
  Command-log load backend opening for memory/NATS/Kafka, Redis-backed
  partition indexing, native command/event store wiring, and the in-memory
  command-log partition index also live in `internal/loadtest`; loadtest now
  reuses core's indexed command-log wrapper instead of carrying a second copy,
  and command/event-log offset listers, including the NATS command/event
  clients, share the same core normalization and ordering helpers. Core
  partition listing paths also share one `LogPartition` ordering helper instead
  of repeating kind/key comparators at each call site, and cursor/broker replay
  paths share one proto event replay ordering helper instead of duplicating the
  seq/partition/offset comparator. Event projection target checks now use the
  same bounded event-partition-offset lister as the ordering helpers instead of
  open-coding limit-overflow detection in the load runner, and promotion
  readiness, projection materialization, and the event replay parity runner
  share the same bounded event-partition lister. Promotion readiness,
  materialization audit, drain-load
  snapshots, and drain-load lag checks also share one bounded
  command-partition-offset helper, and promotion readiness now passes its
  already-listed offset snapshot into materialization audit instead of listing
  the same partitions twice; drain-load member cursors now share one
  partition-offset-to-cursor helper while still refreshing committed offsets
  from the command log after snapshot assignment, and worker/drain-load
  assignment-lister paths share one assignment-to-partition normalization
  helper plus shared assignment constructors for hash, snapshot, drain-load,
  and worker fallback paths, while SQL/worker claim paths share one claim
  constructor, and drain-load cursor/snapshot-assignment paths share one
  lagging-offset filter; hot-thread split checks,
  command partition
  metrics, indexed command-log commit reads, cursor replay/head-cursor reads,
  event partition metrics, and shadow-at-head seeding now use the same
  normalized offset readers too, with required command-log offset-lister gates
  sharing the same wrapper in promotion readiness, materialization audit, and
  drain-load lag/snapshot paths; command partition lag math now lives on the
  offset type instead of being recomputed by metrics, split checks, offset
  sorting, and drain-load filters. Metrics emitters also share partition
  label/sample helpers for command/event partition offsets and hot-partition
  candidate signals instead of rebuilding the same `kind`/`key`/`signal` maps
  and metric metadata at each call site.
  Load-test core setup, including temp/explicit SQLite fallback and Postgres
  setup (admin connection, disposable schema, `search_path`, and
  `core.NewPostgres`), now lives in `internal/loadtest`, and
  command-log load report/config/runtime/stage model types, scalar allocator
  normalization, executor/assignment normalization, load-shape counts, and
  stage aggregation plus capped sample-error collection, partition-scoped drain
  sample formatting, and shared positive integer config defaulting now live in
  `internal/loadmodel`; the core runner now takes those model types directly
  instead of carrying compatibility aliases.
  Command-log promotion-readiness and materialization-audit report/config
  models are also consumed from `internal/loadmodel` directly, without core
  re-export aliases. Internet-scale
  budget schema/loading/formatting and the command-log budget evaluator now
  live in `internal/scalebudget`, with command/gateway/partition tooling
  consuming that package directly instead of importing budget policy through
  `internal/core`; gateway and partition-write load checks now also share the
  same scale-budget violation/evidence mapping instead of local helper copies.
  One-shot tooling commands now share interrupt/timeout
  context setup and timeout capping through `internal/runconfig`. Remaining:
  deeper command-log load-harness extraction and folding the loadgen/preflight
  binaries into subcommands if desired.

### 5. Flatten HTTP handler boilerplate

The 234 handlers in `internal/httpapi` are correctly thin but hand-written:
~8 identical lines of parse/call/serialize each (~1.9k lines total, mostly in
[commands.go](../../internal/httpapi/commands.go) and
[reads.go](../../internal/httpapi/reads.go)).

**Direction:** table-driven route registration with generic decode/respond
helpers; auth/freshness checks become middleware. Saves ~1.2–1.6k lines and
eliminates copy-paste drift.

**Related quick win (done 2026-07-02):** SSE gap-replay in
`httpapi/events.go` and WebSocket gap repair in
[wsapi/conn.go](../../internal/wsapi/conn.go) were ~150 near-identical lines;
now both call `Core.DeliverWithGapRepair` in
[internal/core/stream_delivery.go](../../internal/core/stream_delivery.go).

- Effort: medium (handlers), low (gap replay). Risk: low–medium — large but
  mechanical surface; the heavyweight httpapi tests pin behavior.

### 6. Web app (lower priority, opportunistic)

No logic leakage, but the big pages (`UserProfilePage.tsx` ~1,350 lines,
`ThreadPage.tsx` ~1,300) each hand-roll 30–40 `useState` hooks and repeated
load/error patterns; the projection-staleness retry exists only in
`client.ts`. A shared data-loading hook with staleness retry built in trims
~500 lines and makes the pages splittable. Do this when touching the pages
anyway, not as a standalone project.

### Explicitly rejected

- **Generating sqlite/postgres DDL from a shared schema DSL.** Saves ~500
  lines but replaces two readable dialects with an abstraction that must be
  battle-tested. The duplication is honest; keep both schema files.
- **Sharing TUI/web presentation logic.** Both render independently from the
  same core models with no duplication of business rules; ROI is negative.
- **Merging `internal/policy` into core registration.** On inspection the
  package is not trivial glue: it is a coherent `go:embed` of the default
  privacy policy with a content-hash version and a drift test against
  [doc/default-privacy-policy.md](../default-privacy-policy.md). Merging would
  move an embedded asset into the already-large core package for no gain.

## Architectural principles going forward

- **Protect the core discipline.** One `ExecCmd` authority path, thin
  frontends, zero business logic outside core. This is the repo's best asset;
  enforce it in review.
- **Make "decide" pure and shared** (direction 1). Not just deduplication —
  it is the structural fix that makes the SQL path, the broker-native path,
  and any future execution backend the *same code* with different effect
  application.
- **Replace the flag matrix with named profiles.** `budgied` composes
  storage × shadow × worker × executor × three KV stores × cache × index as
  raw flags; only a handful of combinations are validated, and the docs never
  enumerate which. Define 2–3 named profiles — `single-node`, `multi-node`,
  `internet-scale` — that expand to vetted flag sets. This shrinks the
  test/gate matrix and the deployment docs at once.
- **Set a sunset condition for migration scaffolding.** The shadow and hybrid
  write paths exist to validate the native path. When the native path is
  promoted, delete them and the parity machinery **in the same milestone**.
  Transitional scaffolding that outlives its transition is how core reached
  110k lines.
- **Watch test economics.** The 15k-line decider test and 5.5k-line httpapi
  test files are symptoms of duplicated logic, not thoroughness.
  Consolidating the logic (directions 1 and 5) collapses them naturally.

## Architectural follow-ups (added 2026-07-02)

These came out of the "is the architecture sound?" question after the
quick-win batch. The skeleton is sound; these are the open structural items
that cleanup alone does not answer.

### F1. Decide the consistency model of the native decide path

The deep design question in the internet-scale write path, and the one thing
deduplication (direction 1) does not resolve: when the broker log is
authoritative, `decide()` reads state from SQL projections that are only
*eventually* consistent with that log. Within a partition the worker
serializes decisions, but cross-partition reads (a user's trust level, a
board's settings living under a different partition key) can be stale at
decision time. The SQL path never had this problem because decision and state
shared one transaction.

**Deliverable:** a per-command design table answering, for every command the
native executor handles: which state reads are partition-local (safe), which
tolerate bounded staleness (and what the blast radius of a stale read is),
and which are unsafe and force either a partition-key change or exclusion
from the native path. Record the decision in
[design-internet-scale-writes.md](../../design-internet-scale-writes.md). This
must land **before** native promotion — ideally alongside the shared decide
functions, since extracting them is when each command's reads get enumerated
anyway.

### F2. Modularize/layerize the core package

`internal/core` is ~67k production lines in effectively one Go package, so
nothing enforces layering — any file can reach any other file's internals.
`handler/` and `projections/` are the right precedent; continue carving
compile-enforced seams. Candidate cuts, roughly in order of value:

- **log machinery** (command/event log, partitioning, workers, broker
  bridges — the 20k-line bucket) behind an interface core consumes;
- **derived-view processors** (the ~4.4k of rankings/summaries/search
  processors, which already share a worker pattern);
- **feature services** (twofactor, email verification, captcha glue, AI
  processor) that only need narrow core interfaces.

Do this incrementally — one seam per PR, moving files with `git mv` and
introducing an interface only where the new package boundary demands it. The
goal is compile-enforced dependency direction, not a package count.

### F3. Isolate the internet-scale path so it is removable at any time

We probably still need the internet-scale path, so the goal is not deletion —
it is making the *option* to delete (or promote) it cheap and permanent.
Target state: the entire path lives behind a small set of interfaces wired up
only in `cmd/budgied`, such that removing it is deleting packages plus wiring,
with zero edits inside core's domain logic.

- Define the seam: core exposes narrow ports (command submission, event
  append, projection apply); the broker-native machinery implements them from
  its own package(s) (natural home once F2's log-machinery cut exists).
- Move the remaining load-harness file out of core
  (`command_log_drain_load.go` — ~2k lines, the unfinished half of direction
  3). `gateway_fanout_load.go` and `partition_write_load.go` have already
  moved to `internal/loadtest`.
- Add a CI-checkable guard for the seam, e.g. a `go list`-based test
  asserting that core's domain packages do not import the internet-scale
  packages, so the isolation cannot silently erode.
- **Guard added 2026-07-03:** `internal/core` now has a direct-import guard
  preventing it from reaching back into the extracted load-test, report,
  preflight, run-config, broker-connector, and budget packages.
- Payoff either way: if the epic is promoted, shadow/hybrid deletion is
  contained; if it is shelved, removal is one commit; and until then, the
  default single-node build carries the path only as unused wiring.

## Suggested sequencing

1. **Quick wins (days):** memory stores → fixtures; gap-replay extraction;
   consolidate report-check binaries. **Done 2026-07-02:**
   - Memory chat/counter/presence stores renamed to `*_fixture_test.go` in
     `internal/core` — verified unreachable in production (`-chat-store`,
     `-counter-store`, `-presence-store` accept only `sql`/`nats-kv`);
     ~1,140 lines out of the production build.
   - Gap-replay logic deduplicated into
     `internal/core/stream_delivery.go` (`Core.DeliverWithGapRepair`); SSE
     and WebSocket delivery now share one repair path, with the transport
     policy on replay failure (SSE aborts, WS logs and continues) passed in.
   - Four report-check binaries merged into `cmd/budgie-report-check` with
     `commandlog`/`gateway`/`preflight`/`bundle` subcommands and shared
     strict-JSON/budget-hash helpers; scripts, script tests, runbooks, and
     milestone docs updated. Flags and exit codes are unchanged, and the
     bundle manifest keeps the pre-consolidation `tool` identifier so
     archived staging evidence still verifies.
   - The `internal/policy` merge was **rejected** on inspection (see
     "Explicitly rejected").
2. **Before native promotion:** shared decide functions (direction 1)
   together with the consistency-model decision table (follow-up F1) — the
   extraction enumerates each command's state reads, which is exactly the
   input F1 needs.
3. **Strategic decision:** broker choice (direction 2), then delete the
   losing log path and its gates/docs.
4. **Structural:** internet-scale isolation seam (follow-up F3), starting
   with the log-machinery package cut from F2 and moving the load-harness
   files out of core; then continue core layering (F2) one seam at a time.
5. **At promotion milestone:** delete shadow/hybrid paths and parity
   machinery; introduce named profiles.
6. **Opportunistic:** table-driven HTTP handlers; web data-loading hook.
