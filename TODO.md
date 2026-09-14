# Convia Development Roadmap

This document is the operational development plan for Convia. It tracks what exists, what should be built next, why each milestone exists, and the conditions required to call a milestone complete.

## How to Use This File

- `[x]` means the item is implemented and has local evidence.
- `[ ]` means the item is not complete, even if design discussion has started.
- Keep task IDs stable so pull requests and issues can reference them.
- Work on the earliest incomplete milestone unless a production incident or explicit product decision changes priority.
- Do not create packages, infrastructure, or abstractions solely because they appear in this roadmap.
- Update the relevant checklist in the same pull request that implements an item.
- Add links to architecture decisions, API specifications, dashboards, or runbooks when they are created.
- A milestone is complete only when every exit criterion is satisfied.

## Priority Definitions

- **P0:** Required before the next architectural layer can be built safely.
- **P1:** Required for the first usable communication product.
- **P2:** Required for production readiness and external adoption.
- **P3:** Expansion work after the core platform is stable.

## Current Status

- **Current milestone:** M18 — Standalone Web Application. **The interface exists and a person can use it.** `M18-016` and `M18-017` deliver the shell on the public session surface alone, which is `M18-015` held from the first screen rather than audited at the end: sign in, the three zones, the sidebar with unread counts, and reading, writing, editing and withdrawing in a room. It is compiled into the binary and served from the **same origin as the API** — the decision the rest hangs from, recorded in [ADR 0009](docs/adr/0009-convia-serves-its-own-interface-from-its-own-origin.md), and what makes CORS unnecessary rather than configured. Serving a page from this origin is also what made security headers Convia's problem, so `docs/sessions.md`'s standing gap there is closed. `M18-003` then closed the last way the interface depended on an application key to be usable: a person opens rooms, adds the people they already share one with, and leaves. `M18-018` then took the interface off its timers: a person has an event stream of their own, **authorized per room rather than per tenant**, and changes of membership are announced (`M18-020`), so being added to a room reaches a sidebar the moment it happens — [ADR 0010](docs/adr/0010-a-persons-stream-is-authorized-per-room.md). `M18-022` then replaced operator-created accounts with ones a person registers, identified by the fingerprint of a key their password seals — [ADR 0011](docs/adr/0011-an-account-is-local-and-its-identifier-is-its-key.md) — and `M18-023` let people on different installations share a room, by invitation, with every request between installations signed by the person's own key — [ADR 0012](docs/adr/0012-a-room-lives-on-one-installation-and-visitors-sign.md). What else remains in M18 is calls (`M18-004` to `M18-008`) and the interface's own polish, including `M18-019`.
- **Found by building it, and fixed here:** four of the seven person-facing message routes M31 added changed state with **no CSRF origin check**. The check existed but lived inside the session handler, so it was a habit each new route had to remember, and M31's did not. It is now middleware on the surface, and a test walks the route table and refuses to let a state-changing browser route exist without it. Separately, `convia account create` was printing no password: a CodeQL autofix accepted earlier had discarded it, and since Convia stores only a one-way digest and has no password reset, every account created in that window is unusable forever. Restored, with a source-level regression test so the next autofix fails a build.
- **Discovered while planning M18, and it changes the roadmap:** the UI specification is built around **chat**, and Convia has no messaging domain — not in the code, and nowhere in M00–M30. It is now `M31`, at P1, and it is built **before** the interface so that no screen exists without an API behind it.
- **Previous milestone:** M17 — Presence. **Complete except metrics** (`M17-011`), which is now the third item deferred for the same reason and the third pointing at M22: Convia still has no metrics pipeline. M17 also closed the gap M16's own tests had left open — CI declared no Redis, so every cross-instance test in the repository was silently skipped there; the integration job now runs one, and both M16's relay tests and M17's convergence tests actually execute. M16 is complete except its metrics (`M16-010`), and closed `M14-014`'s remaining half. M15 is complete except manual redelivery (`M15-010`), which needs the operator-facing authority and audit trail that M20 defines. M14 is complete except the resume cursor (`M14-006`), which needs a durable event log, and the metrics (`M14-013`), which need a metrics pipeline. M14 also supplied what `M12-010` was waiting for: a Convia-owned event to translate a provider event into. M13 remains in progress: what is left there is the rate limit (`M13-008`) and the SDK-facing connection guide (`M13-010`). M13 also closed `M12-005` and `M12-006`. M12's remaining items need webhook verification (`M12-009`, `M12-010`) or measured failure modes (`M12-013`, `M12-014`). M11, M10, M09 and M08 are complete. M07 is complete, `M07-009` having been closed by `M18-002`. Phase 0 and M06 are complete.
- **License:** PolyForm Noncommercial License 1.0.0. Convia is free for noncommercial use, and commercial rights are reserved. See [`LICENSE.md`](LICENSE.md).
- **Next implementation milestone:** **`M18-004` — calls in Convia's own product.** A signed-in person can do everything with a room except talk in it: calls are still reachable only with an application key. So it starts with the session surface for calls, exactly as rooms and messages did, and then the browser's media connection (`M18-005` to `M18-008`). **M32 — Contacts and Direct Rooms is deliberately after the interface**, not before: every part of it is a consent surface, and consent surfaces are the ones most punished by speculative design. Convia's own interface now consumes it from the outside, but **no browser has entered a call**: the media path is proved as far as the LiveKit handshake and not a step further. That is still the largest unexamined assumption in the project, and it is what `M13-010` is waiting on
- **Current tenancy capability:** applications can be created, listed, retrieved, renamed with optimistic concurrency, suspended, activated, and deleted through the operator API, which requires an operator credential carrying an applications scope
- **Current identity capability:** an application's people can be resolved into Convia users, listed, retrieved, updated with optimistic concurrency, suspended, activated, and deleted under `/v1/users`, with the tenant taken from the presented credential rather than named in the path
- **Current credential capability:** an application can be issued opaque API keys carrying explicit scopes, which Convia stores only as a digest, verifies in constant time, and can expire or revoke with immediate effect; suspending an application withdraws every key it holds. An application manages its own keys under `/v1/credentials`, and cannot issue one carrying scopes it does not itself hold. See [`docs/authentication.md`](docs/authentication.md)
- **Current public contract:** [`api/openapi.yaml`](api/openapi.yaml), OpenAPI 3.0.3, covering the operational health and readiness endpoints, the authenticated tenant endpoints for users, credentials, rooms, calls, participants, and every resource added since, the authenticated operator endpoints for applications, operator credentials, and every tenant resource, a person's own surface under `/v1/me`, the signed routes installations use with each other under `/v1/peer`, every security scheme, and the shared error, pagination, idempotency, and correlation components
- **Current backend capability:** process startup, environment configuration, graceful shutdown, `GET /health`, and the HTTP transport baseline documented in [`docs/api-conventions.md`](docs/api-conventions.md): request correlation identifiers, structured access logs, panic recovery, strict JSON decoding, and one JSON error schema for every failure
- **Current persistence capability:** PostgreSQL through `pgxpool`, reversible embedded migrations run by `convia migrate`, an isolated integration-test database per test, and the `applications`, `users`, `credentials`, `operator_credentials`, `rooms`, `room_members`, `idempotency_keys`, `calls`, `participants`, `invitations`, `webhook_endpoints`, `webhook_deliveries`, `messages`, `room_read_state`, `accounts`, `sessions`, `room_invitations`, `peer_nonces`, and `remote_rooms` tables, with a call's media session stored beside it and never published
- **Current authentication capability:** every request on the tenant and operator surfaces carries a key, verified on each request and enforced against explicit scopes inside the domain rather than at the handler. Application and operator keys are separate families — separate tables, prefixes, and scope vocabularies — so a key offered to the wrong surface is refused on its shape before any lookup. Failed attempts are budgeted per caller address, resolved through `CONVIA_TRUSTED_PROXIES` where a proxy is configured, so a flood of unusable keys cannot be paid for indefinitely and one client cannot spend another's budget. A person's browser carries a session cookie instead (`M18-002`), and another installation signs each request with the person's own key (`M18-023`)
- **Current invitation capability:** an application issues an invitation to one of its calls, for one of its users or for a guest — somebody Convia has no account for, identified by the invitation alone and by nothing else it ever learns about them. The application receives, exactly once, a credential in a third key family (`cvi_`) that Convia stores only as a digest. The invitee presents it themselves on a surface of its own, and redeeming produces a place in the call and the credential to connect with, in one request. Expiry is derived rather than stored, withdrawal outranks a redemption that already happened, and declining is the invitee's own act. None of the rules about who may be in a call are restated: redeeming goes through the participants package, so an invitation cannot undo a removal. See [`docs/invitations.md`](docs/invitations.md)
- **Current communication capability:** rooms. An application can create durable rooms addressed by an alias it chose, or anonymous ones for a single occasion, and can list, filter, update under optimistic concurrency, close, reopen, and delete them under `/v1/rooms`. Aliases are unique per application and stay reserved after deletion, and a creation can carry an `Idempotency-Key` so a retry after a timeout produces no second room. Conversations are held in those rooms as calls: an application starts one, ends it, and reads the history of a room under `/v1/calls`, with a room holding one call at a time. People take part in those calls as participants: an application admits its own users, reads who is there, promotes a moderator, and removes someone, with joining idempotent by the person and the room's capacity enforced on arrival. See [`docs/rooms.md`](docs/rooms.md), [`docs/calls.md`](docs/calls.md), and [`docs/participants.md`](docs/participants.md)
- **Current media capability:** people can talk. A call asks LiveKit for the room its conversation happens in and releases that room when it ends, and an admitted participant is issued a short-lived credential their client connects with — verified end to end against a real media server, which accepts what Convia signs and refuses what is tampered with. `internal/media` is the boundary between the control plane and whatever transports audio and video; `internal/media/livekit` is the first implementation behind it, speaking the provider's HTTP API directly rather than through its Go SDK. A call whose session cannot be realized is ended rather than left holding its room, a retryable failure answers `503 unavailable` while a refusal answers `500`, and the API secret cannot render itself into a log. The credential is bound to one call and one participant and carries no administrative permission, so moderation stays a control-plane decision. A deployment may still run with no media plane at all, which is `media.Absent` and a supported configuration. Tests assert that media infrastructure never leaves `internal/media`, that only the composition root names a provider, and that a real pinned LiveKit accepts what Convia sends. See [`docs/media.md`](docs/media.md), [ADR 0001](docs/adr/0001-control-plane-media-plane-boundary.md), and [ADR 0002](docs/adr/0002-livekit-over-http-rather-than-its-go-sdk.md)
- **Current event capability:** an application opens one WebSocket at `/v1/events` and is told what happened while it is still news — a call starting or ending, a roster changing, an invitation declined. Nothing else streams: a change the application made through a request that already returned the new state is not announced back to it, and the reasoning for every inclusion and exclusion is recorded in `M14-001`. The stream carries **nothing upstream**, which is how "no media over WebSocket" became a property of the design rather than a rule: a client that sends anything is disconnected, so there is no message that could carry audio and none that could widen what it receives. `events:read` grants the connection and grants no content — each event reaches only a credential that could already have read the thing it is about. Events are announced from the same place they are audited, so the live account and the recorded one cannot drift. Nothing is stored: a subscriber that falls behind is disconnected with a status of its own rather than quietly losing an event, because a client must never believe it saw everything when it did not. See [`docs/events.md`](docs/events.md) and [ADR 0003](docs/adr/0003-a-one-directional-in-process-control-event-stream.md)
- **Current webhook capability:** an application registers a destination and Convia reaches out to it. Every delivery is signed with a secret returned exactly once, carries a stable identifier so a repeat can be recognized, and is a **row** rather than an attempt — it exists before the first try and outlives the last, so an application can ask what Convia tried to tell it, how many times, and what its receiver answered. Failures are retried on a published, deterministic schedule and given up on after roughly two hours or four hours of age, whichever comes first; a destination that fails twenty times in a row is disabled with a reason and resumed by the application once it is fixed. **Convia refuses to be used as a proxy**: no plain http outside development, no address that is not the public internet, no proxy consulted, no redirect followed — and the check runs on the address being connected to at every attempt rather than on a hostname resolved once, because that is the only version of it that cannot be raced. Delivery is at-least-once and says so first. See [`docs/webhooks.md`](docs/webhooks.md) and [ADR 0004](docs/adr/0004-queuing-a-durable-delivery-inside-the-request.md)
- **Current scaling capability:** a deployment can run more than one instance without the event stream narrowing to whichever one a client happened to reach, and without presence depending on which machine answered. `CONVIA_REDIS_URL` is what turns both on, and it is an operational requirement rather than a tuning option — several instances with it unset is the one configuration that is quietly wrong, so each instance says at startup which of the two it is. Redis holds two things and nothing else: the channel instances tell each other over, which stores no keys at all, and presence, whose every key expires and whose absence after everything lapses is asserted by a test. Nothing durable is in either, so no eviction policy can reclaim anything Convia cannot do without. Broadcasting cannot block or fail the request that produced the event, and an unreachable Redis narrows the stream back to one instance rather than failing anything — readiness deliberately ignores it. See [`docs/events.md`](docs/events.md) and [ADR 0005](docs/adr/0005-redis-for-what-instances-tell-each-other.md)
- **Current presence capability:** an application says who is available and Convia holds it for as long as it keeps saying so. Assertions are per **device** and answers are per **person**: the strongest claim wins — `busy`, then `online`, then `away` — so a deliberate do-not-disturb is not undone by a laptop in another room. `offline` is derived rather than asserted and cannot be sent. **No caller's clock reaches a deadline**: a request carries a lifetime, and the expiry is computed by the store — Redis's own clock, inside the script that writes it — which is why two instances never disagree about when a claim lapsed. Whether somebody is **in a call** is deliberately not a field: that is durable, it is in PostgreSQL, and a tripwire keeps this package from importing the roster to find out. Presence streams as `presence.changed` and is the **one event type Convia refuses to deliver by webhook**, because a redelivered presence report arrives after it stopped being true. A heartbeat that changes nothing announces nothing. An unreachable store answers `503` rather than reporting that everybody went offline, which would be a false statement about people rather than a degraded one. See [`docs/presence.md`](docs/presence.md) and [ADR 0006](docs/adr/0006-presence-is-a-claim-with-a-timer.md)
- **Current first-party capability:** a person signs in to Convia's own product. A session is the fourth credential family, `cvs_`, and it lives **only in a cookie** — so an application key offered to the session surface and a cookie offered to the tenant surface are each refused by shape, before any lookup, because neither is even read where it does not belong. **A session carries no authority over a tenant**, and nothing anywhere converts one into a `credentials.Principal`: the shortcut of handing a signed-in person the first-party application's scopes would let anybody who signed in mint a permanent API key outliving their session, their password, and their account, so it is made unrepresentable rather than forbidden. Passwords are **argon2id** where every other Convia secret is a plain SHA-256 digest, which contradicts `M07-004` on purpose — the rule is chosen by where the entropy came from, and a session token, which Convia generates, goes back to the fast digest. Every sign-in failure answers identically, the password is checked **before** the lifecycle so suspension cannot be probed without one, and an unknown username is hashed against a decoy whose parameters a test pins to the ones in force. **A person creates their own account** with a username and a password, and nothing needs configuring (`M18-022`): an account's identifier is the fingerprint of its key, the key is sealed by the password, and a person is named to others by a handle with a check character. See [`docs/sessions.md`](docs/sessions.md), [ADR 0007](docs/adr/0007-a-session-is-a-person-not-a-tenants-authority.md) and [ADR 0011](docs/adr/0011-an-account-is-local-and-its-identifier-is-its-key.md)
- **Current messaging capability:** a room remembers what was said in it. **A conversation is a room** -- there is no second noun for it -- so messages outlive the calls held in the place, which is what makes a history something to open rather than something to lose. Order is a per-room `sequence` allocated by PostgreSQL under the room's row lock, never a timestamp: since M16 each instance stamps `created_at` from its own clock, and a chat that shows a reply above the thing it replies to is broken in a way people notice. Allocating the position optimistically was tried first and **measured** -- nine or ten of sixteen simultaneous appends to one room failed, because every writer that loses a collision re-reads the same highest sequence as every other loser -- so the room's row lock is what serializes them, and the unique index went from being the arbiter of a race to being the assertion that the lock works. An author is a user or a guest's invitation and exactly one of the two, the shape `participants` already uses for the same people. An edit records *that* it happened and not what it said before; a deletion leaves a tombstone that keeps its position, so a history never closes over a hole and slides messages past a client's cursor. A message is the first field in Convia allowed to contain a line break, because every other caller-supplied text is a label. Nothing anybody said reaches the log, asserted against the real log rather than against intent. An application reaches all of it under `/v1/rooms/{id}/messages` and `/v1/messages/{id}`, with `messages:read` and `messages:write` kept **separate from `rooms:read`/`rooms:write`** -- listing rooms is administration, reading their history is reading people's conversations, and a deployment must be able to grant one without the other. Withdrawing is a POST to a sub-resource because the request has to name which person is withdrawing, and naming somebody else's message answers `409` rather than `403`: the credential already carries everything the surface can grant, and a different scope would not help. Three types stream -- `message.posted`, `message.edited`, `message.deleted` -- and all three are **deliverable by webhook where presence is not**, because an event carries no body: it names the message and the room, so a retried delivery cannot write older text over newer and no conversation is copied to an endpoint outside Convia. Read state is **durable** where presence is advisory, is a position rather than a set, and only ever moves forward so two devices cannot pull a badge backwards; `unread` is derived on every read and excludes a person's own messages and withdrawn ones. Erasing somebody **redacts** what they wrote rather than removing it -- deleting it would take the conversation away from the people still in it -- though nothing calls erasure yet, because M06's retention-window job still does not exist. A person reaches the same history as themselves, authorized by room membership: see the first-party conversation capability below. See [`docs/messages.md`](docs/messages.md) and [ADR 0008](docs/adr/0008-a-conversation-is-a-room-and-its-order-comes-from-the-database.md)
- **Current membership capability:** a room knows who belongs to it. Migration `00009` declined to model this and gave a reason M18 invalidated -- that Convia held no credentials for an application's people -- so it arrives now, when something finally depends on it. **It gates people, not applications**: an application's key already carries full authority over its own rooms, and membership is what the session surface checks, where somebody acts as themselves. Deriving it from participation was rejected, because a chat-first room is one somebody joins and reads before any call happens in it. A member has **no role and no lifecycle**: a call has something to moderate and is the record of one occasion, while membership is current state. Adding is idempotent by the person and only a change is audited; removing leaves what they said, because the messages are the room's record of a conversation that did happen. The scopes are `members:read` and `members:write` rather than the room ones, because a credential that renames rooms has never been able to touch people and folding the two together would widen every key already issued. See [`docs/rooms.md`](docs/rooms.md)
- **Current first-party conversation capability:** a signed-in person reads and writes their own conversations under `/v1/me`, which is the first place a session principal decides anything. **The request carries no author**: an application names which of its people is speaking because it is acting on their behalf, and a person cannot name anybody, because naming somebody would mean naming somebody else -- so writing as another person is unrepresentable rather than refused, and the schema forbids the field so that a client sending one is told rather than quietly ignored. Room membership is what it decides against, and **a room somebody is not in answers `404`, never `403`**: a refusal that separates "not yours" from "does not exist" confirms to somebody outside a conversation that the conversation is happening. Editing and withdrawing check authorship instead, because somebody who wrote a message was in the room when they wrote it and being removed since does not hand their words to anybody else. The sidebar answers rooms and unread counts together -- one statement for the counts, one for the rooms -- because a request per row on the screen is what makes an interface feel slow. **A session still grants no authority over a tenant**; nothing converts one into an application key. See [`docs/messages.md`](docs/messages.md)
- **Current user interface capability:** Convia serves its own interface from its own origin (`M18-016`). A person creates an account, signs in, opens rooms, adds the people they already share one with, invites people on other installations and joins their rooms, and reads and writes conversations that update as they happen. Calls are not in it yet.

