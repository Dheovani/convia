# 0017 — The database only moves forward, and is never destroyed

## Status

Accepted

## Date

2026-10-13

## Context

A peer's database is the only copy of its history ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)). Its schema will change from one version of Convia to the next, a person may run an older Convia against a database a newer one wrote, and a file can be damaged, or a key protected with DPAPI can stop opening when the Windows account changes.

## Decision

- **The schema is changed by numbered SQL migrations built into the executable**, and the database records how far it has come in SQLite's `user_version`. Migrations only move forward: there is no undoing one.
- **Before migrating, Convia copies the database**, and keeps the latest copy.
- **A database written by a newer Convia is not opened**: it is left untouched, and the person is asked to update.
- **A database Convia cannot read, or a protected key it cannot open, stops Convia and asks the person what to do**: import an exported identity, or start over. Either way, the unreadable files are moved to a folder named for the moment, and **never deleted**.

## Consequences

- Downgrading Convia never damages a database; it only refuses to open one that is newer.
- A failed migration leaves the copy made before it.
- What could not be read stays on the disk until the person removes it, in case it can be recovered.
