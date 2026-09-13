# ADR 0007 — A session is a person, not a tenant's authority

**Status:** Accepted. *Accounts are created by an operator* is superseded by [ADR 0011](0011-an-account-is-local-and-its-identifier-is-its-key.md); everything else here stands.
**Date:** 2026-09-11
**Milestone:** M18 — Standalone Web Application

## Context

`M07-009` was deferred with a sentence that named exactly what was missing: *browser sessions need the origin and cookie model of the standalone web application, which does not exist yet.* M18 is where that stops being true.

Convia had three credential families when this began — an application key, an operator key, an invitation — and all three are held by software. None of them answers the question the standalone product asks: **who is the person looking at this page?**

## Decision

**A session authenticates one person, carries no authority over a tenant, and lives only in a cookie.**

### The mistake this ADR exists to prevent

Every `Authorize` in Convia takes a `credentials.Principal`: an application and its scopes, which is authority over the **whole tenant**.

So when the standalone interface needs to list rooms, there is an obvious shortcut — hand the signed-in person a `credentials.Principal` for the first-party application. It works immediately, it requires no new code, and it is catastrophic:

- every signed-in person would hold `credentials:write`, and could mint a permanent `cvk_` key that outlives their session, their password, and their account;
- the tenant surface has no per-user filtering anywhere, so every person could read every colleague and every room;
- the entire lifecycle in `internal/sessions` — revocation, rotation, expiry — would become decorative, because the key minted through it is not a session.

`sessions.Principal` is therefore its own type, it carries **no scopes**, and **nothing anywhere converts one into a `credentials.Principal`**. A test asserts the shape of it, so that adding a field is a deliberate act somebody has to argue for.

What a person may do is decided per operation, against the person, by the domain that owns the operation. Those routes do not exist yet. This parcel settles the principal and the surface they will hang off, and deliberately ships no resource routes at all — because the pressure to take the shortcut arrives on the day somebody needs one, and the answer has to already be written down.

### Passwords contradict `M07-004`, deliberately

`M07-004` stores application keys as a plain SHA-256 digest, with a stated reason: twenty-six base32 characters is roughly 130 bits, which cannot be searched, so a slow hash would only add latency to every request.

A person's password is the opposite kind of secret — short, chosen, often reused, **searchable** — so the same reasoning arrives at the opposite conclusion: argon2id, at RFC 9106's memory-constrained parameters.

The conclusions differ because the inputs do. The rule is chosen by where the entropy came from, and both are written down beside each other so neither looks like an oversight. A **session token**, which Convia generates, goes back to SHA-256 for the original reason.

The digest is stored in PHC form, carrying its own parameters, rather than as a bare 32-byte column like every other secret in Convia. That is the one place where following the existing pattern would be precisely wrong: parameters implied by a package constant can never be raised without invalidating every password at once. Carried with the value, an old digest stays verifiable by its own, and is replaced at the one moment Convia holds the password in the clear — a successful sign-in.

### Accounts are created by an operator

There is no self-service sign-up, and the absence is a decision rather than a gap. Open registration needs email verification, which needs a mailer, which Convia does not have — and `AGENTS.md` says not to add infrastructure before a feature requires it.

Two things fall out of that, and both are worth having:

- **The initial password is generated, never chosen.** An operator picking passwords reuses one across the accounts they create; a generated 130-bit secret makes online guessing a non-question rather than a limit to tune.
- **The most common enumeration oracle in web applications is simply absent.** There is no password reset endpoint to probe.

The cost is that a forgotten password needs an operator, out of band, with no way to verify who they are talking to. That channel is the likeliest real-world account takeover in this design, and `docs/sessions.md` states it plainly rather than leaving it to be discovered.

### Same origin, and what that buys

Convia serves its own interface from the same origin as the API. Cookies are first-party, `SameSite=Lax` applies, and **there is no CORS configuration at all** — there is no cross-origin to permit.

The cookie is `__Host-convia_session`, in every environment including development. The prefix is enforced by the browser: a cookie carrying it is accepted only when it is `Secure`, has `Path=/`, and has **no `Domain`**. That last clause is the point. Without it, "host-only" is only host-only because Convia said so — cookies are scoped by domain rather than by origin, so anything that can write a cookie for a parent domain (an XSS on a sibling subdomain, a preview environment, a network attacker on plain http anywhere under the domain) can shadow the session cookie, and Go reads whichever arrives first.

