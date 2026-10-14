# 0019 — The protocol's version is a whole number

## Status

Accepted

## Date

2026-10-14

## Context

Two peers negotiate the version of the protocol they speak when they connect, using the highest both speak ([ADR 0008](0008-the-protocol-between-peers.md)). The version also appears in what they sign ([ADR 0009](0009-the-cryptography.md)), so its form has to be fixed before anything is signed.

## Decision

**The protocol's version is a whole number: 1, 2, 3, …** A new version exists only for a change an older peer cannot understand. A change that older peers simply ignore — a new optional field in a Protocol Buffers message — keeps the version it is made in. Each peer states every version it speaks, and the highest both state is used.

## Consequences

- The schemas of version *n* live in the Protocol Buffers package `convia.v{n}`; a version that breaks compatibility gets a package of its own, and both live side by side while peers move from one to the other.
- In everything signed, the version is encoded as a 32-bit unsigned integer.
