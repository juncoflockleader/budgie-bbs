# Design: Internet-Scale Concurrent Writes with Low-Latency Delivery

## Status

Forward-looking design. The current production design (single global writer
serialized by a Postgres advisory lock + LISTEN/NOTIFY wakeups) is documented in
`deployment-multi-node.md` and targets a small cluster (~20 writes/s). This
document describes how to evolve that same event-log/CQRS foundation to sustain
internet-scale concurrent writes while keeping live update delivery low-latency.

## The problem, stated precisely

Two goals that pull against each other:

1. **Sustain a very high rate of concurrent writes** (posts, replies, reactions,
   votes, moderation, DMs) — orders of magnitude beyond one-command-at-a-time.
2. **Deliver updates to connected clients with low latency** (sub-100 ms p95)
   across millions of concurrent connections.

The current ceiling is **not** LISTEN/NOTIFY. It is the **single global write
lock**: every mutating command takes one cluster-wide `pg_advisory_lock`, so the
whole site commits writes strictly one at a time to maintain a single
authoritative total-order event sequence. That global total order is the
expensive thing — and the key realization is that **it is mostly unnecessary**.

## Key insight: partition the order

Users never actually need a single real-time total order of every event on the
site. They need much weaker, *local* guarantees:

| What needs ordering | Scope of order required |
|---|---|
| Replies within a thread | Total order **within the thread** |
| Thread list / "latest" within a board | Total order **within the board** |
| Moderation action vs. the post it targets | Order **within that thread** |
| A user's own reads after their own write | **Causal** (read-your-writes) |
| Reactions / vote counts | **None** — commutative, just converge |
| Global feeds, rankings, search, stats | **None in real time** — eventual is fine |

So: **require total order only within an aggregate, and run one independent
ordered log per aggregate.** Aggregates are independent, so writes to different
aggregates proceed fully in parallel. The natural aggregate boundary in a BBS is
the **board** (with hot threads sub-partitioned — see below). This replaces one
global writer with thousands of parallel per-partition writers.

This is the standard event-sourcing-at-scale move (Kafka-partition / actor-per-
aggregate). It trades a global linearizable order — which nobody needs — for
**linearizable-within-partition, causal+eventual across partitions**, which is
exactly what a forum needs.

## Target architecture

```
                         ┌───────────────────────────────────────┐
   millions of clients   │   EDGE / GATEWAY TIER (stateless)      │
   (WS / SSE / SSH)  ───▶│   holds connections, owns subscriptions│
                         └───────┬───────────────────────▲────────┘
                       produce   │ commands       events │ fanout
                                 ▼                        │
                         ┌───────────────┐        ┌───────┴────────┐
                         │ COMMANDS log  │        │ FANOUT BUS      │
                         │ (partitioned  │        │ (subject-routed,│
                         │  by aggregate)│        │  best-effort)   │
                         └───────┬───────┘        └───────▲────────┘
                       owns part. │                       │ publish
                                 ▼                        │
                         ┌──────────────────────────┐     │
                         │ WRITER / DECIDER TIER     │─────┘
                         │ consumer group; each      │
                         │ instance is the SOLE      │
                         │ processor for its         │
                         │ partitions (validate,     │
                         │ dedup, decide, append)    │
                         └───────┬──────────────────┘
                        append   │
                                 ▼
                         ┌───────────────┐     ┌──────────────────────────┐
                         │ EVENTS log    │────▶│ PROJECTION CONSUMERS      │
                         │ (partitioned, │     │ read models: per-node     │
                         │  authoritative│     │ caches, KV counters,      │
                         │  ordered)     │     │ search index, feeds/rank  │
                         └───────────────┘     └──────────────────────────┘
```

Three substrates, **deliberately separated** (the current design conflates them
in Postgres):

- **Durability + order** = a partitioned, append-optimized log (Kafka /
  Redpanda). Partition key = aggregate (board, or thread for hot ones). Per-
  partition offset *is* the sequence number.
