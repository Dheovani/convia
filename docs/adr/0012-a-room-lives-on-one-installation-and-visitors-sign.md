# ADR 0012 — A room lives on one installation, and its visitors sign

**Status:** Accepted
**Date:** 2026-09-14
**Milestone:** M18 — Standalone Web Application

## Context

[ADR 0011](0011-an-account-is-local-and-its-identifier-is-its-key.md) made accounts belong to one installation and made each account's identifier the fingerprint of an Ed25519 key sealed by its password. It did so because people on different installations were going to invite each other, and an identifier that proves nothing is copied rather than guessed.

This is that invitation. The product owner decided four things before anything was built:

- **An invitation is into a room.** Friendship invitations come later, in M32, and are a different thing.
- **The room lives on the installation of whoever invited**, and the invitee takes part through their own installation, which talks to the room's home on their behalf.
- **The invitee's installation can act for them only while they are signed in.**
- **A link lasts 24 hours and is used once.**

And from ADR 0011: the link carries the inviting installation's address, and there is no verification code on first contact.

## Decision

### The browser never meets the other installation

A person's page talks to the Convia they signed in to, exactly as before. That installation relays what they do in a room elsewhere to the room's home. Same origin, the cookie, the exact-origin CSRF check and the page's content policy stay as ADR 0009 left them. The other choices would have undone that: a browser talking to the home directly needs CORS, a cookie from another site, and a content policy that trusts any address someone pastes. Replicating the room onto both installations would make ordering, edits and withdrawals a synchronization problem.

### Every request between installations is signed with the person's key

Five headers: the account identifier, the public key, a timestamp, a nonce, and an Ed25519 signature. The signature covers, one per line: a version string, the method, **the authority the request was addressed to**, the path and query, the body's SHA-256, the identifier, the timestamp and the nonce.

The home believes the key only once its fingerprint is the claimed identifier, and the identifier only once the key verifies the signature. The timestamp must be within five minutes of the home's clock. The nonce is claimed in a table whose primary key decides, so a replay is refused even when two instances receive it at once. The authority is signed so that a home cannot replay a request it received to a third installation where the same person is also a member.

Nothing is issued and nothing needs revoking: the key is the credential, and it already exists.

### The key is held by the session, not by the installation

Signing needs the private key, and the key is sealed by the password. So signing in now opens it, and the session keeps it wrapped with AES-256-GCM under a key derived by HKDF from the session's own secret, bound to the session's identifier. Convia stores only the SHA-256 digest of that secret, from which the wrapping key cannot be derived. The database alone opens nothing. A request that needs to sign unwraps the key from its own cookie, uses it, and drops it. When the session ends — signed out, expired, revoked — nothing can sign for the person any more.

This is the choice the product owner made over a long-lived credential issued at acceptance. That would have been simpler, but whoever administers the invitee's machine could have acted for them in the room forever.

### A visitor is served by the handlers a signed-in person is

Accepting an invitation makes the person a user of the home's first-party application. That is the same user they already are if they also sign in there, because the external subject is the account identifier, and the key is the person. They also become a member of the room.

After that, the visitor surface verifies the signature, requires the signer to already be a user, and puts that person into the request exactly where a session would. The history, message, read-state, member and leave handlers serve them without knowing a visitor is there. Membership decides per room, a room they are not in answers `404`, and a suspended user stops on their next request. A signed request that merely arrives never creates a user; only accepting an invitation does.

### The link is not a secret

`https://home/invitations/rin_…` names the home and the invitation, and grants nothing. Previewing or accepting it needs a signature by the key the invited handle names, and the username must also be the one the inviter typed. Unknown, expired, used, withdrawn, and meant for somebody else are one `404`. Two acceptances at once cannot both succeed, because the claim is one conditional update.

The link names the home by the origin the inviter's browser sent. That is the address they reached Convia at, and in the ordinary case it is also the address others reach it at. The interface warns when that address is `localhost`.

### Following a link is following a stranger's address

The invitee's installation connects wherever a link points, from inside its own network. So the client is built on webhook delivery's destination guard: the address is checked at the socket on every attempt, loopback, private and link-local addresses are refused outside development, plain http is refused outside development, no proxy is consulted, and no redirect is followed. An answer that is not a JSON object within 1 MiB is treated as no answer.

What is relayed is re-encoded from what this installation decoded, and only the query parameters the route has are forwarded. A path value reaches the home's URL only if it is a valid identifier. **A home's `401` or `403` becomes this installation's `403`, never its `401`**, because the page reads a `401` as its own session ending.

## Consequences

- **Every session ended once.** Migration `00020` deletes them, because none held a key and only the password can provide one. Signing in now costs a second argon2id derivation to open the key.
- **Nothing is announced for a room elsewhere.** The page's event stream is about rooms here, so a room elsewhere is read on a five-second timer while it is open, and its row carries no unread count.
- **A home that does not answer is a room that cannot be read**, and it cannot be left either: any answer but a confirmation keeps the pointer, so a home that is gone, has moved, or refuses the person would hold it there for good. The person may therefore forget it here, after being told they stay a member at the home. That leaves a membership nothing here remembers, which is the lesser harm next to a room nobody can get rid of.
- **Installations on a private network can only invite each other in development mode.** That is the default for `CONVIA_ENVIRONMENT`. A production installation refuses private addresses, for the reason webhooks do. There is no setting that changes it.
- **The home is named by the Host a request arrived with.** A reverse proxy that rewrites Host breaks signatures until it preserves the original.
- **Calls between installations are not built.** The interface for calls does not exist yet (`M18-004`). When it does, a visitor will reach the home's media plane directly with a token the home issues, because media is never relayed through the control plane.
- **Nothing was adopted for a first contact.** No code for two people to compare. A person accepting a link sees the room's name, the inviter's handle and the home's address, and that is the check.
