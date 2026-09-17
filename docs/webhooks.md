# Webhooks
 
A **webhook** is Convia reaching out to tell an application something it must not miss.

The domain lives in [`internal/webhooks`](../internal/webhooks), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml). The design decisions are in [ADR 0004](adr/0004-queuing-a-durable-delivery-inside-the-request.md) and [ADR 0017](adr/0017-events-are-recorded-with-the-change-that-caused-them.md).

## Webhook or stream?

Both carry the same events, described in [`events.md`](events.md). They are for different problems.

| | Event stream | Webhook |
| --- | --- | --- |
| Reaches | whoever is connected right now | wherever you registered |
| If you are not there | it is replayed when you reconnect, for 24 hours | it waits, and is retried |
| Duplicates | never | possible, by design |
| Needs | an open connection | a public HTTPS endpoint |
| Ordering | as produced | not guaranteed |

Use the stream for a picture that must stay current and that you can rebuild by re-reading. Use a webhook for something you must act on exactly once — a billing record, a message to a user, an entry in your own database. If neither describes your case, poll: for anything that changes slowly, a read is simpler than a destination Convia has to reach, sign for, and eventually give up on.

## Registering

```jsonc
POST /v1/webhooks
{
  "name": "Production receiver",
  "url": "https://hooks.example.com/convia",
  "event_types": ["call.started", "call.ended", "participant.joined"]
}
```

The response carries a `secret`, **and it is the only time it appears**:

```jsonc
{
  "id": "whk_4XZQP7KN2VJH6TBWMDR3YAFC5E",
  "url": "https://hooks.example.com/convia",
  "event_types": ["call.ended", "call.started", "participant.joined"],
  "status": "enabled",
  "consecutive_failures": 0,
  "secret": "whsec_..."
}
```

Convia generates the secret rather than accepting one, because a secret an application chose is one it may have reused. Convia also *keeps* it — it has to, since it signs every delivery with it — so "once" here means that no read ever returns it. Lost it? Rotate.

`event_types` must name at least one type Convia actually delivers. An unknown type is refused rather than stored, because a subscription that can never fire looks exactly like an event that has not happened yet.

**`presence.changed` is refused too, and it is the only type Convia delivers that a webhook cannot carry.** A webhook is a delivery with attempts behind it, so a presence report that failed once would arrive after it stopped being true, and after the newer one that replaced it — leaving an application with a roster that never settles. Presence is advisory by construction, and Convia will not deliver it durably rather than let an integration discover the contradiction for itself. Subscribe to it on the stream instead: `GET /v1/events`, described in [`presence.md`](presence.md).

## Verifying a delivery

Every request carries four headers:

```
Convia-Signature: t=1789041896,v1=5257a869e7ecebeda32affa62cdca3fa793e...
Convia-Delivery:  whd_7KQZP4XN2VJH6TBWMDR3YAFC5E
Convia-Event:     call.started
Convia-Attempt:   1
```

To verify, in three steps:

1. Read `t` and `v1` out of `Convia-Signature`.
2. Compute `HMAC-SHA256(secret, "<t>.<raw request body>")` and hex-encode it.
3. Compare it with `v1` **in constant time**, and reject anything where `t` is more than five minutes from your clock.

The timestamp is inside the signed material, not beside it. Signing only the body would let anyone who once saw a valid delivery replay it forever, because the signature would stay valid for as long as the secret did.

Two things that break verification, both common:

- **Sign the raw bytes.** Parsing the JSON and re-encoding it changes key order and whitespace, and the signature is over bytes.
- **Compare in constant time.** `hmac.Equal` in Go, `hash_equals` in PHP, `crypto.timingSafeEqual` in Node.

`Verify` in [`internal/webhooks/signature.go`](../internal/webhooks/signature.go) is the same twenty lines, and Convia's own tests use it against real deliveries — so the code you are copying is code that is exercised.

## Deliveries arrive at least once

**The same delivery may arrive more than once.** A destination that received a body and failed to answer is indistinguishable from one that never received it, and Convia resolves that by trying again.

`Convia-Delivery` is stable across every attempt at the same event. Record it, and ignore a delivery you have already handled. That is the whole of the consumer's part, and it is why the identifier is in a header rather than only in the body.

`Convia-Attempt` starts at one. A number above one means Convia did not hear back from you last time, which is usually the more useful half of a duplicate.

**Order is not guaranteed.** Two events queued a millisecond apart can arrive in either order, and a retried one arrives after everything queued behind it. Use `occurred_at` on the event, not arrival order.

## Answering

Answer **2xx** and Convia considers it delivered. Anything else is a failure, and what Convia does next depends on which:

| Answer | What Convia does |
| --- | --- |
| `2xx` | Done. |
| `408`, `429`, any `5xx` | Retries on the schedule below. |
| Any other `4xx` | **Gives up immediately.** You are refusing, and repeating it for two hours would be Convia insisting. |
| `3xx` | Treated as a failure and not followed. See below. |
| No response, timeout, connection refused | Retries. |

