# ADR 0019 — A session travels in a cookie or in a header

**Status:** Accepted
**Date:** 2026-09-17
**Milestone:** M35 — Desktop Application

## Context

[ADR 0007](0007-a-session-is-a-person-not-a-tenants-authority.md) put the session in a cookie and said it would live **only** there. [ADR 0009](0009-convia-serves-its-own-interface-from-its-own-origin.md) then served Convia's interface from the API's own origin so that the cookie would be first-party, `SameSite` would have something to compare against, and CORS would be unnecessary rather than merely configured.

Both decisions were right about browsers and wrong about the product. **Convia's own client is a desktop application** — `M35` — with the interface embedded in it and the API on an installation somewhere else. It holds no cookie of Convia's origin, sends no `Origin`, and cannot be made to. Under the old rule it could not sign in, and could not make a single request afterwards.

## Decision

**The session is the same credential, presented two ways.**

- **A cookie**, for the page Convia serves in development, with everything ADR 0007 decided about it unchanged: `__Host-`, `HttpOnly`, `Secure`, `SameSite=Lax`.
- **An `Authorization: Bearer cvs_...` header**, for a client that is not a browser, which keeps it where the operating system keeps secrets.

Only this family is read on this surface. An application's key presented here is still never looked at, and a session presented on the tenant surface is still refused by shape, which is the separation the four families have always had.

### The origin check guards the cookie, not the surface

CSRF is a thing that happens to **ambient** credentials: the browser attaches a cookie to whatever request the page makes, including one another site caused. Nothing can make a browser attach an `Authorization` header it was not asked for, and Convia's application is not a browser at all.

So the exact `Origin` match now runs when the request carries the cookie, and not otherwise. A request carrying both is guarded, because the cookie was sent either way.

### Signing in tells the two apart by what only a browser sends

There is no session yet to guard when somebody signs in or registers, and login CSRF is a real thing. A browser sends `Origin` on every request that changes something, so:

- **with `Origin`**, it is matched exactly as before, and the answer is the cookie and nothing else;
- **without it**, the caller is not a browser, and the answer carries the session in `token` and sets no cookie.

A browser cannot produce the second case, so nothing a page can do reaches it. Changing a password rotates the session the same way: `204` and a new cookie for a browser, `200` and the new token for a client that holds one.

### Why not the alternatives

**A route of its own for the application** would have meant two ways to sign in, two paths to keep correct, and a contract that describes the client rather than the act.

**Letting the application send an origin of its own** would have kept the check's shape and emptied its meaning: any client can write that header, so a check that accepted one would be checking nothing while still looking like a protection.

## Consequences

- ADR 0007's "only in a cookie" no longer holds; everything else it decided does.
- ADR 0009's reason for serving the interface from Convia's origin is spent. The page stays for development, and the desktop application embeds the interface instead.
- A client that holds the token holds a bearer credential: a copy taken from a keychain, a log, or a crash dump works until the session is revoked or expires. The cookie's `HttpOnly` protection has no equivalent here, and the application's job is to keep it out of both.
- Anything that presents the token is served without an origin check, so a program acting for a person is no longer told it must be a page.
