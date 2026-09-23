# Rooms between installations

**A room lives on one installation. People from other installations take part through their own.**

The domain lives in [`internal/peers`](../internal/peers), the routes are in [`api/openapi.yaml`](../api/openapi.yaml) under the `Peers` tag, and the reasoning is in [ADR 0012](adr/0012-a-room-lives-on-one-installation-and-visitors-sign.md).

## The shape of it

```
 Bia's browser ──cookie──▶ Bia's Convia ──signed with Bia's key──▶ Ana's Convia (the room's home)
```

- **The home** is the installation the room was created on. The conversation, its members and its history are there and nowhere else.
- **Bia's own Convia** keeps a pointer to the room and relays what Bia does in it, signed with Bia's key.
- **Bia's browser** talks only to Bia's Convia, exactly as it does for any other room. Cookies, the origin check and the content policy are unchanged.

## Inviting somebody

In a room here, somebody opens **People** and types a handle — `bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH`, which Bia finds on her avatar.

```
POST /v1/me/rooms/{room_id}/invitations
{ "handle": "bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH" }
```

```json
{
  "id": "rin_7KQZP4XN2VJH6TBWMDR3YAFC5E",
  "link": "https://ana.example/invitations/rin_7KQZP4XN2VJH6TBWMDR3YAFC5E",
  "invitee": "bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH",
  "expires_at": "2026-09-15T14:04:56Z"
}
```

- The handle's check character is verified before anything is created, so a typo is refused here rather than producing a link nobody can use.
- Somebody already in the room answers `409`.
- Somebody the room's owner banned answers `403`, and a link already sent to them stops admitting them. Lifting the ban before the link expires makes it work again.
- A visitor is never a room's owner, and never inherits one: moderation stays on the room's home ([ADR 0013](adr/0013-a-room-a-person-opens-has-an-owner.md)).
- **The link lasts a day and is used once.** `DELETE /v1/me/room-invitations/{id}` withdraws it before then, and `GET /v1/me/rooms/{room_id}/invitations` lists the ones a person made into a room that still work, with their links. Only their own: the other people in the room did not send them. Both the link handed back when an invitation is made and the ones in that list name this installation the same way: by the `Origin` when the request carried one, and otherwise by the address the request reached. Two things send no `Origin` and both are ordinary — a page reading a list, and Convia’s own application, whose requests are carried with the browser taken off them on purpose ([ADR 0019](adr/0019-a-session-travels-in-a-cookie-or-a-header.md)).
- **The link names this installation by the address the inviting browser used.** Opened as `localhost`, that is the inviter's own machine, and the interface says so: nobody elsewhere can follow it. Open Convia at an address others can reach.

**The link is not a secret.** It can go through any channel. Accepting it needs a signature by the key whose fingerprint is Bia's identifier, and the username must be `bia`. A link forwarded to somebody else, or read on the way, lets nobody else in.

## Joining

Bia pastes the link into **Join with a link** on her own Convia. It looks first, then joins:

```
POST /v1/me/invitation-previews   { "link": "https://ana.example/invitations/rin_…" }
POST /v1/me/remote-rooms          { "link": "https://ana.example/invitations/rin_…" }
```

Looking shows the room's name, who invited her, and which installation it lives on, before she accepts a stranger's room on the strength of a URL.

Joining makes Bia a user of Ana's installation, identified by her account identifier and named `bia#7QK4` (her username and the first characters of her identifier, so two people named `bia` can be told apart), and a member of the room. Bia's Convia keeps a pointer: the home, the room, who Bia is there, and the room's name.

If the link turns out to point at Bia's own installation, nothing is kept: she is simply a member of that room now, and it appears among her rooms. **That case never leaves the process.** A link whose home is the address the request reached is looked at and accepted here, against the same rows, rather than as a signed request this installation makes to itself — which would make the commonest invitation of all, two people on one Convia, depend on the server being able to dial its own address. Behind a private network, or on somebody's computer, it cannot, and `CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES` is a setting about *other people's* links, not a way to make your own work. Anything whose home is not that exact address travels, whatever it resolves to; guessing at which names mean this same machine is how the guard against reaching the private network gets talked out of its job.

Every failure to use an invitation — unknown, expired, used, withdrawn, meant for somebody else — is one `404`. Two acceptances at once cannot both succeed.

## Using the room

Bia's Convia serves the room under `/v1/me/remote-rooms/{id}`, with the same shapes and the same operations as a room here:

