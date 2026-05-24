# Ranking & Compaction — Raw Ideas

> **Status: exploratory. Not a decision record.** This is a notebook of possibilities and the tradeoffs each one drags along. Nothing here is chosen. The point is to capture the playground while it's fresh, so that when the core exists and you're building hands-on, the option space is already mapped. Every "could / one option / another angle" below is deliberately uncommitted. Resolve them later, with code in front of you.

---

## The lens that organizes all of it

Once the log is the source of truth and projections are folds over it, **ranking stops being a property of a query and becomes a question of how and when you compact.**

Delivery stays honestly chronological — the live tail is append-ordered, so push is clean and there's no reorder-under-cursor problem. Importance is derived *separately*, by passes that fold the log into ranked projections on their own cadence. The two are decoupled. That decoupling is the thing that's only available to us because events (delivery) and projections (state) were split from the start. Everything below is a variation on "what happens in that derived layer."

---

## A. Folds & projections — many rankings from one log

- The log is chronological; "hot," "best," "new," "controversial," "rising" are all just *different folds* over the same events. None of them is privileged or primary. You could run any number simultaneously.
- **Personalized ranking** as a parameterized fold: the same fold, but seeded with this user's vote history / follows / muted authors. Per-user projections are expensive to materialize for everyone, cheap to compute lazily for the active few — a knob, not a fixed answer.
- **Decay / gravity** (the HN-style "score / (age^gravity)" family) wants the current time as an input. That's a tension worth feeling out: a fold that reads `now` isn't reproducible from the log alone. One way to keep it honest is to parameterize the fold by an explicit `as_of` timestamp, so it's a pure function of `(log, as_of)` rather than `(log, wall clock)`. Reproducibility vs. convenience — unresolved on purpose.

## B. Compaction cadence — when does the fold run?

The same fold feels completely different depending on *when* it runs. Options, none chosen:

- **Continuous** — refold on every relevant event. Freshest ranking; constant recompute; the ranked view churns.
- **Windowed** — refold every N seconds / N events. Cheaper; ranking lags reality by up to a window.
- **On-read (lazy)** — refold when someone asks. No wasted work on cold content; the first reader after a quiet spell pays the bill.
- **Debounced / activity-scaled** — a post being hammered recompacts often; a cold post rarely. Probably the most interesting middle, and the most bookkeeping: it needs a notion of "hot."
- Sub-problem that falls out of the last one: **how do you detect hot?** Counting recent vote events in a sliding window, an exponential moving average of activity, a cheap counter that decays — each is its own little tradeoff between accuracy and overhead.

## C. Segmentation & tiering — cold vs. hot parts of the log

This is where compaction meets the scale fork from the Reddit study.

- Old log segments are immutable and cold — natural candidates to **compact into a dense snapshot** ("everything up to seq N, folded") so rebuilding state doesn't mean replaying millions of events. The recent tail stays raw for exact replay and live tailing. The open question is *where you cut*: the snapshot boundary decides how far back cheap exact replay/audit reaches, and re-cutting later is real work.
- **The LSM-tree analogy is worth stealing wholesale.** The whole structure is basically a log-structured merge tree: the live tail is the memtable, compacted segments are SSTables, and a read merges levels. Leveled compaction, tiered compaction, read-time merge of (cold snapshot + warm deltas + hot tail) — all the RocksDB/LevelDB mental furniture transfers. Treating the forum's storage as "an LSM whose values are projections" might be the single most generative framing here.
- **Megathread tiering** is the same idea applied to the hot end: a thread with a huge live audience gets aggressively snapshotted onto a cacheable path while small threads stay fully live — no data-model change, just a routing/compaction policy. Marks the spot where this design answers the scale problem without becoming a different system.

## D. Compacting the votes themselves

