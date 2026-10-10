# Running more than one instance

**Convia can run as several instances behind a load balancer, sharing one PostgreSQL and one Redis.** Nothing a request needs lives only in the process that answered the previous one. This document is the inventory that claim rests on — every piece of state an instance keeps in its own memory, what running two of them does to it, and whether that matters. It is `M25-011`.

The short version: **with Redis configured, two instances behave as one, except that most rate limits are counted per instance** — all but the two that bound guessing a password. Without Redis, run one.

## What Convia is sized for

These are `M25-001` and `M25-002`: the numbers every measurement in `M25` is taken against. They are the hosted service Orbit and Workspace Town are meant to consume, which is larger than any installation somebody runs for themselves, on purpose — an installation that meets them meets the smaller ones.

| | Expected at once |
| --- | --- |
| Tenants | 10 |
| Users across them | 10 000 |
| Connections — event streams and personal streams | 1 000 |
| Rooms with something happening | 500 |
| Calls running | 100 |
| Participants in one call | up to 25 |
| Instances | several, behind one load balancer, sharing one PostgreSQL and one Redis |

Targets are for **one instance of 2 vCPU and 4 GB**, measured at the server, at the 95th percentile, with the load above spread across the instances:

| Operation | p95 |
| --- | --- |
| A read: a listing, a resource, a page of messages | under 200 ms |
| A write: posting a message, opening a room, changing a member | under 300 ms |
| Joining a call, credential included | under 500 ms |
| An event reaching a connected subscriber, from the change that caused it | under 250 ms |
| Signing in, which is argon2id on purpose | under 1 s |

Signing in is slower by design: the password hash is the cost an attacker pays per guess, and making it cheap for the server would make it cheap for them.

## What lives in PostgreSQL, and is therefore shared

Everything durable: tenants, keys, users, accounts, sessions, rooms, members, calls, participants, invitations, messages and read state, webhook endpoints and deliveries, idempotency keys, the nonces of signed requests between installations, the event journal, and the [audit trail](audit.md). An instance holds none of it in memory, so a request reaches the same answer whichever instance it lands on. A person signed in on one instance is signed in on all of them, and a retried request carrying an `Idempotency-Key` is recognised wherever it arrives.

## What lives in memory, instance by instance

| State | With two instances | Verdict |
| --- | --- | --- |
| Event streams being served | each instance serves its own subscribers, and every instance reads every durable event from the journal | correct |
| Ephemeral events, such as presence | relayed between instances through Redis | correct with Redis |
| Presence | held in Redis, or in this process when there is no Redis | **needs Redis** |
| Streams followed for visitors from other installations | run by the instance holding that person's own stream | correct |
| Rate limits | sign-in and registration shared through Redis, the rest per instance | correct with Redis; see below |
| Background work | runs on every instance; see below | correct |

### Presence needs Redis to be shared

With no Redis, presence is kept in the process, so an application asserting somebody is online on one instance and asking on another is told they are offline. Convia cannot tell from inside one process how many others there are, so it cannot refuse to start; it is a deployment rule instead. **More than one instance means Redis is configured.** See [`presence.md`](presence.md#running-more-than-one-instance).

### Rate limits: two are shared, the rest are counted per instance

**Sign-in guesses and registrations are counted across instances** when Redis is configured, in one token bucket computed against Redis's own clock so that instances whose clocks disagree still agree on it. Those two are what bound guessing a password: the reason a 12-character password is enough is that an address can only try so many, and with per-instance counting *n* instances would allow *n* times that. If Redis cannot be reached, each budget is counted by the instance until it can, which is the bound Convia had before, and the fallback is logged once a minute rather than on every request.

Every other budget — failed authentications on the API, each tenant's and each person's request rate, each installation and signer on the peer surface — is a token bucket in the instance that received the request, so behind a load balancer *n* instances allow *n* times it. Those are about fairness between callers, where a few times more is a capacity question, and a shared limiter would be a network round trip on the one path whose whole purpose is to be cheaper than the work it guards.

### Background work runs everywhere, and is safe to

Each instance runs the same loops. None of them assumes it is the only one:

- **Webhook deliveries** are claimed with `FOR UPDATE SKIP LOCKED`, so two workers take different deliveries; a test runs two and checks nothing is sent twice.
- **Presence lapsing** is claimed by one atomic script in Redis, so one instance announces a person leaving and the others find nothing.
- **Pruning the event journal** only ever moves its floor forward, so two instances pruning at once cannot make it go back and let an expired cursor through.
- **Forgetting erased people** changes a row only while it still holds what is being forgotten, so a second pass over the same person does nothing.
- **Forgetting expired nonces** deletes rows nobody reads any more, which is the same whichever instance does it.

## Stopping one instance

Stopping an instance is what a rolling deployment does to each of them in turn, and it is tested end to end (`M25-010`): an instance asked to stop closes every event stream with `1001` and *this instance is shutting down*, so a subscriber reconnects to another instance rather than seeing a broken socket; it stops accepting requests; and it exits within its ten-second deadline. **A call survives it.** Media travels between a client and the media plane, not through Convia, so stopping Convia ends no conversation, and the call is exactly as it was when an instance answers again.

A webhook delivery interrupted by a stop is not lost either: its lease expires and the next worker to look takes it.
