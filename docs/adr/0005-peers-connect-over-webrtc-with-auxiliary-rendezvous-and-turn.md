# 0005 — Peers connect over WebRTC, helped by a rendezvous service and TURN

## Status

Accepted

## Date

2026-10-13

## Context

Two people running Convia at home are each behind a router, usually behind NAT, sometimes behind carrier-grade NAT or a corporate firewall. Neither has an address the other can simply call. Something has to help them find each other and find a path between them ([`README.md`](../../README.md)), and the README allows exactly that kind of help — connectivity infrastructure — as long as it never becomes the owner of identities, conversations or their state.

## Decision

**Peers connect with WebRTC, and ICE finds the path.** The two peers find each other through signaling, exchange their WebRTC negotiation and ICE candidates, and let ICE establish the best path it can: a direct one whenever possible, a relayed one only when no direct path works.

```text
try a direct path
      │
      ▼
does a direct ICE candidate pair work?
   │                 │
  yes                no
   │                 │
   ▼                 ▼
peer to peer       relayed through TURN
```

Where the WebRTC connection itself lives is decided in [ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md).

**Signaling goes through a small rendezvous service the project operates.** It lets a peer become reachable and carries offers, answers and ICE candidates between two peers. It may keep the ephemeral operational state signaling needs — who is reachable right now, routing, rate limits, abuse prevention — but **it is never the durable authority for any of Convia's state**: it does not own or durably store identities, contacts, conversations, messages, calls, media or history. No public DHT is used in the first version: reliability and simplicity come first.

**STUN and TURN come from coturn.** STUN lets each peer learn the address the Internet sees it at; TURN relays traffic when no direct path exists. A relay is a transport fallback and nothing more: it may observe endpoints, timing and volume, and what it carries is encrypted end to end. **No long-lived TURN credential is ever built into the application**: clients authenticate with short-lived credentials, or another mechanism that never hands every client an infrastructure secret.

**A contact is added with an invitation.** An invitation is a portable payload that identifies the inviter cryptographically ([ADR 0004](0004-a-person-is-a-key-their-application-made.md)) and carries what discovery needs to begin. In the first version it is a text code, copied and pasted through any channel; a `convia://` link or a QR code may represent the same object later. There is no central directory of names: a display name never identifies anybody.

**The project operates default infrastructure, and a user can replace all of it.** The defaults — rendezvous, STUN and TURN — make Convia work out of the box. A user can point their Convia at other infrastructure, their own included. **Changing infrastructure never changes an identity**: the same identity works through the defaults, through a self-hosted deployment, or through both over time, so the default infrastructure can never become an identity provider.

## Consequences

- The project runs and pays for a rendezvous service and a coturn deployment. Their uptime decides whether peers can find each other; it never decides what anybody's conversations contain.
- The rendezvous service and TURN see network metadata: who is reachable, which peers try to reach which, when, and how much traffic passes. That is the cost of connectivity infrastructure, and it is stated rather than hidden.
- How often a relay is needed is not assumed: it depends on NAT types, carriers, firewalls and carrier-grade NAT, and is measured once there are peers to measure.
- Still open, and decided by the records they belong to:
  - the format of an invitation, and of the messages exchanged through the rendezvous service (M02-003);
  - how a peer proves its identity to the rendezvous service and to the other peer, and how TURN's short-lived credentials are issued (M02-003 and M02-004);
  - how the rendezvous service is built and deployed, and how a user configures other infrastructure.
