# TypeScript SDK

`convia-sdk` lets a browser application talk to a Convia installation as the signed-in person, without implementing Convia's HTTP and event protocols itself.

This document records the decisions of milestone `M19` in [`TODO.md`](../TODO.md). How the contract itself may change is [`api-compatibility.md`](api-compatibility.md); this governs the package that consumes it.

## What it covers, and what it does not

**The session surface**: the person-facing routes under `/v1/me`, plus the two vocabularies every consumer branches on — the error codes and the event types.

**The tenant surface is deliberately absent**, and not because it is harder. An application's `cvk_` key belongs on a server, `/v1/events` is authenticated by one, and a package that made that key easy to reach from a browser would be a package that made it easy to ship the key there. A server-side SDK is `M20`.

**No media client.** Joining a call answers with an address and a credential, and the consumer hands them to whatever client their installation's media plane needs. The package has **zero runtime dependencies** and names no media vendor anywhere, including in the generated contract — one that depended on a media client would make every consumer ship it, and would break the installation that changed.

## Generated and handwritten

The division is the one [`api-compatibility.md`](api-compatibility.md#sdk-consumption) requires, and it is worth restating because the cost of getting it wrong is silent:

| Part | Where it comes from |
| --- | --- |
| Request and response shapes, error codes, event types | `api/openapi.yaml`, through `openapi-typescript`, into `src/contract.ts` |
| Transport, deadlines, cancellation, retries, the event connection, media handling | Written by hand |

`src/contract.ts` is **committed rather than built on demand**, so the type-checker and consumers do not need the generator and a change to the contract arrives as a diff somebody reads. The cost of committing it is that the copy can fall behind and nothing about it looks wrong — the types compile, the tests pass, and the SDK describes a Convia that no longer exists. `.github/scripts/check_generated.py` closes that, in the same shape as `gofmt -l` and for the same reason: it checks rather than rewrites, because a build that silently rewrote a tracked file would leave somebody's working tree changed by CI.

**It is not re-exported.** The contract describes every surface, including the operator and tenant operations this package must never call, and exporting it whole would make each of those part of what somebody may depend on. `src/index.ts` publishes a list somebody chose.

## Using it

**The examples below are excerpts of files that compile.** `sdk/examples/` is read by `npm run typecheck`, so an example that stopped being true stops the build rather than sitting here being wrong — proved by removing an export and watching the check name it. What they do against a real installation is `sdk/e2e/`.

### Reading and writing

```ts
import { Convia, isConviaError } from 'convia-sdk'

const convia = new Convia()

const me = await convia.get<{ user_id: string; username: string }>('/me')

try {
  await convia.post('/me/rooms', { body: { name: 'Standup' } })
} catch (error) {
  if (isConviaError(error) && error.unauthenticated) {
    // The session is gone. Everything else is a request to fix.
  }
}
```

**There is no token to attach.** The session travels in a cookie the page cannot read, so authentication is `credentials: 'same-origin'` and nothing else — which is also why there is no option pointing the client at another installation: the cookie is `__Host-` prefixed and host-only, so a page on a different origin could not send it.

Every request carries a deadline, thirty seconds by default. A caller's own `AbortSignal` composes with it, and the three ways a request can end without an answer stay apart:

| What happened | What is thrown |
| --- | --- |
| Convia refused it | `ConviaError`, with `status` and `code` |
| Convia answered something unreadable | `Unreadable`, with the status and the start of the body |
| The caller stopped it | the abort they raised |
| The deadline passed | `Timeout` |
| Convia was never reached | `Unreachable` |

A client that confuses them shows an error for a page somebody navigated away from, or reports a slow installation as a cancelled request. **Branch on `code`, never on `message`**: the code is the documented identifier and the message is prose that may be reworded.

### Retrying safely

```ts
import { withRetries } from 'convia-sdk'

await withRetries((key) =>
  convia.post('/me/rooms', { body: { name: 'Standup' }, idempotencyKey: key }),
)
```

**A key and a retry are useless apart.** A key with no retry protects against nothing — `fetch` does not retry, so the only repeat is one the caller makes, and a key minted per call is a different key each time. A retry with no key is the duplicate it was meant to avoid: a client that gets no answer cannot tell whether its request was lost on the way out or on the way back.

So `withRetries` mints one key and hands it to every attempt. It is **opt-in**: a transport that retried by itself would hide the load it was making, and the first anybody would know is an installation that cannot recover because its clients will not let it.

What is retried is what Convia said could end differently — `429` and `503` — plus a request that got no answer at all, which is safe only because a key is carrying it. `Retry-After` wins over the backoff, bounded.

**`409 conflict` is not retried**, and that is a compromise rather than a decision: Convia answers two different things with it, and they differ only in the message. See `M19-014`.

**Four routes under `/v1/me` read the key**: opening a room, posting a message here or in a room elsewhere, and inviting somebody. They are the ones where a repeat would otherwise make a second thing. The rest are already repeatable — joining a call twice is joining it once, and so is accepting an invitation twice — so a key there would ask Convia to remember an answer nothing is going to repeat.

**A person's keys are scoped to their account**, not to the first-party application every person on an installation shares. Scoping there would put them all in one key space, where two people picking the same value meet and one is handed the other's answer.

### Being told what happens

```ts
import { EventStream } from 'convia-sdk'

const stream = new EventStream()

const stop = stream.listen((event) => {
  if (event.type === 'message.posted') {
    // …
  }
})

stream.watch((condition) => {
  if (condition.kind === 'restarted') {
    // The cursor was older than Convia keeps. Re-read over REST.
  }
})

// Later
stop()
stream.close()
```

**Being told is an improvement on asking, never a replacement for being able to ask.** Everything the stream delivers can also be read over REST, and a consumer that cannot work without the stream has built something more fragile than the stream it depends on.

`condition` is a union rather than a boolean because the reasons a stream stops are not interchangeable:

| Condition | What it means | What to do |
| --- | --- | --- |
| `connecting` | Opening, or waiting to | Ask over REST meanwhile |
| `live` | Open; everything arrives in order | Nothing |
| `behind` | Convia dropped events this stream could not keep up with | Re-read what must not have a gap |
| `restarted` | The cursor was older than Convia keeps | Re-read over REST; the stream carries what happens next |
| `ended` | The session no longer authenticates | Sign in again — **nothing reconnects** |

Reconnection resumes from the last cursor by itself. A consumer that keeps the cursor somewhere surviving the process — a page reload, an application restarting — passes it as `after` to get what happened while nothing was connected at all.

### Joining a call

```ts
import { Calls, usable } from 'convia-sdk'

const invitation = await new Calls(convia).join(roomId)

// Use the address exactly as given, and reveal the credential only to present it.
connectSomething(invitation.url, invitation.token.reveal())
```

**The credential does not render itself.** The contract says never log it, and saying so is not enough when `console.log` is one keystroke and a structured logger serialises whatever it is handed — so `toString`, `toJSON`, Node's inspect hook and `Symbol.toPrimitive` all answer `[redacted]`, and `reveal()` is the one way out. Every call to it should be connecting rather than storing or reporting.

`invitation.url` is used exactly as given: a deployment commonly reaches its media infrastructure over a private network, so an address derived from Convia's own works in development and fails in production.

`expiresAt` **bounds when a connection may be opened, not how long it may last.** A client that tears down an established connection at that moment is ending a call for no reason.

## Versioning

The package follows semantic versioning, and what counts as a break is what somebody's code can observe:

| Change | Version |
| --- | --- |
| A new export, a new optional field, a new union member in a vocabulary | minor |
| An export removed or renamed; a parameter made required; a thrown type changed | major |
| A behaviour corrected to match the contract | patch, and named in the changelog |

**A new event type or error code is a minor change, not a major one.** The vocabularies are closed and additive by the contract's own rule, and the package's runtime lists carry an `isEventType` and an `isErrorCode` precisely so an installation newer than the package answers "I do not know this one" rather than being mistaken for something known. A consumer with an exhaustive `switch` over `EventType` will see a type error on upgrade — which is the point, and why those lists are held to the contract by the compiler.

### While it is `0.x`

It is `0.x` and unpublished. The only consumer is this repository's own interface, which takes it through the workspace. **Publishing before a second consumer exists would be taking on a compatibility promise with nobody on the other side**, so the package is `private` until that changes, which also stops an accidental `npm publish`.

Within `0.x`, a minor is where a break lands, as the convention has it.

## Deprecation

An export being removed goes through the same shape the API's own deprecation does, with the periods matched to it:

1. It is marked `@deprecated` in the source, with what to use instead.
2. It keeps working exactly as before.
3. It is removed no sooner than the notice below, counted from the release that announced it.

| What it was | Minimum notice |
| --- | --- |
| Documented here as stable | 180 days |
| Published but not documented here | 30 days |
| Marked `@experimental` | None |

Silent removal of something documented here is never acceptable. An export that only ever existed because the contract had a gap — and the gap was then fixed — is still an export somebody wrote code against.

## The examples

| File | What it shows |
| --- | --- |
| [`examples/reading.ts`](../sdk/examples/reading.ts) | Asking, and telling a refusal from a timeout from an unreachable installation |
| [`examples/listening.ts`](../sdk/examples/listening.ts) | Following a room, and knowing when a gap means going back to REST |
| [`examples/calling.ts`](../sdk/examples/calling.ts) | Joining a call, logging safely, and scheduling a rejoin |

## Not built

- **Publishing.** See above: a compatibility promise needs somebody on the other side of it.
- **Pagination iteration.** The routes that page do so by cursor, and nothing here wraps that yet; a consumer reads `next` and asks again.
- **The tenant surface**, which is `M20`.