- **Delivery** = a fast subject-routed fanout bus (NATS / Redis) — best-effort,
  not the source of truth.
- **Query** = read models (projections) materialized from the events log.

## Write path (CQRS: command → decision → event)

1. **Gateway** receives a write, authenticates, and **produces to the commands
   log**, partitioned by the aggregate key. It returns a pending/ack to the
   client with the assigned command id. No global lock is taken anywhere.
2. **Writer tier** is a consumer group over the commands log. Partition
   assignment makes **each writer instance the sole processor of its partitions**
   — so processing is serial *within* a partition (correct ordering, simple
   in-memory state) and parallel *across* partitions (throughput). This is the
   single-writer-per-aggregate actor model; the "lock" is just local in-process
   serialization, not a distributed lock.
3. The writer **validates** (permissions, sanctions, content filters),
   **deduplicates** by command id against a fast per-partition store (a local
   RocksDB / state store the owner keeps for its partitions — no shared DB on the
   hot path), **decides** the resulting event(s), and **appends to the events
   log**. Using the broker's transactional/idempotent producer, the
   "consume command + append events + commit offset" step is exactly-once.
4. The events log is the authoritative ordered record. Replaying it rebuilds any
   projection — the invariant the whole system already relies on, preserved.

Per-partition single-writer throughput is high (in-memory append, batched
durable replication): ~10k+ commands/s per partition. Thousands of partitions →
millions/s aggregate, bounded by the broker, not the application.

## What does *not* go through the ordered log

The biggest write-volume source on a forum is **reactions/votes**, which vastly
outnumber posts and are commutative. Sending each like through the ordered log
is wasteful and re-concentrates load on hot aggregates.

- **Reactions / vote counts** → sharded counters / CRDTs (e.g. ScyllaDB counters
  or sharded Redis), incremented directly, periodically checkpointed. They
  converge without ordering. Only the *checkpointed* count (or a coarse
  milestone event) need ever touch the log.
- **Presence / typing / chat** → broker-only, best-effort, never durable
  (already the design today). This keeps high-frequency ephemeral churn out of
  the durable log entirely.

Removing likes + presence from the ordered path removes ~90% of raw write volume
before it ever reaches the partitioned log.

## Delivery path (low-latency fanout at scale)

Separate from durability. Goal: an event in the log reaches every interested
connected client in tens of milliseconds.

- **Gateways are stateless and hold the connections** (~50–100k each; scale
  horizontally to millions). Each connection registers its **subscriptions** —
  the scopes it cares about: `thread:T`, `board:B`, `user:U`.
- A gateway subscribes **upstream** to the union of scopes of its connected
  clients on the **fanout bus** (subject-routed: `events.board.B`,
  `events.thread.T`). It receives each relevant event **once** and fans it out in
  local memory to its matching connections.
- The writer tier (or a dedicated fanout worker consuming the events log)
  **publishes each event to the fanout bus** on its scope subjects. Durable
  delivery is *not* required here — the bus is a wakeup/delivery accelerator.
- **Hot scope** (a viral thread watched by millions across thousands of
  gateways): one event fans out to thousands of gateways (cheap for NATS / a
  fanout tree); each gateway then does the heavy local fan-out to its own
  clients in parallel. The amplification that matters (millions of sockets) is
  parallel in-memory writes, not a central bottleneck.
- **Backpressure / correctness**: per-connection bounded queue; on overflow,
  **drop and let the client resync by cursor** — the exact at-least-once +
  dedup + replay model already in place, now per-partition. Missed live events
  are reconciled from the events log; duplicates are tolerated by offset.

## Cursors and replay at scale

The global monotonic `seq` goes away. A client cursor becomes a **vector of
`(partition → offset)`** for the scopes it follows. Gap detection and replay work
exactly as today, but per partition: on reconnect or detected gap, the client
replays each partition from its last offset. This keeps the "DB/log replay is
authoritative; live delivery is best-effort" contract intact.