## Recommended Next Actions

Complete these in order before starting feature development:

1. [x] **NEXT-001:** Initialize Git in this directory if it will be the repository root.
2. [x] **NEXT-002:** Create or select the canonical GitHub repository without changing the Go module path until the repository path is known.
3. [x] **NEXT-003:** Push the current foundation to a non-protected branch.
4. [x] **NEXT-004:** Confirm that the `CI`, `Security`, and `Container` workflows all start on GitHub.
5. [x] **NEXT-005:** Resolve any hosted-runner incompatibility, especially Go 1.26.5 or CodeQL availability. The toolchain moved to the patched Go 1.26.6, the container smoke-test script was rewritten for the runner's ShellCheck version, and the PostgreSQL readiness probes were corrected to check TCP rather than the local socket.
6. [x] **NEXT-006:** Enable GitHub code scanning where the repository plan and visibility support it. The repository is public, so the CodeQL job runs unconditionally.
7. [x] **NEXT-007:** Configure branch protection to require successful CI, security, and container checks.
8. [x] **NEXT-008:** Enable Dependabot alerts and review the first scheduled update run.
9. [x] **NEXT-009:** Decide whether pull requests require one approving review before merge. One approval is required, in a `require-approval` ruleset that repository admins bypass for pull requests only. The checks, linear history, and force-push rules live in a separate `main-protection` ruleset with an empty bypass list, so they bind maintainers too.
10. [x] **NEXT-010:** Begin M02 only after the default branch has a green hosted build.

---

## Phase 0 — Engineering Foundation

### M00 — Backend Foundation

**Priority:** P0
**Status:** Complete locally
**Depends on:** Nothing
**Goal:** Establish the smallest executable and testable Go service foundation.

- [x] **M00-001:** Preserve the repository engineering instructions in `AGENTS.md`.
- [x] **M00-002:** Use the canonical local module name `convia` without inventing a repository URL.
- [x] **M00-003:** Create the executable composition root at `cmd/convia/main.go`.
- [x] **M00-004:** Separate environment configuration into `internal/config`.
- [x] **M00-005:** Separate HTTP server construction into `internal/server`.
- [x] **M00-006:** Provide `GET /health` with a stable JSON response.
- [x] **M00-007:** Handle `SIGINT` and `SIGTERM` through a cancelable context.
- [x] **M00-008:** Shut down HTTP requests with a bounded timeout.
- [x] **M00-009:** Configure HTTP host and port through environment variables.
- [x] **M00-010:** Test configuration defaults, overrides, and invalid values.
- [x] **M00-011:** Test the health endpoint and unsupported HTTP methods.
- [x] **M00-012:** Add a non-root multi-stage container image.
- [x] **M00-013:** Document local run, test, build, and container commands.
- [x] **M00-014:** Pass `go fmt ./...`, `go vet ./...`, `go test ./...`, and `go build ./...` locally.

**Exit criteria:** The service starts without external infrastructure, reports health, stops gracefully, and passes the required local Go checks.

### M01 — Repository Governance and Hosted CI

**Priority:** P0
**Status:** Complete
**Depends on:** M00
**Goal:** Make every proposed change pass repeatable quality, security, and container checks before merge.

- [x] **M01-001:** Add a CI workflow for formatting, `go vet`, tests, coverage, and build.
- [x] **M01-002:** Run the test suite with Go's race detector in CI.
- [x] **M01-003:** Run Staticcheck with a pinned tool version.
- [x] **M01-004:** Upload the coverage profile as a short-lived workflow artifact.
- [x] **M01-005:** Add a security workflow using `govulncheck`.
- [x] **M01-006:** Add CodeQL analysis with extended security queries.
- [x] **M01-007:** Add a scheduled weekly security scan.
- [x] **M01-008:** Add a container build workflow.
- [x] **M01-009:** Verify the configured runtime container user is non-root.
- [x] **M01-010:** Smoke test the containerized health endpoint.
- [x] **M01-011:** Give workflows minimal explicit `GITHUB_TOKEN` permissions.
- [x] **M01-012:** Pin third-party actions to full commit SHAs.
- [x] **M01-013:** Disable persisted checkout credentials.
- [x] **M01-014:** Add concurrency cancellation for superseded workflow runs.
- [x] **M01-015:** Add job timeouts to prevent stuck runners.
- [x] **M01-016:** Configure weekly Dependabot updates for GitHub Actions.
- [x] **M01-017:** Configure weekly Dependabot updates for Go modules.
- [x] **M01-018:** Initialize the Git repository and publish it to GitHub.
- [x] **M01-019:** Confirm all workflows pass on the first GitHub push.
- [x] **M01-020:** Confirm workflows run correctly for pull requests from branches and forks.
- [x] **M01-021:** Enable Dependabot alerts and security updates in repository settings.
- [x] **M01-022:** Enable code scanning and confirm CodeQL results reach the Security tab. The repository is public, so CodeQL runs on every push and pull request.
- [x] **M01-023:** Configure default-branch protection with required status checks.
- [x] **M01-024:** Require branches to be current before merge.
- [x] **M01-025:** Decide whether signed commits or signed tags are required. Commits are signed locally with an SSH signing key, and a tag ruleset requires signed tags. Signed commits are deliberately not enforced on branches: squash merges replace an author's commits with one that GitHub signs using its own key, so the rule would attest to GitHub's signature rather than the author's. Enforce it once a second contributor exists.
- [x] **M01-026:** Add a pull request template with test, security, API, migration, and documentation checkboxes.
- [x] **M01-027:** Add issue templates for bugs, features, and security-safe reports.
- [x] **M01-028:** Add `SECURITY.md` with private vulnerability reporting instructions.
- [x] **M01-029:** Add `CONTRIBUTING.md` when contributors beyond the initial maintainers exist.
- [x] **M01-030:** Record the names of required checks in repository documentation.
- [x] **M01-031:** Validate GitHub Actions workflow files with a pinned Actionlint version in CI.
- [x] **M01-032:** Add the project license and record its commercial-use policy. Convia uses the PolyForm Noncommercial License 1.0.0: free for noncommercial use, with commercial rights reserved.
- [x] **M01-033:** Keep the declared Go toolchain on a patch release without known standard-library vulnerabilities.

**Exit criteria:** The default branch is protected, all hosted checks pass, dependency updates are automated, and repository security reporting is configured.

---

## Phase 1 — Public Control-Plane Foundations

### M02 — HTTP and API Baseline

**Priority:** P0
**Status:** Complete. M02-018 was answered by M18, once the interface's origin was decided.
**Depends on:** M01
**Goal:** Define consistent HTTP behavior before adding public domain endpoints.
**Contract:** [`docs/api-conventions.md`](docs/api-conventions.md), implemented by `internal/api` and `internal/server`.

- [x] **M02-001:** Decide the initial public API prefix, expected to be `/v1` unless an ADR selects another scheme.
- [x] **M02-002:** Keep operational endpoints such as health outside public API versioning.
- [x] **M02-003:** Define the standard JSON success envelope policy, including whether simple resources are returned directly.
- [x] **M02-004:** Define a Convia-owned JSON error schema with stable machine-readable codes.
- [x] **M02-005:** Add a request ID middleware that accepts or generates safe correlation IDs.
- [x] **M02-006:** Return the request ID in response headers and structured logs.
- [x] **M02-007:** Add panic recovery that logs internal context without exposing stack traces to clients.
- [x] **M02-008:** Add bounded request body handling.
- [x] **M02-009:** Reject unsupported content types on endpoints that accept JSON.
- [x] **M02-010:** Reject malformed JSON and unknown fields consistently.
- [x] **M02-011:** Define pagination fields and upper bounds before the first list endpoint.
- [x] **M02-012:** Define timestamp serialization as UTC RFC 3339 with documented precision.
- [x] **M02-013:** Define public identifier formatting without leaking future database internals.
- [x] **M02-014:** Add server-level read, write, idle, and header timeout tests where behavior is non-trivial.
- [x] **M02-015:** Add route-not-found and method-not-allowed JSON responses if public API consistency requires them.
- [x] **M02-016:** Add tests for every middleware success and failure path.
- [x] **M02-017:** Document reverse-proxy assumptions and trusted forwarded-header behavior.
- [x] **M02-018:** Decide CORS behavior only after the standalone UI origin model is known. **There is none, on purpose.** The interface is served from the same origin as the API (`M18-016`, [ADR 0009](docs/adr/0009-convia-serves-its-own-interface-from-its-own-origin.md)), so there is no cross-origin request to allow, and a setting nobody needs is one somebody can get wrong.
- [x] **M02-019:** Ensure operational endpoints cannot accidentally inherit public authentication requirements.
- [x] **M02-020:** Document an endpoint implementation checklist for later milestones.

**Exit criteria:** New endpoints can follow one documented transport contract for errors, IDs, JSON, limits, and logging.

### M03 — API Specification and Compatibility Policy

**Priority:** P0
**Status:** In progress. Every item is complete except M03-010, which cannot start until a released baseline exists.
**Depends on:** M02
**Goal:** Treat Convia's public API as a stable product interface from its first domain endpoint.
**Contract:** [`api/openapi.yaml`](api/openapi.yaml), governed by [`docs/api-compatibility.md`](docs/api-compatibility.md) and validated by `internal/server/contract_test.go`.

- [x] **M03-001:** Select OpenAPI as the REST contract format or record a justified alternative.
- [x] **M03-002:** Add the initial API specification containing shared schemas and errors.
- [x] **M03-003:** Document naming conventions for resources, fields, actions, and enums.
- [x] **M03-004:** Document additive versus breaking API changes.
- [x] **M03-005:** Define deprecation headers and minimum deprecation periods.
- [x] **M03-006:** Define idempotency expectations for mutation endpoints.
- [x] **M03-007:** Define optimistic concurrency behavior where updates can conflict.
- [x] **M03-008:** Define pagination cursor opacity and stability requirements.
- [x] **M03-009:** Add OpenAPI syntax validation to CI. Validation runs inside `go test`, so it needs no separate toolchain.
- [ ] **M03-010:** Add a breaking-change detector once a released baseline exists.
- [x] **M03-011:** Add contract tests that compare implemented routes with the specification.
- [x] **M03-012:** Document API lifecycle states: experimental, preview, stable, deprecated, removed.
- [x] **M03-013:** Define public error code ownership and review requirements.
- [x] **M03-014:** Define how SDK generation or handwritten SDKs consume the contract.
- [x] **M03-015:** Record that media-provider-specific fields are forbidden in public contracts.

**Exit criteria:** The API contract is machine-readable, CI-validated, versioned, and governed by an explicit compatibility policy.

### M04 — PostgreSQL Foundation

**Priority:** P0
**Status:** In progress. The foundation is complete; M04-012 and M04-014 stay open by their own conditions.
**Depends on:** M03 and the first persistence-requiring domain decision
**Goal:** Introduce PostgreSQL only when a real durable resource is ready to be implemented.
**Documentation:** [`docs/database.md`](docs/database.md), implemented by `internal/database`.

- [x] **M04-001:** Select a PostgreSQL driver based on current Go support and operational requirements. `pgx/v5` through `pgxpool`.
- [x] **M04-002:** Select a migration tool with reversible and CI-friendly behavior. `goose/v3`, driven as a library with embedded SQL files.
- [x] **M04-003:** Add database URL configuration with no committed credentials.
- [x] **M04-004:** Add connection pool configuration with safe development defaults.
- [x] **M04-005:** Validate mandatory production database settings at startup. Production requires a verified TLS mode.
- [x] **M04-006:** Establish migration filename and ordering conventions.
- [x] **M04-007:** Add a local PostgreSQL service through Docker Compose.
- [x] **M04-008:** Add an integration-test database isolated from developer data. Each test creates and drops its own database.
- [x] **M04-009:** Add migration-up verification in CI.
- [x] **M04-010:** Add migration-down or forward-recovery tests according to the chosen policy.
- [x] **M04-011:** Add readiness behavior that distinguishes process health from database availability.
- [ ] **M04-012:** Add transaction helpers only after multiple operations need shared transaction ownership.
- [x] **M04-013:** Define SQL query timeouts and context cancellation behavior.
- [ ] **M04-014:** Instrument pool saturation and query latency when observability is introduced.
- [x] **M04-015:** Document backup, restore, and point-in-time recovery expectations before production.

**Exit criteria:** Migrations and integration tests run repeatably, connection failures are explicit, and no domain schema leaks through the public API.

### M05 — Applications and Tenancy

**Priority:** P0
**Status:** Complete
**Depends on:** M04
**Goal:** Represent standalone Convia and external consumers as isolated Convia-owned applications.
**Documentation:** [`docs/applications.md`](docs/applications.md), implemented by `internal/applications`.

- [x] **M05-001:** Define the `Application` domain concept and invariants.
- [x] **M05-002:** Decide whether standalone Convia is represented by a first-party application record. It is an ordinary first-party record, so the standalone product exercises the same tenancy paths as external consumers.
- [x] **M05-003:** Define application lifecycle states. `active`, `suspended`, and `deleted`.
- [x] **M05-004:** Define immutable public application IDs.
- [x] **M05-005:** Add the applications database migration. The schema encodes the identifier format, the name bounds, and the lifecycle states; the transitions between them are implemented with the service.
- [x] **M05-006:** Add repository behavior for create, get, list, update, and lifecycle transitions.
- [x] **M05-007:** Add an application service enforcing invariants independently of HTTP.
- [x] **M05-008:** Add administrative HTTP endpoints only for immediately required operations. Create, list, retrieve, rename, suspend, activate, and delete.
- [x] **M05-009:** Ensure every application-scoped query includes tenant isolation. The users store offers no lookup by identifier alone, so the owning application is always part of the query.
- [x] **M05-010:** Test cross-application access denial. Reading another application's user is reported as missing rather than refused, and is covered by integration tests.
- [x] **M05-011:** Define application display metadata separately from security credentials.
- [x] **M05-012:** Define safe deletion, suspension, and retention semantics.
- [x] **M05-013:** Add audit events for security-relevant application changes. Structured log events; durable audit storage is M21.
- [x] **M05-014:** Document bootstrap of the first administrative application.
- [x] **M05-015:** Add API contract coverage for application responses and errors.

**Exit criteria:** Applications are durable, isolated tenants with tested lifecycle rules and no media-provider coupling.

### M06 — External Users and Identity Mapping

**Priority:** P0
**Status:** Complete
**Depends on:** M05
**Goal:** Let each consuming application map its own users to stable Convia identities without sharing identity namespaces.
**Documentation:** [`docs/users.md`](docs/users.md), implemented by `internal/users`.

