# Canonical encodings

Everything Convia signs is encoded exactly as this page says, so that two implementations sign and verify the same bytes ([ADR 0009](../adr/0009-the-cryptography.md)). Nothing signed is ever a Protocol Buffers message, whose encoding is not canonical. [`internal/canonical`](../../internal/canonical/canonical.go) is the implementation.

## Fields

A value is a sequence of fields, and **each field is preceded by its length in bytes, as a 32-bit big-endian unsigned integer**. Fields are always in the order given below.

| Kind | Bytes |
| --- | --- |
| Text: domains, names, addresses | UTF-8, as given |
| Version | 32-bit big-endian unsigned integer ([ADR 0019](../adr/0019-the-protocol-version-is-a-whole-number.md)) |
| Identity | the raw 32-byte Ed25519 public key |
| Nonce, challenge | the raw 32 random bytes |
| DTLS fingerprint | the session description's `a=fingerprint:` value, with the algorithm in lower case and the hex digits in upper case: `sha-256 AB:CD:…` |
| Role | `offerer` for the peer that made the WebRTC offer, `answerer` for the one that answered it |

## The proof a peer gives on a connection

```text
"convia/v1/peer-auth" | version | signer's role |
offerer's identity | offerer's fingerprint | offerer's nonce |
answerer's identity | answerer's fingerprint | answerer's nonce
```

Both peers encode the two parties in the same order — the offerer's first — and differ only in the signer's role, so neither signature can be replayed as the other.

## The answer to a rendezvous service

```text
"convia/v1/rendezvous-auth" | service address | challenge
```

The service address is the `wss://` address the peer connected to, so an answer given to one service is no use to another.

## An invitation

```text
"convia/v1/invitation" | version | inviter's identity | suggested name | rendezvous hint
```

The inviter signs these bytes; the invitation's text form, `convia1…`, is defined with invitations themselves.

## Test vectors

Inputs: Alice's identity is 32 bytes of `a1`, Bob's 32 bytes of `b0`; Alice's nonce is the bytes `00` to `1f`, Bob's `20` to `3f`; Alice's fingerprint is given as `SHA-256 ab:cd:ef`, Bob's as `sha-256 01:23:45`.

**Peer proof**, version 1, signed by the offerer, Alice offering and Bob answering:

```text
00000013636f6e7669612f76312f706565722d617574680000000400000001000000076f66666572657200000020a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1000000107368612d3235362041423a43443a454600000020000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f00000020b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0000000107368612d3235362030313a32333a343500000020202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f
```

**Rendezvous answer** to `wss://rendezvous.example`, challenge 32 bytes of `07`:

```text
00000019636f6e7669612f76312f72656e64657a766f75732d61757468000000187773733a2f2f72656e64657a766f75732e6578616d706c65000000200707070707070707070707070707070707070707070707070707070707070707
```

**Invitation**, version 1, from Alice, named `Ana Lúcia`, rendezvous hint `wss://rendezvous.example`:

```text
00000014636f6e7669612f76312f696e7669746174696f6e000000040000000100000020a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a10000000a416e61204cc3ba636961000000187773733a2f2f72656e64657a766f75732e6578616d706c65
```
