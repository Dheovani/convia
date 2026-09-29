# Data protection

This is `M23-002`, `M23-003` and `M23-004` — **what Convia holds, what protects it on the way, and what protects it at rest** — and the three are one document because separating them produces three lists of the same tables.

It is a companion to [`threat-model.md`](threat-model.md), which asks who is on the other side of each boundary. This one asks what is worth taking.

## Classification

Four classes. What decides the class is **what somebody could do with it**, not how private it feels.

### Content — the conversations themselves

| Where | What |
| --- | --- |
| `messages.body` | what people said to each other |

**It exists in exactly one place, and that is a property worth keeping.** An event about a message carries the room, the sequence, who wrote it and whether it has been edited or withdrawn — and deliberately **not the body**: a conversation is the last thing that should be copied to every webhook destination an application registered. So neither `event_journal.body` nor `webhook_deliveries.payload` ever holds message text, and a subscriber that wants it reads it back through the API, where the scope it holds is checked.

That also means a late webhook retry cannot write an older text over a newer one, and it means the blast radius of a leaked delivery record is metadata rather than a conversation.

### Credentials — what lets somebody act

| Where | What | Held as |
| --- | --- | --- |
| `credentials.digest` | application keys (`cvk_`) | SHA-256 |
| `operator_credentials.digest` | operator keys (`cvo_`) | SHA-256 |
| `invitations.digest` | invitations (`cvi_`) | SHA-256 |
| `sessions.digest` | sessions (`cvs_`) | SHA-256 |
| `accounts.password_digest` | passwords | argon2id |
| `accounts.sealed_private_key` | a person's signing key | sealed by their password |

**Nothing in this class can be presented back.** A plain digest is right for the first four because each is ~130 bits of Convia's own randomness, which cannot be searched at any rate; argon2id is right for a password because it is low-entropy and chosen by a person. The contradiction is deliberate and the reason is where the entropy came from.

`accounts.sealed_private_key` is the one Convia cannot open at all: the key that signs requests to other installations is sealed by a password Convia never stores, so somebody holding the database can read conversations but cannot **act as** anybody. That is also why there is no password reset.

In memory, every one of these passes through `secret.Value` or `accounts.Password`, which render as `[redacted]` to `fmt` and `slog`, with compile-time assertions that make dropping that a build error.

### Who and where — the social graph

| Where | What |
| --- | --- |
| `accounts.username`, `users.display_name` | what somebody is called |
| `room_members`, `room_bans`, `participants` | who is in a room, who was put out of one |
| `remote_rooms` | which installations somebody has rooms on |
| presence (never a table: this process, or Redis) | whether somebody is around |
| `messages` minus the body | who spoke, when, how often |

Presence is the one thing here that never reaches PostgreSQL. **Where it does live depends on the deployment**: one instance keeps it in its own memory, and several keep it in Redis, which is the same decision the event stream makes and from the same setting. Either way it expires on a clock rather than being deleted, so forgetting it is the default rather than an operation somebody has to run — and either way it is **personal data in a store that is not the database**, which is why Redis must be `rediss` in production and why encrypting it is named below as the deployment's.

**This is the class that is easy to underrate.** Nothing here is a conversation, and all of it together says who somebody talks to, how often, and on which installations — which is frequently more revealing than any single message. `remote_rooms` in particular is the only place that records that two installations have anything to do with each other.

### Application-owned — whatever a tenant put there

| Where | What |
| --- | --- |
| `users.metadata` | a JSONB map an application writes |
| `users.external_subject` | the application's own identifier for a person |

**Convia does not know what is in here.** It is bounded — sixteen entries, forty-character keys, values of 256 bytes, four kilobytes in total — but bounded is not the same as understood. An application that writes a national identity number into it has created an obligation Convia cannot see, which is why this class exists separately and why `M23-017` and `M23-018` have to treat it as personal data by default.

### Operational — the rest