| Bia's browser | Relayed to the home as |
| --- | --- |
| `GET/POST …/messages` | `GET/POST /v1/peer/rooms/{room_id}/messages` |
| `PATCH …/messages/{message_id}` | `PATCH /v1/peer/messages/{message_id}` |
| `POST …/messages/{message_id}/delete` | `POST /v1/peer/messages/{message_id}/delete` |
| `GET/PUT …/read_state` | `GET/PUT /v1/peer/rooms/{room_id}/read_state` |
| `GET …/members` | `GET /v1/peer/rooms/{room_id}/members` |
| `POST …/leave` | `POST /v1/peer/rooms/{room_id}/leave` |

The home serves those with **the same handlers** it serves its own signed-in people. Membership decides, a room Bia is not in answers `404`, and suspending Bia's user at the home stops her on her next request.

What Bia's Convia relays is re-encoded from what it decoded, never forwarded as it arrived, and only the query parameters a route has are passed on.

**A home that refuses Bia answers `403` on Bia's Convia, never `401`.** The page treats a `401` as its own session ending, and a room elsewhere turning Bia away says nothing about her session at home.

## Signatures

Every request between installations carries:

| Header | |
| --- | --- |
| `Convia-Account` | the account identifier |
| `Convia-Public-Key` | the Ed25519 public key, base64 |
| `Convia-Timestamp` | Unix seconds |
| `Convia-Nonce` | 26 base32 characters |
| `Convia-Signature` | base64 |

The signature covers these lines, joined by `\n`:

```
convia-peer-v1
POST
ana.example
/v1/peer/rooms/room_…/messages?limit=50
<hex SHA-256 of the body>
acc_…
1789398296
<nonce>
```

The home accepts it only when all of these hold:

1. The key's fingerprint is the claimed identifier.
2. The signature verifies.
3. The timestamp is within **five minutes** of the home's clock.
4. The nonce has not been seen.

Nonces are claimed in `peer_nonces`, whose primary key decides, so a replay reaching two instances at once is still refused once. They expire with the window.

The **authority** is signed so that a home cannot take a request Bia sent it and replay it to a third installation where Bia is also a member. It is compared with the `Host` the request arrived with, so a reverse proxy in front of the home must preserve `Host`.

## Where the key comes from

Bia's key is sealed by her password (ADR 0011). **Signing in opens it, and the session keeps it**, wrapped with AES-256-GCM under a key derived by HKDF from the session's own secret, which Convia stores only as a SHA-256 digest.

- The database alone cannot sign as anybody.
- A request that needs to sign opens the key from its own cookie, uses it, and drops it.
- Signing out, expiry and revocation end all of it the moment the session stops authenticating.

Migration `00020` ended every existing session once, because none of them held a key.

## Following a link safely

A link is an address somebody else chose, and following it makes an installation connect somewhere from inside its own network. The client uses the same guard as [webhook delivery](webhooks.md):

- the address is checked **at the socket**, on every attempt;
- loopback, private, link-local and reserved addresses are refused in every environment, unless the operator set `CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES`;
- outside development, plain `http` is refused;
- every request is checked once more where it leaves: the home must be exactly a scheme, a lowercase host and an optional port, and the path must be on the peer surface, so no caller can send one anywhere else;
- no proxy is consulted and **no redirect is followed**;
- anything but a JSON object under 1 MiB is treated as no answer.

**Installations on a private network can invite each other only when their operator allows it**, with `CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES=true`. It is off by default in every environment, development included.

It used to follow development mode, as webhook delivery does, and that was a hole: development is the default `CONVIA_ENVIRONMENT`, and following a link is something anybody who registers can cause. On an installation somebody forgot to switch to production, a stranger could create an account, paste links naming addresses inside the server's network, and tell from "not found" against "unreachable" which machines and ports exist there. Webhook delivery keeps its rule, because registering a destination needs an application's key.

Turning it on in production still refuses plain `http`, so installations on a private network reach each other over `https`. Convia warns at startup while it is on.

## Known gaps

- **A room elsewhere is not announced.** The page reads it on a five-second timer while it is open, and its row in the sidebar has no unread count.
- **A home that does not answer cannot be left, only forgotten.** Leaving keeps the pointer until the home confirms, because dropping it silently would leave a membership nothing here remembers. Once leaving has failed, the interface offers to forget the room here anyway (`DELETE /v1/me/remote-rooms/{id}`), after saying that the person stays a member at the home and that nothing here can take them out later. It is how somebody gets rid of a room whose home is gone, has moved, or refuses them.
- **No calls between installations yet.** The call interface itself is still to come (`M18-004`); when it arrives, a visitor will reach the home's media plane directly, with a token the home issues.
- **No verification code on first contact.** Looking at a link shows the room, the inviter's handle and the home's address, and that is the check.
- **Nonces are pruned by whichever instance verifies**, at most once a minute each, rather than by a scheduled job.