- [x] **M06-001:** Define Convia's internal user identity and application-scoped external subject.
- [x] **M06-002:** Decide whether one human can be linked across applications and document privacy implications. Convia never links users across applications.
- [x] **M06-003:** Define uniqueness rules for `(application_id, external_subject)`. Unique regardless of status, so a subject never points at two users.
- [x] **M06-004:** Define display-name, avatar, and metadata ownership. There is no avatar column; an application stores its URL in metadata.
- [x] **M06-005:** Set strict size and shape limits for application-provided metadata.
- [x] **M06-006:** Add user and identity-mapping migrations. One table suffices because users are never linked across applications.
- [x] **M06-007:** Add create-or-resolve behavior with idempotent semantics.
- [x] **M06-008:** Add suspension and deletion behavior. Both transitions are idempotent, deletion is terminal until erasure, and attribute updates arrived with it as `PATCH` with optional `If-Match`. A deleted external subject stays reserved, so resolving it again is refused with `409 conflict` rather than reviving the user.
- [x] **M06-009:** Prevent cross-application identity enumeration.
- [x] **M06-010:** Add tests for duplicate mappings and concurrent creation.
- [x] **M06-011:** Define data export and erasure boundaries.
- [x] **M06-012:** Define how standalone-product accounts map to the same domain model.
- [x] **M06-013:** Add audit events for identity lifecycle changes. Creation is audited without the external subject or display name.
- [x] **M06-014:** Document which user attributes are authoritative in Convia.
- [x] **M06-015:** Add public API examples using only Convia-owned concepts.

**Exit criteria:** Applications can safely resolve their users without identity collisions, enumeration, or provider-specific data.

### M07 — Authentication, Credentials, and Authorization

**Priority:** P0
**Status:** Complete. `M07-009` was closed by `M18-002`.
**Depends on:** M05 and M06
**Goal:** Authenticate applications and users with explicit, least-privilege permissions.

- [x] **M07-001:** Write a threat model for application credentials, user sessions, and media grants. Recorded in `docs/authentication.md`, with media grants scoped out until the media plane exists.
- [x] **M07-002:** Decide the first supported server-to-server authentication mechanism. Opaque bearer keys, not JWTs: Convia is the only verifier, so signing buys nothing and would cost immediate revocation.
- [x] **M07-003:** Define credential identifiers separately from secret material. The identifier is public and travels inside the key, so verification reads one row by primary key before comparing any secret.
- [x] **M07-004:** Store only securely hashed or otherwise appropriately protected credentials. SHA-256 of a ~130-bit random secret, compared in constant time; a slow KDF would only add latency per request.
- [x] **M07-005:** Define credential creation, display-once, rotation, expiration, and revocation. Rotation is deliberately not an endpoint: composing issue and revoke is what gives zero downtime.
- [x] **M07-006:** Define scopes using Convia domain operations. Four scopes over users and credentials, required rather than defaulted.
- [x] **M07-007:** Add authentication middleware after credential verification exists. It wraps only the tenant-facing routes, and a route that acts for an application is not registered at all without it.
- [x] **M07-008:** Add authorization at service boundaries, not only HTTP handlers. Each domain exposes an `Authorized` type that cannot be built without a verified principal, takes the tenant from it, and refuses an ungranted operation before the service runs.
- [x] **M07-009:** Define first-party standalone UI session behavior separately from external API credentials. **Closed by `M18-002`**: a session is a credential family of its own, lives only in a cookie, and carries no authority over a tenant. It was deferred to M18 rather than done here: browser sessions need the origin and cookie model of the standalone web application, which does not exist yet. What M07 settled is the *server-to-server* separation — operator credentials are their own family, so an application key can never administer Convia — which is a different question from how a person's browser holds a session.
- [x] **M07-010:** Add replay resistance where signed requests or tokens require it. Nothing requires it: no request is signed and no token is replayable in a way TLS does not already prevent, because a bearer key proves possession rather than authorizing one specific request. It becomes real work when media grants arrive, and is tracked there.
- [x] **M07-011:** Add rate limits for authentication failures. Sixty failed attempts per caller address, refilled over a minute, checked before the key is read so an exhausted caller costs a map lookup rather than a database query. Only failures are charged, so a working key is never limited. Running behind a proxy needs trusted-forwarder support first, which does not exist yet.
- [x] **M07-012:** Avoid logging raw credentials, bearer tokens, or signed media grants. Asserted by tests over both the audit log and the stored row.
- [x] **M07-013:** Add positive and negative tests for every scope.
- [x] **M07-014:** Add cross-tenant authorization regression tests.
- [x] **M07-015:** Add audit events for credential and permission changes.
- [x] **M07-019:** Add operator credentials so the administrative endpoints are authenticated rather than gated. A separate `operator_credentials` table, `cvo_` token prefix, and scope vocabulary (`applications:*`, `tenants:*`, `operators:*`), so a key offered to the wrong surface is refused on its shape before any lookup. `CONVIA_ADMIN_API` is removed entirely. The first credential is minted by `convia operator issue`, which needs database access, because issuing one over the API requires presenting one.
- [x] **M07-020:** Add trusted-forwarder configuration so Convia can run behind a reverse proxy. `CONVIA_TRUSTED_PROXIES` names the networks whose `X-Forwarded-For` is believed; the chain is walked from the right, skipping trusted hops, because a proxy appends what it saw and anything a client invented sits further left. Unset by default, so nothing changes for anyone who did not ask. A caller connecting directly is charged to its own address whatever it claims.
- [x] **M07-016:** Document emergency credential revocation procedures. `docs/runbooks/credential-revocation.md`, with every command exercised against a running instance. Records that the production path is direct SQL, because the operator API is refused outside development, and that a SQL revocation writes no audit event.
- [x] **M07-017:** Define clock-skew tolerance for expiring tokens. Zero, and correctly so: expiry is evaluated by the one process that issued the credential, against its own clock and the stored timestamp, so there is no second party whose clock could disagree. Tolerance becomes necessary only for signed tokens verified elsewhere, which is the media plane.
- [x] **M07-018:** Add key rotation tests before introducing signed tokens. `TestRotationKeepsTheFleetServed` and `TestRotationKeepsTheOperatorServed` prove the overlap the documented procedure depends on: a replacement authenticates while the original still works, and revoking the original leaves the replacement untouched. There is no signing key to rotate until the media plane introduces one.

**Exit criteria:** Every protected operation has an authenticated principal, explicit scope checks, safe credential lifecycle, and denial-path tests. **Met**, for both the tenant and operator surfaces: no route acting with anyone's authority is registered without its verifier, authorization lives in the domain rather than the handler, and every operation is tested with its scope, without it, and with none.

---

## Phase 2 — Communication Control Plane

### M08 — Room Domain

**Priority:** P1
**Status:** Complete
**Depends on:** M07
**Goal:** Introduce Convia-owned rooms without any LiveKit terminology in public or domain contracts.

- [x] **M08-001:** Define room purpose, lifecycle, visibility, and capacity invariants. Recorded in `docs/rooms.md`: a room is the durable place, a call the occasion. Lifecycle is open, closed, deleted. Capacity is recorded and bounded; enforcement needs calls to enforce against. Visibility is deliberately absent, see M08-010.
- [x] **M08-002:** Decide which room properties are mutable after creation. Alias, name, metadata, and capacity are; identity and timestamps are not. The lifecycle is mutable only through its own operations, never by writing a status field, so a transition keeps its meaning and its audit event.
- [x] **M08-003:** Define durable rooms versus ephemeral sessions. The alias is the distinction: with one, a room is addressable by a name the application chose and returned to indefinitely; without one it is anonymous and created for the occasion.
- [x] **M08-004:** Define application-scoped room aliases and uniqueness. Unique within one application through a partial index, so anonymous rooms do not collide. The alias stays reserved while a room is deleted, because a cached alias must never come to point at a different room.
- [x] **M08-005:** Add room persistence and indexes. Migration `00006`, with the alias uniqueness, listing, and status-filter indexes, and every invariant the domain relies on encoded as a constraint.
- [x] **M08-006:** Add room create, read, list, update, close, and archive services. Create, get, get-by-alias, list, update, close, reopen, and delete. Archiving is what closing means: a fourth retained-but-invisible state would duplicate `deleted` without adding a distinction anyone could act on.
- [x] **M08-007:** Add idempotency for room creation. `POST /v1/rooms` and its operator counterpart honor `Idempotency-Key`: the room is created at most once, and a repeat replays the original response with its status and entity tag. The claim is a single statement, so two simultaneous requests cannot both proceed. A key is scoped to the caller, expires after 24 hours, and is reclaimed by the request that reuses it. A server error or a rate-limited refusal releases the key instead of storing it, so a retry is a real attempt. The mechanism is `internal/idempotency` and knows nothing about rooms; M09-007 adopts it by marking a route.
- [x] **M08-008:** Add public REST endpoints and OpenAPI schemas. Fourteen routes across both surfaces, with `Room`, `RoomPage`, `RoomMetadata`, `RoomStatus`, and the request schemas. The contract test proves routes, security, and error codes match the implementation in both directions.
- [x] **M08-009:** Add pagination and filtering tests. Keyset paging newest first, the `status` filter, deleted rooms excluded unless asked for, and alias lookup answering with one room.
- [x] **M08-010:** Add room membership or access-policy concepts only when required. Not required, and deliberately absent. Convia holds no credentials for an application's people, so it could not enforce a policy about who may enter; the application already knows. A model Convia could not enforce would be worse than none.
- [x] **M08-011:** Prevent application A from resolving application B's room. Every store statement carries the application in its predicate, the tenant surface takes it from the credential rather than the request, and crossing answers `404` rather than `403` so existence is not confirmed. Asserted for get, update, close, reopen, delete, and listing.
- [x] **M08-012:** Define room closure behavior for active calls. Closing stops new calls; a conversation in progress runs to its end. Ending one because an administrator tidied a listing would be the wrong default. Ejecting participants is a call-domain operation and belongs to M09.
- [x] **M08-013:** Define retention and deletion behavior. A deleted room is retained rather than destroyed, keeping the deletion recoverable and the alias reserved. Erasure is one mechanism serving users and rooms alike and belongs to the milestone that builds it.
- [x] **M08-014:** Add domain, repository, HTTP, and concurrency tests. Scope tables covering every operation on both surfaces in three positions, tenant isolation, optimistic concurrency through storage, lifecycle repeatability, and alias reservation across deletion.
- [x] **M08-015:** Add room audit events. Creation, closure, reopening, and deletion. Neither the alias nor the name is recorded, because both are labels an application chose and either may say something about the people using the room; a test asserts they stay out.

**Exit criteria:** Applications can manage isolated rooms through stable Convia APIs with complete lifecycle and authorization tests. **Met.** Rooms are created, addressed by an alias the application chose, listed, filtered, updated under optimistic concurrency, closed, reopened, and deleted on both surfaces; every operation is tested with its scope, without it, and with none; a tenant reaching another's room receives `404` rather than `403`; and creation can be retried safely.

### M09 — Call Lifecycle

**Priority:** P1
**Status:** Complete
**Depends on:** M08
**Goal:** Model calls as Convia control-plane resources independently from media infrastructure.

- [x] **M09-001:** Define call states and allowed transitions. Two states, `active` and `ended`, with one transition between them and `ended` terminal. Both are reachable, which is the rule the vocabulary is held to. Recorded in `docs/calls.md`.
- [x] **M09-002:** Define whether a room can have multiple historical calls and one active call. Exactly that: many over time, one at a time. Only an active call occupies a room, so a room that has hosted a hundred conversations can host another.
- [x] **M09-003:** Define call initiation, ringing, active, ending, ended, and failed semantics as needed. Needed: initiation and the two states. Deliberately not modeled: `ringing` needs invitations (M10), `ending` needs a media plane that takes time to tear down (M11), and `failed` needs something that can fail to establish a call (M11). Each would be a state nothing could enter, and a lifecycle with unreachable states teaches a client rules that are not true. Why a call ended is an `end_reason` instead.
- [x] **M09-004:** Define actor and reason fields for transitions. The actor is `application` or `operator` and is taken from the verified credential, never from a request field: a body that could name the actor would let an application record an operator's name against its own decision. The reason is optional free text, because the reasons a conversation ends are not Convia's to enumerate.
- [x] **M09-005:** Add durable call records and transition history. Migration `00008`, with the history as columns (`ended_at`, `ended_by`, `end_reason`) rather than a separate table. The lifecycle is linear and terminal, so a table would hold exactly one row per ended call, joined on every read, to say what the columns already say. A table becomes the right shape once a call can move between states more than once, which the media plane will bring; moving to one then is a data migration, not a redesign. A check constraint keeps the two shapes a row may take from disagreeing.
- [x] **M09-006:** Enforce one-active-call constraints transactionally where required. A partial unique index on `(room_id) WHERE status = 'active'`, so PostgreSQL settles two simultaneous starts. An application-level check would read, decide, and lose the race in between. Eight concurrent starts produce exactly one call, asserted against a real database.
- [x] **M09-007:** Add idempotent start and end operations. Starting honors `Idempotency-Key`. Ending does not accept one and does not need one: repeating an end already succeeds and returns the call unchanged, so a key would only add a way for a retry to be refused.
- [x] **M09-008:** Add call REST endpoints and contract schemas. Nine operations across both surfaces, with `Call`, `CallPage`, `CallStatus`, `CallActor`, `CallMetadata`, and the request schemas. The contract test proves routes, security, and error codes match the implementation in both directions, and that every response body validates against the schema it claims.
- [x] **M09-009:** Reject invalid transitions with stable public errors. A room already hosting a call and a closed room both answer `409 conflict`; a deleted room answers `404 not found`, because closed is a state the application can undo and deleted is gone from the API. Ending an ended call is not an invalid transition but a repeat, and succeeds.
- [x] **M09-010:** Define behavior when the media provider is temporarily unavailable. Defined in `docs/calls.md`: the call record is the control-plane truth and the media session is realized from it, so a session that cannot be realized ends the call with a reason rather than leaving it occupying its room. A room blocked by a call that never happened is the worse failure. Nothing implements this because there is no provider to be unavailable; that is M11.
- [x] **M09-011:** Keep provider session IDs internal. The public `Call` schema declares `additionalProperties: false` and a contract test asserts twice: that the schema names only Convia-owned fields, and that the body a client actually receives carries only those. A provider identifier added later has to defeat both.
- [x] **M09-012:** Add concurrent transition tests. Eight goroutines starting a call in one room produce exactly one, with every loser refused and no extra row written.
- [x] **M09-013:** Add call history filtering and pagination. Keyset paging newest first, a `status` filter, and a room-scoped listing. Every call is returned, ended ones included: a call history is what the listing is for, which is the opposite of rooms where a deleted room is hidden unless asked for.
- [x] **M09-014:** Add call audit events and timestamps. Starting and ending are audited with the call, room, application, new state, and actor. Neither the metadata nor the end reason is recorded, because both are composed by the application and either may say something about the people in the call; a test asserts they stay out.
- [x] **M09-015:** Define reconciliation behavior for stale active calls. Defined in `docs/calls.md` and deliberately not built. Convia cannot detect a stale call today: with no media plane it has no evidence about a call independent of the requests it received, so every active call is active as far as anything can tell. When the media plane exists, reconciliation ends such calls with a `system` actor, an actor left out of the enum until something produces it.

**Exit criteria:** Calls have a durable, concurrency-safe Convia lifecycle that remains meaningful without a media provider. **Met.** A call is a durable record with a linear lifecycle nothing about media is needed to understand; one call per room is settled by the database rather than by application logic; both surfaces are tested with each scope, without it, and with none; a tenant reaching another's call receives `404` rather than `403`; and no part of the contract names or implies a provider.

### M10 — Participants and Invitations

**Priority:** P1
**Status:** Complete
**Depends on:** M09
**Goal:** Represent who may join and who actually participates in a call.

