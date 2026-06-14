# Counter-Store Repair Runbook

Use this runbook when high-volume unordered counters diverge from the API,
rankings, search, or community statistics. The default production store is SQL
side-store rows; clusters trialing `-counter-store nats-kv` keep the same
identity and aggregate state in a JetStream KV bucket instead. These counters
are intentionally not durable ordered events, so the event log cannot recreate
missing reaction or poll-vote identity after the active counter store loses it.

## Current Counter Surfaces

| Surface | Current store | Serving path | Repair source |
|---------|---------------|--------------|---------------|
| Post reactions | SQL default: `post_reactions` plus `post_reaction_count_shards`; NATS KV opt-in: `reaction/*` and `reaction_count/*` keys | post reads, thread/ranking reaction counts | active-store backup/PITR or verified active-store rows |
| Poll votes | SQL default: `poll_votes` plus `poll_vote_count_shards`; NATS KV opt-in: `poll_vote/*` and `poll_option_vote_count/*` keys | poll option vote counts | active-store backup/PITR or verified active-store rows |
| Reactions received | SQL default: `user_activity.reactions_recv`; NATS KV opt-in: `reaction_received/*` keys | user rankings, trust/activity reads | recompute from active reaction identity and `posts`, or restore active-store backup |
| Login and online counters | `user_activity`, `community_counter_totals` | community stats and user rankings | Postgres backup/PITR, then derived-view repair |
| Async derived views | `posts_fts`, ranking, summary, feed, and community tables | search, rankings, summaries, feeds | durable event log plus current SQL side-store |

`reactPost`, `unreactPost`, and `votePoll` update the configured counter store
and publish non-durable live events. In SQL mode, a projection rebuild snapshots
`post_reactions` and `poll_votes`, clears projection tables, replays durable
post and poll structure, restores valid unordered rows, rebuilds the shard
aggregate rows, and recomputes `reactions_recv`. In NATS KV mode, counter
checkpoints can restore aggregate counts during ordered replay, but per-user
identity still requires the KV bucket backup.

## Preflight

