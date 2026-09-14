# Sessions

**A session proves who a person is. It carries no authority over anything.**

This is the fourth credential family in Convia and the first that a person, rather than a program, presents. The domain lives in [`internal/sessions`](../internal/sessions) and [`internal/accounts`](../internal/accounts), the endpoints are in [`api/openapi.yaml`](../api/openapi.yaml), and the reasoning is in [ADR 0007](adr/0007-a-session-is-a-person-not-a-tenants-authority.md) and [ADR 0011](adr/0011-an-account-is-local-and-its-identifier-is-its-key.md).

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

So what a person may do is decided **per operation, against the person**, by the domain that owns it.

## Accounts belong to the installation

An installation holds as many accounts as people create on it, the way a password manager's file holds as many entries as somebody adds. There is no email, no operator, and nothing to configure: Convia makes its own application the first time it starts, and a person registers from the sign-in page.

So Convia holds four kinds of identity, and they answer four different questions:

| | Question it answers |
| --- | --- |
| **Application** | which *product* is calling Convia's API? |
| **Operator** | who administers this installation? |
| **User** | which of an application's people is this? — asserted by that application, which already knows |
| **Account** | which person is looking at Convia's own interface right now? |

**An instance is not an identity.** Several instances can be one deployment behind a load balancer, sharing a database and a Redis. Accounts live in PostgreSQL, so every instance sees the same people.

## Creating an account

```
POST /v1/accounts
Content-Type: application/json
Origin: https://convia.example

{ "username": "ana", "password": "..." }
```

```
HTTP/1.1 201 Created
Set-Cookie: __Host-convia_session=cvs_...; Path=/; Max-Age=1209600; HttpOnly; Secure; SameSite=Lax
Cache-Control: no-store

{ "account_id": "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "user_id": "usr_...", "username": "ana", "handle": "ana#7KQZP4XN2VJH6TBWMDR3YAFC5EC" }
```

The account is created and its owner is signed in, in one request.

- **The username** is 3 to 32 characters of lowercase ASCII letters, digits, dots, dashes and underscores, beginning with a letter or digit. It is matched case-insensitively, unique on the installation, and fixed once chosen. The alphabet is narrow on purpose: Cyrillic `а` and Latin `a` look identical, and two names nobody can tell apart are how an invitation reaches the wrong person.
- **The password** is at least 12 characters, and that is the only rule.
- **A taken username** answers `409`. That says the name exists, which a registration form cannot avoid.
- **Every attempt is charged to the caller's address**, successes included: twenty an hour, then `429`. What this guards against is somebody succeeding too often — filling the installation with accounts, or walking a list of names — which a budget of failures would never see. The origin is checked before the attempt is charged, so another page cannot spend a household's allowance.

### The identifier is a key's fingerprint

Registering generates an Ed25519 key pair. The account identifier is `acc_` followed by the first sixteen bytes of the SHA-256 digest of the public key, in base32. It is not assigned and cannot be copied onto a different key: whoever claims it can be asked to sign with the private half. That is what invitations between installations will rest on — an installation controls its own database and could write any identifier into it, so only an identifier that proves something is worth anything.

### The password seals the key, so it cannot be reset

The private key is stored only encrypted, with AES-256-GCM under a key derived from the password by argon2id, with a salt of its own and the public key bound in as associated data. **Nobody with the database can use it** — including whoever runs the machine.

The consequence is the password manager's, and the registration form says it before the account exists: **there is no password reset.** Nothing but the password opens the key, and the key is who the account is. A forgotten password loses the account.

### A handle is how a person is named to somebody else

`ana#7KQZP4XN2VJH6TBWMDR3YAFC5EC`: the username, a `#`, the identifier without its prefix, and one check character.

The username is what a person recognizes; the identifier is what cannot be forged. The check character is computed with the Luhn mod 32 algorithm over the identifier alphabet, and catches every single mistyped character and every swap of two neighbours except `A` and `7` — before anything is sent. A mistyped username is caught differently: the account the identifier names has another one. Parsing forgives what copying does — surrounding spaces, lowercase, dashes or spaces inserted to group the identifier.

## Signing in

```
POST /v1/sessions
Content-Type: application/json
Origin: https://convia.example

{ "username": "ana", "password": "..." }
```

The answer is the same `201` body and cookie as creating an account.

