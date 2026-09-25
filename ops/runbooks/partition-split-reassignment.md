# Partition Split And Reassignment Runbook

Use this runbook when a hot board/thread/command partition needs more write
capacity, lower lag, or a safer rollback path. Prefer reassignment when a
single writer is overloaded but the partition shape is still correct. Prefer a
hot-thread reply split when one thread's reply traffic is the bottleneck.

## Signals

Start with these metrics:

```sh
curl -fsS "$BUDGIE_API/metrics" > "/tmp/budgie-partitions-$(date -u +%Y%m%dT%H%M%SZ).prom"
grep -E 'budgie_hot_partition_candidate|budgie_hot_thread_split_shards|budgie_command_partition_lag|budgie_writer_partition_lock_wait|budgie_command_partition_assigned|budgie_command_log_assignment_losses_total' \
  /tmp/budgie-partitions-*.prom
```

Interpretation:

- `budgie_hot_partition_candidate{signal="command_lag"}`: writer is not
  draining the command-log partition fast enough.
- `budgie_hot_partition_candidate{signal="command_count"}`: command rate is
  high enough to watch or pre-split.
- `budgie_hot_partition_candidate{signal="writer_lock_wait_ms_max"}`:
  synchronous SQL lock contention is high for that partition.
- `budgie_hot_partition_candidate{signal="gateway_subscribers"}` or
  `gateway_drops`: fanout pressure may justify gateway capacity before writer
  changes.
- `budgie_command_partition_assigned*` and
  `budgie_command_partition_assignment_generation`: writer ownership and
  rebalance state.
- `budgie_command_log_assignment_losses_total`: a writer stopped because it
  lost ownership; expected during planned rebalances, suspicious otherwise.
- `budgie_hot_thread_split_shards{thread_id=...}`: active split configuration
  visible on this node; compare it across gateways and writers before changing
  or rolling back a split.

## Preflight

1. Confirm every public command-submitting node is using the same
   `-command-log-authoritative` mode and the same intended
   `-hot-thread-splits` local overrides, if any.
2. Confirm every writer in the group uses the same
   `-command-log-worker-group-members`.
3. Check for local split overrides. A startup `-hot-thread-splits` flag wins
   over the persisted admin setting for the same thread, so remove local flags
   before relying on cluster-wide admin updates.
4. Capture current split state:

```sh
curl -fsS -H "Authorization: Bearer $BUDGIE_ADMIN_TOKEN" \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits" | tee /tmp/budgie-hot-thread-splits-before.json
```

5. Capture writer assignment metrics:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E 'budgie_command_partition_assigned|budgie_command_partition_assignment_generation|budgie_command_partition_lag|budgie_hot_thread_split_shards' |
  tee /tmp/budgie-command-assignment-before.prom
```

## Decision: Split Or Reassign

Use **reassignment** when:

- one writer is overloaded but the hot partition is already narrow enough
- you have a spare writer such as `writer-hot`
- command-log ownership is `hash-assignment` or `nats-kv`
- the goal is a reversible ownership move

Use **hot-thread split** when:

- the hot key is a single thread reply stream
- reply writes dominate the partition
- preserving thread read presentation is enough and reply write ordering can be
  distributed across `thread/<id>#reply-N` command-log subpartitions

Use **both** when a hot thread needs split subpartitions and one subpartition
still needs a dedicated writer.

## Hot-Thread Split

Create or increase a split:

```sh
curl -fsS -X PUT \
  -H "Authorization: Bearer $BUDGIE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"shards":4}' \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/${THREAD_ID}" |
  tee /tmp/budgie-hot-thread-split-after.json
```

Expected:

- response includes `"persistent": true`
- new appends route to `thread/${THREAD_ID}#reply-N`
- every gateway and writer reports
  `budgie_hot_thread_split_shards{thread_id="${THREAD_ID}"} 4`
- posts still materialize into the original thread in `created_seq` order
- `budgie_command_partition_lag{kind="thread",key=~"${THREAD_ID}(#reply-.*)?"}`
  drains under normal load

If the API returns `409 conflict` with `blockingPartitions`, wait for these
partitions to drain before retrying:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep "budgie_command_partition_lag" |
  grep "$THREAD_ID"
```

Emergency bypass:

```sh
curl -fsS -X PUT \
  -H "Authorization: Bearer $BUDGIE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"shards":4,"force":true}' \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/${THREAD_ID}"
```

Use `force` only when the incident commander accepts replay/validation risk for
still-queued base or old-subpartition commands.

## Reassign A Hot Command Partition

Add an override to every writer in the assignment group:

```sh
budgied \
  -roles writer \
  -storage postgres \
  -postgres-dsn "$BUDGIE_POSTGRES_DSN" \
  -nats "$BUDGIE_NATS_URL" \
  -command-log-worker nats \
  -command-log-worker-ownership nats-kv \
  -command-log-worker-id writer-hot \
  -command-log-worker-group-members writer-a,writer-b,writer-hot \
  -command-log-worker-assignment-overrides "thread/${THREAD_ID}#reply-0=writer-hot"
```

Rules:

- the override owner must be listed in `-command-log-worker-group-members`
- every writer must use the same override map during the rebalance
- in `nats-kv` mode, changing the map bumps assignment generation
- workers that lose ownership stop before committing more offsets

Validation:

```sh
curl -fsS "$BUDGIE_API/metrics" |
  grep -E "budgie_command_partition_assigned|budgie_command_partition_assignment_generation|budgie_command_partition_lag" |
  grep "$THREAD_ID"
```

Expected:

- `budgie_command_partition_assignment_generation` increases
- previous owner may increment `budgie_command_log_assignment_losses_total`
- `budgie_command_partition_assigned{owner_id="writer-hot"}` is `1` for the
  target partition
- `budgie_command_partition_lag` falls on the target partition

## Roll Back

Remove a hot-thread split:

```sh
curl -fsS -X DELETE \
  -H "Authorization: Bearer $BUDGIE_ADMIN_TOKEN" \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/${THREAD_ID}"
```

If rollback returns `blockingPartitions`, wait for the listed base and reply
subpartitions to drain. Use `?force=1` only for emergency rollback:

```sh
curl -fsS -X DELETE \
  -H "Authorization: Bearer $BUDGIE_ADMIN_TOKEN" \
  "$BUDGIE_API/api/v1/admin/hot-thread-splits/${THREAD_ID}?force=1"
```

Remove a reassignment by deleting the `kind/key=writer` entry from
`-command-log-worker-assignment-overrides` on every writer and restarting or
rolling the writer group. In `nats-kv`, the shared assignment generation should
increase and ownership should return to the hash-selected writer.

After split rollback, confirm every gateway and writer has stopped reporting
`budgie_hot_thread_split_shards{thread_id="${THREAD_ID}"}` before treating the
base thread partition as the only reply-write target.

## Exit Criteria

The operation is complete only when:

- every node that can submit or drain replies reports the same
  `budgie_hot_thread_split_shards` value for the target thread, or no sample
  after rollback
- `budgie_command_partition_lag{kind="thread",key=~"${THREAD_ID}(#reply-.*)?"}`
  is within the documented steady-state budget
- `BudgieCommandLogWriterLagHigh` and `BudgieHotPartitionCandidate` are clear or
  have a follow-up capacity owner
- no unexpected `budgie_command_log_assignment_losses_total` increases continue
  after the planned rebalance window
- test writes to the affected thread materialize once and in stable read order
- regional reads with `X-Budgie-Min-Seq` return
  `X-Budgie-Read-Your-Writes: satisfied`

Attach before/after split JSON, metrics, command output, and incident notes to
the change record.
