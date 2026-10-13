# 0016 — How Convia runs on Windows

## Status

Accepted

## Date

2026-10-13

## Context

Convia keeps running while its window is closed ([ADR 0007](0007-closing-the-window-hides-convia.md)), holds one identity and one database per Windows account ([ADR 0004](0004-a-person-is-a-key-their-application-made.md), [ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)), and needs a record of what it did when something goes wrong — without that record betraying anybody's conversations.

## Decision

- **One Convia per Windows account.** Starting it while it is already running shows the running one's window, and the second process exits.
- **Its files live in `%LocalAppData%\Convia`.** They are bound to this machine by DPAPI, so they do not belong in a roaming profile.
- **It logs to local files only**, in `%LocalAppData%\Convia\logs`, from the info level up, keeping at most seven days and 10 MB. **It never logs** message content, keys, secrets, safety codes, invitations or the network addresses of peers.
- **It does not start with Windows unless the person asks it to.** Starting with Windows is a setting, off by default.

## Consequences

- A second window on the same account is impossible by design; a second person on the same machine has their own account, and their own Convia.
- A log can be shared to diagnose a problem without revealing what anybody said or where their peers are.
- Until somebody turns the setting on, a message written to them waits with its sender until they open Convia ([ADR 0011](0011-a-message-waits-with-its-sender.md)).
