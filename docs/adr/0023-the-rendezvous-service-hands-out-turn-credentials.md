# 0023 — The rendezvous service hands out TURN credentials

## Status

Accepted

## Date

2026-10-15

## Context

TURN relays traffic for peers that cannot reach each other directly, and no long-lived TURN credential may ever be built into the application ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)). The WebRTC of WebView2 accepts only a username and a password for a TURN server: the OAuth credentials of RFC 7635 are not supported by Chromium.

## Decision

**coturn and the rendezvous service share a secret, and the service turns it into short-lived credentials** — coturn's `use-auth-secret`, the mechanism of the TURN REST API draft. The username is the moment the credential expires, as Unix seconds, and a random tag: `1760572800:3f9a1c0e7b2d4a65`. The password is the Base64 of the HMAC-SHA-1 of the username under the secret. coturn verifies a credential with the same secret, and refuses it once its moment has passed.

- **A credential is valid for 24 hours.** The service sends one with the ICE servers right after `welcome`, and a new one every 12 hours while the peer stays connected. A connection relayed through TURN moves to the new credential with an ICE restart; one a day keeps the interruptions of a call rare.
- **Only an authenticated peer gets one**, and a blocked address never does ([ADR 0021](0021-the-rendezvous-service-limits-every-peer.md)).
- **The username never carries the identity**, so coturn learns nothing about who relays, and the rendezvous service logs no credential ([ADR 0022](0022-the-rendezvous-service-logs-nobody.md)).
- **The secret never leaves the two servers.** The rendezvous service reads it from its environment, never from its command line, which other users of the machine can see.
- **coturn logs nothing**, since it would log the addresses of the peers it relays, for the reasons [ADR 0022](0022-the-rendezvous-service-logs-nobody.md) gives for the rendezvous service.

## Consequences

- A credential that leaks lends its holder the relay's bandwidth for at most 24 hours, within whatever quotas the deployment gives coturn; it cannot be revoked one by one, since the secret is shared.
- Changing the secret invalidates every credential handed out, which is how a leak of the secret itself is answered.
- coturn refuses to relay to private, loopback and link-local addresses, so a credential cannot be used to reach the network the relay runs in.
