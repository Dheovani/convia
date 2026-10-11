# Redis unavailable

**Alert:** `the presence store could not be reached` or `a shared budget is counted by this instance alone until Redis answers` in the log, or `convia.redis.command.duration` with errors.

**What happened:** the Redis every instance shares stopped answering — it is down, failing over, or on the other side of a partition. Convia does not store anything there that it cannot do without, so this is a narrow failure, and this page is mostly about how narrow.

## What still works, and what does not

| | while Redis is away | when it is back |
| --- | --- | --- |
| Rooms, messages, calls, participants, invitations, webhooks, sign-in | **Unaffected.** All of it is PostgreSQL. | — |
| Presence | Every request answered `503` with `Retry-After: 5`, at once. Nothing is reported as `offline`, because that would be a false statement about people. | Answers again within about a second. After a restart that lost Redis's memory, everybody reads as offline until their next heartbeat — within 25 seconds for Convia's own app. |
| Sign-in and registration budgets | Counted by each instance alone, so *n* instances allow *n* times the guesses for the length of the outage. | Counted together again. After a restart, every budget starts full. |
| Events on another instance's streams | Still delivered, from the journal, within 200 ms instead of at once. | At once again. |
| Presence changes on another instance's streams | **Lost**: they travel only over Redis, and pub/sub stores nothing. Subscribers on the instance that produced them still receive them. | Delivered again from the next change. |
| Readiness | Unaffected, deliberately: taking every instance out of the load balancer over presence would turn a narrow failure into an outage. | — |

**A Redis that hangs costs one short wait a second, not one a request.** After a command fails to get an answer, the presence store and the budgets stop asking Redis for a second and answer at once with what they would have answered anyway, then let one request find out whether it is back. A budget waits at most 100 ms for Redis; presence waits `CONVIA_REDIS_TIMEOUT`. See [`internal/circuit`](../../internal/circuit/circuit.go) and [`docs/load.md`](../load.md#when-redis-goes-away) for what an outage measured like before and after.

**Expect a burst of errors as it begins, then one a second.** The requests already waiting on Redis when it stopped answering all fail together — 161 in one second on one instance, in the run with Redis stopped — and after that only the one request a second sent to find out whether it is back. A steady stream of them means Redis is still away, not that Convia is getting worse.

## What to do

1. **Confirm it is Redis and not the network to it.** From an instance's host, `redis-cli -u "$CONVIA_REDIS_URL" ping`. An answer here while Convia logs failures is a network or credential problem between them.
2. **Bring Redis back.** Convia needs nothing done on its side: it reconnects, resubscribes and asks again by itself. Do not restart Convia's instances for this — it changes nothing, and it closes every open event stream.
3. **If Redis came back empty**, nothing needs restoring. Presence fills again from heartbeats and the budgets from use. Redis is never a source of truth here ([ADR 0005](../adr/0005-redis-for-what-instances-tell-each-other.md)), which is why it needs no backup.
4. **If the outage was long**, look at sign-in failures by address for its length: that is the window in which each instance counted guesses alone.
