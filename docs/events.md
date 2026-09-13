# Control events

A **control event** is one thing Convia recorded, delivered to an application while it is still news.

The domain lives in [`internal/events`](../internal/events), the endpoint and the envelope are in [`api/openapi.yaml`](../api/openapi.yaml), and the design decisions are in [ADR 0003](adr/0003-a-one-directional-in-process-control-event-stream.md).

## What streams, and what does not

Convia records a great deal and streams very little. The line is not "what is interesting": it is **what goes stale in seconds**.

| Event | Subject | What it means |
| --- | --- | --- |
| `call.started` | call | A conversation began in a room. |
| `call.ended` | call | It finished, whoever ended it. |
| `participant.joined` | participant | Somebody is now in the call. |
| `participant.left` | participant | Somebody left of their own accord. |
| `participant.removed` | participant | Somebody was put out. |
| `participant.role_changed` | participant | What somebody may do changed. |
| `invitation.declined` | invitation | An invitee said they are not coming. |
| `message.posted`, `message.edited`, `message.deleted` | message | Somebody said something in a room, changed it, or withdrew it. See [`messages.md`](messages.md). |
| `room.member_added` | room | Somebody now has a place in the room. `data.user_id` says who. |
| `room.member_removed` | room | Somebody no longer does, whether they left or the application removed them. |
| `presence.changed` | user | Convia will now say something different about whether somebody is available. |

Everything else Convia records is deliberately absent, and each has a reason:

- **Rooms, users, credentials, applications.** These change because the application changed them, through a request that already returned the new state. Announcing it back would tell a client what it just did.
- **A connection credential being issued.** It is the result of a request the subscriber made, and it is a fact about a secret.
- **An invitation being issued or withdrawn.** The application's own acts, as above.
- **An invitation being redeemed.** This one *is* somebody else's act, but it already arrives as `participant.joined`, carrying the invitation that let them in. Publishing both would report one arrival twice.

Declining is the exception among invitations because it is the invitee's own decision, it is the only signal that somebody is not coming, and nothing else observes it.

Membership is the exception among rooms, for the same kind of reason. While only an application changed who was in a room, announcing it would have told the application what it just did. Since M18 a person adds another person, and the one added is not the one who made the request: their sidebar has no other way to learn it. Leaving and being removed are one type because membership records no actor, and a type that claimed to know which it was would be guessing. Only a change is announced, as only a change is audited, and erasure announces nothing — broadcasting every room somebody had been in would publish exactly the record erasure removes.

Presence is the other exception, and it is the interesting one: an application asserts it, which is exactly why rooms and users are absent. The difference is that **the assertion is not the change**. What a subscriber is told is the aggregate across a person's devices, and the moment a claim lapsed on a timer — and the instance that sent the heartbeat knows neither. It comes with two rules of its own, both in [`presence.md`](presence.md): a heartbeat that changes nothing announces nothing, and `presence.changed` is the one event type Convia refuses to deliver by webhook.

## Opening a stream

```
GET /v1/events
Authorization: Bearer cvk_...
Connection: Upgrade
Upgrade: websocket
Sec-WebSocket-Version: 13
```

Everything that decides what the stream carries happens **before** the upgrade, while the exchange is still an ordinary HTTP request that can be refused with an ordinary JSON error. Once the connection is a socket there is no status code left to send.

| Answer | Meaning |
| --- | --- |
| `101` | The connection is now a stream. |
| `401 unauthenticated` | No usable key. |
| `403 forbidden` | The key may not open a stream, or nothing it holds could be delivered on one. |
| `429 rate_limited` | Too many streams are open. `Retry-After` says when to try again. |
| `503 unavailable` | The instance is shutting down. |

## What the stream carries is what the key could already read

`events:read` grants the connection. It grants no content.

Each event is delivered only to a credential that could have read the thing it is about:

| Events | Scope that already permits reading them |
| --- | --- |
| `call.*` | `calls:read` |
| `participant.*` | `participants:read` |
| `invitation.*` | `invitations:read` |
| `message.*` | `messages:read` |
| `room.member_*` | `members:read` |
| `presence.*` | `presence:read` |

So a key holding `events:read` and `calls:read` receives call events and nothing else. A key holding `events:read` and no read scope is **refused** rather than given an empty connection — an empty stream is indistinguishable from a quiet one, and a client would wait indefinitely for events that were never going to come.

The tenant is the one the key proves. There is no path parameter, no query, and no message that could name another.

## The stream is one direction

**Convia accepts nothing from a client on this connection.** A client that sends any message — text or binary — has its connection closed with `1003 unsupported data`.

There is no subscribe message, no filter message, and no acknowledgement. The subscription was settled from the credential during the handshake and cannot be changed.

This is also how M14-010 is answered: the stream cannot carry audio or video because it cannot carry anything upstream at all. Media reaches the media plane over WebRTC, using the credential from `POST /v1/participants/{participant_id}/session`. See [`media.md`](media.md).

## The envelope