## Consistency model

- **Within a thread/board**: linearizable (single partition writer, ordered
  log). Replies, moderation-vs-target, thread ordering are all correct.
- **Read-your-writes** for the author: route the author's subsequent reads to a
  projection that has applied their event (sticky-by-aggregate read routing), or
  echo the new event optimistically to the author's own session (the gateway
  already holds it).
- **Across partitions**: causal + eventual. Cross-board views (global "latest",
  rankings, search, stats) are **asynchronously derived** by stream processors
  that merge partitions — eventually consistent, which is correct for feeds.
- **Idempotency**: per-partition command-id dedup → no double-posts under retry.

## Cross-cutting / global reads

Exactly the things that "needed" global order — handle them as **asynchronous
derived views**, never on the write hot path:

- **Global feeds / "latest across boards" / rankings** → stream processors
  (Kafka Streams / Flink, or custom consumers) merge the events log into
  materialized views in a KV store; refreshed continuously, served from cache.
- **Search** → an external index (OpenSearch / Meilisearch) fed from the events
  log via a connector.
- **Stats / counters** → windowed aggregations over the log (already modeled as
  idempotent snapshot commands today; becomes a stream job).

## Hot-partition handling

A single board or thread going viral re-concentrates writes on one partition.
Mitigations, in order:

1. **A single partition is already fast** — single-writer is in-memory serialize
   + batched durable append, ~10k+/s. Most "hot" is well within one partition.
2. **Sub-partition hot threads**: shard a viral thread's *replies* across N
   sub-partitions keyed by `hash(thread, author)`. Replies then have a coarse
   `(timestamp, tiebreak)` order rather than strict total order — acceptable for
   a 50k-comment thread where exact global reply order is meaningless to readers.
3. **Reactions never hit the ordered path** (see above) — which is where the
   real viral volume is.
4. **Elastic partition reassignment**: the writer consumer group rebalances
   partitions across instances; add writer nodes to spread hot partitions.

## Technology choices (concrete, with alternatives)

| Concern | Primary choice | Alternatives |
|---|---|---|
| Ordered partitioned log | **Redpanda** (low-latency, no ZK) | Apache Kafka, Apache Pulsar |
| Writer-tier state/dedup | Embedded **RocksDB** state stores | Kafka Streams, a per-shard SQLite |
| Fanout / delivery bus | **NATS** (subject routing, fast fanout) | Redis pub/sub (sharded), Kafka-direct |
| Hot counters (reactions) | **ScyllaDB** counters / sharded Redis | Cassandra, Redis CRDTs |
| Queryable projections | **Postgres** read replicas (per region) | CockroachDB, Scylla for hot views |
| Search | **OpenSearch** | Meilisearch, Typesense |
| Stream processing (feeds/rank) | **Kafka Streams / Flink** | custom consumers |
| Partition ownership | **broker consumer-group assignment** | etcd leases, a custom coordinator |

Partition ownership comes "for free" from the broker's consumer-group rebalance
protocol — no separate coordination service needed for the writer tier.

## Capacity model (order-of-magnitude)

- **Partitions**: start at ~4k board partitions → 4k parallel write lanes.
- **Writes**: ~10k commands/s per partition single-writer; aggregate bounded by
  the broker (millions/s) long before the application. Likes diverted to
  counters remove the bulk of volume.
- **Connections**: ~50–100k per gateway × M gateways → millions of concurrent.
- **Delivery latency**: append (acks=quorum, few ms) → fanout publish → gateway
  → client; sub-100 ms p95 on a regional network is achievable.
- **Watch**: per-partition writer lag, broker produce latency, fanout-bus
  delivery lag, gateway send-queue drops, hot-partition skew.

## Migration: evolve, don't rewrite

The current system is *already* event-log + CQRS + projections + a `Bus`
interface + cursor-based replay. The scale design is the same shape with the
single global order relaxed. Phased path:

