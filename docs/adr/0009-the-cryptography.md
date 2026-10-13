# 0009 — The cryptography

## Status

Accepted

## Date

2026-10-13

## Context

[ADR 0004](0004-a-person-is-a-key-their-application-made.md) made a person an Ed25519 key and [ADR 0008](0008-the-protocol-between-peers.md) made peers prove they hold theirs; both left the exact cryptography to this record. It has to say what protects a conversation, an exported identity, the safety code two people compare, the proof a peer gives, and an invitation — precisely enough that two implementations produce the same bytes.

## Decision

### What protects a conversation

**In the first version, DTLS is the only encryption, and it is enough.** WebRTC's DTLS encrypts end to end between the two peers, relays included, with fresh keys for every session. A layer of Convia's own above it is reconsidered the moment a message could pass through anything other than that session: delivery to a peer that is offline (M02-006), groups (M02-008), several devices, or a requirement for security after a key is compromised.

### An exported identity

The file is encrypted with **XChaCha20-Poly1305** under a key derived from the password with **Argon2id**. The salt, the nonce and the Argon2id parameters are stored in the file beside the ciphertext, so that the parameters can be raised later without making older files unreadable; they are never below the second recommended set of RFC 9106 (64 MiB, three passes, four lanes).

### The safety code

```text
digest = SHA-256("convia/v1/safety-code" || first key || second key)
```

The two keys are the raw 32-byte Ed25519 public keys, the lower first when compared byte by byte, so both people compute the same digest. The first 30 bytes of the digest are read as six 5-byte big-endian integers; each is reduced modulo 10¹⁰ and written as exactly ten decimal digits, zeros included. The six are concatenated into 60 digits and shown as twelve groups of five.

### The proof a peer gives

Each peer contributes a fresh **32-byte random nonce** to every connection. Each signs, with its identity key, a transcript of:

- the domain `convia/v1/peer-auth`;
- the protocol version the two negotiated;
- its role in the connection — the one that made the WebRTC offer, or the one that answered it;
- both identity public keys;
- both DTLS fingerprints, as both session descriptions carried them;
- both nonces.

Fields are encoded in that fixed order, each preceded by its length — never as a Protocol Buffers message, whose encoding is not canonical. A peer verifies the other's signature over the transcript with the other's role, so neither signature can be replayed as the other.

### The proof given to the rendezvous service

The service sends a **32-byte random challenge**, used once and valid for 30 seconds. The peer signs the domain `convia/v1/rendezvous-auth`, the service's own address, and the challenge, so that an answer given to one service is no use to another.

### An invitation

An invitation holds the format's version, the inviter's identity public key, the name the inviter suggests for themselves, and a hint of the rendezvous service the inviter uses, and it is **signed by the inviter's identity key**, so that changing any part of it invalidates it. It is **reusable**, like a card handed out, rather than used once or expiring. Its text form is **Bech32m with the human-readable part `convia`** — `convia1…` — whose checksum catches a code that was copied wrongly.

## Consequences

- The cryptography comes from Go's standard library and `golang.org/x/crypto`, under [ADR 0003](0003-dependencies-carry-permissive-licenses.md); none of it is written here.
- An invitation, being reusable, works for anybody it reaches: what keeps a contact honest is the safety code, not the secrecy of the invitation.
- Encodings that are signed or compared are canonical and specified here; changing one is a new version of the protocol.
- In a space, messages are also signed by their authors, because they can arrive through another member ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)); still nothing is encrypted above DTLS.