The name does not vary by environment, because `http://localhost` is a secure context: a name that differed would mean the production cookie path was never exercised until production.

### CSRF has one load-bearing layer, and it is not SameSite

It is tempting to count three layers. There are not three.

- **`SameSite=Lax`** stops a cross-*site* POST. It does not stop a **sibling subdomain**, which is same-site, and it sends the cookie on a top-level GET navigation.
- **The JSON content-type requirement** blocks the `enctype="text/plain"` form trick, because `api.DecodeJSON` checks the header before reading the body. But it does nothing for a route that takes no body, and Convia's API is full of bodyless POSTs.
- **`Sec-Fetch-Site`** is not an independent layer at all: it shipped in the same browser generation as `SameSite`, so the clients that ignore one lack the other.

So **`Origin` carries it**: required on every unsafe method on this surface, matched **exactly** against the address Convia was reached at, and failing closed when absent. Exactly rather than by suffix — a suffix match is what lets the sibling subdomain back in, which is the whole attack.

The invariant that keeps `SameSite=Lax` meaningful at all is that **no route on this surface changes state on a GET**, and a test walks the route table to enforce it.

### Bounding argon2id

Hashing at 64 MiB is a memory cost paid per concurrent hash, on an **unauthenticated** route. Without a bound, anybody who can reach the API decides how much memory the process allocates — and since an unknown email is hashed against a decoy, a flood of invented addresses is enough.

Four concurrent hashes, a two-second wait for a place, and then `503` with `Retry-After`. Refusing is better than queueing: the server's write timeout is far longer than anybody will wait to sign in, so a queue turns a flood into a slow death.

### The refusal says nothing

Unknown address, wrong password, suspended account, suspended person, unreadable digest — one `401`, one message. Two things make it real beyond the message:

- **Order.** The password is verified *first*, then the lifecycle. Checking status first would reveal that an address belongs to a suspended account without needing its password; reporting suspension differently afterwards would confirm the password was right. `internal/credentials` already established this ordering; it is copied rather than reinvented.
- **Timing.** An unknown address is compared against a decoy digest built with the parameters currently in force, so it costs what a known one costs. A test asserts the decoy's parameters match, because the day somebody raises the cost and leaves it behind, the channel reopens silently.

## Consequences

- **A fourth surface, and a switch that can no longer fail open.** `handler()` had no `default`, so a surface added and not wired would have served its routes with no middleware at all. It now panics at startup, and a new test walks every route claiming to need a credential and sends it one without — watching the *log* as well as the status, because a handler refusing on its own answers `401` too, and the difference is exactly what needed catching.
- **One new module in the build.** `golang.org/x/crypto`, BSD-3-Clause, for argon2. `golang.org/x/sys` follows it. Both are already covered by the media tripwire's allowlist.
- **`internal/config` stayed a leaf.** The first-party application identifier is validated for shape inline rather than by calling the applications domain, because configuration is imported by everything and importing a domain back would be the first edge of a cycle.
- **Per-account throttling is not built.** The per-address budget is separate from the API's and far tighter, but an attacker spread across many addresses is bounded only by the generated password's entropy. A naive per-account lockout is a denial of service against a named person, and doing it properly needs shared state — which the Redis added in M16 now makes possible. Recorded rather than half-built.
- **Revocation does not reach a live WebSocket.** `/v1/events` verifies once at the handshake and then streams for hours, so signing out everywhere leaves an open stream running. It is a pre-existing gap that applies equally to a revoked `cvk_`, and closing it needs a registry of live streams keyed by credential — tractable over the M16 relay, and its own piece of work.
- **No security headers yet.** Serving the SPA from the API origin will make an XSS in the bundle a Convia problem for the first time, and `HttpOnly`, `SameSite`, and the Origin check all fail at once against same-origin script. A CSP constrains how the bundle is built, so it is decided with the bundle rather than retrofitted — and it is named as a prerequisite of that parcel rather than left to be remembered.