- [x] **M10-001:** Distinguish room membership, invitation, and call participation. Recorded in `docs/participants.md`. Room membership stays unmodelled for the reason M08 gave: Convia holds no credentials for an application's people. An invitation is permission to join that has not been used yet, and is only authorization once the party presenting it is not the party that granted it, which is M13. Participation is who actually joined, and it is what this slice builds.
- [x] **M10-002:** Define participant lifecycle states. `joined`, `left`, and `removed`, all reachable. `removed` is not a reason attached to `left`: it is terminal with a policy of its own, because someone a moderator put out cannot rejoin that call. That policy is the whole justification for a third state, and it is why the distinction is not folded into a reason field the way a call's ending is.
- [x] **M10-003:** Define participant roles and capabilities using Convia concepts. `moderator` and `member`, and the difference is one Convia can enforce: a moderator may remove someone and change a role. Roles about media — publishing, subscribing, sharing a screen — are deliberately absent, because they describe capabilities of a media plane that does not exist and a role that promises what nothing enforces is worse than no role. An omitted role is `member`, since a mistyped field must never hand someone the authority to remove other people.
- [x] **M10-004:** Define invite creation, acceptance, rejection, expiration, and revocation. All five, and the reason they waited is what shaped them: an invitation is only authorization when the party presenting it is not the party that granted it, so it is a **credential** — a third key family alongside `cvk_` and `cvo_`, stored only as a digest, presented by the invitee on a surface of its own. `Authorized` has no `Redeem` and no `Decline`, which makes an application completing its own invitation unrepresentable rather than merely forbidden. Expiry is derived from the row instead of stored, so nothing has to sweep and no invitation ever reads as usable after it has stopped being. Withdrawal outranks a redemption that already happened, because the ordinary reason to revoke a link is that it reached the wrong person. Redeeming again is allowed and expected: joining is idempotent by the person, so a dropped client returns to the participation it had.
- [x] **M10-005:** Add participant and invitation persistence. Participants in migration `00009`; invitations in `00011`, with the digest length, the guarantee that every invitation expires, the rule that a redemption names the participation it produced, and the mutual exclusion of declining and redeeming all encoded as constraints. There is deliberately **no** uniqueness on `(call_id, user_id)`: an application may reissue a lost link, and two invitations for one person redeem into the same participation anyway.
- [x] **M10-006:** Enforce call capacity and uniqueness rules. Uniqueness by a partial unique index on `(call_id, user_id) WHERE status = 'joined'`. Capacity by a transaction that takes a row lock on the call before counting and inserting: counting and then inserting would let two joins both see the last seat free and both take it, and no application-level check closes that window. Ten simultaneous arrivals at a three-seat room admit exactly three, asserted against a real database.
- [x] **M10-007:** Add join authorization independent of media token issuance. Whether someone may join is decided entirely in the control plane: the call must be active, the person must be one of this application's active users, the room's capacity must not be reached, and they must not have been removed from that call. No media token is involved, and none exists.
- [x] **M10-008:** Add leave and forced-removal behavior. Both are separate operations, because leaving is what a person does and removal is what a moderator does to someone else, and the record must not confuse them. Both are repeatable, and a repeat never overwrites who removed someone the first time nor converts a removal into a departure.
- [x] **M10-009:** Define reconnection behavior without creating duplicate participants. Joining is idempotent by the person and needs no idempotency key: a client whose network dropped and came back is the same person, and the response is `201` when it admitted them and `200` when they were already there. A person who genuinely left and returns becomes a new participation, so the call keeps both stints.
- [x] **M10-010:** Define guest participation requirements. A guest is somebody with no Convia user, and **Convia learns nothing about them**: no name, no address, no identity of any kind. Their participation is identified by the invitation they redeemed, and the application that sent it is the only party that knows who is behind it — which is the same reasoning that keeps display names out of rosters, applied where it has the most force. That identity does everything an account would: one invitation is one presence, so a reconnecting guest returns to their own seat; two guests are two people; capacity counts them, because capacity belongs to the room rather than to how somebody arrived; and removal is terminal, because the participation their invitation maps to is exactly what they would present again. Migration `00012` makes `participants.user_id` nullable, adds `invitation_id`, requires exactly one of the two, and adds the guest equivalent of the presence-uniqueness index. Asking for neither a person nor a guest is refused rather than read as a guest invitation, because a request that forgot to name somebody must not become a link anybody can use.
- [x] **M10-016:** Keep the guest path unreachable from the tenant API. `AdmitGuest` cannot verify the invitation it is handed — invitations depend on participants, so depending back would be a cycle — and it therefore trusts its caller completely. What makes that safe is that it is **absent from the interface** the authorization wrapper and both HTTP handlers consume: no route, no scope, no application-facing operation reaches it, and `Admission` has no field that could name an invitation. Two tests assert both, so an application cannot seat a guest by naming an invitation it does not hold, one that was revoked, or one belonging to somebody else.
- [x] **M10-011:** Add REST endpoints and OpenAPI schemas. Participants done as before; invitations add four tenant operations and two on the invitation surface, with `Invitation`, `IssuedInvitation`, `InvitationPage`, `InvitationStatus`, `Redemption`, the request schema, and an `InvitationKey` security scheme. The scopes `invitations:read` and `invitations:write` are separate from `participants:*` because an invitation is a credential that leaves Convia: managing a roster must not imply minting ways into a call. The contract test proves routes, security, and scopes match the implementation in both directions.
- [x] **M10-012:** Add concurrent join and capacity tests. Eight simultaneous joins by one person produce one participation and one identifier; ten simultaneous arrivals at a three-seat room admit exactly three, with every loser refused and no extra row written.
- [x] **M10-013:** Add authorization tests for moderator actions. A member cannot remove or promote; a moderator can; a moderator from another call cannot lend authority; a moderator who left has none. Convia does not decide whether the application may remove someone, because it already may — what Convia decides is whether the participant the application named was entitled to, since Convia is what holds the roster.
- [x] **M10-014:** Add participant audit and call-history events. Joining, leaving, removal, and a role change are audited with the participant, call, application, person, state, role, and removing authority. The removal reason is not recorded, because it is composed by the application and may say something about the person removed; a test asserts it stays out.
- [x] **M10-015:** Define privacy rules for participant lists. A roster names people only by their Convia user identifier, with no display name. A roster is read by more callers and stored in more places than a user record is, so copying a name into every entry would put it where nobody asked for it and would duplicate something the application owns and can change. A contract test asserts the published schema carries only identifiers and states, and that a response never contains the person's name.

**Exit criteria:** Participation is authorized, capacity-safe, reconnectable, auditable, and independent from LiveKit identities. **Met for participation and invitations:** every join is authorized in the control plane alone, capacity is settled by a lock rather than by hope, a reconnection returns the participant already present, every transition is audited without application-composed text, and nothing in the domain or the contract names a media provider. Invitations add a credential Convia enforces rather than records: it is refused on its shape when offered to the wrong surface, it stops working when its tenant is suspended, and an end-to-end test shows an invited person reaching a real media server holding nothing but the link they were sent. Guests close the milestone: somebody with no account at all does the same, and Convia holds nothing about them beyond the invitation that let them in.

### M11 — Internal Media Boundary

**Priority:** P1
**Status:** Complete
**Depends on:** M09 and M10
**Goal:** Create the smallest internal abstraction that prevents LiveKit from leaking into the domain or public API.

- [x] **M11-001:** Enumerate the exact media operations required by implemented call flows. Two: open a session when a call begins, and close it when the call ends. Issuing a participant the credentials to connect and disconnecting one who was removed are deliberately absent, because nobody can connect yet and their shape would be a guess that M13 would have to work around.
- [x] **M11-002:** Define the interface in the consuming internal package. `mediaPlane` is declared unexported in `internal/calls`, which is what consumes it; `internal/media` holds only the Convia-owned types, the failures, and the implementation Convia ships with.
- [x] **M11-003:** Use Convia-owned request and response types at the boundary. `media.SessionRequest` carries a Convia call identifier and nothing else; `media.Session` carries an opaque reference the adapter produced. Capacity, participants, and permissions are all decided in the control plane, so restating them at the boundary would create two places to be right about one rule.
- [x] **M11-004:** Keep provider room names, grants, tokens, and metadata internal. The session reference is stored beside the call rather than on it: `calls.Call` has no field for it, reading it means asking the store by name, and no public representation has anywhere to put it. This is unrepresentable rather than merely tested. The alternative would not have been dangerous today, since responses are built field by field and nothing serializes a domain call directly; what it buys is that the guarantee holds for code nobody has written yet, at the cost of one extra read when a call ends. A test follows a reference through the domain, a read, a listing, and the audit trail, the last being the most realistic way one would have escaped.
- [x] **M11-005:** Define error translation from provider failures to Convia service errors. `media.ErrUnavailable` becomes `503 unavailable`, a new error code added because telling a client `internal_error` for a transient outage would mislead it into not retrying. `media.ErrRejected` is logged with its detail and answered `500`, because an operator has to act.
- [x] **M11-006:** Define retryable versus terminal provider failures. `media.Retryable` is the single place that decides. Collapsing the two would be wrong in both directions: treating every failure as retryable turns a misconfiguration into an infinite retry loop, and treating none as retryable turns a one-second outage into a failed call.
- [x] **M11-007:** Define idempotency expectations for provider operations. Convia asks for a session exactly once per call and remembers the answer, so an adapter is never asked to open one for a call that already has one and never has to guess whether a second request means a second room. Releasing is asked for once, when the call actually transitions to ended; a repeated end releases nothing further. Both are asserted.
- [x] **M11-008:** Add a deterministic fake for service-level tests only if the interface is justified. It is justified: `AGENTS.md` permits an interface for architectural isolation, and here the isolation is the requirement rather than a side effect. The fake lives in the call package's tests rather than in production code, because that is the only thing that needs it; it moves if M12 or M13 need one too.
- [x] **M11-009:** Test that provider failures do not corrupt call state. A call whose session cannot be realized is ended and its room is free for the next one, with the attempt kept in the history and its reason recorded. An ending never depends on the media plane: a provider that cannot be reached must not keep conversations open in Convia that ended in reality.
- [x] **M11-010:** Record an ADR describing the control-plane/media-plane boundary. `docs/adr/0001-control-plane-media-plane-boundary.md`, the first ADR in the repository. It records what was decided, what was deliberately left out and why, what the boundary costs, and what would cause it to be revisited.
- [x] **M11-011:** Add an architecture test or review check preventing provider imports outside adapters. `internal/media/boundary_test.go` asserts that media infrastructure is imported only from under `internal/media`, and that the public contract never names a provider. The import check works from an inverted list — every dependency that is *not* media infrastructure is named with its reason — because listing the providers instead would mean guessing which one a contributor might reach for, and the guess would be wrong exactly when it mattered. It passes trivially today, which is the point: it is a tripwire left for M12.
- [x] **M11-012:** Avoid designing abstractions for unsupported providers. There is one intended provider and the boundary is shaped for the operations implemented flows need, not for portability in advance. What it provides is containment: nothing about a provider reaches the domain or the contract, so replacing one is work confined to one package.

**Exit criteria:** Implemented call flows depend on a narrow internal media capability, and no public contract contains LiveKit-specific concepts. **Met.** Starting and ending a call go through a two-operation interface declared in the consuming package; the provider's own reference has no field in any domain or public type; and a test reads the specification and fails on any provider name appearing in it.

### M12 — LiveKit Adapter

**Priority:** P1
**Status:** In progress
**Depends on:** M11
**Goal:** Implement the initial media plane behind the internal boundary.

- [x] **M12-001:** Pin a supported LiveKit server and Go SDK version. The server is pinned to `livekit/livekit-server:v1.13.6` in `docker-compose.yml` and in CI. **The Go SDK was deliberately not adopted**, which is a departure from this item recorded in [ADR 0002](docs/adr/0002-livekit-over-http-rather-than-its-go-sdk.md): `github.com/livekit/protocol` was measured at 161 modules, including the complete pion WebRTC stack, NATS, Redis, Prometheus, OpenTelemetry, and zap, and `AGENTS.md` forbids the control plane from carrying media or importing a custom WebRTC implementation. Convia needs two JSON requests and a signed token, so it speaks the documented room service API from `net/http` and adds one dependency, `github.com/golang-jwt/jwt/v5`, which has none of its own.
- [x] **M12-002:** Add LiveKit endpoint, key, and secret configuration with strict validation. `CONVIA_LIVEKIT_URL`, `CONVIA_LIVEKIT_API_KEY`, `CONVIA_LIVEKIT_API_SECRET`, and an optional `CONVIA_LIVEKIT_TIMEOUT`. The rule is all or nothing: none set means no media plane, which is a supported deployment, and some set is refused at startup naming what is missing — that case is never intentional and would otherwise be indistinguishable from the first until somebody started a call. Production additionally requires `https` and a secret of at least 32 characters, because every request carries a bearer token signed with it.
- [x] **M12-003:** Ensure secrets are never logged or returned through APIs. `media.APISecret` renders as `[redacted]` through `Stringer`, `GoStringer`, and `slog.LogValuer`, with compile-time assertions so that dropping one becomes a build error rather than a silent leak. Reading the real value takes a deliberate `Reveal`. The tests cover the paths a credential actually escapes by: a configuration struct printed with `%v` or `%+v` while debugging, a structured log record, a failure to load, and the error text produced by a real server refusing a real token.
- [x] **M12-004:** Map Convia calls to internal provider rooms. One call to one room, named after the call identifier as it stands: it is already unique and already prefixed, and a name derived by a rule rather than stored is one a future reconciliation can recompute without a lookup. A room is never reused across calls, because one that outlived its call would let a stale credential reach a conversation it was never issued for.
- [x] **M12-005:** Map Convia participant capabilities to least-privilege provider grants. Every admitted participant gets the same grant — join this one room, publish, subscribe — because that is what a call is. **A moderator gets nothing extra, deliberately.** Handing a client `roomAdmin` would let it remove people directly on the provider, where Convia would neither authorize it nor find out, leaving the control plane's account of the conversation quietly wrong. A distinction in the role vocabulary arrives when one exists that is worth enforcing.
- [x] **M12-006:** Issue short-lived media connection credentials. Five minutes, decided by the control plane rather than the adapter so that the policy is readable in one place instead of buried behind the boundary. Issuing makes no request to the provider at all — the token is signed locally and verified when its holder connects — so admitting somebody cannot time out and works during an outage that would prevent starting a new call.
- [x] **M12-007:** Implement provider room creation only if eager creation is required. It is required, and the reason is M11's own decision rather than the provider's behaviour: LiveKit would create the room on the first connection, but a call whose session cannot be realized is ended rather than left holding its room, and that only means anything if Convia talks to the provider while the caller is still waiting. Creating lazily would move the first sign of a broken media plane to a participant failing to connect, long after Convia answered that the call had started.
- [ ] **M12-008:** Implement participant removal and room termination operations. **Room termination is done**, and is best-effort: a provider that cannot be reached must never keep a conversation open in Convia that ended in reality, and a room the provider no longer has is a success rather than a failure, because Convia asks it to reclaim empty rooms on its own. **Participant removal is deferred to M13**: there is no participant in a provider room until Convia issues someone the credentials to create one, and the identity scheme those rooms use is decided when the tokens are minted.
- [ ] **M12-009:** Verify provider webhook signatures before trusting events. **Deferred with `M12-010`.** The dependency chosen in `M12-001` was chosen partly for this: verifying a webhook parses attacker-supplied tokens, which is where hand-rolled JWT handling accepts forgeries, and the library is already present and reviewed.
- [ ] **M12-010:** Translate provider events into Convia-owned internal events. **The envelope now exists** — M14 defines `events.Event` and the closed vocabulary it carries — so what was blocking this is gone. What remains is `M12-009`: a provider event cannot be translated before its signature is verified, and translating an unverified one would let anybody who can reach the webhook endpoint announce anything to every subscriber of a tenant.
- [x] **M12-011:** Add adapter unit tests around mapping and error translation. They assert what Convia **sends** — method, path, body fields, the exact grant inside the signed token — and not only what it does with the answer, because the wire format is this package's responsibility now rather than an SDK's. Error translation is covered by a table over the statuses a provider actually answers with, including the case where a wrong endpoint path answers `404` and must not be mistaken for a missing room.
- [x] **M12-012:** Add integration tests against an isolated LiveKit container. Gated on `CONVIA_TEST_LIVEKIT_*` so `go test ./...` stays runnable with no infrastructure, and always set in CI against the pinned image. Each test names its rooms after freshly generated call identifiers, so runs never collide. They earned their place immediately: the obvious reading of the provider's permission model is that `roomAdmin` scoped to a room authorizes deleting it, and it does not — `DeleteRoom` requires `roomCreate`, and a token carrying only `roomAdmin` is answered `401`. Nothing but a real server would have said so.
- [ ] **M12-013:** Add timeout, retry, and circuit-breaking behavior based on measured failure modes. **Timeouts are done**, configurable and defaulting to five seconds, with an exhausted one classified as retryable because nothing is known about a request that was never answered. **Retry and circuit breaking are deferred**, as this item itself requires: there are no measured failure modes yet. Retrying inside the adapter would also fight the layer above, which already ends the call and answers `503` so the client can decide.
- [ ] **M12-014:** Add reconciliation for control-plane/provider divergence. **Deferred.** It needs an operation that lists what the provider holds, which is a third call added for a flow nothing runs yet. The leak it would repair is bounded meanwhile: rooms are created with a ten-minute empty timeout, so a session Convia failed to release is reclaimed by the provider rather than kept forever.
- [x] **M12-015:** Document local LiveKit setup without exposing it to external consumers. [`docs/media.md`](docs/media.md), written for operators and contributors. The local server is an opt-in Compose profile, because most work on Convia does not need one, and only the control API port is published: nobody can connect yet, so the media ports arrive with M13. A test still asserts that no provider name appears in the public contract.

**Exit criteria:** Authorized Convia participants can obtain media access while all LiveKit details remain internal and integration-tested. **Half met.** The details are internal and integration-tested: three tests enforce the containment — media infrastructure imports, provider package imports, and provider names in the specification — and a real pinned server confirms the wire format on every push. Participants cannot obtain media access yet, which is M13.

### M13 — Client Session Bootstrap

**Priority:** P1
**Status:** In progress
**Depends on:** M10 and M12
**Goal:** Provide clients one Convia endpoint for joining a call and receiving short-lived connection instructions.