1. **Decouple delivery from durability now**: introduce the fanout bus (NATS) as
   the `Bus` implementation for live delivery; keep Postgres as the log. Put the
   event body in the fanout message to kill the fetch-by-seq read amplification.
   (Low risk; the `NATSBus` seam already exists.)
2. **Introduce the partitioned events log** behind the existing event API: write
   to per-board logs; keep a compatibility view for global cursors during
   transition. Replace the global advisory lock with per-partition writers.
3. **Move reactions/presence off the ordered log** to counters/broker-only.
4. **Make global views asynchronous** (feeds, rankings, search, stats as stream
   jobs).
5. **Shard projections / add the gateway tier** for connection scale.

Crucially, the **client protocol and the event/cursor model stay stable** — the
cursor generalizes from a scalar `seq` to a per-partition offset vector, but the
welcome/resume/replay handshake is unchanged. SQLite single-node mode remains for
development and small deployments.

## Trade-offs and explicit non-goals

- **We give up global real-time linearizability.** There is no longer a single
  instantaneous total order of all site events. We keep linearizable-within-
  aggregate + causal + eventual-global. This is a deliberate, forum-appropriate
  trade.
- **More moving parts.** A broker, a fanout bus, a counter store, a search index,
  stream jobs, and a gateway tier replace "just Postgres." This is justified only
  at internet scale; below that, the current single-writer Postgres design is
  simpler and correct, and should be kept.
- **Operational complexity and cost** rise substantially. Adopt phase-by-phase,
  gated by real measured ceilings (`budgie_writer_lock_wait_ms` saturation,
  fanout lag), not speculatively.
- **Strong cross-board transactions are not supported** (no global ACID across
  aggregates) — and were never needed.

## Definition of done (scale targets)

- Sustained writes scale ~linearly with partition/writer count; no single global
  serialization point on the write path.
- Reply/thread/board ordering remains correct (linearizable within aggregate).
- Reactions and presence impose no load on the ordered log.
- Live delivery p95 < 100 ms to millions of concurrent connections; hot scopes
  do not bottleneck a central component.
- Reconnect/replay reconciles any missed live events per partition; duplicates
  tolerated.
- Global feeds/rankings/search are eventually consistent and never on the write
  hot path.

---

## Appendix A: Phase 1 sketch — split delivery from durability via NATS

This is a **sketch**, not committed code. Phase 1 keeps Postgres as the
authoritative log and replaces the live cross-node wakeup. Today
`appendEvent` does `bus.Publish` (local) **+ `pg_notify`** (metadata only), and
every node's `pq.Listener` then **fetches the event by `seq`** before
re-publishing locally. Phase 1 sends the **full event body over NATS**, so
receivers deliver from memory — no fetch-by-seq, no DB read on the delivery
path. Durability, the advisory write lock, projections, the cursor-based gap
replay in `wsapi.deliverEvent`, and the client protocol are all untouched.

The only genuinely missing piece is a remote subscriber loop; the rest is wiring
plus gating off the now-redundant Postgres wakeup. `NATSBus` already publishes
outward (`internal/core/nats_bus.go`); it needs a single broadcast subject and
an inbound loop.

