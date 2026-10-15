# 0026 — A contact needs the inviter's consent, and a request expires

## Status

Accepted

## Date

2026-10-15

## Context

An invitation is reusable and works for anybody it reaches ([ADR 0009](0009-the-cryptography.md)), so holding one says nothing about whether its inviter wants to talk to the holder. Before two peers first connect they share no secret, so neither can be told when the other comes online ([ADR 0012](0012-only-contacts-can-tell-when-a-person-is-online.md)).

## Decision

**Accepting an invitation sends its inviter a request**, and the two become contacts only when the inviter accepts it. The inviter sees the name the requester suggests and their safety code, and accepts or declines. Until then, nothing but the request passes between them.

**Declining is silent**: the requester is never told, and the inviter's Convia no longer answers them while the request would have lasted.

**A request lasts 7 days.** A request with no answer after 7 days counts as declined for the requester, whose Convia stops trying, and disappears for the inviter. The invitation itself stays valid: the requester may send a new request.

**While a request waits, the requester tries to reach the inviter every 5 minutes** that Convia is open, since there is nothing yet to be notified through; once the two are contacts, they wait for each other as [ADR 0024](0024-peers-connect-on-demand-and-reconnect-when-they-can.md) says.

## Consequences

- An invitation that leaks lets a stranger ask, never talk; and a person can be asked by many strangers, so a Convia keeps at most 100 requests waiting.
- A request reaches the inviter only when both are online within the same 7 days.
- Anybody who knows a person's public key can ask them, since the key is in the invitation; declining, removing and blocking ([ADR 0028](0028-removing-or-blocking-a-contact-is-silent.md)) answer them.