- A post with 10,000 votes is 10,000 `post.voted` events. Once cold, those could **fold into an aggregate** (a count, maybe a histogram) so the log doesn't carry them all forever.
- The tradeoff is sharp and worth not glossing: aggregating away individual vote events costs you the ability to *change* a vote cleanly, and costs audit granularity ("who voted what"). One option is to move per-user vote detail to a side index while the main log keeps only the aggregate — splitting "the count" (hot, cheap) from "the receipts" (cold, archival).

## E. "Merge" — two different senses, both rich

The word does double duty.

- **Log-merge (federation / sharding).** The moment there are two independently-sequenced logs, ranking has to interleave them. `seq` is authoritative *within* a log but meaningless *across* logs. So cross-log ordering needs something else — logical clocks, hybrid logical clocks, or simply accepting eventual convergence. This is the deepest tradeoff in the doc: strict cross-log order is expensive and chatty; relaxed order is cheap but lets two instances briefly disagree on ranking. Most federated systems quietly land on "relaxed, converges eventually" — interesting to know that's the gravity well, without deciding to fall into it.
- **CRDT counters** are the natural-feeling tool for the federated-vote-count case specifically: vote tallies are basically grow-only / PN-counters, which converge without coordination. Worth a look as a borrowable primitive before inventing anything.
- **Structural merge (content-level).** Totally separate meaning: merging duplicate or crossposted threads, or folding two threads into one. That's a *domain* event (`thread.merged`) with its own projection semantics — how do replies, votes, and cursors of the merged-away thread get re-homed? A different but equally fun rabbit hole.

## F. Ranking as its own log

- A spicy inversion: make the *ranked view* itself an append-only derived log — a "ranking log" produced by folding the content log. Then even ranking changes are replayable and streamable.
- That opens a clean client choice: clients that want stable order ignore the ranking log; clients that want live re-rank subscribe to `rank.changed` events and animate the reshuffle deliberately. Live re-rank becomes *opt-in per client*, which is exactly the escape from Reddit's all-or-nothing posture — without forcing it on the chronological tail.

## G. Semantic compaction — the fold isn't always a dumb sum

- Moderation already makes deletion and edits into events, so compaction has to *honor* them: a redacted post's votes probably shouldn't accrue; an edited post might reset, preserve, or re-baseline its score.
- The tension: the more semantics the fold respects, the less it's a pure additive fold and the more it's a small rules engine. Powerful — and exactly where subtle ranking bugs would hide. Worth holding lightly as a "how smart should the fold be?" dial rather than a fixed point.

## H. A hypothesis worth pressure-testing (still not a decision)

One property keeps recurring across the ideas above, and it might be worth *trying to preserve* — stated as a hypothesis to poke at, not a rule to obey:

> *What if compaction were always a pure function of the log up to some `seq` (plus explicit parameters like `as_of`), producing a projection, with no hidden state?*

If that held, a few nice things would follow more or less for free: every ranking would be reproducible and auditable ("why is this ranked here?" → replay), and you could run *competing* ranking algorithms side by side over the same log — A/B a "hot" fold against a "best" fold — without forking the data. The moment compaction depends on mutable state outside the log, those properties evaporate.

It's tempting to elevate this to a law. Resisting that for now: it's a hypothesis that might cost too much in some cadence/decay scenarios (see A and B), and the right call is to feel out where it pays for itself and where it doesn't, while building.

---

## Things to try (not a roadmap — a play list)

- Implement one trivial fold (`new`, pure chronological) and one scored fold (`hot`) over the same log and watch them diverge.
- Try the same scored fold at three cadences (continuous, windowed, on-read) and feel the difference in churn and freshness.
- Prototype the read-time merge of (cold snapshot + hot tail) and measure rebuild cost vs. full replay.
- Sketch a `rank.changed` stream and a client that opts into live re-rank — see whether the reshuffle is delightful or nauseating, and at what cadence the answer flips.
- Toy with a PN-counter for a vote tally and merge two of them, just to get the CRDT feel before federation is real.
- Push on hypothesis H deliberately: find a ranking you *want* that can't be a pure fold, and see what it would actually cost to keep it pure anyway.

Nothing here commits the design. It's the terrain. Pick a path when you're standing on it.
