# 0018 — Replacing, protecting and deleting an identity

## Status

Accepted

## Date

2026-10-13

## Context

An identity lives on one computer and moves only as an exported file ([ADR 0004](0004-a-person-is-a-key-their-application-made.md), [ADR 0009](0009-the-cryptography.md)). That leaves three questions: what importing does where an identity already exists, how strong the password protecting an export must be, and whether a person can remove their identity altogether.

## Decision

- **Importing where an identity already exists replaces it, only with the person's consent.** The current identity and its history are moved to a folder of their own, never deleted ([ADR 0017](0017-the-database-only-moves-forward-and-is-never-destroyed.md)), and the imported identity takes their place.
- **An export's password has at least 12 characters**, and no rules about which characters: length is what makes a password hard to guess.
- **A person can delete their identity.** Convia first offers to export it, then asks them to confirm, then deletes the identity, the contacts and the history for real. Contacts are not told: they simply stop seeing the person.

## Consequences

- An identity is never lost by importing another over it; it is set aside, where the person can still find it.
- Deleting is the one way Convia destroys data on purpose, and it happens only after the person has been offered a copy and has confirmed.
- A deleted person's contacts keep their side of the conversations, and wait for messages that will not come.