Identifiers, timestamps, statuses, `idempotency_keys`, `peer_nonces`, `event_journal`, `webhook_deliveries`. Sensitive as a set rather than individually: a delivery record says which events an application saw and when, which is the social graph again at a coarser grain.

`peer_nonces` and `event_journal` are the two that expire by design — a nonce once a replay of its request would be refused anyway, a journal entry after a day.

## In transit

Every connection Convia makes or accepts, and what encrypts it.

| Connection | In production | Enforced by |
| --- | --- | --- |
| A client → Convia | HTTPS, terminated at a reverse proxy | the deployment; see `M34-002` |
| Convia → PostgreSQL | `sslmode` of `require`, `verify-ca` or `verify-full` | configuration refuses to start otherwise |
| Convia → Redis | `rediss` | configuration refuses to start otherwise |
| Convia → the media plane | `https` | configuration refuses to start otherwise |
| Convia → an application's webhook | `https` | the destination guard refuses plain `http` |
| Convia → another installation | `https` | the same guard |
| Another installation → Convia | HTTPS at the proxy, plus a signature | the deployment, and `internal/peers` |
| The media plane → Convia | HTTPS at the proxy, plus a signature | the deployment, and the media API secret |
| A client → a media plane | DTLS-SRTP | WebRTC itself |

**Three things are worth saying plainly about this table.**

First, **`require` is not verification.** PostgreSQL's `require` encrypts and does not check who it is talking to; only `verify-ca` and `verify-full` do. Convia accepts all three because the database is usually on a network the deployment controls, and refusing `require` would stop installations that are fine. A deployment whose database is not on such a network should use `verify-full`, and this document is where that is said.

Second, **Convia terminates no TLS itself.** Everything reaching it over HTTPS does so because a proxy in front of it does the work. That is a deployment responsibility today and is `M34-002`; until then, an installation reachable from outside and configured without one is carrying sessions and signed requests in the clear.

Third, **plain `http` between installations is a development affordance**, gated on the environment rather than on a flag. There is no setting that turns it on in production, because the only reason to want one is the reason not to have one.

## At rest

**Convia encrypts nothing at rest itself, and that is the decision rather than an omission.**

What it does instead is hold nothing in a form that can be presented: credentials are digests, passwords are argon2id, and a person's signing key is sealed by a password Convia never stores. Encrypting the same rows with a key the same process holds would protect against a stolen disk and against nothing else, while adding a key nobody has a story for losing.

So the responsibilities divide like this:

| | Whose |
| --- | --- |
| Encrypting the database volume and its backups | the deployment's |
| Encrypting Redis, if what it holds warrants it | the deployment's |
| Holding no presentable secret in either | Convia's, and already true |
| The key that opens a person's signing key | **the person's** — it is their password, and nobody else has it |
| The media API secret and the database password | the deployment's, via `M23-005` |

**The one key Convia genuinely owns is the one it deliberately cannot use.** `accounts.sealed_private_key` is opened with a key derived from the password on each request that needs it and dropped when the request ends. There is no escrow, no recovery, and no administrative override — which is the property that makes an installation's operator unable to impersonate the people on it, and the reason losing a password loses the account.

`M23-004` is therefore answered as: **key ownership is the person's for identity, the deployment's for storage, and Convia's for neither.**

## What this leaves open

- **No secret manager.** The media API secret, the database password and the LiveKit key are environment variables. `M23-005`.
- **No defined rotation.** Nothing says how often a signing key or an API secret changes, or what happens to what was signed with the old one. `M23-006`.
- **No retention policy for content**, and deliberately. A deleted **person** is erased thirty days later — see [`users.md`](users.md) — but a message nobody asked to remove is kept, because truncating a conversation nobody asked to truncate is worse than a large table.
- **No classification of what an application writes into `users.metadata`**, because Convia cannot see it. `M23-018` is where somebody qualified decides what that obliges.