```json
{
  "id": "evt_2QF7XKN4VJH6TBWMDR3YAC5EZP",
  "version": 1,
  "type": "participant.joined",
  "occurred_at": "2026-09-10T14:04:56.154Z",
  "application_id": "app_MXHJAY4MJNX2FO22XWJ3XNCKHT",
  "subject": { "type": "participant", "id": "part_4XZQP7KN2VJH6TBWMDR3YAFC5E" },
  "correlation_id": "req_01K4Z8N5QW6M7XPYB2VD3HTJRA",
  "data": {
    "call_id": "call_7KQZP4XN2VJH6TBWMDR3YAFC5E",
    "role": "member",
    "status": "joined",
    "guest": false,
    "user_id": "usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"
  }
}
```

The version travels on each event rather than being negotiated once per connection, so an event forwarded into a log or a queue stays interpretable away from the stream it arrived on.

`correlation_id` is the request that caused the event. It matches the `X-Request-ID` of that request and the audit entry for the same occurrence, which is what ties the three together during an incident. It is absent when the cause was not a request.

**Everything an event carries is a value Convia assigned** — identifiers, states, roles, flags. Application-composed text stays out: a removal reason, an end reason, and call metadata may each describe a person, and a stream reaches every subscriber at once. A client that wants them reads the resource.

Two rules for a client that wants to keep working across Convia releases: **ignore an event type you do not recognize**, and **ignore a `data` key you do not recognize**. Both sets are additive.

## A person's stream

Convia's own interface listens too, with a session rather than a key:

```
GET /v1/me/events
Cookie: __Host-convia_session=cvs_...
Origin: https://convia.example
Connection: Upgrade
Upgrade: websocket
```

It is not the application's stream with a different credential, and the difference is the design. An application's stream is authorized once, for a whole tenant, by scopes that cannot change while it is open. **A person's is authorized per room**, and which rooms is exactly what changes while it is open. [ADR 0010](adr/0010-a-persons-stream-is-authorized-per-room.md) records why it is built the way it is.

**It carries what the person could already read, and nothing else.** That is the rule an application's stream follows with scopes, applied to a person:

| Carried | Delivered when |
| --- | --- |
| `message.*` | it names a room the person is in |
| `room.member_*` about somebody else | it names a room the person is in |
| `room.member_*` about the person | they were in the room before it, **or** are in it after |

The last row is the one that needs saying. Somebody just added was not in the room a moment ago, and somebody just removed no longer is, so judging either by one side alone would withhold exactly the event the person needs. Calls, participants, and presence are absent because nothing on the session surface reads them yet; they arrive when something does.

The envelope is the same, **without `correlation_id`**. The request that caused an event was usually somebody else's, and the identifier exists to be matched against an access log only an operator reads.

**Which rooms is read before the upgrade**, so a person whose rooms cannot be read is told so with a `500` rather than handed a silent socket. It is then kept current by the membership events the stream itself carries, and **read again every minute**, when the session is also authenticated again:

- A session that no longer authenticates — signed out, expired, suspended — closes the stream with **`4001`**. A reconnect would be refused before the upgrade, and a browser hides a refused handshake's status from scripts, so this close is the one moment the reason can still be said.
- A check that could not be made — the database did not answer — leaves the stream open. It says nothing about the person, and closing every stream on an instance over it would send every tab to reconnect into the same outage.
- A membership event lost between instances costs at most that minute, because the next read finds the room anyway.

Stated rather than hidden: **within that minute**, a stream may still carry events about a room its person has just left, or a session that has just ended. What travels is identifiers, and reading what they point at asks for membership and a session again, on every request.

**The handshake must come from Convia's own page**, though it is a GET. It opens a connection that goes on carrying whatever the cookie is entitled to, so a page on a sibling subdomain that could open one would be reading somebody's conversations as they happen — cross-site WebSocket hijacking, which `SameSite` does not see for the reason it does not see a sibling's POST. The exact-match `Origin` check every state-changing request on the session surface passes is applied to it too, and an `Origin` other than this instance's is refused with `403`.

## Nothing is stored

An event goes to the streams open at the moment it is published, and is then gone.

- An event produced while a client is disconnected is **not** waiting for it when it returns.
- There is no cursor and no replay.
- A client that reconnects should re-read whatever it cares about over REST, and then keep listening.

Which instance a client reaches does not matter, as long as the deployment is configured for more than one — see *Running more than one instance* below.

This is the honest summary of what an in-process fan-out can promise, and it is why anything a consumer must not miss belongs in a REST read or in a [webhook](webhooks.md), which is a delivery with attempts behind it.

One event type goes the other way: **`presence.changed` streams and can never be a webhook.** An endpoint that asks for it is refused at registration. A presence report that failed once would arrive after it stopped being true, and after the newer one that replaced it, which is a roster that never settles — advisory and durable-with-retries are contradictory promises. See [`presence.md`](presence.md).

## Falling behind

Each stream has a queue of 256 events. It absorbs a garbage-collection pause or a slow network without costing anybody a connection.