- [x] **M13-001:** Define the public join-session response without provider-specific field names. `JoinSession` carries a participant, a call, an address, a credential, and an expiry. A contract test asserts the schema forbids unnamed properties and publishes nothing else, and that the served body mentions no provider, no room name, and no grant.
- [x] **M13-002:** Include only the connection data required by supported clients. The address is separately configurable because Convia routinely reaches the media plane on a private network a browser cannot resolve; publishing the address Convia uses would hand every client something that cannot work. Unset, it is derived by swapping the scheme, which is right when one server answers both.
- [x] **M13-003:** Bind media grants to application, user, call, participant, and capabilities. The credential's identity is the Convia participant — opaque, unique, and already the handle removal uses — and its room is that call's own media session, which only the call package can read. The application and the user are bound by what Convia checked before issuing rather than by a claim in the token, since a claim nothing verifies would be decoration. Capabilities are uniform, and `M12-005` says why.
- [x] **M13-004:** Set short expiration and document renewal behavior. Five minutes, and the trade is stated rather than hidden: the credential is presented once to open a connection and the connection outlives it, so what a short life bounds is how long a copy taken from a log or an old device still works. The cost is that a client which drops after expiry cannot reconnect with it and asks for another. Renewal is calling the same endpoint again.
- [ ] **M13-005:** Prevent reuse after participant removal or call termination where technically possible. **Issuing stops immediately**, which is the enforcement that matters: eligibility is re-decided on every request, so someone removed, suspended, or whose call ended gets nothing however recently they were admitted. Both are asserted against the database and end to end. **A connection already open is not severed**, which is the remaining half: it needs the media plane to eject a live participant, and it is bounded meanwhile by the five-minute lifetime.
- [x] **M13-006:** Add idempotent join-session creation. Unlike the other participant operations, this one mints something new on every call, so a request that timed out would otherwise leave a usable credential behind that nobody received. Verifying it needed care: the token is a signature over claims whose finest resolution is one second, so two credentials issued in the same second are byte-identical for reasons that have nothing to do with idempotency, and an assertion without a second of separation passes either way.
- [x] **M13-007:** Add authorization, capacity, suspended-user, and ended-call denial tests. Six denials against a real database — left, removed, suspended, ended, another tenant's participant, and one that never existed — each asserting the media plane was never even asked. Capacity is not among them because it is enforced on arrival rather than on issuing: someone already admitted is already counted, and refusing them a credential because the room later filled would put a person in a call who cannot hear it. Scope is covered by the shared table, which requires the write scope rather than the read one.
- [ ] **M13-008:** Add rate limits for token issuance. **Deferred, and not silently.** Every write endpoint is equally exposed to a caller holding a valid key, and minting a second credential for a participant who already has one grants nothing the first did not, so the exposure is resource exhaustion rather than privilege. Limiting this one endpoint alone would be arbitrary; it belongs with a general per-tenant limit, next to the failed-authentication budget that already exists.
- [x] **M13-009:** Ensure tokens and connection secrets are redacted from telemetry. `media.Token` renders as `[redacted]` through `fmt`, `%#v`, and `slog`, with compile-time assertions so that dropping a method becomes a build error rather than a silent leak. Exactly one `Reveal` exists, in the line that writes the response, and a contract test asserts that line delivers the real credential while an integration test asserts the audit trail records the issuing without the credential.
- [ ] **M13-010:** Document the WebRTC connection sequence for SDK authors. The endpoint, its guarantees, the expiry trade, and the renewal rule are documented in `docs/participants.md` and `docs/media.md`. What is not written is the sequence an SDK author follows after receiving the credential, because that is a client-side protocol narrative and M19 is where a TypeScript SDK will establish what it actually needs said.
- [x] **M13-011:** Add contract examples for successful join and stable failures. Three cases join the shared response table — instructions issued, nothing to connect to, and no longer in the call — each validated against the schema the specification names for that status, in both directions.
- [x] **M13-012:** Add an end-to-end join test using disposable infrastructure. Against the real binary and a real pinned LiveKit: a call is arranged, a credential issued, and **the media server accepts it at the signalling handshake** while refusing a tampered copy. Removal and ending the call are then shown to refuse immediately, and the log is checked to carry neither the credential nor the API secret. This is the exit criterion demonstrated rather than argued.

**Exit criteria:** A client can join through Convia alone; external consumers never need direct server-side LiveKit integration. **Met.** One endpoint returns everything a client needs, a real media server accepts it, and nothing in the request or the response names a provider — asserted by the schema, by the served body, and by the import tripwires that keep provider code inside `internal/media`.

### M14 — Real-Time Control Events

**Priority:** P1
**Status:** In progress
**Depends on:** M09 and M10
**Goal:** Use WebSocket only for control-plane events that materially require low-latency delivery.

- [x] **M14-001:** Enumerate events that cannot be handled adequately through REST polling or webhooks. Seven, and the rule that picked them is **what goes stale in seconds**: a call starting or ending, somebody joining, leaving, being removed or having their role changed, and an invitation being declined. What is excluded matters as much and each exclusion has a reason. Rooms, users, credentials and applications change because the application changed them, through a request that already returned the new state; announcing it back would tell a client what it just did. A connection credential being issued is the result of a request the subscriber made and is a fact about a secret. An invitation being redeemed **is** somebody else's act, but it already arrives as `participant.joined` carrying the invitation that let them in, and publishing both would report one arrival twice. Declining is the exception among invitations because nothing else observes it. Webhooks are not the alternative for any of these: M15 serves server-side consumers with a public endpoint, and this serves the second in which a roster is still correct.
- [x] **M14-002:** Define a versioned Convia event envelope. The version travels on each event rather than being negotiated once per connection, so an event forwarded into a log, a queue, or M15's webhook body stays interpretable away from the stream it arrived on. `additionalProperties: false` on the envelope and a parity test in both directions keep the published schema and the delivered bytes the same thing.
- [x] **M14-003:** Define event IDs, timestamps, subject types, and correlation IDs. The subject type is **derived from the event type rather than supplied**, so an event claiming to be about a call while carrying a participant identifier cannot be constructed. The correlation identifier is the request that caused the event, matching the `X-Request-ID` of that request and the audit entry for the same occurrence, and it is absent rather than empty when nothing caused it. Everything an event carries is a value Convia assigned; the removal reason, the end reason, and call metadata are application-composed text that may describe a person, and tests in each emitting package assert each one stays out.
- [x] **M14-004:** Define connection authentication and authorization. The same middleware as every other route, and deliberately so: a WebSocket endpoint that quietly skipped it would be the most valuable route in the API to find. Everything that decides what the stream carries happens **before the upgrade**, while the exchange is still an ordinary request that can be refused with an ordinary JSON error — after the upgrade there is no status code left to send.
- [x] **M14-005:** Define subscription scopes and tenant isolation. `events:read` grants the connection and grants no content: each event is delivered only to a credential that could already have read the thing it is about, so the stream is not a second way to be granted anything. A credential holding the stream scope and no read scope is **refused rather than given an empty connection**, because an empty stream is indistinguishable from a quiet one and a client would wait forever. The tenant comes from the verified key, and no path, query, or message could name another — there being no message at all.
- [ ] **M14-006:** Define reconnect, resume cursor, and missed-event behavior. **Half done, and the half that is missing is stated rather than implied.** Reconnect and missed-event behaviour are defined: nothing is stored, an event produced while a client was away is gone, and a client that reconnects re-reads over REST. A subscriber is always told when its view may be incomplete — falling behind closes the stream with a status of its own instead of silently dropping an event, so a client never believes it saw everything when it did not. **A resume cursor is deferred**: it needs a durable ordered log with a retention policy, which is a table, a write on every domain operation, and a decision about how long Convia keeps a record of who was in which conversation. M15 and M16 will both have opinions about its shape, and fixing it here would fix it before they do.
- [x] **M14-007:** Define heartbeat and idle timeout behavior. A ping every 30 seconds, answered within 10 or the connection closes. A control stream is legitimately silent for hours — a tenant with no calls running produces nothing — so silence cannot be read as failure, and the ping is what separates a quiet tenant from a dead connection. There is no separate idle timeout, because an idle client that is still there answers, and one that is not fails the same heartbeat.
- [x] **M14-008:** Apply bounded queues and explicit slow-consumer handling. A queue of 256 events per stream absorbs a garbage-collection pause without costing anybody a connection; a subscriber that fills it is **disconnected** with a close code of its own. Dropping an event and carrying on was the alternative and is worse than it sounds: the subscriber would keep receiving events with no way to know its picture had a hole in it. Publishing takes no context and returns no error, which is what makes it safe to call from inside `Start`, `Join`, and `Remove` — one stuck consumer cannot slow down or fail the request that produced the event.
- [x] **M14-009:** Apply maximum message and connection limits. Eight streams per application, 1024 per instance, both answered with the same `429` so a tenant is never told anything about the instance's total load. A client's messages are bounded at 1 KiB, which is not really a size limit: it is the point at which Convia stops reading something it is going to refuse anyway.
- [x] **M14-010:** Prevent WebSocket use for ordinary audio or video payloads. **The stream carries nothing upstream at all.** Convia never interprets a client message, so a client that sends one — text or binary — is disconnected with `1003` rather than ignored. That makes this a property of the design rather than a rule somebody has to keep following: there is no message that could carry media, and no protocol extension that would quietly become the place to put it. It also removes a security question, since nothing a client sends can widen what it receives.
- [x] **M14-011:** Add protocol contract tests. OpenAPI 3.0.3 cannot describe a stream, but it describes the exchange that opens one and the shape of what travels on it, so the endpoint, its refusals, the `Event` schema and the type vocabulary are all in `api/openapi.yaml`. A real emitted event is validated against the published schema, the vocabulary is compared in both directions, and the close codes are pinned as the public contract they are. One test upgrades through the **whole middleware chain**, which is where a wrapper that did not pass the connection through would break the one route that needs to take it over — and nothing inside `internal/events` would have noticed.
- [x] **M14-012:** Add concurrent connect, disconnect, and shutdown tests. Subscribers arriving and leaving while events are published, which is the ordinary state of a running instance. Shutdown needed a step of its own: a hijacked connection is invisible to `http.Server.Shutdown`, so the composition root stops the broker first, which tells every subscriber why and waits for them inside the shutdown deadline — and gives up rather than letting one stuck socket hold an instance open past every deadline an orchestrator has.
- [ ] **M14-013:** Add metrics for active connections, delivery latency, and dropped events. **Deferred**, because Convia has no metrics pipeline and `AGENTS.md` says not to add an observability stack before there is something worth observing. What exists meanwhile is the data those metrics would be derived from: every stream logs a line when it opens and a line when it closes carrying how many events it delivered and why it ended, and the broker can report how many are active. This arrives with OpenTelemetry rather than as a counter nobody scrapes.
- [x] **M14-014:** Document horizontal scaling requirements before adding Redis pub/sub. The broker is in-process, so a subscriber connected to one instance never sees an event produced on another. `docs/events.md` states the two things a deployment may do today — run one instance, or route a tenant's requests *and* streams to a fixed one — and says plainly what must not happen: several instances behind a round-robin balancer, which would look like it worked. This is the concrete use case `M16-001` asks for before Redis is added, and it is the right shape for pub/sub: ephemeral, fan-out only, never a source of truth.

**Exit criteria:** Authorized clients receive bounded, versioned, resumable control events without carrying media payloads. **Met except resumable.** Delivery is bounded by a queue, a heartbeat, and two ceilings; the envelope is versioned and published; and media cannot travel on the stream because nothing can. Resuming is `M14-006`, deferred with its reasoning, and its absence is answered rather than hidden: a subscriber is always told when its view may be incomplete.

### M15 — Webhooks for External Applications

**Priority:** P1
**Status:** In progress
**Depends on:** Stable domain events from M09 and M10
**Goal:** Notify server-side consumers of durable Convia events reliably and securely.

- [x] **M15-001:** Define webhook endpoint registration and lifecycle. An application registers a destination under `/v1/webhooks` and gets a signing secret **once**; it can rename it, repoint it, resubscribe it, rotate its secret, disable it, enable it again, and delete it. Disabling and deleting are deliberately different: disabling stops delivery and leaves the record readable, while deleting takes the deliveries with it, which is the honest reading of removing a destination. Convia disables an endpoint itself after repeated failures, so the enabled state is a fact about whether deliveries are getting through rather than only about what the application asked for.
- [x] **M15-002:** Define event subscriptions per endpoint. The vocabulary is M14's, not a second one, and a type Convia does not deliver is refused rather than stored — a subscription that can never fire is indistinguishable, from the outside, from an event that has not happened yet. The fan-out is one indexed statement that inserts nothing when a tenant has no endpoint subscribed to the type, which is what makes it acceptable to run on the way out of every announced event.
- [x] **M15-003:** Define a versioned webhook envelope shared with domain event semantics where appropriate. It is not "where appropriate" — it is the **same envelope**, byte for byte. A consumer reading a webhook body and a client reading the stream are looking at one object, and the `version` field M14 put on each event rather than on the connection is what made that possible.
- [x] **M15-004:** Sign deliveries using rotating application-specific secrets. HMAC-SHA256 over `timestamp.body`, in a `Convia-Signature` header whose scheme is versioned in the value so a second algorithm can travel beside the first during a migration. Rotation takes effect **immediately**, with no grace period: rotation exists because a secret may have been exposed, and one that kept working for an hour would keep working for whoever exposed it. This is also the one secret Convia stores rather than digests, and the migration says why — nobody presents it back, Convia signs with it, and a digest cannot produce a signature.
- [x] **M15-005:** Include delivery IDs, timestamps, and replay-defense guidance. The timestamp is **inside** the signed material rather than beside it, which is the difference between a scheme with replay defence and one where anybody who saw a valid delivery could repeat it for as long as the secret lived. `Convia-Delivery` is stable across attempts, `Convia-Attempt` counts from one, and the documentation leads with recording the identifier rather than mentioning it at the end.
- [x] **M15-006:** Persist delivery attempts durably. A delivery is a row that exists before the first attempt and outlives the last, so an application can ask what Convia tried to tell it and what happened without anybody having kept a second copy. The body that was sent is stored as text rather than JSONB, deliberately: JSONB is a parsed representation that reorders keys, and a body round-tripped through it would no longer match its own signature.
- [x] **M15-007:** Define retry schedule, maximum age, and terminal failure behavior. Seven waits — 30s, 1m, 2m, 5m, 15m, 30m, 1h — so eight attempts over roughly two hours, and no jitter, because `M15-014` asks for deterministic tests and a schedule nobody can predict is one nobody can assert on. A delivery outstanding for more than four hours is given up on regardless of attempts: a webhook that late is worse than none, because a consumer would act on it. Which failures are worth retrying is a decision rather than a default — a destination answering `400` is refusing, and repeating it for two hours would be Convia insisting.
- [x] **M15-008:** Add idempotency guidance for consumers. Delivery is at-least-once and `docs/webhooks.md` says so before it says anything else, because a destination that received a body and failed to answer is indistinguishable from one that never received it. Ordering is not guaranteed either, and that is stated rather than left to be discovered.
- [x] **M15-009:** Add endpoint disablement after sustained failures. Twenty consecutive give-ups, reset by any success, so this is a statement about a destination that has stopped working rather than one having a bad afternoon. Disabling finishes whatever was queued for it, which is what keeps the worker's index free of work that is never going to happen and keeps an application from reading outstanding deliveries that are not.
- [ ] **M15-010:** Add manual redelivery with authorization and audit logging. **Deferred.** Everything it needs exists — the delivery rows, the payload as it was signed, the endpoint — so what is missing is not mechanism but authority: replaying is the kind of operation an operator performs on a tenant's behalf during an incident, and M20 is where the operator surface and its audit requirements are defined. Doing it now would mean inventing that authority twice.
- [x] **M15-011:** Protect against SSRF and unsafe destination networks. The attack is worth naming: a destination is chosen by a tenant and fetched by Convia's own process from inside Convia's own network, so without this an application could point Convia at a cloud metadata service and read the recorded status code as an oracle. Three things answer it. The check is on **addresses, not names**, because a name an attacker controls resolves to whatever it likes. It runs **in the dialer**, at every attempt, so there is no earlier answer to race. And **no proxy is consulted**, because through one the connected address is the proxy's and the destination becomes a header nothing inspects — a single `HTTPS_PROXY` would have disabled all of it. Private destinations are reachable only in development, and that is not a setting: the only reason to want one is the reason not to have one.
- [x] **M15-012:** Apply connection, response-size, redirect, and timeout limits. Ten seconds an attempt end to end, five to connect, at most 8 KiB read back and discarded, and **redirects are not followed** — a `3xx` is a failed delivery. Following one would let a destination point Convia somewhere else after registration, which is the same attack as `M15-011` wearing a different hat.
- [x] **M15-013:** Redact secrets and sensitive payloads from logs. The signing key is a redacting type with compile-time assertions, exactly as the media secret is, so dropping a method becomes a build error rather than a silent leak. It is also absent from every read projection, so no value a handler holds carries one. The other half is what Convia refuses to store: a destination's response body is text Convia did not write, and only its status code is kept.
- [x] **M15-014:** Add deterministic retry and signature tests. The schedule is walked attempt by attempt against a real database and a real receiver, asserting the documented wait each time and the give-up at the end. The signature tests are the ones that matter most, because they are the consumer's side: a changed body, a changed timestamp, another endpoint's key, a replay hours later, and a signature dated in the future are each refused, and `Verify` — the twenty lines the documentation tells consumers to write — is what checks Convia's real deliveries.
- [x] **M15-015:** Add integration tests with a local webhook receiver. A real HTTP consumer that verifies the signature it was sent, so what is proved is not that Convia produced a header but that somebody following the documentation can check it. It also answers the failure codes on cue, which is how the retry policy, the disablement, the lease, and the age limit are all exercised against real rows.
- [ ] **M15-016:** Close the window between a domain change committing and its delivery being queued. **New, and it is the cost of `M15-006` written down rather than left implicit.** The delivery is inserted in the same request, immediately after the domain transaction commits, so a crash in between loses it — logged loudly, and never delivered. Closing it means a transactional outbox: the delivery written inside the transaction that wrote the change, which today would mean every write path in three packages accepting a hook that runs inside its store's transaction. [ADR 0004](docs/adr/0004-queuing-a-durable-delivery-inside-the-request.md) records why that was not the change to make on the way past, and `docs/webhooks.md` tells consumers plainly rather than letting them assume otherwise.