```go
// internal/core/nats_bus.go (extended)

// NATSConn is the minimal pub/sub surface core needs. The concrete nats.go
// adapter lives outside core so internal/core keeps no broker dependency.
type NATSConn interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, handler func(data []byte)) (func() error, error) // -> unsubscribe
}

const natsEventSubject = "budgie.events" // single subject in Phase 1

type natsEnvelope struct {
	Node   string          `json:"node"`   // origin node id, for self-skip
	Scopes []string        `json:"scopes"` // not carried in OutboundMessage
	Body   json.RawMessage `json:"body"`   // proto.EventToOutbound(evt) as JSON
}

func (b *NATSBus) Publish(evt *proto.Event) {
	b.local.Publish(evt) // local subscribers on this node
	if b.conn == nil {
		return
	}
	body, _ := json.Marshal(proto.EventToOutbound(evt))
	data, _ := json.Marshal(natsEnvelope{Node: b.nodeID, Scopes: evt.Scopes, Body: body})
	metrics.EventsPublishedRemote.Inc()
	_ = b.conn.Publish(natsEventSubject, data) // ONE publish per event
}

type wireEvent struct {
	Event   proto.EventKind `json:"event"`
	Seq     int64           `json:"seq"`
	ESeq    int64           `json:"eseq"`
	Payload json.RawMessage `json:"payload"`
	TS      int64           `json:"ts"`
}

func (b *NATSBus) onRemote(data []byte) {
	var env natsEnvelope
	if json.Unmarshal(data, &env) != nil || env.Node == b.nodeID {
		return // malformed or self-originated -> skip (origin already delivered locally)
	}
	var w wireEvent
	if json.Unmarshal(env.Body, &w) != nil {
		return
	}
	recordRemoteWakeup(w.TS) // reuse existing metric helper (ingested + lag)
	// LOCAL-ONLY publish — must not re-emit to NATS, or you get a broadcast storm.
	b.local.Publish(&proto.Event{
		Kind: w.Event, Seq: w.Seq, ESeq: w.ESeq, TS: w.TS,
		Payload: w.Payload, // json.RawMessage passes straight through EventToOutbound
		Scopes:  env.Scopes,
	})
}
```

Gate off the redundant Postgres wakeups when NATS owns cross-node delivery
(`crossNodeViaBus` set when a `NATSBus` is injected via a `WithBus` option):

```go
// core.go Run(): NATS handles cross-node delivery, skip the PG listener
if c.pgDSN != "" && !crossNodeViaBus {
	startPGListener(ctx, c.pgDSN, c.nodeID, c.DB, c.Bus)
}

// log.go appendEvent(): skip pg_notify when NATS carries the wakeup
if currentSQLFlavor == postgresFlavor && currentNodeID != "" && !crossNodeViaBus {
	tx.Exec(`SELECT pg_notify($1,$2)`, pgNotifyChannel, notifyPayload)
}
```

The concrete `nats.go` adapter (implementing `NATSConn`) lives in e.g.
`internal/natsconn`, and `cmd/budgied` gains a `-nats <url>` flag that dials it,
builds `core.NewNATSBus(conn, nodeID)`, injects it with `core.WithBus`, and calls
`Start()` to begin consuming.

**Before -> after**

| | Today (LISTEN/NOTIFY) | Phase 1 (NATS) |
|---|---|---|
| Write commits | `pg_notify` metadata `{seq,scopes,node}` | NATS publish **full body** |
| Other nodes | wake -> `SELECT … WHERE seq=?` -> local publish | receive -> reconstruct in memory -> local publish |
| DB reads on delivery path | 1 per node per event | **0** |
| Gap recovery | `wsapi` cursor replay from Postgres | **unchanged** |
| Dedup | by `seq` in `deliverEvent` | **unchanged** (tolerates at-least-once dupes) |

**Why it's safe:** Postgres stays authoritative; NATS is a best-effort
accelerator. A dropped NATS message is reconciled by the existing
`deliverEvent` gap detection -> `core.Replay`. Dedup-by-`seq` already tolerates
duplicate live delivery, so both at-least-once and at-most-once NATS work.
`MemBus` / SQLite single-node mode is untouched (only used when `-nats` is unset).

**Verification:** unit-test `onRemote` (reconstruct + local-only publish, no
re-emit to a fake `NATSConn`; self-skip by `node`); integration via the
two-node `cluster-smoke.sh` against a NATS container, asserting cross-node
delivery and zero `events WHERE seq=` SELECTs on the delivery path; existing
suites stay green with `-nats` unset.

Scope: one new file (`natsconn`), ~80 lines added to `nats_bus.go`, ~4 one-line
gates, and the budgied flag. It removes the read-amplification and cleanly
separates delivery from durability, which every later phase builds on.