1. Identify the user-visible symptom and affected surface:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E 'budgie_derived_view_lag_events|budgie_outbox_jobs|budgie_write_region_proxy_failures_total'
```

2. Capture before-repair evidence:

```sh
curl -fsS "$BUDGIE_API/metrics" > "/tmp/budgie-counters-before-$(date -u +%Y%m%dT%H%M%SZ).prom"
psql "$BUDGIE_POSTGRES_DSN" -c "SELECT COUNT(*) AS reactions FROM post_reactions"
psql "$BUDGIE_POSTGRES_DSN" -c "SELECT COUNT(*) AS poll_votes FROM poll_votes"
psql "$BUDGIE_POSTGRES_DSN" -c "SELECT COALESCE(SUM(count_value),0) AS reaction_shard_total FROM post_reaction_count_shards"
psql "$BUDGIE_POSTGRES_DSN" -c "SELECT COALESCE(SUM(count_value),0) AS poll_vote_shard_total FROM poll_vote_count_shards"
psql "$BUDGIE_POSTGRES_DSN" -c "SELECT COUNT(*) AS users_with_activity FROM user_activity"
```

If the cluster uses `-counter-store nats-kv`, also capture the JetStream KV
bucket status and backup metadata for the configured
`-counter-store-nats-bucket` before changing state.

3. Keep only one repair owner active. Stop duplicate one-shot rebuilds and avoid
   running `-derived-view-watermarks` for a view owned by a dedicated processor.

4. Decide which state is damaged:

- if `post_reactions` or `poll_votes` rows are missing, restore those SQL rows
  from Postgres backup, PITR, or a verified replica first
- if `-counter-store nats-kv` is enabled, restore the KV bucket or affected key
  ranges from JetStream backup/snapshot before treating SQL side-store tables as
  authoritative
- if only `user_activity.reactions_recv` is wrong in SQL mode, recompute it from
  current SQL side-store rows; in NATS KV mode, repair the
  `reaction_received/*` keys or derive them from a verified KV identity backup
- if only rankings, search, summaries, feeds, or community snapshots are stale,
  run derived-view backfill
- if projection tables were cleared or corrupted, run the full projection
  rebuild before derived-view backfill

## SQL Side-Store Repair

The current side-store is the source of truth for reaction identity and poll
vote identity. Do not attempt to reconstruct lost rows from the ordered `events`
table; reactions and poll votes no longer append durable ordered events.

After restoring side-store rows from backup or after confirming the base rows
are intact, recompute the derived shard totals and `reactions_recv`:

```sh
psql "$BUDGIE_POSTGRES_DSN" <<'SQL'
BEGIN;

TRUNCATE post_reaction_count_shards, poll_vote_count_shards;

INSERT INTO post_reaction_count_shards (post_id, shard, count_value, updated_at)
SELECT post_id, shard, COUNT(*), COALESCE(MAX(ts), 0)
  FROM (
        SELECT post_id,
               ts,
               (COALESCE((
                  SELECT SUM(ascii(substr(pr.user_id, i, 1)))
                    FROM generate_series(1, length(pr.user_id)) AS i
                ), 0)::BIGINT % 64)::INTEGER AS shard
          FROM post_reactions pr
       ) seeded
 GROUP BY post_id, shard;

INSERT INTO poll_vote_count_shards (poll_id, option_id, shard, count_value, updated_at)
SELECT poll_id, option_id, shard, COUNT(*), COALESCE(MAX(ts), 0)
  FROM (
        SELECT poll_id,
               option_id,
               ts,
               (COALESCE((
                  SELECT SUM(ascii(substr(pv.user_id, i, 1)))
                    FROM generate_series(1, length(pv.user_id)) AS i
                ), 0)::BIGINT % 64)::INTEGER AS shard
          FROM poll_votes pv
       ) seeded
 GROUP BY poll_id, option_id, shard;

UPDATE user_activity SET reactions_recv = 0;

INSERT INTO user_activity (user_id, reactions_recv)
SELECT p.author_id, COUNT(*)
  FROM post_reactions pr
  JOIN posts p ON p.id = pr.post_id
 WHERE p.author_id <> ''
   AND p.author_id <> pr.user_id
 GROUP BY p.author_id
ON CONFLICT(user_id)
DO UPDATE SET reactions_recv = EXCLUDED.reactions_recv;

COMMIT;
SQL
```

Check for foreign-key or restore damage before rebuilding projections:

```sh
psql "$BUDGIE_POSTGRES_DSN" <<'SQL'
SELECT COUNT(*) AS reaction_orphans
  FROM post_reactions pr
  LEFT JOIN posts p ON p.id = pr.post_id
  LEFT JOIN users u ON u.id = pr.user_id
 WHERE p.id IS NULL OR u.id IS NULL;

SELECT COUNT(*) AS vote_orphans
  FROM poll_votes pv
  LEFT JOIN polls p ON p.id = pv.poll_id
  LEFT JOIN poll_options po ON po.id = pv.option_id AND po.poll_id = pv.poll_id
  LEFT JOIN users u ON u.id = pv.user_id
 WHERE p.id IS NULL OR po.id IS NULL OR u.id IS NULL;
SQL
```

Expected: both orphan counts are zero. If either count is non-zero, restore a
cleaner backup slice or delete only rows that point to permanently removed
parents and attach the query output to the incident record.

## Projection Rebuild

Run this when the core projection tables, posts, polls, or search rows were
corrupted or restored out of order. The rebuild preserves current unordered SQL
side-store rows by snapshotting them before the projection clear:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -rebuild-projections
```

If the SQL event log is unavailable and the broker event-log candidate has
green parity, rebuild from that broker source.

JetStream example:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -rebuild-projections \
  -rebuild-source nats \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG
```

Kafka/Redpanda example:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -rebuild-projections \
  -rebuild-source kafka \
  -kafka-brokers "$BUDGIE_KAFKA_BROKERS" \
  -kafka-event-topic budgie.events \
  -kafka-event-partitions 32
```

After repairing base projections, rebuild affected async views. Prefer the
smallest group:

```sh
./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views rankings

./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views search

./budgied -storage postgres -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views community
```

Use `-backfill-derived-views all` only after restore, schema migration, or broad
projection loss.

## NATS KV Counter-Store Repair

Use this section only for clusters started with `-counter-store nats-kv`.

The JetStream KV bucket is the source of truth for reaction identity, poll-vote
identity, and aggregate counter shards. Do not attempt to reconstruct lost
identity from the ordered `events` table. Restore the affected bucket or key
ranges from JetStream backup/snapshot first, then restart all serving/writing
nodes with the same `-counter-store nats-kv`,
`-counter-store-nats-bucket`, and `-counter-store-shards` settings.

If reaction and poll-vote identity keys are intact but aggregate shard keys are
missing, stale, or copied from the wrong shard count, run a single repair owner
with writes paused or routed away from the affected bucket:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -counter-store nats-kv \
  -counter-store-nats-bucket BUDGIE_COUNTER_STORE \
  -counter-store-shards 64 \
  -repair-counter-store-aggregates
```

This scans `reaction/*` and `poll_vote/*` identity keys, rebuilds
`reaction_count/*` and `poll_option_vote_count/*` aggregate shards, and removes
aggregate shard keys that no longer have surviving identity records. It does
not recreate missing identity, and it does not rebuild `reaction_received/*`
without a verified identity-to-post-author source.

After restoring the bucket, emit or wait for a fresh counter checkpoint from a
worker using the same counter-store backend:

```sh
./budgied \
  -roles worker \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -counter-store nats-kv \
  -counter-store-nats-bucket BUDGIE_COUNTER_STORE \
  -counter-checkpoint-interval=5m
```

Then rebuild affected derived views (`rankings`, `search`, `community`, or
`all`) so materialized reads converge on the restored active counter store.

If only aggregate counts survive through `counter.checkpointed` events but KV
identity keys are lost, the system can recover coarse post-reaction and
poll-option vote counts, but users may lose reaction undo state, poll-vote
identity, and abuse-review evidence. Treat that as data loss and restore a
better KV backup before promotion.

## Counter-Store Shard Repair Gates

Use this section for promotion and counter shard failure drills.

The current bridge already emits durable coarse `counter.checkpointed` snapshots
into `counter_checkpoints`. These checkpoints restore aggregate post-reaction
and poll-option vote counts during event-log replay, but they are not a
replacement for per-user reaction or poll-vote identity storage. Identity rows
still require active-store backup/restore and shard-native recovery before
promotion. The command handlers already use a backend-neutral counter mutation
lifecycle; NATS KV is the first durable distributed adapter behind that
contract and can now rebuild aggregate reaction/poll shards from surviving KV
identity keys. Account hard-delete cleanup also snapshots and removes the
deleted user's active-store reaction and poll-vote identity, decrementing
aggregate shards and received-reaction counters. `MemoryCounterStore` remains a
non-SQL fixture for development and tests, not a durable or multi-node
production counter store.
Enable periodic snapshots on exactly the worker tier with
`-counter-checkpoint-interval=5m`; in Postgres deployments the existing
background-worker leader lock keeps only one node emitting checkpoints.

A full shard drill is not complete until operators can prove:

- every counter shard has an owner, checkpoint position, and replay range
- checkpoint or milestone events can restore coarse aggregate counts without
  pushing reaction and vote churn back through the ordered log
- per-user reaction and poll-vote identity remains available for undo,
  moderation, and abuse review
- aggregate shard rebuild from identity can drain, copy, verify, and roll back
  without double-counting
- a failed shard returns a retryable error or serves a documented stale value
  instead of silently dropping writes

Until identity backup/restore and write-drain gates exist for the deployed
counter backend, treat SQL side-store backup/PITR or JetStream KV backup as the
recovery mechanism for reaction and poll-vote identity.

## Validation

Validate the base SQL state:

```sh
psql "$BUDGIE_POSTGRES_DSN" <<'SQL'
WITH expected AS (
  SELECT p.author_id AS user_id, COUNT(*) AS reactions_recv
    FROM post_reactions pr
    JOIN posts p ON p.id = pr.post_id
   WHERE p.author_id <> ''
     AND p.author_id <> pr.user_id
   GROUP BY p.author_id
),
actual AS (
  SELECT user_id, reactions_recv
    FROM user_activity
   WHERE reactions_recv <> 0
)
SELECT COALESCE(expected.user_id, actual.user_id) AS user_id,
       COALESCE(expected.reactions_recv, 0) AS expected,
       COALESCE(actual.reactions_recv, 0) AS actual
  FROM expected
  FULL OUTER JOIN actual USING (user_id)
 WHERE COALESCE(expected.reactions_recv, 0) <> COALESCE(actual.reactions_recv, 0);
SQL
```

Expected: zero rows.

Validate user-visible reads and async lag:

```sh
curl -fsS "$BUDGIE_API/metrics" > "/tmp/budgie-counters-after-$(date -u +%Y%m%dT%H%M%SZ).prom"

curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "X-Budgie-Min-Seq: $EXPECTED_HEAD_SEQ" \
  "$BUDGIE_API/api/v1/rankings/users"

curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "X-Budgie-Min-Seq: $EXPECTED_HEAD_SEQ" \
  "$BUDGIE_API/api/v1/rankings/threads"
```

Pass criteria:

- sampled posts show expected reaction counts
- sampled polls show expected option vote counts and viewer vote identity
- the `reactions_recv` mismatch query returns zero rows
- selected `budgie_derived_view_lag_events{view=...}` returns to zero or the
  documented steady-state lag budget
- `budgie_outbox_jobs` has no dead repair or stat-history jobs
- reads that require `X-Budgie-Min-Seq` return `X-Budgie-Read-Your-Writes: satisfied`
- `BudgieProjectionLagHigh` clears

## Exit Criteria

- Base side-store counts are restored or the incident explicitly records data
  loss accepted by product and moderation owners.
- Projection rebuild and selected derived-view backfills completed successfully.
- API samples for reactions, polls, user rankings, and thread rankings match
  SQL validation queries.
- `BudgieProjectionLagHigh` and related processor lag alerts are clear for one
  full alert window.
- The incident record includes before/after metrics, SQL validation output,
  rebuild commands, and whether Postgres PITR or replica copy was used.