**Exit criteria:** External applications receive signed, retryable, auditable events without relying on internal provider webhooks. **Met.** A registered destination receives signed deliveries it can verify with the documented twenty lines, retries follow a published schedule, every attempt is a readable row, and nothing anywhere in the path names a media provider — the events are Convia's own, which is what `M12-009` and `M12-010` were waiting for.

### M16 — Redis and Distributed Ephemeral State

**Priority:** P1
**Status:** In progress
**Depends on:** A demonstrated distributed-state requirement from M14, M15, or M17
**Goal:** Introduce Redis for justified ephemeral coordination, never as the durable source of truth.

- [x] **M16-001:** Document the first concrete Redis use case before adding the dependency. It was documented before this milestone started, which is the order the gate asks for: [ADR 0003](docs/adr/0003-a-one-directional-in-process-control-event-stream.md) built the event stream on an in-process broker and stated the consequence, and `docs/events.md` named what must not happen — several instances behind a round-robin balancer, each serving only its own subscribers, looking like it worked. The use case is therefore **carrying control events between the instances of one deployment**, and nothing else has been added alongside it.
- [x] **M16-002:** Select and pin a maintained Redis client. `github.com/redis/go-redis/v9` at v9.21.0, BSD-2-Clause. Its cost was measured rather than assumed: **three modules enter the build** — the client, `cespare/xxhash/v2` (MIT), and `go.uber.org/atomic` (MIT); everything else in its graph is a test dependency of a dependency. Hand-rolling RESP and a resubscribing pub/sub loop was the alternative, and reconnection under a partition is exactly the kind of code `AGENTS.md` says a dependency is for.
- [x] **M16-003:** Add connection and pool configuration. `CONVIA_REDIS_URL` and an optional `CONVIA_REDIS_TIMEOUT`. Unset means there are no other instances, which is a supported deployment and every local process. The URL is validated for **shape rather than reachability**: refusing to start because Redis was down would take a working API offline over a stream that degrades to what it was before this milestone. Production requires `rediss`, because this channel carries the events of every tenant on the deployment between machines.
- [x] **M16-004:** Add local Redis through Docker Compose, opt-in under the `shared` profile the way the media plane is under `media` — a single instance needs none of it. Persistence is switched off in the command itself, so a restart loses no Convia state by construction rather than by luck.
- [x] **M16-005:** Define key naming, tenant scoping, and versioning conventions. One channel, `convia:v1:events`: a namespace so Convia's traffic is recognizable on a Redis somebody else is also using, and a version so a future envelope can run beside this one during a rolling deployment. **Tenant scoping is not in the channel name and deliberately so** — the broker decides who receives what from the credential that opened the stream, and decides it identically whether the event arrived locally or over the wire, which a test asserts. A channel per tenant would buy less traffic between machines that already share a database, at the cost of subscribing and unsubscribing as streams come and go; ADR 0005 records it as the optimization for when traffic justifies it.
- [x] **M16-006:** Define TTL for every ephemeral key category. **There are no keys.** Publish/subscribe writes nothing, so there is no category to give a lifetime to, and a message nobody is listening for is gone the instant it is sent — which is the strongest bound available rather than the absence of one. A test asserts the key count does not move while events are carried, so the day something starts being stored, this stops being true loudly.
- [x] **M16-007:** Prevent secrets and unnecessary personal data from entering Redis. What travels is the event envelope M14 defined, which already carries only values Convia assigned and refuses application-composed text — the removal reason, the end reason, and call metadata are all excluded at the source, tested in three packages. The Redis URL itself routinely carries a password, so the address is redacted before it reaches a log.
- [x] **M16-008:** Define behavior when Redis is unavailable. Startup pings once, reports loudly, and serves. **Readiness deliberately ignores Redis**: an instance whose stream has narrowed to its own subscribers is still answering every request correctly, and taking it out of the load balancer would turn a narrowed stream into an outage. Broadcasting queues rather than sends, so an unreachable Redis cannot slow a request; a full queue drops and counts, which is acceptable here and nowhere else — the durable half of delivery is webhooks. go-redis re-establishes the subscription underneath, so an outage leaves a gap in what subscribers saw rather than a stream that never recovers.
- [x] **M16-009:** Add integration tests against disposable Redis. Two relays are two instances: what one publishes the other receives, an instance does not receive its own events, the envelope survives the round trip byte for byte, and a message this version cannot read is skipped without costing the relay. Two of them need no Redis at all and are the more important pair — broadcasting against a dead address must not wait, and an unreachable channel must be reported rather than fatal.
- [ ] **M16-010:** Add metrics for pool usage, operation latency, and failures. **Deferred**, for the same reason as `M14-013` and `M15`'s equivalent: Convia has no metrics pipeline, and `AGENTS.md` says not to add an observability stack before there is something worth observing. What exists meanwhile is the count of events that could not be carried, reported while it is happening and again when the process stops — the number those metrics would be derived from.
- [x] **M16-011:** Document eviction-policy assumptions. There are none to make, and that is the point rather than an omission: pub/sub holds nothing, so no eviction policy can reclaim anything Convia depends on and no `maxmemory` setting can lose it. ADR 0005 states it, and the rule it establishes for the day keys do arrive — `M17` will want them — is that nothing in Redis is ever a source of truth.
- [x] **M16-012:** Test that durable state remains recoverable without Redis. It holds by construction: nothing durable is in Redis, so there is nothing to recover. What is tested instead is the claim underneath it — a broker with no relay behaves exactly as it did before this milestone, and the code that would carry events between instances is **absent rather than unused**, so a single-instance Convia cannot fail in a way only a multi-instance one could.

**Exit criteria:** Redis supports a documented ephemeral need, has bounded data lifetimes, and cannot become an accidental durable authority. **Met, and the third clause structurally.** The need was documented a milestone before the dependency was added; the data lifetime is zero, because pub/sub stores nothing; and Redis cannot become a durable authority because there is nothing durable in it — asserted by a test on the key count rather than by intent. A tripwire keeps the client inside `internal/events/redis`, reachable only from the composition root.

### M17 — Presence

**Priority:** P1
**Status:** In progress
**Depends on:** M14 and M16
**Goal:** Expose useful, privacy-aware presence derived from ephemeral signals.

- [x] **M17-001:** Define presence states and their exact semantics. Four, and the vocabulary is closed: `busy`, `online`, `away`, and `offline`. Three are asserted and one is not — `offline` means nothing is saying anything, either because nothing ever was, because it was withdrawn, or because the last claim lapsed. **It is refused as an input**, with the operation that was meant instead, because presence aggregates across devices and a client that could assert it would be claiming somebody is unavailable while another of their devices is plainly active.
- [x] **M17-002:** Distinguish application presence, Convia connection presence, and call participation. The middle one **does not exist and is not approximated**: no user connects to Convia, because the only socket Convia serves is the control-event stream and it belongs to an application's backend. The third is kept apart structurally rather than by convention — `in_call` is not a field, and a tripwire refuses to let `internal/presence` import `internal/calls` or `internal/participants`. Folding a durable fact behind an advisory expiry would mean that the first time Redis was unreachable, Convia would report that nobody was in any call, and the participants API would contradict it in the same second.
- [x] **M17-003:** Define heartbeat, timeout, and disconnect transitions. A heartbeat every 20 seconds against a default lifetime of 60, so losing one to a hiccup costs nothing. The lifetime is bounded at 10 and 300 seconds and a value outside that is **refused rather than clamped** — a caller asking to be online for an hour has misunderstood presence, and quietly giving it a minute would leave it heartbeating once an hour with its users flickering. Withdrawing takes one device or all of them, and withdrawing what was never asserted succeeds, because a client retrying its own tidying-up must not be told it failed.
- [x] **M17-004:** Define multi-device aggregation behavior. The strongest claim wins, in the order `busy`, `online`, `away` — deliberately not "most available first". `busy` is the only state a person asks for on purpose and must not be undone by a laptop in another room; `away` is the only one an application normally *infers*, so it ranks below the two that are acts. `since` comes from the earliest device still asserting the winning state, so a client showing "away for 20 minutes" does not watch it reset every heartbeat, and `expires_at` is the furthest deadline, so one device going quiet does not take somebody offline. The rule lives in one place and both stores call it.
- [x] **M17-005:** Define visibility and privacy policies per application. Three parts, each enforced rather than documented. **Scope**: `presence:read` and `presence:write` are separate, so an application that reports presence from its session tier and reads it from its API tier gives each one of the two. **Tenancy**: the application comes from the credential and there is no path, query, or body field anywhere on this surface that could name another. **Footprint**: what is stored is a state, a start, and a deadline — no address, no user agent, no location; the response carries no device list and no device count, because how many screens somebody has open is not a colleague's business; the device identifier never leaves Convia; and presence is the one thing Convia announces that is **not written to the audit log**, because it arrives thousands of times more often and where a person is at a given minute does not belong in a durable record.
- [x] **M17-006:** Store presence only as ephemeral state. Two kinds of key, both self-clearing: one hash per person being asserted about, whose own expiry is set past its last claim, and one sorted set of deadlines whose members leave as they come due. Every claim expires and nothing is written that does not; a test asserts that once everybody has lapsed there is no hash, no deadline, and no bookkeeping left. Nothing durable is derived from any of it, which is what makes an eviction survivable.
- [x] **M17-007:** Publish Convia-owned presence events. `presence.changed`, on a new subject type `user`, carrying the new state and the previous one and nothing an application composed. It is the **exception** to the rule that keeps rooms and users off the stream, and the exception is principled: every other application-asserted fact is absent because announcing it would tell a client what it just did, whereas here **the assertion is not the change** — what a subscriber is told is the aggregate across devices, and the moment a claim lapsed on a timer, and the instance that sent the heartbeat knows neither. Only a move is announced; a heartbeat that refreshes a deadline publishes nothing.
- [x] **M17-008:** Handle unclean disconnects and process crashes. There is nothing to handle, and that is the design rather than an omission: a claim lapses on its own, so a client that vanished and an instance that crashed produce the same outcome without either being detected. What needed building is the *announcement* — a timer tells nobody — so each instance sweeps lapsed deadlines every five seconds. **Correctness never depends on it**: a read ignores a claim past its deadline whether or not anything has swept it, so a sweeper that stops running costs subscribers the announcement and never the answer.
- [x] **M17-009:** Test clock skew and delayed heartbeat behavior. Answered structurally instead of by assuming NTP: `presence.Assertion` **has no time field**, so no caller's clock reaches a stored deadline, and in a shared deployment the expiry is computed by Redis inside the script that writes it. Two instances whose own clocks differ agree exactly on when a claim lapses because neither is asked. A delayed heartbeat therefore needs no special case — it is one that arrived after the claim lapsed, so the person went offline and came back, which is what happened.
- [x] **M17-010:** Test cross-node presence convergence. Against a real Redis, with two and four separate clients standing in for instances: what one asserts another reads to the millisecond, devices heartbeating to different instances aggregate into one person, withdrawing on one is immediately visible on the other, the per-person device ceiling holds across them, and **four instances sweeping the same lapse at once produce exactly one departure**. That last one was verified by breaking the implementation and watching it report four.
- [ ] **M17-011:** Add metrics for active users and stale entries without high-cardinality labels. **Deferred**, for the third time and for the same reason as `M14-013` and `M16-010`: Convia has no metrics pipeline, and `AGENTS.md` says not to add an observability stack before there is something worth observing. All three are the same piece of work and it is `M22-005`/`M22-006`.
- [x] **M17-012:** Document that presence is advisory rather than a durable guarantee. Documented, and then made **structural** so the documentation cannot be ignored: `presence.changed` is the one event type Convia refuses to deliver by webhook, and an endpoint that asks for it is refused at registration with the stream named instead. A webhook is a delivery with attempts behind it, so a presence report that failed once arrives after it stopped being true and after the newer one that replaced it — a roster that never settles. Presence cannot be treated as durable because Convia will not deliver it durably.

**Exit criteria:** Presence converges across instances, respects privacy, expires safely, and is not confused with durable participation history. **Met.** Convergence is tested against a real Redis from four clients at once; privacy is a scope split, a tenancy taken from the credential, a response with no device count, and an absence from the audit log; expiry is bounded by Convia rather than negotiable by a caller and is decided by a clock no client can reach; and the last clause is a compile-time boundary rather than a promise — `internal/presence` cannot import the roster, so presence and participation cannot be confused by anybody reading either.

---

## Phase 3 — Product and Integration Surfaces

### M18 — Standalone Web Application

**Priority:** P1
**Status:** In progress
**Depends on:** M07, M08, M09, M10, and M13
**Goal:** Deliver Convia's own user interface on top of the same public platform concepts offered to external consumers.

