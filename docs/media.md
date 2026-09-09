# The media plane

Convia owns the control plane: applications, users, rooms, calls, participants, and the rules governing them. It does not transport audio and video. Something else does, and everything Convia knows about that something lives in [`internal/media`](../internal/media).

This document is for operators and contributors. **Nothing here appears in Convia's public API**, and that is enforced by a test rather than by review — see [ADR 0001](adr/0001-control-plane-media-plane-boundary.md).

## Running without one

A Convia with no media plane configured is a supported deployment, not a broken one. Rooms are created, calls start and end, participants join and are removed, the whole API behaves exactly as documented — and nobody can connect, because there is nothing to connect to.

This is what Convia does when none of the media variables are set, and it is what the service logs at startup:

```
no media plane is configured, so calls carry no audio or video
```

## Configuring one

| Variable | Required | Meaning |
| --- | --- | --- |
| `CONVIA_LIVEKIT_URL` | with the others | The LiveKit server's HTTP endpoint |
| `CONVIA_LIVEKIT_API_KEY` | with the others | The API key it should identify Convia by |
| `CONVIA_LIVEKIT_API_SECRET` | with the others | The shared secret Convia signs its requests with |
| `CONVIA_LIVEKIT_TIMEOUT` | no, defaults to `5s` | How long Convia waits for an answer |

**All or nothing.** Set none of the first three and Convia runs without a media plane. Set some of them and it refuses to start, naming the ones that are missing. That case is refused rather than tolerated because it is never what anybody meant: a deployment missing one value would otherwise start happily and be indistinguishable from one that meant to have no media plane at all, until somebody started a call and could not hear anybody.

**Production is stricter.** The URL must use `https`, and the secret must be at least 32 characters. The first is not about protecting the media: every request Convia makes carries a bearer token signed with the API secret, so a plaintext hop hands that token to anyone on the path. The second is because the secret is an HMAC key — a short one can be recovered offline by anyone holding a single signed token, and nothing about the deployment would look wrong while that happened.

The variables name their provider, unlike every other setting Convia has. That is deliberate: they configure a LiveKit server specifically, and a provider-neutral name would invite pointing them at something that does not speak its protocol. An operator deploying the media plane is the one person who has to know what they are deploying.

## Running one locally

The local LiveKit is opt-in, because most work on Convia does not need it:

```bash
docker compose --profile media up -d
```

That starts `livekit-server` on `127.0.0.1:7880` with the development key and secret in [`docker-compose.yml`](../docker-compose.yml). Point Convia at it by uncommenting the media block in your `.env`:

```bash
CONVIA_LIVEKIT_URL=http://127.0.0.1:7880
CONVIA_LIVEKIT_API_KEY=devkey
CONVIA_LIVEKIT_API_SECRET=a-development-secret-long-enough-to-be-accepted
```

Those credentials are development-only. They are committed on purpose, they grant nothing anywhere else, and they must never be reused. A real deployment takes its key and secret from the deployment environment or a secret manager.

Only the control API port is published. The ports a participant would connect media on are not, because nobody can connect yet; they arrive with the join sessions of M13.

### The integration tests

They are skipped unless all three are set:

```bash
export CONVIA_TEST_LIVEKIT_URL=http://127.0.0.1:7880
export CONVIA_TEST_LIVEKIT_API_KEY=devkey
export CONVIA_TEST_LIVEKIT_API_SECRET=a-development-secret-long-enough-to-be-accepted

go test ./internal/media/...
```

Each test creates rooms named after freshly generated call identifiers and deletes them afterwards, so runs never collide with each other or with anything else on the server.

These matter more here than integration tests usually do. Convia speaks LiveKit's HTTP API directly rather than through its Go SDK — [ADR 0002](adr/0002-livekit-over-http-rather-than-its-go-sdk.md) explains why — so the unit tests assert what Convia *sends*, and only a real server can confirm that what it sends is right. CI runs them against a pinned server on every push.

## What the adapter does

Two operations, which is all the implemented call flows need:

**A call starts, so a room is created.** Eagerly, before anyone joins, even though LiveKit would create it on the first connection anyway. That is the point: a call whose media session cannot be realized is ended rather than left holding its room, and that only means anything if Convia talks to the provider while the caller is still waiting. Creating lazily would move the first sign of a broken media plane to a participant failing to connect, long after Convia answered that the call had started.

**A call ends, so the room is deleted.** Best-effort: a provider that cannot be reached must never keep a conversation open in Convia that ended in reality. A room the provider no longer has is a success rather than a failure, because Convia asks it to reclaim empty rooms on its own.

The room is named after the call, and a room is never reused across calls.

**Room capacity is not sent to the provider.** ADR 0001 left open whether the media plane should enforce it too; it should not. Convia's capacity can be changed while a call is running, and a number copied to the provider at creation would then be stale in the restrictive direction — a participant Convia admitted would be refused by the provider, and Convia would have no way to explain it.

## When it fails

| What happened | What a client sees |
| --- | --- |
| The provider could not be reached, or did not answer in time | `503 unavailable` — retry |
| The provider understood the request and refused it | `500 internal_error`, logged with the detail — an operator must act |

The distinction is the reason there are two errors rather than one. Treating every failure as retryable turns a misconfiguration into an infinite retry loop; treating none as retryable turns a one-second outage into a failed call. A `5xx`, a `429`, or a timeout is retryable; a rejected credential or a malformed request is not.

Convia does not retry internally. It reports `503`, and the call is already ended by the time the client sees it, so a retry starts a new call rather than resuming one that half-exists.

## What is not implemented

- **Participant credentials and disconnection.** Nobody can connect yet. These arrive with the join sessions of M13, from a flow that exists, rather than being guessed at now.
- **Provider webhooks and events.** Convia has no internal event concept to translate them into yet.
- **Reconciling rooms the provider still holds.** A session that could not be released is logged and not reclaimed automatically.
- **Retries and circuit breaking.** These should be shaped by measured failure modes, and there are none to measure.

## Security

The API secret is never logged, never returned, and never included in an error. It is held as a type that renders itself as `[redacted]` through `fmt`, through `%#v`, and through `slog`, so logging a whole configuration struct is harmless. Tests assert each of those paths, including against errors produced by a real server.

The tokens Convia signs live for one minute, carry only the single permission the request needs, and appear nowhere but the `Authorization` header of the request they were minted for.
