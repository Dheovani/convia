# Sessions

**A session proves who a person is. It carries no authority over anything.**

This is the fourth credential family in Convia and the first that a person, rather than a program, presents. The domain lives in [`internal/sessions`](../internal/sessions) and [`internal/accounts`](../internal/accounts), the endpoints are in [`api/openapi.yaml`](../api/openapi.yaml), and the reasoning is in [ADR 0007](adr/0007-a-session-is-a-person-not-a-tenants-authority.md).

## The four families

| Family | Prefix | Held by | Presented as |
| --- | --- | --- | --- |
| Application key | `cvk_` | an integration's server | `Authorization: Bearer` |
| Operator key | `cvo_` | whoever runs Convia | `Authorization: Bearer` |
| Invitation | `cvi_` | the person invited | `Authorization: Bearer` |
| **Session** | `cvs_` | **a person's browser** | **a cookie** |

Each surface reads only its own kind. A session cookie is never looked for on the tenant routes and an API key is never looked for here — so a credential offered to the wrong surface is not so much rejected as never read.

## What a session is *not*

Everywhere else in Convia, authority belongs to an **application**: a key carries scopes over a whole tenant, and every person in that tenant is data the key can read.

A session is not that with a different prefix. It carries **no scopes at all**.

The reason is worth being blunt about. If signing in produced an application's authority, then every person who signed in could mint a permanent `cvk_` key — one that keeps working after they sign out, after they change their password, after their account is deleted. Every guarantee on this page would be decorative.

So what a person may do is decided **per operation, against the person**, by the domain that owns it. Those routes are being added deliberately, one at a time. Today the session surface is exactly what is on this page: sign in, sign out, say who you are, change your password.

## Why accounts at all, if I host this myself?

Because a deployment is an **installation**, not a person — and a conversation needs people. A call takes at least two, and there is no way to hold one where the only identity is the server.

**And an instance is not an identity either.** Since M16, several instances can be one deployment behind a load balancer, sharing a database and a Redis. Accounts live in PostgreSQL, so every instance sees the same people. Adding a machine adds capacity and nothing else.

So Convia holds four kinds of identity, and they answer four different questions:

| | Question it answers |
| --- | --- |
| **Application** | which *product* is calling Convia's API? |
| **Operator** | who administers this installation? |
| **User** | which of an application's people is this? — asserted by that application, which already knows |
| **Account** | which person is looking at Convia's own interface right now? |

The first three served Convia as a **platform**. An account is the first that serves it as a **product**, and it is what the interface has no substitute for: a sidebar shows *somebody's* avatar, presence is about *somebody*, a message has an author, a call roster lists people.

### What they are not: a defence against a compromised machine

Worth saying plainly, because it is the natural assumption and it is wrong.

**Whoever has the machine has the database**, and with the database they can insert a row into `accounts` and sign in as whoever they like. argon2id makes *reading existing passwords* expensive — which matters, because people reuse passwords on services that are not yours — but it stops nothing for somebody already inside.

What accounts actually contain is narrower and more ordinary: one colleague reading another's conversations, a stolen laptop holding a session that can be scoped and revoked, and an audit log that says who did what.

### The cost, and where this is going

