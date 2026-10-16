# 0032 — Reading and presence stay on the reader's machine

## Status

Accepted

## Date

2026-10-16

## Context

Messengers commonly tell the other side when a message was read, when somebody is typing, and whether they are online. Each is a fact about a person sent to somebody else; presence, in Convia, would also mean watching every contact through the rendezvous service, where connections are otherwise made on demand ([ADR 0024](0024-peers-connect-on-demand-and-reconnect-when-they-can.md)).

## Decision

- **Unread counts exist, on this machine only.** Each conversation counts the messages received since the person last had it open, and nothing about it is sent.
- **There are no read receipts and no typing indicators.** A message is shown as delivered once stored by the other side ([ADR 0011](0011-a-message-waits-with-its-sender.md)), and no further.
- **The interface does not show whether a contact is online** ([ADR 0014](0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).

## Consequences

- Nobody learns when, or whether, their messages were read, or when the other person is there.
- A person cannot tell whether a contact is around before writing; a message waits for them either way.