- [x] **M18-001:** Choose the frontend stack based on team capability and long-term maintenance. **React, TypeScript, Tailwind, and Vite**, with Convia serving the built assets from its own origin. The stack is the boring choice on purpose — the largest hiring pool, and the TypeScript SDK `M19` delivers is what the application will consume. Same-origin is the part that mattered most and it was decided here rather than later: it is what makes the session cookie first-party, `SameSite=Lax` meaningful, and **CORS unnecessary entirely**. Tailwind was added once the interface existed, as a deliberate second decision rather than part of the first: it runs at build time and emits one static stylesheet, because the play CDN injects styles inline and would need `unsafe-inline` in the policy the page is served under. Its theme is the design tokens themselves, so there is no second place where a colour is defined.
- [x] **M18-002:** Define first-party authentication and session management, which also closes `M07-009`. A fourth credential family, `cvs_`, in a `__Host-` cookie, with an idle window of fourteen days and an absolute one of ninety that nothing extends. What the session *authorizes* was the decision that mattered: **nothing**. It carries no scopes and cannot become a `credentials.Principal`, because a signed-in person holding a tenant's authority could mint a key that outlives every control in this milestone. Person-facing resource routes are therefore added deliberately, one at a time, rather than inherited. CSRF rests on an **exact-match `Origin` check that fails closed**, because `SameSite=Lax` does not see a sibling subdomain and the JSON content-type check does nothing for a bodyless POST; a test enforces the invariant that keeps Lax meaningful, that no route here changes state on a GET.
- [x] **M18-003:** Build a room list and room creation flow. A person opens a room with **a name and nothing else** — an alias, metadata and a capacity are the application's to decide — and is in it from the moment it exists, because the room and its first member are one transaction; as two writes, a failure between them would leave a room no person could ever reach. They add somebody, and they leave. **Discovery is a shared room:** a person names only somebody they already share a room with, which is the one answer to `M32-002`'s question that is not an enumeration oracle, needs no consent model, and leaves contacts, requests and blocking with M32 where they belong. Every reason somebody cannot be added is one `404` with one sentence, so a person learns neither which identifiers exist nor who is suspended, and the people list leaves suspended people out for the same reason. Removing somebody else is absent on purpose: membership has no role for that power to rest on, and deciding moderation by accident is what `M32-004` warns against. Members carry display names on this surface and not on the application's, resolved in one read per page, and the conversation now names its speakers.
- [ ] **M18-004:** Build call start, join, leave, and end flows.
- [ ] **M18-005:** Add microphone and camera permission handling.
- [ ] **M18-006:** Add device selection and persisted preferences.
- [ ] **M18-007:** Add pre-join media preview.
- [ ] **M18-008:** Add participant roster and call-state feedback.
- [ ] **M18-009:** Add responsive layouts for supported screen sizes.
- [ ] **M18-010:** Meet keyboard navigation and screen-reader requirements.
- [ ] **M18-011:** Handle denied permissions and unavailable devices clearly.
- [ ] **M18-012:** Handle reconnection and degraded network states.
- [ ] **M18-013:** Add component tests for user-visible state transitions.
- [ ] **M18-014:** Add a small set of end-to-end tests for critical call journeys.
- [x] **M18-015:** Ensure the UI uses Convia APIs rather than privileged internal shortcuts. Held from the start rather than checked at the end: everything on the screen came from a request any browser could have made with the same cookie, and the page has no path to the database, no endpoint of its own, and no way to act with the first-party application's key. It is what makes the product evidence that the platform works.
- [x] **M18-016:** Serve the interface. `web/` with React, TypeScript, Tailwind and Vite, built into a Go package that embeds it, served from the **same origin as the API** at every path `/v1` has not claimed. Same origin is the decision the rest hangs from, and [ADR 0009](docs/adr/0009-convia-serves-its-own-interface-from-its-own-origin.md) records it: it is what makes the session cookie first-party, gives `SameSite` something to compare against, and makes CORS **unnecessary rather than configured** — a setting nobody can get wrong because there is none. The page is served under `default-src 'none'` with no `unsafe-inline`, which closes `docs/sessions.md`'s security-headers gap and is only possible because the build emits nothing inline; a test asserts the policy never grows the keyword. The router's fallback fails **towards the API**: a mistyped `/v1/roomss` is a refusal a client can parse, never HTML. A binary built without the bundle serves the API and answers 503 with the command that fixes it, so `go build ./...` needs no Node.
- [x] **M18-017:** Sign in, read, and write, against the public session surface alone — which is `M18-015`, held from the first screen rather than audited later. The sidebar, the conversation, posting, editing, withdrawing, and read state, with the three zones the specification describes. A failed sign-in is worded identically for an unknown address and a wrong password, because the server refuses to distinguish them and a client that did would give away what the server would not; a network failure is a separate type, because telling somebody their password is wrong when the connection dropped is a lie. What was typed survives a refusal.
- [x] **M18-018:** Give a person something to subscribe to. `GET /v1/me/events` is a person's own stream, and it is **not the tenant's handler with a different verifier**: an application's stream is authorized once, for a tenant, by scopes that cannot change while it is open, and a person's is authorized **per room**, which is exactly what changes while it is open. The broker holds the rooms each person's stream covers — read before the upgrade, kept current by the membership events the stream itself carries, and read again every minute — and checks each event against them as it delivers, so no query ever runs on the publish path. A change to a person's own place is delivered when they were in the room before it **or** are in it after, which is what lets both being added and being removed arrive; a read racing a membership change is reconciled by replaying what arrived during the read. The session is authenticated again on the same minute, and one that has ended closes the stream with `4001`, the one moment a browser can still be told why. It carries only what a person could already read — messages and members — and leaves out `correlation_id`, which is somebody else's request. People's streams have ceilings apart from applications', so that signed-in tabs cannot refuse the first-party application its own backend stream. The handshake is held to the exact-origin check despite being a GET, because it opens a connection that keeps carrying whatever the cookie is entitled to. The interface stops polling while the stream is open, reads exactly what each event names, catches up when it reconnects, and falls back to its timers whenever it cannot connect. The minute is stated rather than hidden, in [ADR 0010](docs/adr/0010-a-persons-stream-is-authorized-per-room.md): within it a stream may still carry identifiers about a room just left, or for a session just ended.
- [ ] **M18-019:** Decide what a broken screen looks like. There is no error boundary, so a component that throws takes the page with it. It wants deciding alongside what a recoverable failure is, rather than a blank page with a generic apology.
- [x] **M18-020:** Announce changes of membership. `room.member_added` and `room.member_removed`, about the room and naming the person, announced only on a change exactly as only a change is audited. They reach an application's stream with `members:read`, its webhooks, and the streams of the people in the room — which is how a person added by somebody else finds out. Leaving and being removed are **one type**, because membership records no actor and a type that claimed to know which it was would be guessing. **Erasure announces nothing**: broadcasting every room somebody had been in would publish exactly the record erasure removes.
- [ ] **M18-021:** Translate the interface, starting with Portuguese (pt-BR) beside English. Every string a person reads is English and written inline today, so this is extraction before it is translation. Four things to decide rather than discover: **how the locale is chosen** — the browser's `Accept-Language` first and an explicit choice that overrides it, which is a setting and therefore waits on the settings screen or lives in a small switch until then; **the library**, which must allow commercial relicensing (i18next is MIT and FormatJS is BSD-3-Clause, both fine) and must not load catalogues from a third-party origin, because the Content-Security-Policy forbids it and the catalogues belong in the bundle anyway; **plurals**, which are already hand-written in English ("1 unread message") and need real plural rules rather than a ternary per language; and **what Convia itself says**. Error bodies carry English prose, and `docs/api-conventions.md` promises only that the `code` is stable, so the interface must translate from the code and never display a server message — several fallbacks still show `error.message`, and this item is where they stop. Dates and times already follow the browser's locale. A test should fail when a string reaches the screen without passing through the catalogue, or the next screen added will quietly be English again.
- [x] **M18-022:** Let a person create their own account, with nothing to configure. **This reverses the operator-created accounts of `M18-002`**, which were decided without the product owner and were wrong for the product: somebody who installs Convia to talk to people met a sign-in form that refused everyone until a variable was set and a command was run against the database. An installation now holds as many accounts as people register, with a **username and a password and no email**, like the entries in a password manager's file; `POST /v1/accounts` creates one and signs its owner in. The first-party application is a fixed row Convia makes on first start, so `CONVIA_FIRST_PARTY_APPLICATION` and `convia account create` are gone. **An account's identifier is the fingerprint of an Ed25519 key** generated for it, rather than random characters with the time mixed in: collisions were never the risk, forgery is — once invitations travel between installations, each installation can write any identifier into its own database, and only one that is a key's fingerprint can be challenged. **The private key is stored only sealed by the password**, with AES-256-GCM under argon2id, so nobody with the database can use it and **no password can be reset** — said on the form before the account exists. A person is named to others by a **handle**, `username#IDENTIFIER` plus a Luhn mod 32 check character that catches every mistyped character. Usernames are lowercase ASCII so two cannot look identical. Registering is **rationed by use, successes included**, twenty an hour per address, because what it guards against is somebody succeeding too often. Every account created under `M18-002` is lost: an old identifier cannot become a key's fingerprint, and a key cannot be sealed by a password Convia never held. See [ADR 0011](docs/adr/0011-an-account-is-local-and-its-identifier-is-its-key.md) and [`docs/sessions.md`](docs/sessions.md).
- [x] **M18-023:** Invite somebody on another installation into a room. The product owner decided the shape before anything was built:
  - **the invitation is into a room** (friendship invitations are M32's);
  - **the room lives on the inviting installation**, and the invitee takes part through their own, which talks to the room's home on their behalf;
  - **that installation can act for them only while they are signed in**;
  - **a link lasts a day and is used once**.

  How it works:
  - **Signatures.** Every request between installations is signed with the person's Ed25519 key, over the method, the authority it was addressed to, the path, the body's digest, a timestamp and a nonce. The home believes the key only once its fingerprint is the claimed identifier. It claims the nonce in a table whose primary key decides, and refuses anything more than five minutes from its clock. Signing the authority is what stops a home replaying a request to a third installation.
  - **The key.** It is held by the **session**: signing in opens it, and it is wrapped under a key derived from the session's own secret. The database alone cannot sign as anybody, and signing out ends all of it.
  - **The link** is not a secret. Accepting it needs the invitee's signature and the username the inviter typed. Every reason it cannot be used is one `404`, and two acceptances at once cannot both succeed.
  - **Visitors.** A visitor becomes a user of the home's own product and a member of the room. They are then served by **the same handlers** a signed-in person is: a visitor surface verifies the signature and puts the person where a session would.
  - **The browser never meets the other installation**, so cookies, the origin check and the content policy are untouched. What is relayed is re-encoded, and a home's `401` becomes a `403`, so a room elsewhere cannot sign somebody out of their own Convia.
  - **Following a link.** It uses webhook delivery's destination guard, so outside development a link cannot point an installation at a private address, and no redirect is followed.

  The interface invites by handle from the People panel and warns when a link names `localhost`. Links are joined from **Join with a link** after a preview, and rooms elsewhere are listed under **Elsewhere**.

  Known limits:
  - rooms elsewhere are read on a timer and carry no unread count;
  - a home that does not answer cannot be left, only forgotten here, and the person stays a member there;
  - migration `00020` ended every session once;
  - calls between installations wait for `M18-004`.

  See [ADR 0012](docs/adr/0012-a-room-lives-on-one-installation-and-visitors-sign.md) and [`docs/peers.md`](docs/peers.md).

**Exit criteria:** A user can complete the supported room and call journey accessibly through Convia's standalone product.

### M19 — TypeScript Client SDK

**Priority:** P1
**Status:** Not started
**Depends on:** Stable M03 and M13 contracts
**Goal:** Let browser applications integrate with Convia without directly implementing its HTTP and event protocols.

- [ ] **M19-001:** Define supported browser and TypeScript versions.
- [ ] **M19-002:** Decide generated versus handwritten REST client boundaries.
- [ ] **M19-003:** Expose Convia-owned types and errors.
- [ ] **M19-004:** Implement authenticated REST transport with cancellation.
- [ ] **M19-005:** Implement idempotency-key support for mutations.
- [ ] **M19-006:** Implement real-time control-event connection and reconnection.
- [ ] **M19-007:** Wrap media connection details without requiring server-side LiveKit knowledge.
- [ ] **M19-008:** Define stable event listener and cleanup behavior.
- [ ] **M19-009:** Add unit tests for serialization and error translation.
- [ ] **M19-010:** Add browser integration tests against a disposable Convia stack.
- [ ] **M19-011:** Publish API reference and minimal integration examples.
- [ ] **M19-012:** Add package provenance, integrity, and release automation.
- [ ] **M19-013:** Define semantic versioning and deprecation policy.
- [ ] **M19-014:** Test compatibility with Orbit and Workspace Town prototypes.

**Exit criteria:** A supported browser application can authenticate, manage calls, receive events, and join media using only Convia's SDK and public contracts.

### M20 — Server SDKs and Integration Examples

**Priority:** P2
**Status:** Not started
**Depends on:** Stable M03, M07, M08, M09, and M15 contracts
**Goal:** Make server-to-server integration safe, idiomatic, and well documented.

- [ ] **M20-001:** Prioritize SDK languages using confirmed consumer requirements.
- [ ] **M20-002:** Implement the first server SDK with explicit timeouts and contexts.
- [ ] **M20-003:** Implement credential handling without logging secrets.
- [ ] **M20-004:** Expose typed Convia errors and retry guidance.
- [ ] **M20-005:** Support idempotent mutation requests.
- [ ] **M20-006:** Add webhook signature verification helpers.
- [ ] **M20-007:** Add pagination iterators without hiding network errors.
- [ ] **M20-008:** Add unit tests against contract fixtures.
- [ ] **M20-009:** Add integration tests against a disposable service.
- [ ] **M20-010:** Publish runnable examples for room, call, participant, and webhook flows.
- [ ] **M20-011:** Add semantic versioning and compatibility documentation.
- [ ] **M20-012:** Add release provenance and checksum verification.

**Exit criteria:** At least one real external application integrates server-side without constructing raw requests or knowing media-provider details.

### M21 — Administration and Operations Surface

**Priority:** P2
**Status:** Not started
**Depends on:** M05 through M17 as applicable
**Goal:** Give authorized operators safe visibility and control without direct database manipulation.

- [ ] **M21-001:** Define operator roles separately from tenant application roles.
- [ ] **M21-002:** Require strong authentication for operator access.
- [ ] **M21-003:** Add application lookup and lifecycle controls.
- [ ] **M21-004:** Add credential revocation and rotation controls.
- [ ] **M21-005:** Add room and call inspection using redacted data.
- [ ] **M21-006:** Add participant removal and emergency call termination.
- [ ] **M21-007:** Add webhook delivery inspection and redelivery.
- [ ] **M21-008:** Add audit-log search with strict access controls.
- [ ] **M21-009:** Require reasons for high-impact operator actions.
- [ ] **M21-010:** Add tests preventing privilege escalation.
- [ ] **M21-011:** Add confirmation and re-authentication for destructive actions.
- [ ] **M21-012:** Document operational ownership and escalation paths.

**Exit criteria:** Routine support and incident actions can be performed through audited, least-privilege operations rather than database access.

---

## Phase 4 — Production Readiness

### M22 — OpenTelemetry and Structured Observability

**Priority:** P2
**Status:** Not started
**Depends on:** Meaningful domain and infrastructure behavior
**Goal:** Make failures and performance understandable across HTTP, database, Redis, webhooks, and media adapters.

- [ ] **M22-001:** Define service name, environment, version, and instance resource attributes.
- [ ] **M22-002:** Add OpenTelemetry configuration with disabled-by-default local behavior if appropriate.
- [ ] **M22-003:** Trace inbound HTTP requests with safe route names.
- [ ] **M22-004:** Propagate trace context to supported outbound calls.
- [ ] **M22-005:** Instrument database, Redis, webhook, and media adapter boundaries.
- [ ] **M22-006:** Define request, error, latency, and saturation metrics.
- [ ] **M22-007:** Define call-control metrics without user or room IDs as metric labels.
- [ ] **M22-008:** Correlate structured logs with trace and request IDs.
- [ ] **M22-009:** Redact credentials, tokens, personal data, and sensitive metadata.
- [ ] **M22-010:** Add telemetry tests with in-memory exporters.
- [ ] **M22-011:** Create initial service and dependency dashboards.
- [ ] **M22-012:** Define SLOs for availability, latency, and media-join control operations.
- [ ] **M22-013:** Create actionable alerts tied to runbooks.
- [ ] **M22-014:** Define telemetry retention and sampling policies.

**Exit criteria:** Operators can trace a failed request across dependencies, measure SLOs, and investigate without exposing sensitive data.

### M23 — Security and Privacy Hardening

**Priority:** P2
**Status:** Not started
**Depends on:** Threat models for implemented features
**Goal:** Systematically reduce application, infrastructure, supply-chain, and privacy risk.

- [ ] **M23-001:** Maintain a living threat model for each trust boundary.
- [ ] **M23-002:** Classify stored and transmitted data by sensitivity.
- [ ] **M23-003:** Define encryption-in-transit requirements for every connection.
- [ ] **M23-004:** Define encryption-at-rest responsibilities and key ownership.
- [ ] **M23-005:** Move production secrets to an approved secret manager.
- [ ] **M23-006:** Define secret and signing-key rotation procedures.
- [ ] **M23-007:** Add secret scanning and push protection in GitHub.
- [ ] **M23-008:** Add dependency license policy and review automation if required.
- [ ] **M23-009:** Generate a software bill of materials for release images.
- [ ] **M23-010:** Sign release images and publish provenance attestations.
- [ ] **M23-011:** Scan built container images for known vulnerabilities.
- [ ] **M23-012:** Define patch deadlines by vulnerability severity.
- [ ] **M23-013:** Add abuse controls and tenant-aware rate limits.
- [ ] **M23-014:** Test SSRF, injection, broken access control, and token leakage risks.
- [ ] **M23-015:** Run an external penetration test before general availability.
- [ ] **M23-016:** Define vulnerability intake, triage, disclosure, and remediation procedures.
- [ ] **M23-017:** Define personal-data export, correction, deletion, and retention workflows.
- [ ] **M23-018:** Review Brazilian LGPD and other applicable regulatory obligations with qualified counsel.

**Exit criteria:** Documented controls cover the implemented attack surface, critical findings are resolved, and privacy operations are executable.

### M24 — Test Strategy and Reliability

**Priority:** P2
**Status:** Not started
**Depends on:** Each implemented domain milestone
**Goal:** Build confidence through deterministic layers of tests and explicit failure-mode coverage.

- [ ] **M24-001:** Maintain unit tests for every domain invariant and transition.
- [ ] **M24-002:** Maintain HTTP contract tests for every endpoint and error class.
- [ ] **M24-003:** Maintain database integration tests against disposable PostgreSQL.
- [ ] **M24-004:** Maintain Redis integration tests only for implemented Redis behavior.
- [ ] **M24-005:** Maintain LiveKit adapter integration tests against a pinned server version.
- [ ] **M24-006:** Maintain webhook delivery tests with deterministic clocks and retry scheduling.
- [ ] **M24-007:** Maintain WebSocket connection and slow-consumer tests.
- [ ] **M24-008:** Add end-to-end tests only for critical standalone and SDK journeys.
- [ ] **M24-009:** Track coverage by meaningful package and critical behavior.
- [ ] **M24-010:** Set coverage gates only after a baseline and risk review.
- [ ] **M24-011:** Run race detection on every pull request.
- [ ] **M24-012:** Add fuzz tests for parsers, identifiers, webhook signatures, and state machines.
- [ ] **M24-013:** Add failure-injection tests for database, Redis, webhook, and media outages.
- [ ] **M24-014:** Quarantine no flaky test without an owner, issue, and removal deadline.
- [ ] **M24-015:** Publish test duration and flake trends.
- [ ] **M24-016:** Add backup restore and disaster-recovery exercises.

**Exit criteria:** Critical behavior is covered at the lowest reliable test layer, failure modes are exercised, and flaky tests are actively eliminated.

### M25 — Performance and Horizontal Scaling

**Priority:** P2
**Status:** Not started
**Depends on:** Stable critical flows and production-like observability
**Goal:** Validate that the control plane scales without relying on premature optimization.

- [ ] **M25-001:** Define expected tenant, user, room, call, participant, and connection volumes.
- [ ] **M25-002:** Define latency and throughput targets for critical endpoints.
- [ ] **M25-003:** Create representative load-test scenarios.
- [ ] **M25-004:** Measure baseline CPU, memory, allocation, and goroutine behavior.
- [ ] **M25-005:** Profile database queries and verify indexes with realistic data sizes.
- [ ] **M25-006:** Test connection pool saturation and recovery.
- [ ] **M25-007:** Test WebSocket fan-out and slow consumers across multiple instances.
- [ ] **M25-008:** Test Redis failure and failover behavior where Redis is used.
- [ ] **M25-009:** Test media-provider control API degradation independently from media quality.
- [ ] **M25-010:** Verify graceful shutdown while calls and control connections are active.
- [ ] **M25-011:** Verify no in-memory state prevents horizontal scaling.
- [ ] **M25-012:** Establish performance regression thresholds in scheduled CI.
- [ ] **M25-013:** Document capacity assumptions and scaling triggers.
- [ ] **M25-014:** Optimize only measured bottlenecks with before-and-after evidence.

**Exit criteria:** Measured capacity meets documented targets, instances scale horizontally, and performance regressions are detectable.

### M26 — Deployment and Operations

**Priority:** P2
**Status:** Not started
**Depends on:** M22 through M25
**Goal:** Deploy, operate, recover, and roll back Convia predictably.

- [ ] **M26-001:** Select the initial deployment environment based on actual operational requirements.
- [ ] **M26-002:** Define immutable image naming and promotion between environments.
- [ ] **M26-003:** Add environment-specific configuration validation.
- [ ] **M26-004:** Add liveness, readiness, and startup probes with distinct semantics.
- [ ] **M26-005:** Define rolling deployment and connection-draining behavior.
- [ ] **M26-006:** Define migration execution ownership and rollback policy.
- [ ] **M26-007:** Define database backup frequency, retention, encryption, and restore tests.
- [ ] **M26-008:** Define Redis persistence expectations based on its actual uses.
- [ ] **M26-009:** Define LiveKit deployment and capacity ownership.
- [ ] **M26-010:** Add staging with production-like topology and isolated data.
- [ ] **M26-011:** Add deployment smoke tests and automated rollback signals.
- [ ] **M26-012:** Write runbooks for common dependency and saturation incidents. The directory exists at [`docs/runbooks/`](docs/runbooks/), started by the credential revocation procedure written for `M07-016`.
- [ ] **M26-013:** Define on-call ownership and incident severity levels.
- [ ] **M26-014:** Run a restore drill and a rollback drill before production launch.
- [ ] **M26-015:** Add Kubernetes only if the selected environment and scaling model justify it.

**Exit criteria:** A tested process exists for deployment, migration, rollback, incident response, backup, and restore.

### M27 — Release and Compatibility Management

**Priority:** P2
**Status:** Not started
**Depends on:** Stable API and deployment process
**Goal:** Release the service, contracts, images, and SDKs as one governed platform.

- [ ] **M27-001:** Define semantic versioning boundaries for service, API, events, and SDKs.
- [ ] **M27-002:** Define release branch and tag policy.
- [ ] **M27-003:** Generate changelogs from reviewed change metadata.
- [ ] **M27-004:** Build release binaries and images from protected tags.
- [ ] **M27-005:** Publish checksums, signatures, SBOMs, and provenance.
- [ ] **M27-006:** Verify artifacts in a clean environment before publication.
- [ ] **M27-007:** Add compatibility tests for supported SDK and API versions.
- [ ] **M27-008:** Define database migration compatibility during rolling upgrades.
- [ ] **M27-009:** Define event-schema compatibility during mixed-version deployments.
- [ ] **M27-010:** Document upgrade and rollback instructions.
- [ ] **M27-011:** Publish deprecation notices through documented channels.
- [ ] **M27-012:** Maintain a supported-version matrix.

**Exit criteria:** Releases are reproducible, signed, documented, backward-compatible within policy, and safely reversible.

---

## Phase 5 — Advanced Communication Capabilities

### M28 — Screen Sharing

**Priority:** P3
**Status:** Not started
**Depends on:** Stable video calls and client SDK
**Goal:** Add screen sharing as a Convia capability with explicit authorization and user feedback.

- [ ] **M28-001:** Define who may start screen sharing.
- [ ] **M28-002:** Define simultaneous-share limits.
- [ ] **M28-003:** Map the capability internally to provider permissions.
- [ ] **M28-004:** Add browser capture and cancellation handling.
- [ ] **M28-005:** Add clear active-share indicators.
- [ ] **M28-006:** Handle browser and operating-system support differences.
- [ ] **M28-007:** Add start, stop, replacement, and disconnect tests.
- [ ] **M28-008:** Add control events without exposing provider track concepts publicly.
- [ ] **M28-009:** Add quality and bandwidth telemetry.
- [ ] **M28-010:** Document security risks around accidental content sharing.

**Exit criteria:** Authorized users can reliably start and stop screen sharing with clear UI state and provider-independent contracts.

### M29 — Recording, Transcription, and Derived Media

**Priority:** P3
**Status:** Not started
**Depends on:** Legal, privacy, storage, and product approval
**Goal:** Add derived-media features only with explicit consent, retention, and access controls.

- [ ] **M29-001:** Confirm product requirements and applicable consent laws.
- [ ] **M29-002:** Define explicit recording authorization and participant notification.
- [ ] **M29-003:** Define recording lifecycle and failure states.
- [ ] **M29-004:** Select secure object storage and encryption controls.
- [ ] **M29-005:** Define retention, deletion, legal hold, and export behavior.
- [ ] **M29-006:** Define transcript ownership and access controls.
- [ ] **M29-007:** Isolate provider recording identifiers internally.
- [ ] **M29-008:** Add audit events for every recording and transcript access.
- [ ] **M29-009:** Add malware and content-safety controls where files are processed.
- [ ] **M29-010:** Add end-to-end consent, failure, retention, and deletion tests.

**Exit criteria:** Derived media is consented, encrypted, access-controlled, auditable, and deletable according to documented policy.

### M30 — General Availability Readiness

**Priority:** P2
**Status:** Not started
**Depends on:** All P0, P1, and selected P2 milestones
**Goal:** Verify that Convia is supportable as both a standalone product and an external platform.

- [ ] **M30-001:** Freeze and review the initial stable public API surface.
- [ ] **M30-002:** Complete an architecture review focused on tenant isolation and media abstraction.
- [ ] **M30-003:** Complete security and privacy reviews with no unresolved critical findings.
- [ ] **M30-004:** Complete load, soak, failover, backup, and restore tests.
- [ ] **M30-005:** Validate the standalone critical user journeys.
- [ ] **M30-006:** Validate at least one Orbit integration in a non-production environment.
- [ ] **M30-007:** Validate at least one Workspace Town integration in a non-production environment.
- [ ] **M30-008:** Publish API, SDK, webhook, authentication, and operational documentation.
- [ ] **M30-009:** Publish service limits, support boundaries, and status communication channels.
- [ ] **M30-010:** Establish production SLOs, dashboards, alerts, and on-call coverage.
- [ ] **M30-011:** Complete incident-response and disaster-recovery exercises.
- [ ] **M30-012:** Confirm release, rollback, credential rotation, and emergency revocation procedures.
- [ ] **M30-013:** Resolve or explicitly accept every launch-blocking risk.
- [ ] **M30-014:** Produce a launch checklist with named owners and dates.
- [ ] **M30-015:** Conduct a post-launch review and update this roadmap from real usage.

**Exit criteria:** Convia has stable contracts, verified integrations, operational ownership, tested recovery, and no unaccepted launch-blocking risk.

---

### M31 — Messaging and Conversations

**Priority:** P1
**Status:** Complete
**Depends on:** M14 and M18-002
**Goal:** Give Convia the durable messaging its own product is built around, and that no external consumer can build without it.

This milestone is out of numerical order and that is deliberate: it was discovered while planning M18, when the user interface specification turned out to be built around chat that Convia has no domain for. It is P1 and it is built **before** the interface, so that no screen exists without an API behind it.

- [x] **M31-001:** Decide what a conversation is, and whether it is the same thing as a room. **It is a room.** There is no `conversations` table: a room is already "the place", and a conversation held in a place over time is what its messages are. Messages hang off the room rather than the call, which is what lets chat survive a call ending. See [ADR 0008](docs/adr/0008-a-conversation-is-a-room-and-its-order-comes-from-the-database.md).
- [x] **M31-002:** Define a message: authorship, ordering, and what Convia is authoritative for. An author is a user or a guest's invitation and exactly one of the two, which is the shape `participants` already uses for the same people. The body is stored without interpretation.
- [x] **M31-003:** Decide the ordering guarantee, and what a client may assume about it. A per-room `sequence` allocated by PostgreSQL under the room's row lock, never a timestamp: since M16 each instance stamps `created_at` from its own clock. Strictly increasing per room, and deliberately **not** dense.
- [x] **M31-004:** Define editing and deletion, including what remains visible afterwards. An edit records *that* it happened, not what it said before. A deletion leaves a tombstone that keeps its position, so a history never closes over a hole and slides messages past a client's cursor. Only the author does either.
- [x] **M31-005:** Define attachments, or record why they are deferred. **Deferred**: they need object storage, an upload path, a content-scanning story and a retention story, none of which exist. See [`docs/messages.md`](docs/messages.md).
- [x] **M31-006:** Add keyset pagination over history, in both directions. The cursor is the sequence itself rather than an opaque token, because a client already holds it and read state will be expressed in it. `older` opens a room, `newer` catches up. A limit above the maximum is refused rather than clamped, as every other listing in Convia does.
- [x] **M31-007:** Define read state, and decide whether it is durable or advisory. **Durable**, and the contrast with presence is the reason: presence is a claim with an expiry, read state is a fact about something a person did, and a badge that came back because a laptop closed would be wrong in a way people notice. It is a **position, not a set**, it only ever moves forward so two devices cannot fight, and `unread` is derived on every read so it cannot drift -- excluding a person's own messages and withdrawn ones.
- [x] **M31-008:** Publish `message.*` control events, and decide which of them are durable. **All three are**, unlike presence, and the reason is that **an event carries no body**: it names the message and the room, so a retried delivery that arrives late cannot write older text over newer, and a conversation is never copied to the webhook endpoints an application registered. They stream where rooms and users do not, because a message is the application's *other* instances learning what one of them did.
- [x] **M31-009:** Define per-person authorization, which is the first real use of a session principal. **Room membership decides it**, and it gates people rather than applications: an application's key already carries full authority over its own rooms, so membership is what the *session* surface checks. The surface itself is `/v1/me/rooms` and the message routes under it, and **the request has no author field** -- an application names which of its people is speaking because it is acting on their behalf, and a person cannot name anybody, because naming somebody would mean naming somebody else. Writing as another person is unrepresentable rather than refused, which is the move M18 made with the tenant. A room somebody is not in answers `404` and never `403`, because a refusal that separates "not yours" from "does not exist" confirms that a conversation is happening to somebody outside it. Editing checks authorship rather than membership: being removed from a room does not hand your own words to anybody else. See [`docs/messages.md`](docs/messages.md) and [`docs/rooms.md`](docs/rooms.md).
- [x] **M31-010:** Bound message size, rate, and history growth. Body 1-4000 characters, page 25 by default and 100 at most refused rather than clamped, request body 1 MiB. A test asserts the three cannot contradict one another. History per room is **deliberately unbounded** -- truncating a conversation nobody asked to truncate is worse than a large table. **Rate is not bounded**, for messages or anything else: the only budget in Convia covers failed authentication, and the general one is `M13-008`, which needs the shared state M16's Redis now makes possible.
- [x] **M31-011:** Define retention and erasure, consistent with the boundaries M06 set for users. **Erasing a person redacts what they wrote rather than removing it**: the row keeps its place and loses its body and its author. Deleting the messages would take the conversation away from the people still in it, and clearing a name would erase nothing, because the name never lived here. Withdrawn messages are included, since a tombstone still names its author, and read positions go too. Nothing calls it yet: M06's missing retention-window job is the same missing piece.
- [x] **M31-012:** Add integration tests over ordering, pagination, and concurrent authorship. Sixteen simultaneous appends to one room, both paging directions, tombstones keeping their place, authorship refusals, tenant boundaries, read state races, erasure, and assertions against the real log and the real event stream that nothing anybody said leaves in either.

**Exit criteria:** An application can hold a conversation through Convia's public API, read its history reliably, and be told about new messages while they are still news — and a person signed in to Convia's own product can do the same as themselves rather than as their tenant.

---

### M32 — Contacts and Direct Rooms

**Priority:** P3
**Status:** Not started
**Depends on:** M31-009
**Goal:** Let one person reach another without anybody composing an invitation, and without Convia becoming a place strangers can call you from.

This milestone answers a friction found in use rather than in planning: starting a call with somebody you talk to every day should not require issuing an invitation, and it should not require creating a room first.

**It is anchored on `users`, not on `accounts`, and that is the load-bearing decision.** An account is how somebody signs in to Convia's own product; `docs/users.md` already records that the product is an application like any other, and that an account's identifier becomes an external subject of the first-party application. Anchoring a contact list on accounts would give the feature to Convia's own product and to nothing else. Anchoring it on users gives it to every application, and Convia's own product receives it through the same path it already uses for participation — one code path rather than two.

**It is two concepts, and conflating them is the trap.** A contact relationship is who may reach whom; a direct room is where the conversation happens. Folding the second into the first would make "remove a contact" mean "delete a conversation", which is the same mistake `M31-011` refused for erasure.

**M31's room membership already carries part of this.** A room with two members is a durable place for those two, and joining a call in it needs no invitation. What is missing is **discovery** — how one person names another at all — and **consent** — whether being named is enough to be reached.

Invitations do not go away. They remain what they were built for: somebody Convia has no user for, reached once, for one call.

- [ ] **M32-001:** Decide what a contact is: mutual by construction, or a follow that each side holds separately. The answer decides whether removal is symmetric.
- [ ] **M32-002:** Decide how one person names another before any relationship exists, and what that lookup may reveal. An address that confirms whether a stranger has an account is an enumeration oracle, and the sign-in surface already refuses to be one.
- [ ] **M32-003:** Define the request lifecycle: pending, accepted, declined, withdrawn — and whether a declined request is visible to the person who sent it.
- [ ] **M32-004:** Define blocking, and decide what it hides in both directions. This is a moderation surface and the one part of this milestone that is dangerous to guess at.
- [ ] **M32-005:** Decide whether a direct room is created on the relationship or on the first message, and what happens to it when the relationship ends. It must not be deleted: the messages are a record of a conversation that happened.
- [ ] **M32-006:** Guarantee that one pair has exactly one direct room, which is a uniqueness constraint on an unordered pair.
- [ ] **M32-007:** Decide whether a contact list is visible to the application that owns the users, and what an operator may see. A social graph is more sensitive than a roster.
- [ ] **M32-008:** Publish `contact.*` events, and decide which are durable.
- [ ] **M32-009:** Extend erasure: a contact names two people, so erasing one must not leave the other holding a row about them.
- [ ] **M32-010:** Bound the graph — how many contacts, how many pending requests — so that a request queue cannot be used to harass somebody.
- [ ] **M32-011:** Integration tests over consent, blocking, and the uniqueness of a direct room.

**Why this is P3 rather than P1.** Everything here is a consent surface, and consent surfaces are the ones most punished by being designed speculatively. Nothing has ever consumed Convia from outside: no browser has entered a call, and the interface that would show a contact list does not exist. Building the social graph before anything has been used means guessing at the shape of requests, blocks, and what a stranger may learn — and `M32-004` in particular will be wrong if it is decided without a real interface to be wrong in.

**Exit criteria:** Two people who have never exchanged an invitation can find each other, agree to be reachable, and hold a call in a room neither of them created. Either can withdraw that agreement, and withdrawing it does not rewrite the conversation they already had.

---

## Explicitly Deferred Until Required

The following items are intentionally not current implementation tasks:

- Kubernetes manifests before a deployment platform requires Kubernetes.
- Multiple backend microservices before independent scaling or ownership justifies them.
- A second media provider before a real portability requirement exists.
- Custom WebRTC, RTP, or SFU implementations.
- GraphQL, CQRS, event sourcing, or a message broker without a demonstrated need.
- PostgreSQL or Redis packages before the milestone that consumes them begins.
- SDK packages before stable public contracts exist.
- Recording or transcription before consent, privacy, retention, and storage decisions are approved.
- Coverage exclusions or vanity thresholds that conceal meaningful untested behavior.

## Roadmap Maintenance Checklist

- [ ] **ROADMAP-001:** Review this file at the start of every milestone.
- [ ] **ROADMAP-002:** Review priorities monthly or after a major product decision.
- [ ] **ROADMAP-003:** Split tasks into GitHub issues when work is scheduled, preserving task IDs.
- [ ] **ROADMAP-004:** Assign an owner and target release when a task becomes active.
- [ ] **ROADMAP-005:** Add new tasks when implementation reveals missing work.
- [ ] **ROADMAP-006:** Remove tasks only with a documented reason or superseding decision.
- [ ] **ROADMAP-007:** Keep completed items checked instead of deleting project history.
- [ ] **ROADMAP-008:** Keep the `Current Status` and `Recommended Next Actions` sections accurate.
