# Authentication and Credentials

An **application** proves who it is to Convia with an API key. This document records the threat model and the decisions of milestone M07 in [`TODO.md`](../TODO.md). The domain lives in [`internal/credentials`](../internal/credentials), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

> **Status.** Both surfaces are authenticated. An application presents an application key; an operator presents an operator key. There is no configuration that serves either of them openly, and the `CONVIA_ADMIN_API` gate that once stood in for operator authentication has been removed. See [Not Yet Implemented](#not-yet-implemented) for what is still missing.

## Two Surfaces

| | Tenant-facing | Operator |
| --- | --- | --- |
| Paths | `/v1/users`, `/v1/credentials` | `/v1/applications/...`, `/v1/operator/credentials` |
| Key | `cvk_...` | `cvo_...` |
| Who the tenant is | Taken from the key | Named in the path |
| Scopes | `users:*`, `credentials:*` | `applications:*`, `tenants:*`, `operators:*` |
| Authentication | Required, every request | Required, every request |

The split exists because the two answer different questions. An application acts on **itself**, so naming a tenant would be redundant at best and a tenant-crossing bug at worst. An operator acts on **someone else**, so it must name them — and that is exactly why an application's key can never be enough to do it.

The operator routes exist to bootstrap: create the first application, issue its first credential. After that, an application manages its own users and its own keys through the tenant surface.

## The Two Key Families Never Cross

An operator credential lives in **its own table**, carries **its own scopes**, and is presented as **`cvo_`** rather than `cvk_`.

None of that is decoration. Every query in the credentials package carries an application in its `WHERE` clause; if an operator credential could live in that table with no application, one forgotten predicate would turn an omission into a privilege escalation. Kept apart, the tenant store cannot return an operator credential, because it does not read that table.

The distinct token prefix does the same work at the front door. A presented key names its own family, so Convia routes it to one verifier without querying for it, and **a key offered to the wrong surface is refused on its shape, before any database work**. An operator key on `/v1/users` and an application key on `/v1/applications` both fail the same way an unknown key does.

Operator scopes are named apart for a related reason. On an application key, `users:write` means "my users". An operator acts on someone else's, so reusing the name would make one string mean two different amounts of authority depending on which key carried it. A test asserts the two vocabularies do not overlap.

| Scope | Permits |
| --- | --- |
| `applications:read` | Listing and reading tenants |
| `applications:write` | Creating, renaming, suspending, activating, deleting tenants |
| `tenants:read` | Reading any application's users and credentials |
| `tenants:write` | Managing any application's users and credentials, including issuing a key on its behalf |
| `operators:read` | Reading operator credentials |
| `operators:write` | Issuing and revoking operator credentials |

## Bootstrapping the First Operator

Issuing an operator credential over the API requires presenting one, so the first cannot come from the API.

```bash
convia operator issue "deployment" applications:write tenants:write
```

It writes the row directly, which is why it needs database access — **the right authority to mint the key that administers Convia**. Whoever holds it could write that row by hand regardless; going through this command means the digest, the identifier format, and the scope validation are the same code the service uses.

The secret is printed once, to standard output rather than the structured log, because logs are shipped, retained, and read by people who should not receive key material.

`convia operator list` and `convia operator revoke <id>` complete the set, so withdrawing a key during an incident does not mean composing SQL under pressure.

**An instance with no active operator credential answers `401` on the whole operator surface.** That is the correct posture for a fresh install rather than a fault, but it is also the state an operator is least likely to intend, so Convia warns about it at startup. It is a warning and never a refusal to start: the tenant surface is unaffected, and taking it down would punish the applications already using it.

**Issuing cannot escalate here either.** The scopes requested must be a subset of the ones the calling key holds. Without that rule a key carrying only `operators:write` could mint one carrying authority over every tenant in Convia.

## Presenting a Key

```http
GET /v1/users
Authorization: Bearer cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5
```

A request that does not authenticate is answered `401` with a `WWW-Authenticate: Bearer` challenge. **Every reason produces the same answer** — absent, malformed, unknown, wrong secret, revoked, expired, or belonging to an application Convia no longer serves — so the response cannot be used to learn which keys exist or which were once valid.

A verification that fails because Convia could not reach its database is answered the same way. Granting access when verification could not run would be the worse mistake, and reporting it differently would tell an attacker when Convia is degraded.

A key that authenticates but lacks the scope an operation requires is answered `403 forbidden`. The two are distinct because the remedies differ: `401` needs a different key, `403` needs a broader grant.

## Where Authorization Happens

**Not in the handlers.** Each domain exposes an `Authorized` type that can only be constructed from a verified principal, and refuses any operation whose scope the principal does not carry, before the underlying service runs. The operator surface has the same thing: `AuthorizeOperator`, built from a verified operator principal.

That placement is deliberate. A check that lives in a handler is one a future route, command, or background job can forget. A check that lives in the operation itself cannot be reached around — and on the tenant surface the application comes from the principal too, so there is no request field that could name a different one, making a tenant-crossing bug at the transport layer not merely unlikely but unrepresentable.

The operator surface deliberately does take its tenant from the path, because an operator acts on someone else by definition. What bounds it is the key: crossing into a tenant requires an operator credential carrying a tenants scope, and no application key can hold one.

Tests cover every operation from both sides: with its scope, without it, and with no scopes at all, asserting in each refusal that the underlying service was never reached. An authorization that ran after the work would be no authorization at all.

## Rate Limiting Failed Attempts

Failed authentication attempts are budgeted per caller address: **60 attempts, refilled over a minute.** Exceeding it is answered `429` with a `Retry-After` header.

**Only failures are charged.** A client presenting a working key is never limited, however much traffic it sends. A client that does meet this limit is presenting a key that will not start working by being repeated — a revoked or expired credential in a retry loop is the usual cause.

This is **not** a defence against guessing a key. A secret with 130 bits of entropy cannot be searched at any rate. What the limit protects is the cost of the attempt: each well-formed but wrong key is a database read, and a flood of them is load Convia would otherwise pay for. The budget is checked **before** the key is verified, so an exhausted caller costs a map lookup rather than a query.

### What that costs

Checking before verifying has a consequence worth stating plainly: **while an address is out of budget, even a valid key from that address is refused.** Verifying it is precisely the work being declined. Counting a flood without stopping it would protect nothing, so this is the deliberate half of the trade.

The numbers are chosen around that. One indexed read takes a few hundred microseconds and PostgreSQL serves tens of thousands a second, so a tight budget would buy almost nothing while making the collateral refusal likely. Sixty per minute leaves a misconfigured client retrying every few seconds far from the limit, and still cuts a flood to one attempt a second.

### Running behind a proxy

**Convia reads `X-Forwarded-For` only from networks an operator named in `CONVIA_TRUSTED_PROXIES`.** Trusting a header nobody told it to trust would let a caller evade its own limit and spend someone else's budget by claiming their address.

Unset — the default — every client behind a proxy shares the proxy's address, so one misconfigured client can exhaust the budget for all of them. **Set it before deploying behind a proxy.** [`api-conventions.md`](api-conventions.md) documents the format and how the chain is read.

The reading is what makes the header safe to use at all. The chain is walked **from the right**, skipping trusted hops, because a proxy appends what it saw and anything a client invented sits further left. A caller connecting directly is charged to its own address whatever it claims, so the limiter cannot be turned into a weapon: a caller can neither escape its own budget by rotating the header nor spend someone else's by naming them. Both are asserted by tests.

### Where the state lives

In the process. Several Convia instances therefore enforce the budget per instance rather than across the fleet. That is the right trade while Convia runs as one — a shared limiter would put a network round trip on the path whose whole purpose is to be cheaper than the work it guards — and Redis is the answer when Convia is actually replicated.

A bucket exists only for an address that has failed recently, and the map is capped. When it is full and nothing has recovered, a new caller is allowed rather than refused: turning a memory bound into a way to lock out every address at once would be the outage the limit exists to prevent.

## Issuing Cannot Escalate

When an application issues a key for itself, **the scopes requested must be a subset of the ones the calling key already holds.**

Without that rule a key carrying only `credentials:write` could mint one carrying `users:write`, and every credential in Convia would effectively grant everything. The refusal happens before the credential is created, so the escalated key never exists even briefly.

## Threat Model

What Convia is defending, and against whom.

| Asset | Threat | Mitigation |
| --- | --- | --- |
| A key in flight | Interception | TLS is required in production; the key is never placed in a URL, only in a header |
| A key at rest in Convia | Database disclosure — backup, replica, or breach | Only a SHA-256 digest is stored, so a stolen database yields no working key |
| A key at rest elsewhere | Leaked into a repository, log, or ticket | A distinctive `cvk_` prefix lets secret scanners recognize one; Convia never logs key material |
| A key after compromise | Continued use by an attacker | Revocation takes effect on the next request, with no window and no cache to invalidate |
| Another tenant's data | A valid key used against another application | The tenant comes from the verified key, never from the request |
| The existence of keys | Probing for valid identifiers | Every failure returns one indistinguishable error |
| A digest | Recovery by timing the comparison | Comparison is constant-time |
| The whole surface | A key with more access than it needs | Scopes are explicit and required; there is no implicit access |
| Convia's own capacity | A flood of attempts with keys that will never work | Failed attempts are budgeted per address, checked before the key is read |

**Out of scope for Convia.** The application's own accounts, passwords, and sessions are the application's responsibility. Convia authenticates the *application*, not the people using it — [`users.md`](users.md) explains why Convia deliberately holds no credentials for them.

## Why Opaque Keys and Not JWTs

A signed token carries its claims, so any holder of the signing key can verify it without asking the issuer. Convia is the only party that verifies these keys, so that property buys nothing here, and it costs a great deal:

- **Revocation would stop being immediate.** A signed token is valid until it expires. Withdrawing one before then needs a denylist that every verifier consults — which is the database lookup a signed token existed to avoid.
- **Signing keys need managing.** Generation, storage, rotation, and clock-skew tolerance are all real work that an opaque key does not require. `M07-010`, `M07-017`, and `M07-018` exist for signed tokens and do not apply while keys are opaque.
- **Nothing is gained in return.** No third party needs to verify a Convia key offline.

**The cost of the choice is honest:** every authenticated request reads one row by primary key. That is a deliberate trade — an indexed lookup in exchange for revocation that actually works. If it ever becomes a bottleneck, the fix is a short-lived cache with a bounded staleness window, and the staleness would have to be argued for explicitly, because it would weaken revocation.

This decision applies to server-to-server credentials. Media grants, which are handed to browsers and verified by a media server that is not Convia, are a different problem and will be signed.

## Identifier and Secret

A key is one string with three parts:

```
cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5
└┬┘ └───────────┬────────────┘ └───────────┬────────────┘
 │              │                           │
 │              │                           the secret, never stored
 │              the credential identifier, public
 the prefix, so scanners recognize a leak
```

The identifier travels with the secret **on purpose**. Verification looks up exactly one row by primary key and then compares one digest. Without it, verification would have to test the presented secret against every credential of every application — work that grows with the size of the system and cannot be indexed.

The identifier is not secret. It appears in listings, in audit records, and in the key itself. The secret is the only part that proves anything.

## Storage

Convia stores `SHA-256(secret)` and never the secret.

**Why not bcrypt, scrypt, or Argon2?** Those exist to make guessing expensive when the secret is a human-chosen password with maybe 30 bits of entropy. This secret is 26 base32 characters from `crypto/rand` — roughly 130 bits — so it cannot be guessed, dictionary-attacked, or found in a rainbow table. A deliberately slow hash would add latency to every authenticated request and buy nothing against an attacker who would need to exhaust a 130-bit space regardless.

The comparison is `crypto/subtle.ConstantTimeCompare`, so the time taken does not depend on which byte differs.

## Scopes

Scopes name **domain operations**, not HTTP routes, so a permission keeps its meaning when a route is added, renamed, or split.

| Scope | Permits |
| --- | --- |
| `users:read` | Reading the application's users |
| `users:write` | Resolving, updating, and changing the lifecycle of its users |
| `credentials:read` | Reading its own credentials, never their secrets |
| `credentials:write` | Issuing and revoking its own credentials |

**Scopes are required.** A request without them is rejected rather than given a default, because a default is either useless or quietly broader than anyone asked for. There is no scope that grants everything.

Operations on applications themselves — creating, renaming, deleting a tenant — are deliberately absent. Those are operator actions, not something an application does to itself, and they stay behind the administrative gate until an operator credential exists.

## Lifecycle

**Issuing** returns the secret exactly once, in the response that creates the credential. Convia cannot show it again because Convia does not have it. A lost secret is not reset; it is replaced.

**Expiry** is optional and must be in the future. It needs no scheduled job: the state is derived from the timestamp on every verification, so a credential stops working the moment it passes.

**Revocation** takes effect on the next request. There is no grace period and nothing to propagate. Withdrawing a key during an incident, including withdrawing many at once, is [`runbooks/credential-revocation.md`](runbooks/credential-revocation.md).

**Rotation** is not a separate operation, because composing the two existing ones is what gives zero downtime:

1. Issue a second credential with the same scopes.
2. Deploy it, and confirm traffic is using it.
3. Revoke the first.

An endpoint that swapped a key atomically would give the fleet no window to pick up the new one, which is the opposite of what rotation is for.

**A withdrawn credential stays visible.** Unlike a deleted tenant, a revoked key is an operational fact an operator needs: what was issued, and when it stopped working. Listings include revoked and expired credentials with their status.

## Suspension Withdraws Access

A credential stops authenticating the moment Convia stops serving its application. Suspending an application therefore withdraws every key it holds at once, and activating it restores them, because the check happens on every request rather than at issue time.

This is why the credentials domain asks whether an application is **active** rather than whether it **exists**. Administering a suspended tenant is still possible for an operator; acting *as* that tenant is not.

## What Is Never Logged

Convia never writes a secret, a digest, or a presented key to any log or audit record. Audit entries identify a credential by its public identifier and record its scopes. An audit trail that carried key material would be a second copy of the thing it exists to protect.

Failure to authenticate is not audited per attempt. What bounds a flood of failures is the budget described in [Rate Limiting Failed Attempts](#rate-limiting-failed-attempts), not an audit record per attempt.

A credential revoked directly in the database, as an incident sometimes requires, produces no audit event either: the record is written by the service, and a direct `UPDATE` does not go through it. [`runbooks/credential-revocation.md`](runbooks/credential-revocation.md) says what to capture by hand instead.

## Not Yet Implemented

- **bulk revocation**, so withdrawing many of one tenant's keys is still one request per credential;
- **`last_used_at` on an operator credential**, with the same cost and the same open design question as the tenant equivalent below;
- **a cache for verification**, which is the answer if the per-request lookup ever becomes a bottleneck. It is not built, because any staleness weakens revocation and would have to be argued for;
- **`last_used_at` on a credential**, which operators will want in order to retire keys nobody presents. It costs a write on every authenticated request, so it needs a design rather than a column;
- **media grants**, which are signed rather than opaque and belong to the media plane.
