# Invitations

How a person hands out the means to ask them to be a contact ([ADR 0009](../adr/0009-the-cryptography.md), [ADR 0026](../adr/0026-a-contact-needs-the-inviters-consent.md)). [`internal/invitation`](../../internal/invitation/invitation.go) is the implementation.

## Contents

| Field | Bytes |
| --- | --- |
| Version | 1 byte: `1` |
| Identity | the inviter's raw 32-byte Ed25519 public key |
| Name | 1 byte of length, then up to 128 bytes of UTF-8: the name the inviter suggests |
| Rendezvous hint | 1 byte of length, then up to 255 bytes: the address of the inviter's rendezvous service |
| Signature | 64 bytes: the inviter's Ed25519 signature of the [canonical invitation](canonical-encodings.md#an-invitation) |

The fields are concatenated in that order, and nothing follows the signature.

## Text form

The bytes are written as **Bech32m** (BIP 350) with the human-readable part `convia`: `convia1…`. Its checksum catches a code copied wrongly. Unlike a Bitcoin address, an invitation is not limited to 90 characters; it is at most 1,023.

An invitation is read whole, upper or lower case but not mixed, with the spaces around it ignored. It is refused, and the person told why, when:

- its checksum does not match: it was not copied whole, or not exactly;
- it is not an invitation: another prefix, or its bytes are not the fields above;
- its version is newer than this Convia reads;
- its signature does not match what it says: it was changed after it was made.

## Test vector

The identity whose Ed25519 seed is 32 bytes of `11`, named `Ana`, rendezvous hint `wss://rendezvous.example`:

```text
convia1q8gy4v3jws4mf2e6zd5t63s4unndqgj2kudqz6a0s5s2xvkfw7rnwq6pdes3samnwvaz7tmjv4hxget6wehh2uewv4uxzmtsd3jnjdk7cqaeza8p736wyykjzxndvryfmqlzjn2jf586wdk9cyfynappq52w5tx6qrh3eemc0nrn366gk23n55ugsnraz3kdwtszytjkqcepdpye
```
