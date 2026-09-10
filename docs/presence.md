# Presence

**Presence is advisory.** It is the one thing Convia holds that is not a record.

Everything else Convia exposes happened: a call started, somebody joined, an invitation was redeemed. Presence is a claim with an expiry on it, and the honest description of what it is worth is *this was true a moment ago*. Show it in an interface. Do not authorize on it, reconcile from it, or bill against it.

The domain lives in [`internal/presence`](../internal/presence), the endpoints are in [`api/openapi.yaml`](../api/openapi.yaml), and the design decisions are in [ADR 0006](adr/0006-presence-is-a-claim-with-a-timer.md).

## Three questions that are not the same question

| Question | Who answers it | Where the answer lives |
| --- | --- | --- |
| Is this person available? | the application, by heartbeat | here, until it lapses |
| Is this person connected to Convia? | **nobody** | it does not exist |
| Is this person in a call? | Convia | PostgreSQL, `GET /v1/calls/{call_id}/participants` |

The middle row is worth being plain about. **No user connects to Convia.** The only socket Convia serves is the control-event stream, and it belongs to an application's backend rather than to a person; a participant's client connects to the media plane, which is somewhere else entirely. So there is no connection for Convia to notice going away, and reporting one would be inventing a signal.

The third row is the one that would be tempting to fold in, and it is deliberately absent from this resource. A participant is a record with a lifecycle; presence is a claim with a timer. Putting the first behind the second would mean that the first time the ephemeral store was unreachable, Convia would report that nobody was in any call — false, and contradicted by the participants API in the same second.

## The heartbeat

```
PUT /v1/users/{user_id}/presence/{device_id}
Authorization: Bearer cvk_...
Content-Type: application/json

{ "state": "online", "lifetime_seconds": 60 }
```

```json
{
  "user_id": "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E",
  "state": "online",
  "since": "2026-09-10T14:00:00.000Z",
  "expires_at": "2026-09-10T14:01:00.000Z"
}
```

**Send it every 20 seconds.** The default lifetime of 60 seconds covers three at that interval, so one lost to a momentary hiccup costs nothing.

You assert per **device** and Convia answers about the **person**. That is the whole shape of this surface: the application knows which of its sessions is reporting, and what it wants back is the thing it would otherwise have to compute.

### No timestamp is sent, and none is accepted

A caller states how long its claim should stand. Convia computes the deadline from its own clock — Redis's, when a deployment has one.

This is not a detail. It is why a client whose clock is a day fast cannot make somebody permanently available, and why two Convia instances never disagree about when a claim lapsed: neither of them is asked.

`lifetime_seconds` must be between **10 and 300**. A value outside that is refused rather than clamped, because a caller asking to be online for an hour has misunderstood what presence is, and quietly giving it a minute would leave it heartbeating once an hour with its users flickering offline in between.

## The states

| State | What it means | Asserted? |
| --- | --- | --- |
| `busy` | do not disturb | yes |
| `online` | available | yes |
| `away` | idle | yes |
| `offline` | nothing is being said | **no** |

`offline` is never sent to Convia. It is what a person is when nothing is asserting anything — because nothing ever was, because it was withdrawn, or because the last claim lapsed. A request that asserts it is refused with the operation that was meant instead.

### Several devices are one person

The strongest claim wins, in the order the table above is written: **`busy`, then `online`, then `away`.**

That is not "most available first", which would be the obvious rule and the wrong one. `busy` is the only state on the list a person asks for deliberately, and a deliberate request not to be disturbed must not be undone by a laptop in another room reporting activity. `away` ranks last of the three because it is the only one an application normally *infers* — an idle timer rather than an act.

`since` is when the person entered the state they are in, taken from the earliest device still asserting it. A heartbeat is not a change, so a client showing *away for 20 minutes* does not watch it reset every twenty seconds.

`expires_at` is the **latest** deadline among the live claims: one device going quiet does not take somebody offline while another is still talking.

Convia holds at most **16 devices** per person, and a seventeenth is refused rather than evicting the oldest. Evicting would look like it worked, and would surface as presence flapping with nothing in the application's own logs to explain it. A client hitting this is generating a new device identifier per request.

## Going away

```
DELETE /v1/users/{user_id}/presence/{device_id}   # one device
DELETE /v1/users/{user_id}/presence               # every device: a sign-out
```

Both answer with the person's presence rather than `204`. Closing one tab does not usually take somebody offline, and a client that assumed it did would show the wrong thing until its next read.

Withdrawing what was never asserted succeeds and changes nothing. A client retrying its own tidying-up after a lost connection must not be told it failed.

## Reading

```
GET /v1/users/{user_id}/presence
GET /v1/presence?user_id=usr_A&user_id=usr_B        # up to 100, in the order asked
```

Somebody nothing is being said about is `offline` rather than absent: *Convia knows nobody by that name* and *nobody is saying anything about them* are different answers, and only the first is a mistake. A user the application does not have is a `404` on either route.

There is **no device list and no device count** in the response. The question presence answers is whether somebody is available; how many screens they have open is a detail about their day, and a colleague reading a roster has no business learning it.

