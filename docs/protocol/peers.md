# The protocol between peers

How two peers open a connection and prove who they are on it ([ADR 0008](../adr/0008-the-protocol-between-peers.md), [ADR 0009](../adr/0009-the-cryptography.md)). The messages are in [`proto/convia/v1/peer.proto`](../../proto/convia/v1/peer.proto) and [`handshake.proto`](../../proto/convia/v1/handshake.proto); the core's side is [`internal/peers`](../../internal/peers/peers.go), and the WebView's is [`frontend/src/transport`](../../frontend/src/transport/connections.ts).

## Negotiation

The peer that wants a connection chooses a random 16-byte session, and offers through the rendezvous service ([the rendezvous protocol](rendezvous.md)); every offer, answer and candidate of that connection carries the session. An offer with a session already open renegotiates that connection — an ICE restart, for instance — and must keep its DTLS fingerprint, or the connection is ended.

When both peers offer to each other at once, the one whose public key is lower, compared byte by byte, keeps its own offer and ignores the other's; the other answers it ([ADR 0024](../adr/0024-peers-connect-on-demand-and-reconnect-when-they-can.md)).

A session description must carry exactly one DTLS fingerprint, of SHA-256, SHA-384 or SHA-512.

## The data channel

Each connection has two data channels, both negotiated, reliable and ordered, and every message on them is binary. The one with id 0 carries `PeerMessage`s; the one with id 1, `convia-files`, carries only `FileChunk`s, so a file being sent never holds a message back ([ADR 0036](../adr/0036-files-wait-with-their-sender-up-to-100-mb.md)).

## The handshake

```text
offerer                                 answerer
 │ ── hello: versions ──────────────────► │
 │ ── challenge: nonce ─────────────────► │
 │ ◄────────────────── hello: versions ── │
 │ ◄────────────────── challenge: nonce ─ │
 │ ── proof: identity, signature ───────► │
 │ ◄───────────── proof: identity, signature ── │
 │ ◄──────────── interest ──────────────► │
```

