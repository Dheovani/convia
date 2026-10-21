# 0031 — Link previews may come later, fetched only by their sender

## Status

Accepted

## Date

2026-10-16

## Context

A preview of a link — its title, a description, an image — has to be fetched from the site the link names, which then learns who looked, from where and when. If the reader's Convia fetched it on its own, a contact could send a link to a server of theirs and learn the reader's address and the moment they were online, without the reader doing anything — undoing [ADR 0032](0032-reading-and-presence-stay-on-the-readers-machine.md); could point it at the reader's own network, at a router or a camera, from inside it; could trigger what a link does by being opened, such as confirming or unsubscribing; and would make Convia parse whatever any site serves.

## Decision

**Link previews are allowed, and not built now.** When they are, they keep these rules:

- **Only the sender fetches a preview**, when they write the message, and sends it with the message. **Nothing is ever fetched because a message arrived**: the reader's Convia shows what it was sent and reaches no site.
- **Only `http` and `https` addresses are fetched, and never one on a private, loopback or link-local network**, whatever the name resolves to.
- **A preview always shows the link's real address**, which following it confirms as any link does ([ADR 0030](0030-messages-are-written-in-a-subset-of-markdown.md)), so a preview whose title misrepresents its site misleads nobody into it.
- **What may never leave is the conversation**: a preview fetches the link's own address, and sends the site nothing a conversation holds — no other message, no contact, no identity.

Whether previews are a setting is decided when they are built.

## Consequences

- Links are shown as links, with no preview, until then.
- A site learns no more from a preview than from its sender opening the link themselves.
