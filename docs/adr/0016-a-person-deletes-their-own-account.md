# ADR 0016 — A person deletes their own account

**Status:** Accepted
**Date:** 2026-09-16
**Milestone:** M18 — Standalone Web Application

## Context

A person could register, but nothing let them leave. Operators could suspend an account, and the erasure `M31-011` defined for messages had nobody calling it. `M18-031` asked what a person deleting their own account takes with them.

The product owner decided:

- **the password confirms it**, as it does a password change;
- **rooms on other installations are left at their homes**, and one whose home does not confirm is forgotten here instead;
- **a room the person owned passes on** as it does when an owner leaves, and **one left empty is deleted**;
- **the username is freed**.

## Decision

### The account row is deleted, and the user row is retired

The account goes, for good. Its sessions and its pointers to rooms elsewhere go with it, by their foreign keys, and so does the sealed key: nobody can sign in as it or sign as it again. The username is free once the row is gone, because the unique index is the only thing that held it.

The user row stays, deleted, with its display name cleared. Messages, participations and accepted invitations point at it, and removing it would need each of those rewritten. The name goes because it was the username, which somebody else may now take: kept, it would let a newcomer be mistaken for somebody who left.

### What they said is redacted

`M31-011`'s erasure runs: each message keeps its place and loses its body and its author, and read positions go. Deleting the messages would take the conversation away from the people still in it.

### Leaving is leaving

Each room here is left the way a person leaves one, so the people still in it are told with `room.member_removed` and a call lets the person go. Bans naming them go too. A room the person opened that nobody is left in is deleted, since nobody could reach it again; an application's room stays the application's.

This differs from erasure, which announces nothing: the person is gone, and the others' sidebars have no other way to learn it.

### A home that does not confirm is forgotten

Leaving a room elsewhere needs the person's key, which is only available now. A home that does not answer is forgotten here rather than keeping the account alive, and the person may stay a member there. The interface says so before asking for the password.

### Invitations they made are withdrawn

A pending invitation is a message from somebody who no longer exists. Accepted ones stay, as the record of how somebody arrived.

### The account goes last

The steps run in order, and the account is deleted only after everything else succeeded. A deletion interrupted halfway leaves somebody who can still sign in and ask again, and every step is safe to repeat.

### It lives in its own package

`internal/departure` calls accounts, peers, rooms, messages and users. None of those may depend on the others for this, and `sessions` cannot import `peers`, which imports it.

### It is a POST to a verb

`POST /v1/me/delete`, because it carries the password in a body, which a `DELETE` is not promised to carry. A wrong password is `403 wrong_password` and is charged to the sign-in budget, as a password change's is.

## Consequences

- Nothing can be recovered, not even by an operator.
- A person may remain a member of a room on an installation that did not answer, with a user there that nobody here can sign as.
- Presence is not withdrawn: it lapses on its timer, and the person can no longer be seen by anybody who shares a room with them, because nobody does.
- The audit line records counts and identifiers only.
