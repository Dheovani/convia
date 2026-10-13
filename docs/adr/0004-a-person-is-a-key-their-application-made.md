# 0004 — A person is a key their own application made

## Status

Accepted

## Date

2026-10-11

## Context

Two instances of Convia that talk to each other need to know who is on the other side ([`README.md`](../../README.md)). The README rules out the usual answer: there is no central authority to issue accounts, check them or say who is who. Whatever a person's identity is, their own application has to be able to create it, and the other peer has to be able to check it, with nobody in between.

## Decision

- **A person's identity is a key pair their own Convia generates**, on its first run, on their own machine. The identity is the Ed25519 public key; holding the private key is what being that person means. No server issues it, records it or vouches for it.
- **One device per person in the first version.** The identity lives on one computer. The model leaves room for a later record to add devices whose keys the identity signs; nothing in the first version depends on it.
- **A name is what a person calls themselves, and nothing more.** Each person chooses their display name freely. It is not unique, and Convia never treats it as identifying anybody: two people may share a name, and the key is what tells them apart.
- **An identity can be exported to a file protected by a password**, which is how it survives the loss of the computer it lives on. Importing that file on another computer moves the identity there; it does not make a second device.
- **Two people can verify each other with a safety code**: a short code both applications derive from the two identities, which the two people compare through some other channel — in person, on a call. Verifying is optional; a conversation works without it, and the interface says whether a contact was verified.

## Consequences

- Losing both the computer and the exported file is losing the identity. Nobody can recover it, because nobody else ever held it.
- Since names are not unique, the interface must never let a name alone stand for a person where it matters — a contact is always the key behind the name.
- Still open, and decided by the records they belong to:
  - how one person learns another's key in the first place, and how a contact is added (M02-002);
  - how a peer proves it holds its private key when two peers connect (M02-003 and M02-004);
  - how the private key is protected on the machine (M02-005);
  - the format of the exported file, and how its password protects it (M02-004);
  - how the safety code is derived (M02-004).
