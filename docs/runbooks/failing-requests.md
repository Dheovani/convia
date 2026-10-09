# Failing or slow requests

**Alerts:** the `5xx` share above 1% for five minutes; `convia.redis.pool.waits{outcome="timed_out"}` or `convia.database.pool.waits` rising; join latency above a second for ten minutes.

Three conditions and one runbook, because they are usually the same incident seen from three places.

## First, establish which it is

**A `5xx` is Convia failing. A `4xx` is Convia working.** A refused credential, a room somebody is not in, a rate limit — those are correct answers, and an investigation that starts by counting them starts in the wrong place. The request histogram is cut by status; look only at the `5xx` share.

Then read the dashboard's rows in order, which is the order they were put in:

1. **Is it up?** If the `5xx` share is the only thing moving, this is a failure. If `http.server.active_requests` is climbing too, Convia is behind rather than broken, and the cause is below rather than in a handler.
2. **Is it fast?** The 95th percentile cut by `http.route` says whether this is everywhere or one route. One route is a handler or a query; everywhere is a dependency.
3. **What is it carrying?** A latency rise means one thing at ten calls and another at a thousand. Check this before concluding anything from the first two.
4. **What is underneath?** This is where the answer usually is.

## Is it Convia or is it Redis

`convia.redis.command.duration` and `convia.redis.pool.waits`, both cut by `pool`, are there so nobody has to guess.

**Waits timing out is the one to act on.** It means Convia asked for a connection and there was none — and the symptom is a slow handler with nothing in its own timings to explain it, which is the most misleading shape an incident takes. If that is rising, the pool or the Redis is the incident and everything above is a consequence.

`presence` and `events` are counted apart because they point at the same Redis: which one is slow is the useful distinction, and the address cannot say it.

## Is it the database

`convia.database.pool.waits` is the one to act on, and it is the same signal as the Redis one above for the same reason: a pool that has handed out every connection makes the next query queue, and the symptom is a slow handler with nothing in its own timings to explain it.

Read it beside `convia.database.pool.limit`. **Ten connections in use means nothing on its own** — it is a healthy pool of fifty and an exhausted one of twelve, and the count without the ceiling cannot tell them apart. `convia.database.pool.wait.duration` says what the waiting cost: a thousand waits of a microsecond is a pool running warm, and ten waits of a second each is ten requests somebody noticed.

Beneath that is a span per query inside the trace of the request that made it: open a slow trace and the query is in it, named `postgresql select` and so on, with the statement on the span. **The metrics say whether requests are slow and the traces say why this one was** — a trace is one occurrence, and a pool filling up is a distribution.

**The statement is there and the arguments are not.** If the statement alone is not enough to identify which call site it was, the trace's parent span says which route it was serving.

## Joining a call specifically

The three routes are `POST /v1/participants/{participant_id}/session`, `POST /v1/me/rooms/{room_id}/call/join` and `POST /v1/peer/rooms/{room_id}/call/join`.

These reach the media plane, which is the one dependency with no metrics of its own. A trace of a slow join shows the outbound span named `media.request`; if that is where the time is, the media plane is the incident and Convia is waiting correctly.

**A visitor's join goes further than a local one.** It crosses to another installation, so a slow `peer/...` join with a fast local one is somebody else's deployment rather than this one.

## If it is none of these

Turn `CONVIA_LOG_LEVEL` to `debug`, reproduce, and **turn it back**. Those lines name identifiers and the shape of what people are doing, and a deployment that leaves the level on has built a detailed record of its people by accident. [`../observability.md`](../observability.md) says why.

## Related

- [`../observability.md`](../observability.md) — every instrument named here.
- [`subscribers-falling-behind.md`](subscribers-falling-behind.md) — the one incident that hides from all of this.
