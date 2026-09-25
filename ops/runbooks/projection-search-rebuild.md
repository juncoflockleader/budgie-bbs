# Projection And Search Rebuild Runbook

Use this runbook when a derived projection, search index, ranking table,
summary table, or feed processor is stale, corrupt, or being promoted to a new
processor owner. The durable event log remains the source of truth.

## Derived View Groups

`budgied -backfill-derived-views` accepts individual view names, comma-separated
lists, `all`, and these operational groups:

| Group | Views |
|-------|-------|
| `search` | `search.posts`, `search.digest` |
| `rankings` | `rankings.boards`, `rankings.threads`, `rankings.replies`, `rankings.users`, `rankings.blessings`, `rankings.archives` |
| `summaries` | `summaries.boards`, `summaries.unread_threads` |
| `community` | `community_stats`, `community_stat_history` |
| `feeds` | `feeds.latest`, `feeds.resident` |

Prefer the smallest group that repairs the symptom. Use `all` only after
restore, schema migration, or broad projection loss.

## Preflight

1. Identify the stale view:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep 'budgie_derived_view_lag_events'
```

2. Capture the durable head and lag before repair:

```sh
curl -fsS "$BUDGIE_API/metrics" > "/tmp/budgie-projection-before-$(date -u +%Y%m%dT%H%M%SZ).prom"
```

3. Check that no conflicting processor owner is running for the selected view.
If a dedicated processor owns a view, keep the processor enabled and use
backfill for repair; do not also run `-derived-view-watermarks` for that view.

4. Choose the rebuild source:

- `sql`: normal source, reads from the SQL durable event log
- `nats`: broker-shadow source, requires a populated JetStream event-log stream
  and `-event-log-shadow-nats-stream`

## Repair From SQL Event Log

Repair only post and digest search:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views search
```

Repair an external Meilisearch post index after index deletion or corruption:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -post-search-index meilisearch \
  -meilisearch-url "$BUDGIE_MEILISEARCH_URL" \
  -meilisearch-api-key "$BUDGIE_MEILISEARCH_API_KEY" \
  -meilisearch-index budgie_posts \
  -backfill-derived-views search.posts
```

This clears the configured external post-search index, rebuilds projections
from the durable event log, repopulates current non-redacted post documents, and
then advances the `search.posts` watermark. The SQL event log remains the source
of truth; Meilisearch stores only searchable candidate metadata.

Repair all ranking views:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views rankings
```

Repair summaries and feed views after a board-membership policy incident:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views summaries,feeds
```

For point repairs after a known durable sequence, use `-rebuild-from-seq`:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views search \
  -rebuild-from-seq 12345
```

## Repair From NATS Event-Log Shadow

Use this only after event-log shadow parity is green and the stream is known to
contain the same event history as SQL.

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -backfill-derived-views search \
  -rebuild-source nats \
  -event-log-shadow-nats-stream BUDGIE_EVENT_LOG
```

The NATS rebuild path must fail closed if the stream is missing, not direct
readable, or not configured with `budgie.eventlog.>` subjects.

## Repair From Kafka Event Log

Use this only after Kafka event-log promotion readiness is green and the topic
is known to contain the same event history as SQL.

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -backfill-derived-views search \
  -rebuild-source kafka \
  -kafka-brokers "$BUDGIE_KAFKA_BROKERS" \
  -kafka-event-topic budgie.events \
  -kafka-event-partitions 32
```

The Kafka rebuild path uses the SQL event-position catalog to list logical
event partitions and fails closed when broker runtime config or event partition
count is missing.

## Validation

After repair:

```sh
curl -fsS "$BUDGIE_API/metrics" > "/tmp/budgie-projection-after-$(date -u +%Y%m%dT%H%M%SZ).prom"
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  -H "X-Budgie-Min-Seq: $EXPECTED_HEAD_SEQ" \
  "$BUDGIE_API/api/v1/rankings/boards"
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  "$BUDGIE_API/api/v1/search?q=probe"
curl -fsS -H "Authorization: Bearer $BUDGIE_USER_TOKEN" \
  "$BUDGIE_API/api/v1/digest/search?q=probe"
```

Pass criteria:

- selected `budgie_derived_view_lag_events{view=...}` returns to zero or the
  documented steady-state lag budget
- reads that require `X-Budgie-Min-Seq` return `X-Budgie-Read-Your-Writes:
  satisfied`
- search and digest search return expected probe results
- `BudgieProjectionLagHigh` clears
- no dedicated processor logs replay errors after the repair

## Rollback And Escalation

The backfill command advances watermarks only after the selected projection
source has been rebuilt. If validation fails:

1. Stop the owning processor for the affected view.
2. Run the same backfill from the SQL source without `-rebuild-from-seq`.
3. If SQL repair still fails, run the full projection rebuild:

```sh
./budgied \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -rebuild-projections
```

4. Restart only one owner for each async processor.
5. Attach before/after metrics, command output, and processor logs to the
   incident record.
