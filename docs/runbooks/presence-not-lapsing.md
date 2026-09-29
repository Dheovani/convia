# Presence not lapsing

**Alert:** `convia.presence.overdue` climbing over fifteen minutes.

**What happened:** claims are passing their deadline faster than the sweeper is clearing them.

## What it does and does not break

**No answer is wrong.** Every read of presence already ignores a claim past its deadline, so nobody is reported as present who is not. That is worth being sure of before doing anything urgent.

**What is missing is the telling.** Expiry is a timer, and a timer tells nobody. The sweeper is what turns a lapsed claim into an event, so while it is behind, a subscriber that saw somebody arrive never sees them leave — unless they left on purpose, which is the case that does not need announcing because the application did it.

So the symptom people report is **somebody who looks online long after they closed the app**, in a client that is drawing from the stream rather than re-reading.

## Find out why

Three causes, in the order they are likely:

1. **The sweeper is not running.** It is a goroutine in every instance and its lifetime is the process. If every instance is passing its own liveness checks, it is running — look for `presence that lapsed could not be announced` in the logs, which is what a failing pass writes.
2. **The store is slow or unreachable.** Claiming an expired entry is a write, and it is exclusive across the deployment. Check `convia.redis.command.duration` and `convia.redis.pool.waits{outcome="timed_out"}` for the `presence` pool; if those are unhappy, this is a symptom rather than the problem, and [`failing-requests.md`](failing-requests.md) is the runbook.
3. **There is more than a pass can take.** Each pass claims a bounded batch. A deployment that has grown past what one batch a tick can clear will have an `overdue` that climbs steadily rather than in steps — which is the shape to look for.

## What to do about the third

It is the only one that is a decision rather than a repair, and the decision is not to make the batch enormous: a larger batch is a longer write under an exclusive claim, which makes every instance's pass slower. If a deployment genuinely produces more lapses than a pass can take, the sweep interval is the dial, not the batch.

## What not to do

**Do not restart instances to clear it.** The claims are in the shared store, not in a process, so a restart clears nothing and loses the in-flight passes.

## Related

- [`../presence.md`](../presence.md) — what a claim is and how expiry works.
- [ADR 0006](../adr/0006-presence-is-a-claim-with-a-timer.md) — why presence is a claim with a timer rather than a state.
- [`../observability.md`](../observability.md) — the instruments this alert is written over.
