# Case Study: Why Reddit Isn't Real-Time

*A design case study informing our architecture. The question: a company with Reddit's resources could clearly deliver a more real-time experience than it does. It deliberately doesn't. Understanding why — properly, not cynically — sharpens our own decisions.*

---

## The puzzle

Reddit is, at its core, threaded discussion — the same shape as a forum or a BBS. Comments arrive, people reply, threads grow. On a classic term BBS, all of that was pushed to you live over an open connection. Reddit, with vastly more engineering capacity, mostly makes you refresh. The naive reading is that they simply haven't bothered, or that they're withholding a better experience for profit. Both contain a grain of truth and both miss the structural reason. The honest answer is that several distinct pressures all point the same way, and only the shallowest of them is about greed.

It's worth ordering these from the most architecturally fundamental to the most incentive-driven, because the fundamental ones are the ones that actually transfer to our project.

---

## 1. Ranking is incompatible with naive real-time (the deep reason)

This is the one that matters most and gets discussed least.

Our BBS design gets clean server-push almost for free because the log is **append-only and ordered by arrival**. A new event has an obvious position (the end) and an obvious delivery (push it to everyone tailing the log). "New" and "where it goes" are the same fact.

Reddit's core product is the precise opposite. Its feeds and comment threads are sorted by a **score that changes continuously** — "Hot," "Best," "Top," "Controversial" are all functions of vote counts, time decay, and vote distribution, recomputed as votes arrive. The sort key is mutable, and it's mutating constantly.

Now imagine pushing every change in real time. As votes trickle in, the page **reorders itself underneath the reader's cursor.** The comment you're halfway through reading jumps three positions. A reply you were about to upvote slides off-screen. This isn't a delightful live experience — it's disorienting bordering on unusable. The moment your sort key is a score rather than arrival order, naive push becomes *actively worse* UX, not better.

So Reddit isn't failing to do the thing a BBS does. The thing a BBS does **doesn't apply** to Reddit's data model. They've effectively chosen a *read-time* sort: ranking is computed when you open the thread and held stable while you read, and seeing the re-ranked state requires an explicit refresh. The refresh isn't laziness — it's the seam where a mutable global ordering is allowed to change without yanking the page around.

**The transferable lesson:** append-only real-time is easy; ranked real-time is genuinely hard. We get clean push *because* we chose chronological, immutable ordering. The day we add vote-sorting to a thread, we inherit Reddit's exact problem — and we'll have to choose the same way they did (freeze the order on load, show a "12 new replies — refresh to re-rank" affordance) or build something considerably more sophisticated.

---

## 2. Real-time is most expensive exactly where it would feel best (scale economics)

A persistent connection per reader is cheap on a small board and punishing at Reddit's scale.

Consider the pathological case: a breaking-news megathread with hundreds of thousands of concurrent readers. Under WebSocket push, a single new comment becomes one event multiplied across every live socket — and the hottest threads, the ones where live updates would feel most alive, are precisely the ones that would melt the fan-out layer. Real-time's cost scales with exactly the thing that makes it desirable: audience size on a single hot object.

Polling inverts this economically. A `GET` for a thread snapshot is **trivially cacheable** — a CDN can serve the same slightly-stale snapshot to a million people from cache and never trouble the origin. Here staleness is not a defect; it's the *enabler*. "A few seconds out of date" is what lets one cached response satisfy enormous read fan-out for almost nothing.

So the cheap-at-scale path and the real-time path diverge hard, and at Reddit's scale the cheap path wins by default on the largest objects.

**The transferable lesson:** our single-writer, fan-out-to-every-subscriber core is correct for a small-to-medium board and is the *same* architecture that gets expensive on a megathread. That's not a reason to change anything now — premature scale-optimization is its own trap, and we have ample headroom to tune later — but it's a known fork: very-large-audience threads can later be tiered onto a cacheable snapshot path while small threads stay fully live, all without changing the data model (see the protocol's transport ladder, which already degrades along this axis).

---

## 3. Mobile changes the cost-benefit (battery and flaky links)

The majority of Reddit's traffic is the phone app, and a held-open socket on mobile carries real costs: battery drain from keeping the radio warm, reconnection storms when connectivity flaps between cell and wifi, and OS-imposed limits on background connections.

For a genuinely high-frequency feed, a socket can actually be *more* battery-efficient than repeated polling (no repeated radio wake-ups). But that's not the typical Reddit session. The typical session is "open app, read three things, lock phone." For that usage shape, a persistent live connection is overhead in service of liveness the user wasn't asking for. Poll-on-foreground fits the behavior better.

**The transferable lesson:** liveness should match how a surface is actually used. Our chat and presence surfaces genuinely want a socket; a user skimming yesterday's threads on a phone does not. The protocol should let a client choose its liveness tier per session, not impose one globally.

---

## 4. Engagement incentives remove the pressure to fight the hard problems (the amplifier, not the root)

Only now do we reach the cynical explanation — and the point of putting it last is that it's an *amplifier* of the first three, not the root cause.

Real-time push optimizes for **resolution**: you see the new reply, you're caught up, you leave. Pull-with-refresh optimizes for **return**: you come back, you reload, the ranking algorithm gets another chance to surface something sticky, you scroll. An ad-supported attention business is structurally biased toward the second pattern, and that bias is real.

But notice what the bias actually does. It doesn't *create* the non-real-time design — the ranking incompatibility and scale economics already push hard in that direction. What the engagement incentive does is **remove any motivation to spend engineering effort fighting upstream** to deliver a resolving, real-time experience that would partly conflict with the business model anyway. The greed story alone makes Reddit sound like it's withholding something easy. The fuller picture: it's a hard problem that conflicts with their product, *and* they have no incentive to solve it. Both clauses matter.

---

## What Reddit does ship in real-time — and why it's revealing

Reddit is not uniformly non-live. It pushes real-time at the edges: live comment-count bumps, "live" event threads, chat, notification badges. The pattern is the tell. Real-time appears on the **append-y, unranked** surfaces — a count going up, a chat line arriving, a new-message badge — and stays away from the **ranked, reorderable** core.

That seam is exactly the one in our own design: the durable-vs-ephemeral event split. The surfaces Reddit makes live are the surfaces our architecture also treats as naturally pushable. Reddit didn't avoid real-time because it's hard in general; it avoided it where it collides with ranking and scale, and embraced it where it doesn't. That's a coherent, defensible engineering position — and it happens to validate the line we've already drawn.

---

## Conclusion: what this means for us

1. **Our clean push story is contingent on chronological ordering.** It is one of the real reasons BBSes felt fast that has nothing to do with terminals — append-ordered message bases meant "new" always meant "at the end." Guard that property. If ranking ever enters, treat it as a deliberate, contained decision with its own UX answer, not a default.
2. **Liveness is a per-surface, per-session choice, not a global mode.** Chat and presence want a socket; archival reading does not; a phone on a flaky link wants graceful degradation. The protocol is built to span that range (the transport ladder), which is what lets us avoid Reddit's all-or-nothing posture.
3. **The scale fork is real but not now.** We have headroom. The thing to preserve is *optionality*: keep the data model transport-agnostic so that tiering hot threads onto a cacheable path later is a routing change, not a rewrite.

Reddit's choice was the right one for Reddit. The instructive part is *which* pressures drove it — because we've made the opposite choice on the opposite data model, and knowing exactly why theirs diverged tells us precisely where ours would start to bend.
