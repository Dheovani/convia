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
| `CONVIA_LIVEKIT_CLIENT_URL` | no | Where a *browser* reaches the media plane, if that is not where Convia does |

**Convia's address and the client's are not always the same.** A deployment
commonly reaches its media server on a private network that no browser can
resolve, so the address published to clients is configured separately. Unset,
it is derived from `CONVIA_LIVEKIT_URL` by swapping the scheme for `ws`, which
is right whenever one server answers both. Set it and it is taken as given,
because the case it exists for is precisely the one where the client's address
is not a transformation of Convia's.

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

The server is told to announce `127.0.0.1` as its address and publishes the two ports a browser carries media on in development mode, `7881/tcp` and `7882/udp`, so a page on this machine can join a call. It also sends its reports to `http://host.docker.internal:8080/media/reports`, which is a Convia running on the host at the port `scripts/dev` uses; change the URL in [`docker-compose.yml`](../docker-compose.yml) if yours runs elsewhere.

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

Five operations, which is what the implemented call flows need:

**A call starts, so a room is created.** Eagerly, before anyone joins, even though LiveKit would create it on the first connection anyway. That is the point: a call whose media session cannot be realized is ended rather than left holding its room, and that only means anything if Convia talks to the provider while the caller is still waiting. Creating lazily would move the first sign of a broken media plane to a participant failing to connect, long after Convia answered that the call had started.

**A call ends, so the room is deleted.** Best-effort: a provider that cannot be reached must never keep a conversation open in Convia that ended in reality. A room the provider no longer has is a success rather than a failure, because Convia asks it to reclaim empty rooms on its own.

**Someone Convia admitted needs a credential, so one is signed.** This is the only operation that makes no request at all: a LiveKit access token is signed locally and verified by the server when its holder connects. Admitting somebody therefore cannot time out and never reports the media plane unavailable, which means people can still be let into a running conversation during an outage that would prevent starting a new call.

**Someone Convia put out of a call is disconnected.** Leaving, being removed, and losing one's place in the room all close the person's connection at once, with a token that may act inside that one room and no other. Somebody already gone is a success.

**A report that somebody left is checked.** Convia asks the room whether the person is still connected before believing it, because a reloaded page opens its new connection before the old one is reported gone. See [ADR 0014](adr/0014-a-call-in-a-room-ends-when-its-people-leave.md).

The room is named after the call, and a room is never reused across calls.

**Room capacity is not sent to the provider.** ADR 0001 left open whether the media plane should enforce it too; it should not. Convia's capacity can be changed while a call is running, and a number copied to the provider at creation would then be stale in the restrictive direction — a participant Convia admitted would be refused by the provider, and Convia would have no way to explain it.

## What the media server reports

A person whose browser crashes never asks to leave, so the media server has to say so. It sends what happened to `POST /media/reports`, which Convia serves only when a media plane is configured, and a LiveKit server is told where to send it in its own configuration:

```yaml
webhook:
  api_key: devkey              # the key Convia is configured with
  urls:
    - https://convia.example/media/reports
```

**Reports have to reach Convia.** Without them a person whose connection dropped stays in the call as far as Convia knows, and a call in a room a person opened runs until somebody leaves it. The address is one the media server can reach, which is frequently a private one.

**Nothing is read before the signature is checked.** A report carries a token signed with the API secret, naming the API key and carrying a digest of the body. The algorithm is pinned, the issuer must be this deployment's key, and a minute of clock skew is allowed. Every way a signature can fail is the same `401`, charged against the same failure budget as a wrong API key.

Convia acts on three kinds and ignores the rest:

| The media server says | Convia |
| --- | --- |
| somebody connected | disconnects them again if they are not in the call: removed, gone, or the call is over |
| somebody's connection went away | asks whether they are still connected, and records them as having left only if not |
| a room finished | ends the call and records everybody left, when the call is in a room a person opened |

A call in a room a person opened ends when its last participant has left, recorded as ended by `system`. An application's call is never ended by a report. A report about a room Convia does not know is ignored, and one that cannot be checked because the media server did not answer is refused with `503`, so that it is sent again.

## When it fails

| What happened | What a client sees |
| --- | --- |
| The provider could not be reached, or did not answer in time | `503 unavailable` — retry |
| The provider understood the request and refused it | `500 internal_error`, logged with the detail — an operator must act |

The distinction is the reason there are two errors rather than one. Treating every failure as retryable turns a misconfiguration into an infinite retry loop; treating none as retryable turns a one-second outage into a failed call. A `5xx`, a `429`, or a timeout is retryable; a rejected credential or a malformed request is not.

Convia does not retry internally. It reports `503`, and the call is already ended by the time the client sees it, so a retry starts a new call rather than resuming one that half-exists.

## What is not implemented

- **Rate limits on issuing credentials.** See [`participants.md`](participants.md).
- **Reconciling rooms the provider still holds.** A session that could not be released is logged and not reclaimed automatically, and a report about a room Convia does not know is ignored rather than acted on.
- **Reconciling calls whose reports never arrived.** A call in a person's room whose last connection went away while reports could not reach Convia stays running until somebody leaves it. Nothing asks the media server who is connected on a schedule.
- **Retries and circuit breaking.** These should be shaped by measured failure modes, and there are none to measure.

## Security

The API secret is never logged, never returned, and never included in an error. It is held as a type that renders itself as `[redacted]` through `fmt`, through `%#v`, and through `slog`, so logging a whole configuration struct is harmless. Tests assert each of those paths, including against errors produced by a real server.

The tokens Convia signs for its own API calls live for one minute, carry only the single permission the request needs, and appear nowhere but the `Authorization` header of the request they were minted for. The one that disconnects somebody, or asks whether they are connected, is scoped to that call's room.

A report is attacker-supplied until its signature verifies, and the token it carries is parsed by the reviewed library `M12-001` chose rather than by hand. Nothing in a report is translated before it verifies, and what is translated is three identifiers: which room, which participant, and what happened. The provider's own identifiers, tracks and settings are never read.

The credential handed to a client is the one token that leaves Convia's process. It lives five minutes, is bound to one call and one participant, and carries no administrative permission at all — a moderator moderates through Convia's API, never through the media plane, so that every removal is authorized, recorded, and reflected in Convia's own state. It is a redacting type everywhere except the single line that writes it into the response, and a test asserts it never reaches a log.
