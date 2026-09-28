# Threat model

This is `M23-001`, and it is meant to be **read against the code rather than instead of it**. Every claim here names where it is enforced, so a claim that stops being true is a claim somebody can find.

It is organised by **trust boundary** — by who is on the other side and what they are trusted for — because that is where a mistake becomes somebody else's problem. It is not organised by feature: a feature that crosses three boundaries is three different questions.

**It is a living document.** A change that adds a boundary, or changes what one is trusted for, changes this. A change that adds a route to a boundary that already exists usually does not.

## What Convia is protecting

Three things, in this order:

1. **Conversations and calls between people.** What was said, who said it, who was in a room. This is the thing somebody loses if Convia is wrong.
2. **Whose authority is whose.** An application acting as a tenant, a person acting as themselves, an operator administering Convia, an installation acting for one of its people — four kinds of authority that must not become each other.
3. **The installation itself.** The network it runs in, the database it holds, and the work it can be made to do for free.

## The boundaries

| Who is on the other side | Reaches | Proves themselves with |
| --- | --- | --- |
| An application | `surfaceTenant` | an API key (`cvk_`) |
| An operator | `surfaceOperator` | an operator key (`cvo_`) |
| Whoever holds an invitation | `surfaceInvitation` | the invitation itself (`cvi_`) |
| A person signed in here | `surfaceSession` | a session (`cvs_`), in a cookie or a header |
| Somebody signing in or registering | `surfaceSignIn`, `surfaceRegistration` | nothing |
| Another installation, for a stranger | `surfacePeer` | a signature by that person's key |
| Another installation, for a member | `surfaceVisitor` | the same, plus already being a user here |
| The media plane | `surfaceMedia` | a signature with the media API secret |
| Nobody | `surfacePublic` | nothing; operational endpoints only |

And four Convia crosses outwards: another installation, an application's webhook destination, the media plane, and its own stores.

Which surface a route is on is **declared in the route table** (`internal/server/server.go`), not remembered inside a handler, and the `switch` that wires them has no default — a surface nobody connected stops the process at startup rather than serving routes with no authentication at all.

---

## An application → Convia

**Trusted for:** acting as its own tenant, within the scopes its key carries.

**Not trusted for:** naming which tenant it is. Convia takes the tenant from the key, never from the request, so `/v1/users` and `/v1/credentials` act on the caller's own data and cannot be pointed at anybody else's.

**What stops the obvious attack:** a key is ~130 bits of randomness stored as a SHA-256 digest, so the database does not hold anything that can be presented. Scopes are checked per route. Every failure charges a budget per address.

**Not covered:** a leaked key is a leaked key until it is revoked; there is no binding to an address or a certificate. `docs/runbooks/credential-revocation.md` is the procedure. A general per-tenant rate limit is `M13-008`/`M23-013` and does not exist.

## An operator → Convia

**Trusted for:** administering Convia itself — creating tenants, suspending them, issuing their first keys.

**Not trusted for:** anything inside a tenant's data that is not administrative. An operator key lives in its own table with its own scopes, so an application key can never reach this surface and an operator key can never act as an application.

**What stops the obvious attack:** the same shape-refusal every family gets, plus the first key being minted against the database rather than over the API — issuing one requires presenting one.

**Not covered:** there is no audit trail an operator cannot write to, and no second pair of eyes on a destructive administrative act.

## Whoever holds an invitation → Convia

**Trusted for:** taking the one place the invitation names, once.

**Not trusted for:** being the person it was addressed to. The invitation *is* the credential, which is what lets Convia enforce its expiry and its withdrawal rather than merely record them.

**What stops the obvious attack:** it is presented by its holder rather than asserted by the granter; redeeming it is one request that both proves and consumes.

## A person → Convia

**Trusted for:** being themselves, and nothing about any tenant.

**Not trusted for:** carrying authority. A session carries **no scopes** and cannot become a `credentials.Principal` ([ADR 0007](adr/0007-a-session-is-a-person-not-a-tenants-authority.md)). Person-facing routes are added one at a time rather than inherited, so a route that acts for a person exists because somebody wrote it down, not because a surface widened.

**What stops the obvious attack:**

- **Guessing a password.** argon2id where every other Convia secret is a plain digest, because the rule is chosen by where the entropy came from. Every sign-in failure answers identically, and an unknown username is hashed against a decoy so the timing does not answer either. Failed sign-ins are budgeted per address.
- **Filling an installation with accounts.** Registering is rationed by every use, not by its failures.
- **Stealing a session from the page.** The cookie is `HttpOnly` and `__Host-` prefixed, so a script in the page cannot read it and a compromised subdomain cannot write it.
- **CSRF.** Three independent layers: `SameSite=Lax`, a JSON content type an HTML form cannot send, and an **exact-match `Origin` check that fails closed** on every state-changing request. A test walks the route table and proves no route on this surface changes state on a `GET`, which is what keeps `SameSite=Lax` meaningful.

**Not covered:** there is no rate limit per *account*, deliberately — a naive one is a trivial denial of service against a known user. There is no password reset, by design: the account's private key is sealed by the password, so nobody without it can recover the account, including whoever runs the installation.