Answer quickly. Convia gives an attempt **10 seconds** end to end, and a receiver that does real work before answering will hit it. Acknowledge first, work afterwards.

Nothing you return is stored beyond the status code. A response body is text Convia did not write, and it would end up in a table your own operators read.

**Redirects are not followed.** A `3xx` is recorded as a failure. Following one would let a destination point Convia somewhere else after registration, which is the shape of the attack the section below exists to stop.

## Retries

Seven waits, so eight attempts, deterministic and without jitter:

| After attempt | Next attempt in |
| --- | --- |
| 1 | 30 seconds |
| 2 | 1 minute |
| 3 | 2 minutes |
| 4 | 5 minutes |
| 5 | 15 minutes |
| 6 | 30 minutes |
| 7 | 1 hour |
| 8 | Convia gives up |

Roughly two hours of trying. There is no jitter because the schedule is published and tested; the thundering herd jitter defends against is many consumers retrying one source, and here the retries are Convia's, spread across destinations that failed at different moments already.

**A delivery outstanding for more than four hours is given up on regardless.** A webhook that arrives four hours after the conversation it describes has ended is worse than none, because you would act on it.

## A failing endpoint is eventually disabled

After **20 consecutive give-ups**, Convia disables the endpoint, says why in `disabled_reason`, and stops sending. The count resets on any success, so this is a statement about a destination that has stopped working rather than one having a bad afternoon.

Fix the receiver, then `POST /v1/webhooks/{id}/enable`. The failure count resets, and **the events you missed are not resent** — see the age limit above. Re-read what you need over REST.

You can disable an endpoint yourself while a receiver is being replaced. Disabling also finishes whatever was already queued for it, so nothing arrives hours later from before the pause.

## Rotating a secret

```
POST /v1/webhooks/{endpoint_id}/rotate
```

Returns a new secret once. **The old one stops working immediately** — rotation exists because a secret may have been exposed, and one that kept working for a grace period would keep working for whoever exposed it.

The deployment order is therefore: teach your receiver to accept either secret, rotate, then remove the old one.

## Where Convia will and will not connect

A destination is chosen by a tenant and fetched by Convia's own process, from inside whatever network Convia runs in. Without a guard, an application could register `http://169.254.169.254/latest/meta-data/` and use Convia to read a cloud instance's credentials — and even without seeing the body, the recorded status code is already an oracle. The same trick reaches an unauthenticated database, an internal admin panel, or another tenant's Convia.

So:

- **`https` only.** Plain `http` is refused outside development.
- **No address that is not the public internet.** Loopback, the private ranges, link-local (which includes cloud metadata), unique-local IPv6, carrier-grade NAT, multicast, and the documentation and reserved blocks are all refused — including an IPv4 address written as an IPv6-mapped one, which is the usual way past a naive check.
- **The check runs at the socket, at every attempt.** Not on the hostname, and not once at registration: a name an attacker controls can resolve to a public address while the registration is being checked and to a private one an hour later. Only the address actually being connected to cannot be raced.
- **No proxy is consulted.** Through a proxy the connected address is the proxy's, and the destination becomes a string in a header that nothing inspects — a single `HTTPS_PROXY` in the environment would silently disable all of the above.

A development instance may reach a local receiver, because that is how anybody tests a webhook. **Production may not, and there is no setting that changes it.** The only reason to want one is the reason not to have one.

## Reading what happened

Every delivery is a row, which is what a webhook has and a stream does not:

```
GET /v1/deliveries?endpoint_id=whk_...
GET /v1/webhooks/{endpoint_id}/deliveries
GET /v1/deliveries/{delivery_id}
```

Each says which event it carried, how many attempts it took, when the next one is due, what the destination answered, and Convia's own description of the last failure. The body that was sent is not included: it is the event, and keeping a copy on every delivery would put the same content in a second place with a different lifetime.

Deleting an endpoint deletes its deliveries with it. An application that wants to keep the history disables the endpoint instead.

## Scopes

| Scope | Permits |
| --- | --- |
| `webhooks:read` | Reading endpoints and deliveries, never a signing secret |
| `webhooks:write` | Registering, changing, enabling, disabling, deleting, and rotating |

They are separate from `events:read`, which grants a live stream. Holding a connection open and asking Convia to make requests to an address of your choosing are different powers, and only the second turns Convia into a client of somewhere else.

## A delivery is owed exactly when its change happened

A delivery is queued by the transaction that made the change it reports. If the change is undone, nothing is owed; if it commits, the delivery is already in the queue, and no crash afterwards can lose it. When the queue cannot be written, the change is refused with it, and the caller is told the request failed.

The body carries the event's `cursor` too, which a webhook consumer may ignore.

## Running more than one instance

Unlike the event stream, webhooks are safe across instances and always have been. The queue is a table, and workers claim work with a row lock that skips what another worker holds, so two instances divide a backlog rather than duplicate it. A worker that dies mid-attempt strands nothing: its lease expires and the next worker takes the delivery again.

That is also the source of the duplicates a consumer must expect, and it is the right trade — losing a delivery is worse than sending one twice to somebody who recorded its identifier.
