# Known Postgres-path issues (tracked)

The CI `go-postgres` job runs the core + httpapi suites against a real Postgres,
which surfaced pre-existing **Postgres-only** defects (the SQLite suite is green).
Postgres is stricter than SQLite about ambiguous columns and foreign keys, so
these latent bugs only appear on the multi-node deployment path.

Until they are fixed, the `go-postgres` CI job is **non-blocking**
(`continue-on-error`) so it reports without gating; the SQLite and web jobs are
authoritative. **These are real multi-node bugs and should be fixed.**

## Open

1. **Reactions are broken on Postgres** — `pq: column reference "reactions_recv"
   is ambiguous`. The `reactPost` write path runs a query that references
   `reactions_recv` without a table qualifier where it is ambiguous (a join/CTE
   over `user_activity` plus another table that also has the column). Fails every
   reaction on Postgres. Fix: qualify the column.
   - Tests: `TestCommunityRankingsAndStats`, `TestReactionWritesStayOffDurableEventLog`,
     `TestReactionCounterShards*`, `TestCounter*`, `TestHTTPCommunityRankingsAndStats`,
     `TestBoardMemberRequirementsAdmission`.

2. **Automod review insert violates a FK on Postgres** —
   `insert or update on "moderation_reviews" violates foreign key constraint
   "moderation_reviews_reporter_fkey"`. The automod path records a review with
   reporter = the synthetic `"automod"` actor, which is not a real `users` row, so
   the `reporter` FK rejects it on Postgres. SQLite tolerates it. Fix: seed a
   reserved `automod` system user, or make `reporter` nullable / drop the FK for
   system reporters.
   - Test: `TestBoardAutomodExecution`.

3. **Partition-lock semantics differ on Postgres** —
   `TestPostgresPartitionLockAllowsOtherBoardWriteEndToEnd`: a same-partition
   concurrent write returns error code `forbidden` instead of the expected
   `lock_unavailable`. Needs investigation (could be the test's contention setup
   or the advisory-lock contention mapping under Postgres).

## How to reproduce / verify a fix

The test harness provisions a unique Postgres schema per test, so it is safe
against a shared server:

```bash
BUDGIE_TEST_POSTGRES_DSN="postgres://USER:PASS@HOST:5432/DB?sslmode=disable" \
  go test ./internal/core/... ./internal/httpapi/...
```

Once all three are fixed, remove `continue-on-error` from the `go-postgres` job in
`.github/workflows/ci.yml` so the Postgres path is gating again.