For a small installation, `convia account create` per person is operator work, and there is no password reset — [*Getting an account*](#getting-an-account) says why, and says that the out-of-band channel it leaves behind is the likeliest route to a real takeover here.

The direction that removes both problems for self-hosted deployments is **for Convia to stop holding passwords at all**: point it at the identity provider the host already runs — Keycloak, Authentik, a corporate directory — and Convia receives an identity rather than verifying one. No password to leak, no reset to perform by hand, and people are administered where they are already administered.

That is not built and is not on the roadmap yet. The shape of this package is what makes it affordable later: an account is already a row that points at a Convia user, so an externally-authenticated person needs a different way of *proving* who they are and nothing different about *being* somebody.

## Getting an account

There is **no self-service sign-up**, and no password reset. Both need email verification, which needs a mailer Convia does not have.

```bash
convia account create ana@example.com "Ana Ribeiro"
```

```
Created account acc_7KQZP4XN2VJH6TBWMDR3YAFC5E for Ana Ribeiro (ana@example.com)
Convia user: usr_7KQZP4XN2VJH6TBWMDR3YAFC5E

R4NDOMLYGENERATEDPASSWORD7

This password is shown once and is not stored. Convia cannot show it again.
```

**The password is generated, never chosen by the operator.** An operator who picks passwords reuses one across the accounts they create; a generated one has the same entropy as every other secret Convia mints.

Two consequences, and the second is the uncomfortable one:

- The most common enumeration oracle in web applications — a password-reset form — is simply absent here.
- **A forgotten password needs an operator, out of band, with no way to verify who they are talking to.** That social-engineering channel is the likeliest real account takeover in this design. An operator resetting a password should generate a new one, hand it over through a channel that proves identity, and expect every session to end.

## Signing in

```
POST /v1/sessions
Content-Type: application/json
Origin: https://convia.example

{ "email": "ana@example.com", "password": "..." }
```

```
HTTP/1.1 201 Created
Set-Cookie: __Host-convia_session=cvs_...; Path=/; Max-Age=1209600; HttpOnly; Secure; SameSite=Lax
Cache-Control: no-store

{ "account_id": "acc_...", "user_id": "usr_...", "email": "ana@example.com", "display_name": "Ana Ribeiro" }
```

`user_id` is the identifier **every other part of Convia** addresses this person by. An account is not a second notion of who somebody is: it points at a row in the first-party application's users, so rooms, calls, participants, and presence keep working through the domains that already exist.

### Every failure is the same failure

An address nobody has, a wrong password, a suspended account, a suspended person, a stored digest Convia cannot read — one `401`, one message.

Two things make that real beyond the wording:

- **The password is checked first, the lifecycle second.** Checking status first would reveal that an address belongs to a suspended account without needing its password. Reporting suspension differently *after* a correct password would confirm the password was right. Both are oracles; doing the expensive, uninformative work first and answering identically afterwards is what avoids them.
- **An unknown address is hashed anyway**, against a decoy with the same cost parameters, so it takes as long to refuse as a real one. A test asserts the decoy's parameters match the ones in force — because the day somebody raises the cost and leaves the decoy behind, the timing gap reopens with nothing failing.

## The cookie

`__Host-convia_session`, with `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, and no `Domain`. The same name in every environment, including development — `http://localhost` is a secure context, so the prefix works there, and a name that differed by environment would mean the production path was never exercised before production.

**The `__Host-` prefix is the load-bearing part.** It is enforced by the browser: only the exact host that set the cookie can set it. Without it, "host-only" holds only because Convia said so — cookies are scoped by domain rather than by origin, so an XSS on `docs.convia.example`, a forgotten preview environment, or a network attacker serving plain http on any sibling could set `Domain=.convia.example` with the same name. The browser would then send both, and Convia would read whichever came first.

`Max-Age` carries the **idle** window rather than the absolute one, so a browser stops sending a credential that stopped working. It is `Max-Age` rather than `Expires` because `Expires` depends on the client's clock.

## Two ways a session ends on its own

| | Window | Extended by use? |
| --- | --- | --- |
| Idle | 14 days | yes |
| Absolute | 90 days | **no** |

The idle window is measured from last use, and last use is written **at most once an hour** rather than on every request. `docs/authentication.md` lists `last_used_at` on an application credential as not implemented for exactly that cost; the difference here is the caller — one browser, not a fleet.

The consequence is stated rather than hidden: the effective idle window is fourteen days to **fourteen days and an hour**. Staleness only ever shortens it, never lengthens it.

And an honest note on what an idle timeout is worth for a product like this: a tab left open that reconnects a WebSocket keeps a session alive indefinitely, and no server-side timer can tell that from somebody at the keyboard. What this actually bounds is **how long a stolen cookie stays useful after its owner stops working**, which is still worth having, and is not the same claim.

## Signing out

```
DELETE /v1/sessions/current    # this browser
DELETE /v1/sessions            # every session, including this one
```

**Revoking the row is the sign-out.** A session token is a bearer credential, so Convia does not trust a client to forget it; clearing the cookie is a courtesy to a browser that would otherwise keep sending something dead.

Signing out answers the same way whether or not the session was still live, so it cannot be used to ask whether one is.

Signing out **everywhere** deliberately does not spare the browser asking. Somebody reaching for it believes a device was lost.

## Changing a password

```
PATCH /v1/me/password
{ "current_password": "...", "new_password": "..." }
```

Three things happen together:

1. the password changes;
2. **every other session ends**, because the ordinary reason to change a password is believing somebody else has it;
3. **this session is rotated** — new session, new secret, new cookie — because the token in this browser may have leaked too.

The current password is required. A stolen session must not be enough to lock the owner out of their own account.

**The only rule for a new password is length: at least 12 characters.** No composition requirements, ever. A capital, a digit and a symbol make passwords more predictable rather than less, because people satisfy those rules in the same few ways.

## Why a password is hashed differently from every other secret

`M07-004` stores application keys as a plain SHA-256 digest, and gives the reason: 130 bits of randomness cannot be searched, so a slow hash buys nothing and costs latency on every request.

A password is the opposite kind of secret — short, chosen, reused, **searchable** — so the same reasoning reaches the opposite answer: **argon2id**, 64 MiB, three passes.

The two live side by side on purpose. The rule is chosen by where the entropy came from, not by habit. A **session token**, which Convia generates, goes back to SHA-256 for the original reason.

The stored digest carries its own parameters (`$argon2id$v=19$m=65536,t=3,p=1$...`) rather than taking them from a constant, which is what makes raising the cost later possible: an old digest stays verifiable by its own parameters and is replaced at the one moment Convia holds the password in the clear — a successful sign-in.

### Hashing is bounded, and that can be a `503`

argon2id's memory cost is paid per concurrent hash, on an **unauthenticated** route. Four at a time, a short wait for a place, then:

```
HTTP/1.1 503 Service Unavailable
Retry-After: 1
{"error":{"code":"unavailable","message":"Convia is verifying too many passwords at once. Try again in a moment."}}
```

**This is not a refusal of the credentials** and must never be read as one. Without the bound, anybody who can reach the API decides how much memory Convia allocates.

## Cross-site request forgery

Three mechanisms are in play and **only one of them carries the weight**. It is worth being precise, because counting them as three independent layers would be wrong.

| | Stops | Does not stop |
| --- | --- | --- |
| `SameSite=Lax` | cross-**site** POST, PUT, PATCH, DELETE | a **sibling subdomain** (same-site); top-level GET navigation |
| JSON content type | the `enctype="text/plain"` form trick | a route with **no body** |
| **`Origin`, exact match** | both of the above | a proxy that strips the header |

So `Origin` is **required** on every unsafe method here, matched **exactly** against the address Convia was reached at, and an absent or `null` value is refused. Exactly rather than by suffix — a suffix match is precisely what lets `evil-convia.example` or a sibling back in.

`Sec-Fetch-Site` is consulted where present, but it is **not** a fourth layer: it shipped in the same browser generation as `SameSite`, so any client old enough to ignore one lacks the other.

One invariant holds the first row up: **no route on this surface changes state on a GET**, because `SameSite=Lax` sends the cookie on a top-level GET navigation. A test walks the route table and enforces it.

**The check is middleware, applied to the surface, not a call inside each handler.** It started as the latter and that was a mistake with a cost: when M31 added seven person-facing routes for messages, four of them changed state and none of them called it. Nothing failed, because nothing was watching — the check was a habit each new handler had to remember. It is now wrapped around every route declared on a browser surface, inside authentication so that a request with no session is still answered as unauthenticated, and a second test walks the table and refuses to let a state-changing route exist without it.

There is **no CORS configuration**, because there is no cross-origin to permit. [ADR 0009](adr/0009-convia-serves-its-own-interface-from-its-own-origin.md) records why the interface is served from this same origin, which is what keeps that true.

## What is written down, and what is not

Convia records sign-in, sign-out, sign-out-everywhere, password change, session eviction, and **refused** sign-ins. Each line carries the account identifier and, on a refusal, whether the password or the status was the reason.

**That reason is never returned to the caller.** The asymmetry is the point: an operator investigating a locked-out colleague needs to know which it was, and the person at the form must not be able to tell.

Never logged: the password, the digest, the session token, or the submitted email. Identifiers Convia assigned say what an operator needs without putting a credential or a contactable address into a file that is shipped and retained.

## Running it

```bash
CONVIA_FIRST_PARTY_APPLICATION=app_MXHJAY4MJNX2FO22XWJ3XNCKHT
```

The identifier of the application that owns Convia's own product — an identifier rather than a name, because names are not unique and there is no lookup by one. Create it once with the operator API, then configure it.

**Unset means nobody signs in to Convia itself**, and the session routes are not registered at all. That is a supported deployment: Convia is a platform first, and an instance serving only integrations has no use for them.

An instance that *is* configured and has no accounts says so at startup, the same way one with no operator credential does:

```
a first-party application is configured but no account can sign in
  remedy=create one with: convia account create <email> <display-name>
```

## Known gaps

Named here rather than discovered later.

- **No per-account rate limiting.** Failed sign-ins are budgeted per caller address, separately from and far more tightly than the rest of the API. An attacker spread across many addresses is bounded only by the password's entropy. A naive per-account lockout is a denial of service against a named person; doing it properly needs state shared between instances, which the Redis from M16 now makes possible.
- **Revocation does not reach a live event stream.** `GET /v1/events` verifies once at the handshake and then streams for hours, so signing out everywhere leaves an open stream running until it closes. It is a pre-existing gap that applies equally to a revoked application key.
- ~~**No security headers.**~~ They arrived with the interface, as this said they would. The page is served under `default-src 'none'` with no `unsafe-inline`, alongside `nosniff`, `no-referrer`, and `same-origin` opener isolation. See [`interface.md`](interface.md#the-policy). The API's own responses still carry none of their own, which matters less than it sounds — they are JSON, served with a correct content type behind `nosniff` — but it is a real remaining difference and is named here rather than counted as done.
- **No password reset, and no email verification.** Both wait for a mailer. See *Getting an account* for what an operator does meanwhile, and why that channel deserves care.