**Signing in also opens the account's key, and the session holds it** — wrapped with AES-256-GCM under a key derived by HKDF from the session's own secret, which Convia stores only as a SHA-256 digest. That is what lets this installation sign for the person toward another installation while they are signed in, and at no other time: the database alone opens nothing, and signing out ends it. See [`peers.md`](peers.md).

`user_id` is the identifier **every other part of Convia** addresses this person by. An account is not a second notion of who somebody is: it points at a row in the first-party application's users, whose external subject is the account identifier and whose display name is the username, so rooms, calls, participants, and presence keep working through the domains that already exist.

### Every failure is the same failure

A username nobody has, a wrong password, a suspended account, a suspended person, a stored digest Convia cannot read — one `401`, one message.

- **The password is checked first, the lifecycle second.** Checking status first would reveal that a name belongs to a suspended account without needing its password. Reporting suspension differently *after* a correct password would confirm the password was right.
- **An unknown username is hashed anyway**, against a decoy with the same cost parameters, so it takes as long to refuse as a real one. A test pins the decoy's parameters to the ones in force.

Registering already says whether a name is taken, so this is not what keeps usernames private — a username is half of a handle meant to be shared. What it keeps is the form's own promise: whatever went wrong, signing in answers alike.

## The cookie

`__Host-convia_session`, with `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, and no `Domain`. The same name in every environment, including development — `http://localhost` is a secure context, so the prefix works there, and a name that differed by environment would mean the production path was never exercised before production.

**The `__Host-` prefix is the load-bearing part.** It is enforced by the browser: only the exact host that set the cookie can set it. Without it, "host-only" holds only because Convia said so — cookies are scoped by domain rather than by origin, so an XSS on `docs.convia.example`, a forgotten preview environment, or a network attacker serving plain http on any sibling could set `Domain=.convia.example` with the same name. The browser would then send both, and Convia would read whichever came first.

`Max-Age` carries the **idle** window rather than the absolute one, so a browser stops sending a credential that stopped working. It is `Max-Age` rather than `Expires` because `Expires` depends on the client's clock.

## Two ways a session ends on its own

| | Window | Extended by use? |
| --- | --- | --- |
| Idle | 14 days | yes |
| Absolute | 90 days | **no** |

The idle window is measured from last use, and last use is written **at most once an hour** rather than on every request. The consequence is stated rather than hidden: the effective idle window is fourteen days to **fourteen days and an hour**. Staleness only ever shortens it, never lengthens it.

And an honest note on what an idle timeout is worth for a product like this: a tab left open that reconnects a WebSocket keeps a session alive indefinitely. What this actually bounds is **how long a stolen cookie stays useful after its owner stops working**, which is still worth having, and is not the same claim.

## Signing out

```
DELETE /v1/sessions/current    # this browser
DELETE /v1/sessions            # every session, including this one
```

**Revoking the row is the sign-out.** A session token is a bearer credential, so Convia does not trust a client to forget it; clearing the cookie is a courtesy to a browser that would otherwise keep sending something dead.

Signing out answers the same way whether or not the session was still live, so it cannot be used to ask whether one is. Signing out **everywhere** deliberately does not spare the browser asking.

## Changing a password

```
PATCH /v1/me/password
{ "current_password": "...", "new_password": "..." }
```

Four things happen together:

1. the password changes;
2. **the account's key is sealed again** under the new password — the same key, so the identifier and the handle do not change;
3. **every other session ends**, because the ordinary reason to change a password is believing somebody else has it;
4. **this session is rotated** — new session, new secret, new cookie — because the token in this browser may have leaked too.

The current password is required. A stolen session must not be enough to lock the owner out of their own account, and since the key is sealed by the password, out of their own identity with it. The new digest and the newly sealed key are written in one statement, so they never describe different passwords.

## Why a password is hashed differently from every other secret

`M07-004` stores application keys as a plain SHA-256 digest: 130 bits of randomness cannot be searched, so a slow hash buys nothing and costs latency on every request.

A password is the opposite kind of secret — short, chosen, reused, **searchable** — so the same reasoning reaches the opposite answer: **argon2id**, 64 MiB, three passes. A **session token**, which Convia generates, goes back to SHA-256 for the original reason.

The stored digest and the sealed key each carry their own parameters (`$argon2id$v=19$m=65536,t=3,p=1$...`, `$argon2id-aes256gcm$...`), which is what makes raising the cost later possible: old values stay usable by their own parameters and are replaced together at the one moment Convia holds the password in the clear — a successful sign-in.

