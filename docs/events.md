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

Everything else Convia records is deliberately absent, and each has a reason:

- **Rooms, users, credentials, applications.** These change because the application changed them, through a request that already returned the new state. Announcing it back would tell a client what it just did.
- **A connection credential being issued.** It is the result of a request the subscriber made, and it is a fact about a secret.
- **An invitation being issued or withdrawn.** The application's own acts, as above.
- **An invitation being redeemed.** This one *is* somebody else's act, but it already arrives as `participant.joined`, carrying the invitation that let them in. Publishing both would report one arrival twice.

Declining is the exception among invitations because it is the invitee's own decision, it is the only signal that somebody is not coming, and nothing else observes it.

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

## Nothing is stored

An event goes to the streams open at the moment it is published, and is then gone.

- An event produced while a client is disconnected is **not** waiting for it when it returns.
- There is no cursor and no replay.
- A client that reconnects should re-read whatever it cares about over REST, and then keep listening.

This is the honest summary of what an in-process fan-out can promise, and it is why anything a consumer must not miss belongs in a REST read — or, when webhooks arrive with M15, in a delivery with an attempt behind it.

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

`4000` is in the range RFC 6455 reserves for applications, because falling behind is not a transport condition and borrowing a protocol code for it would say something untrue.

## Heartbeat

Convia sends a ping every **30 seconds** and closes the connection if it is not answered within **10**.

A control stream is legitimately silent for hours — a tenant with no calls running produces nothing — so silence cannot be read as failure. The ping is what separates a quiet tenant from a dead connection, and it keeps intermediaries from reclaiming a connection they believe is idle. Nothing is expected of the client: its WebSocket implementation answers the ping for it.

## Limits

| Limit | Value | Why |
| --- | --- | --- |
| Streams per application | 8 | One per instance of an application's backend, with room for a rolling deployment. The stream carries the whole tenant, so it is not one per user. |
| Streams per Convia instance | 1024 | So that many tenants cannot together do what one is already stopped from doing. |
| Queue per stream | 256 events | See *Falling behind*. |
| Bytes read from a client | 1024 | The point at which Convia stops reading something it is going to refuse anyway. |

Both ceilings are answered with the same `429`, so a tenant is never told anything about the instance's total load.

## Running more than one instance

**The broker is in-process.** A subscriber connected to instance A does not see an event produced on instance B.

This is a real operational constraint, not a footnote. A deployment today must do one of:

- **run a single instance**, which is what local development and small deployments do; or
- **route each tenant's streams to a fixed instance**, so that every event about a tenant is produced on the instance its subscribers are connected to. That requires the same tenant's *API requests* to land there too, since events are produced by the requests that change things — which in practice means routing by application, not by connection.

Neither is a long-term answer. The long-term answer is a shared publish/subscribe channel, which is [`M16`](../TODO.md), and this is the concrete use case `M16-001` asks for before Redis is added: the events would be ephemeral, fan-out only, with no durability requirement — precisely what Redis pub/sub is for, and precisely not a durable source of truth.

What must not happen in the meantime is a deployment quietly running several instances behind a round-robin balancer and believing the stream is complete. It would look like it worked.

## Shutting down

An instance stopping tells its subscribers before it goes: every stream is closed with `1001`, and the process waits for those close frames within its shutdown deadline.

This is a step of its own because a WebSocket connection is hijacked from the HTTP server, which neither tracks it nor waits for it. Without the step, an orderly shutdown would look to a subscriber exactly like a crash.

## Observability

Every stream logs a line when it opens and a line when it closes, the second carrying how many events it delivered and why it ended. That is what an operator needs today.

Metrics — active connections, delivery latency, dropped events — wait for a metrics pipeline to exist. See `M14-013` in [`TODO.md`](../TODO.md).