1. When the data channel opens, each peer sends `hello`, with every version of the protocol it speaks, and `challenge`, with a fresh 32-byte nonce.
2. Once it has both of the other's, each peer uses the highest version both speak. If they share none, it ends the connection, and the person is told which of the two has to update.
3. Each sends `proof`: its identity and its signature of the [peer proof](canonical-encodings.md#the-proof-a-peer-gives-on-a-connection), in its own role, over both identities, both fingerprints and both nonces.
4. Each verifies the other's proof, in the other's role, against the identity it meant to reach. The connection is proven only then.

Before the handshake completes, anything but these messages, a message twice, or a proof that does not verify ends the connection. After it, a handshake message ends it too, and a message this version does not know is ignored ([ADR 0019](../adr/0019-the-protocol-version-is-a-whole-number.md)).

## Interest

Once proven, each peer sends `interest`, saying whether it wants the connection, and again whenever that changes. A connection that neither wants for 10 minutes is closed ([ADR 0024](../adr/0024-peers-connect-on-demand-and-reconnect-when-they-can.md)).

## Becoming contacts

A proven peer that is not a contact may send only a request, within 30 seconds, or it is disconnected ([ADR 0026](../adr/0026-a-contact-needs-the-inviters-consent.md)).

```text
requester                                 inviter
 │ ── contact_request: name, part ──────────► │   the person decides
 │ ◄──────────── contact_accepted: name, part │
 │ ── contact_confirmed ────────────────────► │
```

- The requester sends `contact_request`, with the name it suggests and its random 32-byte part of the [pair secret](canonical-encodings.md#the-pair-secret), each time it connects while the request waits. Sending the same part again does not make the request younger.
- The inviter answers with `contact_accepted` once the person accepts, then or whenever the requester next connects, until the requester sends `contact_confirmed`.
- A declined request is never answered: the inviter's peer refuses the requester's connections until the request would have expired.
- Two people asking each other become contacts through the request of the one whose key is lower; the other's request is ignored.
- A contact who asks again — having removed the other, say — is accepted at once, with a new pair secret.

## A conversation

Contacts exchange their messages over their connection ([ADR 0010](../adr/0010-a-conversation-is-kept-on-each-peer.md), [ADR 0011](../adr/0011-a-message-waits-with-its-sender.md)); a peer that is not a contact is neither written to nor heard.

```text
each peer                                 the other
 │ ── holding: highest held, every one before ──► │
 │ ◄──── direct_message … (what is missing) ───── │
 │ ── acknowledgement: ids ─────────────────────► │
```

- **A message** is `direct_message`: a random 16-byte identifier, its author's sequence number in the conversation (1, 2, 3, …), its hybrid logical clock — milliseconds since 1970, and a counter for messages in the same millisecond — and its text, of 1 to 4,000 characters ([ADR 0029](../adr/0029-a-message-holds-at-most-4000-characters.md)).
- **Entries besides messages:** a `direct_message` with `reply_to` is a message answering another ([ADR 0035](../adr/0035-replies-and-reactions.md)); one with `reaction` and no text puts an emoji on a message, or takes it off. Each is an entry of its author's sequence, delivered and caught up as messages are. For each person, emoji and message, the reaction with the latest clock decides; an entry about a message the peer does not hold yet is kept, and counts once the message arrives. A reaction is one emoji: a pictograph, with what joins or modifies it into one.
- **Editing and deleting** ([ADR 0034](../adr/0034-a-message-is-edited-and-deleted-by-its-author.md)): an entry with `edit` gives a message its new text, and one with `deletion` deletes it for both; either counts only from the message's own author. The edit with the latest clock is the text; a deletion wins over every edit. Each peer then wipes the text it no longer shows — the message's earlier text and earlier edits, or all of it once deleted — except in an entry still waiting to be delivered, which keeps its text until it is. A wiped entry is sent with `wiped` and no text, holding its place in its author's sequence for a peer that has to catch up.
- **Files** ([ADR 0036](../adr/0036-files-wait-with-their-sender-up-to-100-mb.md)): an entry with `attachment` is a file of 1 byte to 100 MB, its text the file's name, with its size, type, SHA-256 and orientation. The type is `image/png`, `image/jpeg`, `image/gif` or `image/webp` for a picture, whose metadata its sender removed before sending, keeping only its EXIF orientation (1 to 8); anything else is `application/octet-stream`. The entry travels as messages do; the file itself stays with its sender until the recipient pulls it: `file_request` asks for at most 1 MiB from an offset, and the sender answers on the files channel with `file_chunk`s of 64 KiB from it. The recipient asks again from where it stopped whenever a connection opens, and checks the SHA-256 once the file is whole, starting over if it differs. A file is only served by its author, and is removed with the message when it is deleted or forgotten.
- **The order** of a conversation is by clock, then by identifier compared byte by byte, so both peers show the same order. A peer's clock moves past every message it receives, so a reply always comes after what it answers.
- **Catching up:** once proven, each peer sends `holding`, the highest sequence number of the other's messages it holds with every one before it. The other takes everything up to it out of its outbox, and sends, in order, every message after it.
- **Delivery:** a message is stored, and in the outbox, before it is shown as sent. It is sent when a connection opens, and again after 30 seconds without an acknowledgement. The recipient answers `acknowledgement` only once the message is stored — again for one it already holds, storing nothing — and the sender then takes it out of the outbox: it is delivered.
- **Refused:** a message longer than 4,000 characters, empty, not UTF-8, or whose identifier or sequence number another entry already has; a reaction with text, a reply, no message to be on, or anything but one emoji; a file over 100 MB, empty, of another type, or whose name is empty, longer than 255 bytes, or holds a path separator or a control character; an entry of more than one kind, or wiped yet holding text; is neither stored nor acknowledged.