### Hashing is bounded, and that can be a `503`

argon2id's memory cost is paid per concurrent derivation, on two **unauthenticated** routes. Four at a time, a short wait for a place, then:

```
HTTP/1.1 503 Service Unavailable
Retry-After: 1
{"error":{"code":"unavailable","message":"Convia is verifying too many passwords at once. Try again in a moment."}}
```

**This is not a refusal of the credentials** and must never be read as one. Registering derives twice — the digest and the key's seal — and both take a place in the bound.

## Cross-site request forgery

Three mechanisms are in play and **only one of them carries the weight**.

| | Stops | Does not stop |
| --- | --- | --- |
| `SameSite=Lax` | cross-**site** POST, PUT, PATCH, DELETE | a **sibling subdomain** (same-site); top-level GET navigation |
| JSON content type | the `enctype="text/plain"` form trick | a route with **no body** |
| **`Origin`, exact match** | both of the above | a proxy that strips the header |

So `Origin` is **required** on every unsafe method on the browser surfaces — signing in and registering included — matched **exactly** against the address Convia was reached at, and an absent or `null` value is refused. A page elsewhere that could post here could sign somebody in, or register them, as an account the attacker controls.

`Sec-Fetch-Site` is consulted where present, but it is **not** a fourth layer: it shipped in the same browser generation as `SameSite`.

One invariant holds the first row up: **no route on these surfaces changes state on a GET**, because `SameSite=Lax` sends the cookie on a top-level GET navigation. A test walks the route table and enforces it, and a second test refuses to let a state-changing browser route exist without the origin check. The check is middleware on the surface rather than a call inside each handler, because a check every new route has to remember is one a new route eventually forgets — four once did.

There is **no CORS configuration**, because there is no cross-origin to permit. [ADR 0009](adr/0009-convia-serves-its-own-interface-from-its-own-origin.md) records why.

## What is written down, and what is not

Convia records registration, sign-in, sign-out, sign-out-everywhere, password change, session eviction, and **refused** sign-ins. Each line carries the account identifier and, on a refusal, whether the password or the status was the reason. **That reason is never returned to the caller.**

Never logged: the password, the digest, the key in any form, the session token, or the username. Identifiers Convia derived say what an operator needs without putting a credential or a person's chosen name into a file that is shipped and retained.

## Running it

Nothing to configure. The first time Convia starts it makes its own application, `app_CONVIAAAAAAAAAAAAAAAAAAAAA`, and leaves it alone on every later start — so an operator who suspends it stops every session, and a restart does not undo that. See [`applications.md`](applications.md#standalone-convia-is-an-application).

Whoever runs Convia can stop somebody signing in, and let them back:

```bash
convia account suspend acc_7KQZP4XN2VJH6TBWMDR3YAFC5E
convia account activate acc_7KQZP4XN2VJH6TBWMDR3YAFC5E
```

### What accounts are not: a defence against a compromised machine

**Whoever has the machine has the database**, and can insert a row into `accounts` and sign in as it, or suspend anybody. What they cannot do is **use somebody's key**, because it is sealed by a password they do not have — which is what invitations between installations will depend on. argon2id makes guessing that password expensive, and a weak one is still a weak one.

## Known gaps

- **Signing in costs two argon2id derivations**: one to verify the password, one to open the key the session holds.
- **No verification code on first contact.** Two people comparing a short code out of band would stop somebody in the middle substituting an invitation. Named, not adopted.
- **No per-account rate limiting.** Failed sign-ins are budgeted per caller address. An attacker spread across many addresses is bounded only by the password's strength. A naive per-account lockout is a denial of service against a named person; doing it properly needs state shared between instances.
- **Registration is rationed per address, per instance.** Several instances each allow twenty an hour, as every limiter in Convia does until it moves to Redis.
- **Revocation reaches a person's live stream within a minute, not at once.** See [ADR 0010](adr/0010-a-persons-stream-is-authorized-per-room.md).
- **Revocation does not reach an application's live stream.** `GET /v1/events` verifies its key once at the handshake. It predates the session surface.
- **The API's own responses carry no security headers.** The page is served under a strict policy (see [`interface.md`](interface.md#the-policy)); the JSON responses rely on a correct content type behind `nosniff`.