## Who may see it

Two scopes, and they are separate on purpose:

| Scope | Grants |
| --- | --- |
| `presence:write` | asserting and withdrawing |
| `presence:read` | reading, and `presence.changed` on a stream |

Holding one does not grant the other. An application that reports presence from its session tier and reads it from its API tier gives each of them one, so a key that can say where somebody is cannot ask where everybody is.

The tenant is the one the credential proves. There is no path parameter, no query, and no body field anywhere on this surface that could name another application — two applications naming the same user identifier are two unrelated people, exactly as they are everywhere else in Convia.

## Presence on the event stream

```json
{
  "id": "evt_2QF7XKN4VJH6TBWMDR3YAC5EZP",
  "version": 1,
  "type": "presence.changed",
  "occurred_at": "2026-09-10T14:04:56.154Z",
  "application_id": "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
  "subject": { "type": "user", "id": "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E" },
  "data": { "state": "away", "previous": "online" }
}
```

Presence is the only application-asserted fact Convia streams back, and [`events.md`](events.md) explains why every other one is absent: announcing it would tell a client what it just did. **Here the assertion is not the change.** What a subscriber is told is the aggregate across a person's devices, and the moment a claim lapsed on a timer — and the instance that sent the heartbeat knows neither.

Two things follow, and both matter to an integration:

- **Only a move is announced.** A heartbeat that refreshes a deadline without changing the state publishes nothing. Otherwise presence would be the loudest thing on every stream while saying the least.
- **`presence.changed` cannot be delivered by webhook.** An endpoint that asks for it is refused at registration. A webhook is a delivery with attempts behind it, so a presence report that failed once arrives after it stopped being true, and after the newer one that replaced it — a roster that never settles. Advisory and durable-with-retries are contradictory promises, and Convia refuses the contradiction rather than leaving an application to discover it.

Departures arrive a few seconds late. Expiry is a timer and a timer tells nobody, so each instance sweeps for lapsed claims every five seconds and announces what it finds. A read never waits for that: it decides for itself whether a claim still stands.

## Running more than one instance

`CONVIA_REDIS_URL` decides where presence lives, the same way it decides whether the event stream spans instances.

| | Where presence lives | What works |
| --- | --- | --- |
| unset | in the process | everything, for one instance |
| set | in Redis, shared | everything, for any number |

**Several instances with it unset is the one configuration that is quietly wrong.** A heartbeat would reach whichever instance the load balancer chose and a read would reach whichever it chose next, so presence would depend on which machine answered — and it would look like it worked. Convia cannot detect that from inside one process, so each instance says at startup which of the two it is.

Nothing about the API changes between them. The in-process store is not a fallback or a cache: with one instance there is nowhere else for presence to be, and a network round trip to answer a question the process already knows would be worse in every respect.

## What Redis holds, and what may be evicted

Presence is the first thing Convia ever writes to Redis. Two kinds of key, both self-clearing:

- **one hash per person** being asserted about, holding a claim per device, whose own expiry is set past the latest of them;
- **one sorted set of deadlines** for the whole deployment, whose members are removed as they come due.

Every claim expires and nothing is written that does not. A test asserts that once everybody has lapsed there is nothing left — no hash, no deadline, no bookkeeping.

**If Redis evicts either under memory pressure, presence reads as offline and nothing else in Convia is affected.** That is the safe direction, and it is the only eviction assumption presence makes. [ADR 0005](adr/0005-redis-for-what-instances-tell-each-other.md) set the rule for the day keys arrived and it still holds: nothing in Redis is a source of truth.

## When presence is unavailable

```
HTTP/1.1 503 Service Unavailable
Retry-After: 5

{"error":{"code":"unavailable","message":"Presence is temporarily unavailable. Nothing else about this application is affected.","request_id":"..."}}
```

Presence is the one part of Convia with no durable copy to fall back on, so an unreachable store is **reported rather than answered as `offline`**. Telling an application that everybody went offline, when what happened is that Convia lost its ephemeral store, would be a false statement about people rather than a degraded one — and an application would act on it.

Nothing else is affected. Calls, rooms, participants, invitations, and webhooks are answered from PostgreSQL and are untouched. Readiness deliberately ignores presence for the same reason: an instance that cannot report who is available is still serving every other request correctly, and taking it out of the load balancer would turn a narrow failure into an outage.

## What Convia stores about a device

A state, when the device began saying it, and when the claim lapses. Nothing else — no address, no user agent, no location, no identity beyond the Convia user the application already named.

The device identifier is the application's own and Convia never interprets it. **Use a value generated per installation** rather than anything describing the device: Convia stores what it receives, the identifier appears in a URL and therefore in an access log, and a model name is a detail about a person that presence does not need.

It never leaves Convia. It is not in any response, and it is not in the event — which carries only values Convia assigned, the same rule the rest of the stream follows.

Presence is also **not audited**. Everything else Convia announces is written to the audit log beside the event, because it is a change to a record. This is a claim with a timer, it arrives thousands of times more often, and where a person is at a given minute is the last thing that belongs in a durable log.