## Another installation → Convia

This is the newest boundary and the largest.

**Trusted for:** acting for **one person**, proved by a signature with that person's own key. The account identifier is the fingerprint of the key, so an installation that does not hold somebody's key cannot act as them, whatever it writes in its own tables ([ADR 0012](adr/0012-a-room-lives-on-one-installation-and-visitors-sign.md)).

**Not trusted for:** being trustworthy. **Anybody can register on any installation**, so a valid signature says who is asking and nothing about whether they should be served.

**What stops the obvious attack:**

- **Replay.** The signature covers the method, the authority, the target, a digest of the body, the account, a timestamp and a nonce. A nonce is claimed once, and a timestamp more than five minutes from the home's clock is refused.
- **Redirecting a request to a third installation.** The authority is signed, so a home cannot take a request sent to it and present it somewhere else the same person is a member.
- **Being served without being anybody.** `surfaceVisitor` requires the signer to already be a user here, made by accepting an invitation, never by a request that merely arrives.
- **Flooding.** Every request is charged against two budgets at once, successes included: 300 a minute per signer and 3 000 per address. The ratio matters — an address is a whole installation, and equal budgets would let one person spend everything theirs had.
- **Not speaking the same protocol.** Answered as its own thing rather than as a refused credential, so an operator is told what to fix.

**Not covered, and worth being explicit:** a home is the authority on its own rooms, so it can lie to a visitor about anything inside one — who is in it, what was said, whether the visitor is still a member. That is not an escalation, because the home could make all of those true. What a home must **not** be able to do is make the visitor's installation act on anything outside the rooms that visitor is in there, and that is enforced by looking every event's room up in the pointers kept for that person at that home.

## Convia → another installation

**What is crossed:** a link an invitation names, chosen by whoever sent it.

**Trusted for:** nothing. Following a link makes this installation connect somewhere from inside whatever network it runs in, on the word of anybody who can register.

**What stops the obvious attack:** the same guard webhook delivery uses — the address is checked **at the socket on every attempt**, so a name that resolves to a public address once and a private one a second later cannot get past it. No proxy is consulted, no redirect is followed, the answer is size-bounded, and reaching private addresses is a setting an operator turns on rather than a side effect of the environment.

## Convia → an application's webhook destination

**Trusted for:** nothing. The URL is chosen by a tenant and fetched by Convia's own process.

**What stops the obvious attack:** the same guard, and this is where it was written (`M15-011`). Without it, an application could register `http://169.254.169.254/latest/meta-data/` and read a cloud instance's credentials — the delivery record holds the response status, and a `200` is already an oracle.

## Convia ↔ the media plane

**Outwards**, the address is **operator configuration**, not a caller's choice, which is why this client carries no destination guard: there is no attacker input in the address.

**Inwards**, the media plane reports who connected and who left, signed with the API secret only it and Convia hold. **What it says is evidence, not an instruction**: every report is checked against Convia's own record before anything is acted on.

## A person's client → a media plane

The one place a person's browser or application talks to something other than the Convia it signed in to — and, for a call in a room elsewhere, to a **different installation's** media plane.

**What crosses:** a media address and a credential the home issued for one person in one call, with an expiry. Nothing about the session goes with it.

**What stops the obvious attack:** the credential is issued by the home, for the home, and names one participant in one call. Convia's own application allows encrypted media addresses; a Convia serving the interface as a page names its own media server **exactly**, scheme, host and port, and no other — which is why a call in a room elsewhere is something the application does and the page cannot.

## Convia ↔ its stores

**PostgreSQL** is the source of truth and must use a verified TLS mode in production. **Redis** carries events and presence between instances and must be `rediss` in production. Neither holds anything presentable: keys and sessions are digests, passwords are argon2id, and a person's private key is sealed by their password.

**Not covered:** encryption at rest is the deployment's (`M23-004`), and Convia has no secret manager (`M23-005`).

## The reference client → Convia

Convia's own interface and the desktop application are **one client among many** ([ADR 0021](adr/0021-the-reference-client-is-one-client-among-many.md)), and cross the person boundary above with no privilege of any kind. The application's own process holds the session so it never enters the webview, refuses to carry the routes whose answer *is* a session, and strips the browser's headers from what it does carry.

## Out of scope, deliberately

- **Somebody who holds the database.** They can read every conversation. Convia does not claim otherwise, and the one thing they cannot do is act as somebody — the private key is sealed by a password Convia never stores.
- **Somebody who runs the installation.** An installation is trusted by the people who chose it. Federation is built so that trusting *your* installation does not mean trusting anybody else's.
- **Denial of service by resources rather than by requests.** Budgets bound how often; they do not bound a deliberately expensive query.
- **Supply chain.** `M23-007` onwards.

## Open findings

| | |
| --- | --- |
| A home's `call` object is passed into this installation's own answer as the bytes it arrived as, which breaks the relay rule in [`peers.md`](peers.md) and can break this installation's published contract | `M33-009` |
| No rate limit per tenant | `M13-008`, `M23-013` |
| No audit trail an operator cannot write to | `M23` |
| No secret manager, no defined rotation | `M23-005`, `M23-006` |