A subscriber that fills it is **disconnected with close code `4000`**, reason `events were dropped because this stream fell behind`.

That is deliberately louder than dropping an event and carrying on. A subscriber that kept receiving events after losing one would have no way to know its picture had a hole in it. The close says: your view is incomplete, re-read over REST, then reconnect.

## Close codes

| Code | Meaning | What to do |
| --- | --- | --- |
| `1000` | The stream ended normally. | Reconnect if you still want events. |
| `1001` | This instance is shutting down. | Reconnect; you will reach another instance. |
| `1003` | You sent a message. The stream is one direction. | Fix the client; do not retry blindly. |
| `4000` | You fell behind and events were dropped. | Re-read over REST, then reconnect. |
| `4001` | A person's stream only: the session that opened it no longer authenticates. | Sign in again; a reconnect will be refused. |

`4000` is in the range RFC 6455 reserves for applications, because falling behind is not a transport condition and borrowing a protocol code for it would say something untrue.

## Heartbeat

Convia sends a ping every **30 seconds** and closes the connection if it is not answered within **10**.

A control stream is legitimately silent for hours — a tenant with no calls running produces nothing — so silence cannot be read as failure. The ping is what separates a quiet tenant from a dead connection, and it keeps intermediaries from reclaiming a connection they believe is idle. Nothing is expected of the client: its WebSocket implementation answers the ping for it.

## Limits

| Limit | Value | Why |
| --- | --- | --- |
| Streams per application | 8 | One per instance of an application's backend, with room for a rolling deployment. The stream carries the whole tenant, so it is not one per user. |
| Streams per Convia instance | 1024 | So that many tenants cannot together do what one is already stopped from doing. |
| Streams per person | 16 | A browser opens one per tab, and a person may hold ten sessions. It still bounds what one stolen cookie can hold open. |
| People's streams per Convia instance | 4096 | Counted apart from the applications', so that signed-in tabs cannot refuse every tenant its backend stream. |
| Queue per stream | 256 events, or 64 on a person's | See *Falling behind*. A person's carries their rooms rather than a tenant, and there are far more people than backends, so a queue allocated in full per tab is smaller. |
| Bytes read from a client | 1024 | The point at which Convia stops reading something it is going to refuse anyway. |

Both ceilings are answered with the same `429`, so a tenant is never told anything about the instance's total load.

## Running more than one instance

The broker is in-process, so on its own an instance serves only the subscribers connected to it. **Set `CONVIA_REDIS_URL` on every instance and that stops being true**: events are carried between them over one publish/subscribe channel, and a subscriber sees what happened wherever it happened. The same setting decides where [presence](presence.md) lives, and for the same reason.

```bash
CONVIA_REDIS_URL=rediss://redis.internal:6379/0
```

**This is an operational requirement, not a tuning option.** Several instances with it unset is the one configuration that is quietly wrong: each serves only its own subscribers, no request fails, nothing is logged as an error, and it looks like it works. Convia cannot detect it from inside one process — an instance has no way to know how many others exist — so the check is yours. Each one says at startup which of the two it is:

```
no shared channel is configured, so event streams are served by this instance alone
shared channel configured  address=redis.internal:6379  origin=ins_...
```

What the relay does **not** change:

- **Who receives what.** The broker decides that from the credential that opened the stream, and decides it the same way whether the event was produced here or elsewhere.
- **What a stream promises.** Nothing is stored, in Redis or anywhere else. An event produced while a subscriber is away is still gone, and a subscriber that falls behind is still disconnected.
- **Whether Convia works.** An unreachable Redis narrows the stream back to one instance and is reported loudly; it does not fail requests, and readiness deliberately ignores it. Anything a consumer must not miss belongs in a [webhook](webhooks.md), which was safe across instances from the first commit.

Ordering is per-instance rather than global. Two events produced on different instances arrive in whatever order the network delivered them, so use `occurred_at` rather than arrival order — the same rule webhooks already ask for.

The channel is `convia:v1:events`: a namespace, so Convia's traffic is recognizable on a Redis somebody else is also using, and a version, so a future envelope can run beside this one during a rolling deployment. It is one channel for the whole deployment rather than one per tenant, which is a choice about traffic between machines that already share a database, not about isolation. [ADR 0005](adr/0005-redis-for-what-instances-tell-each-other.md) records why, and what would justify changing it.

Locally, Redis is opt-in the same way the media plane is:

```bash
docker compose --profile shared up -d
```

## Shutting down

An instance stopping tells its subscribers before it goes: every stream is closed with `1001`, and the process waits for those close frames within its shutdown deadline.

This is a step of its own because a WebSocket connection is hijacked from the HTTP server, which neither tracks it nor waits for it. Without the step, an orderly shutdown would look to a subscriber exactly like a crash.

## Observability

Every stream logs a line when it opens and a line when it closes, the second carrying how many events it delivered and why it ended. That is what an operator needs today.

Metrics — active connections, delivery latency, dropped events — wait for a metrics pipeline to exist. See `M14-013` in [`TODO.md`](../TODO.md).
