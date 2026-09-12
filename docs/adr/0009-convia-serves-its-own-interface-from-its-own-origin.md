# 0009 — Convia serves its own interface, from its own origin

**Status:** Accepted
**Date:** 2026-09-12
**Milestone:** M18

## Context

Convia is two things at once, and this is the first decision where the two pull in different directions. It is a platform other products integrate with over HTTP, and it is a standalone product with its own user-facing application. Everything built up to M17 served the first. The interface serves the second, and it has to be built on the same public API rather than beside it — `M18-015` says so, and it is the only way the product is evidence that the platform works.

Two things had to be decided before a single component could be written, because everything else follows from them: **where the interface is served from**, and **how it gets there**.

The alternatives were the usual ones. A bundle on a CDN or an object store, calling an API on another host. A separate web service in front of both. A Node server rendering pages. Each is a normal thing to do and each was rejected here for reasons that are specific to what Convia already decided in M18-002.

## Decision

**Convia serves its own interface, from the same origin as its API, compiled into the same binary.**

The source lives in `web/` and is built by Vite into `internal/web/assets/dist`, which a Go package embeds with `go:embed`. The router serves it from the fallback that used to answer 404: any path the API has not claimed, read with GET or HEAD, is the page.

## Why the same origin

Because M18-002 put the session in a cookie, and a cookie is a property of an origin.

Same origin is what makes that cookie first-party rather than third-party — the category browsers are in the middle of removing entirely. It is what gives `SameSite=Lax` something to compare against. It is what lets the `__Host-` prefix mean what it claims. And it is what makes **CORS unnecessary rather than merely configured**: there is no cross-origin request to permit, so there is no allowlist to get wrong, no `Access-Control-Allow-Credentials` to set, and no preflight to reason about.

A bundle served from anywhere else would have turned every one of those from a property into a setting. The interesting part is not that the settings are hard — it is that a wrong one fails open and looks fine in testing.

The cost is real and worth naming: **Convia's page and Convia's API share a security boundary.** An XSS in the interface is an XSS on the origin that holds the session cookie. That is why the page is served under a `default-src 'none'` Content-Security-Policy with no `unsafe-inline`, and why the build is arranged to emit no inline script and no inline style so that the policy can stay that strict. `docs/sessions.md` listed the absence of security headers as a known gap, with the note that they would arrive with the interface that needed them. This is that.

## Why in the binary

Because a Convia that starts should be a Convia whose interface is the one it was built with.

Reading the bundle from a directory at runtime creates a second thing to deploy, a second thing to forget, and a version skew between a page and the API it calls that nobody notices until a field goes missing. Embedding removes all three: there is one artefact, and it either has an interface or it does not.

It does mean `go build` alone produces a binary with no interface, because the bundle needs Node. That is not hidden. Such a binary logs it at startup with the command that fixes it, and serves a page saying so under **503** — not 404, because the page is not missing and the request was not wrong: this deployment does not have an interface, which is what 503 means. The committed page that says this is also what keeps the embed pattern matching in a fresh checkout, so `go build ./...` works with no Node installed anywhere.

## Why the fallback rather than the route table

The route table is the API, and a test compares it against the OpenAPI document **in both directions** — every route documented, every documented operation implemented. A page is not an operation and has no place there.

So the interface hangs off the handler that answers unmatched paths, and that handler has to tell two things apart. A single-page interface routes in the browser, so `/rooms/abc` is a screen and must answer with the page, or reloading anything but the first screen would break. But `/v1/roomss` is a typo in an API call and must answer with JSON, or a client gets HTML where it expected a refusal and the failure surfaces as a parser choking on `<` somewhere far from the mistake.

**The split is by prefix and it fails towards the API.** Anything under `/v1` is the API's, always. A test asserts both halves.

## Consequences

- There is no CORS configuration anywhere in Convia, and adding one should be read as evidence that something has gone wrong with this decision rather than as a feature.
- Releasing the interface means releasing Convia. There is no way to ship a page against an older API, which removes a class of skew and removes the ability to hotfix the page alone. That trade is accepted.
- The container image gains a Node stage. The Go stage does not depend on Node being present — it copies a directory — so a build without the interface stays possible and stays honest.
- `connect-src 'self'` in the policy will have to change when calls arrive: joining one means a WebSocket to the media server, which is a different origin. It is left at `'self'` deliberately rather than widened in advance, so that the day it changes, somebody decides what to allow.
- The interface **polls**. Convia's event stream is on the surface an application reaches with its key; a person has no way to subscribe to their own rooms. That is a gap this decision exposes rather than one it creates, and it is recorded in `TODO.md` rather than worked around.

## Related

- [ADR 0007 — A session is a person, not a tenant's authority](0007-a-session-is-a-person-not-a-tenants-authority.md)
- [`docs/interface.md`](../interface.md)
- [`docs/sessions.md`](../sessions.md)
