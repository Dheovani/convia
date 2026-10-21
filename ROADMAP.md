# Roadmap

What Convia builds, in order: phases, the milestones in each, and the tasks in each milestone. Every task has an ID that never changes, `M01-001`, so it can be referred to from commits, pull requests and records. Why things are built the way they are is in [`docs/adr`](docs/adr/README.md), not here.

A task is ticked when it is done, and nothing else is written about it. **A task that begins with "Decide" needs the product owner's decision**, recorded as an ADR, before any task that depends on it is started (AGENTS.md, rule 3). The plan is adjusted as the work teaches us; a task's ID never changes, and one that is no longer wanted is removed rather than renumbered.

## Phase 1 — Foundation

### M01 — Repository foundation

The repository, the stack and the rules every change is held to.

- [x] **M01-001:** Define Convia in the README, and the rules for working on it in AGENTS.md.
- [x] **M01-002:** Record the stack and the first platform ([ADR 0001](docs/adr/0001-a-go-core-in-a-wails-window-for-windows.md)).
- [x] **M01-003:** Build an empty Wails v2 window with a React, TypeScript, Vite and Tailwind CSS interface.
- [x] **M01-004:** Require the checks of [ADR 0002](docs/adr/0002-what-a-change-must-pass.md) in CI.
- [x] **M01-005:** Run CodeQL for Go and TypeScript.
- [x] **M01-006:** Update dependencies weekly with Dependabot.
- [x] **M01-007:** Enforce the dependency license policy ([ADR 0003](docs/adr/0003-dependencies-carry-permissive-licenses.md)).
- [x] **M01-008:** Say how vulnerabilities are reported (SECURITY.md) and how changes are made (CONTRIBUTING.md).
- [x] **M01-009:** Decide the proposed license exception for build-time tools in ADR 0003.
- [x] **M01-010:** Run mutation testing periodically and before releases, on the critical packages, once the first of them exists.
- [x] **M01-011:** Carry over the interface's design from before the reset: the theme, the mark and the icon, the shared controls, and the choice of language.

### M02 — The decisions before the first conversation

Nothing that two peers do together can be built before these are decided ([`README.md`](README.md)). Each is decided by the product owner and recorded as an ADR.

- [x] **M02-001:** Decide identity ([ADR 0004](docs/adr/0004-a-person-is-a-key-their-application-made.md)).
- [x] **M02-002:** Decide how peers discover each other and connect across NAT ([ADR 0005](docs/adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md), [ADR 0006](docs/adr/0006-one-peer-connection-per-pair-in-the-webview.md)).
- [x] **M02-003:** Decide the protocol between peers ([ADR 0008](docs/adr/0008-the-protocol-between-peers.md)).
- [x] **M02-004:** Decide the cryptography ([ADR 0009](docs/adr/0009-the-cryptography.md)).
- [x] **M02-005:** Decide how a conversation is stored locally, ordered and synchronized ([ADR 0010](docs/adr/0010-a-conversation-is-kept-on-each-peer.md)).
- [x] **M02-006:** Decide what happens to a message sent to a peer that is offline ([ADR 0011](docs/adr/0011-a-message-waits-with-its-sender.md)).
- [x] **M02-007:** Decide how voice, video and screen sharing are negotiated between peers ([ADR 0015](docs/adr/0015-calls-and-screen-sharing-ride-the-peer-connection.md)).
- [x] **M02-008:** Decide groups and conversations with more than two people ([ADR 0013](docs/adr/0013-a-space-is-replicated-by-its-members.md)).
- [x] **M02-009:** Decide what closing the window does ([ADR 0007](docs/adr/0007-closing-the-window-hides-convia.md)).
- [x] **M02-010:** Decide who may subscribe to a person's reachability ([ADR 0012](docs/adr/0012-only-contacts-can-tell-when-a-person-is-online.md)).
- [x] **M02-011:** Decide how members of a space who are not contacts learn when one another are online ([ADR 0014](docs/adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).

## Phase 2 — The local peer

Everything one instance of Convia needs on its own machine before it can talk to another: the shape of the core, where it keeps things, and who it is.

### M03 — The application core and its window

How the Go core is organised, how it talks to the interface, and how the application lives on Windows.

**Depends on:** M01.

- [x] **M03-001:** Lay out the Go core in packages by responsibility — identity, storage, protocol, connectivity, spaces, calls — each testable without the window.
- [x] **M03-002:** Define the bridge between the core and the interface: which Go methods Wails exposes, which events the core emits to the interface, and how an error crosses it.
- [x] **M03-003:** Generate the bridge's TypeScript bindings in the build, and check in CI that they match the Go code.
- [x] **M03-004:** Keep Convia's files under the user's application data directory, and nowhere else.
- [x] **M03-005:** Decide what happens when Convia is started while it is already running for the same user ([ADR 0016](docs/adr/0016-how-convia-runs-on-windows.md)).
- [x] **M03-006:** Enforce that decision.
- [x] **M03-007:** Decide what the core logs, where, for how long, and what it must never log ([ADR 0016](docs/adr/0016-how-convia-runs-on-windows.md)).
- [x] **M03-008:** Log accordingly, on the machine only.
- [x] **M03-009:** Choose the notification-area library, under [ADR 0003](docs/adr/0003-dependencies-carry-permissive-licenses.md).
- [x] **M03-010:** Hide the window on close, show it from the notification-area icon, and quit from the icon's menu ([ADR 0007](docs/adr/0007-closing-the-window-hides-convia.md)).
- [x] **M03-011:** Decide whether Convia starts with Windows ([ADR 0016](docs/adr/0016-how-convia-runs-on-windows.md)).
- [x] **M03-012:** Shut the core down cleanly on quit: close connections, flush storage, end background work.
- [x] **M03-013:** Show a recoverable error, rather than a blank window, when the interface fails.
- [x] **M03-014:** Let the person turn starting with Windows on and off.

### M04 — Local storage

Where a peer keeps its identity, its contacts and its conversations ([ADR 0010](docs/adr/0010-a-conversation-is-kept-on-each-peer.md)).

**Depends on:** M03.

- [x] **M04-001:** Check `modernc.org/sqlite` and everything it brings against [ADR 0003](docs/adr/0003-dependencies-carry-permissive-licenses.md), and add it.
- [x] **M04-002:** Decide how the database schema is versioned and migrated ([ADR 0017](docs/adr/0017-the-database-only-moves-forward-and-is-never-destroyed.md)).
- [x] **M04-003:** Open the database, apply its migrations, and refuse one it cannot read without damaging it.
- [x] **M04-004:** Protect secrets with DPAPI, bound to the user's account.
- [x] **M04-005:** Generate the history's content key, keep it protected with DPAPI, and encrypt and decrypt message content with XChaCha20-Poly1305.
- [x] **M04-006:** Define the schema for the identity, contacts, conversations, messages, the outbox and their sequence numbers.
- [x] **M04-007:** Decide what happens when the database or a protected key cannot be read ([ADR 0017](docs/adr/0017-the-database-only-moves-forward-and-is-never-destroyed.md)).
- [x] **M04-008:** Handle it that way, and say so in the interface.
- [x] **M04-009:** Test storage against a real database file: migrations, encryption, and that content is never written in clear.

### M05 — Identity

A person is a key their own application made ([ADR 0004](docs/adr/0004-a-person-is-a-key-their-application-made.md), [ADR 0009](docs/adr/0009-the-cryptography.md)).

**Depends on:** M04.

- [x] **M05-001:** Generate an Ed25519 identity on the first run, and keep its private key protected with DPAPI.
- [x] **M05-002:** Let the person choose and change their display name.
- [x] **M05-003:** Compute the safety code exactly as ADR 0009 specifies, with published test vectors.
- [x] **M05-004:** Export the identity to a file protected by a password, with Argon2id and XChaCha20-Poly1305 and the parameters stored in the file.
- [x] **M05-005:** Import an exported identity on a machine with none.
- [x] **M05-006:** Decide what happens when an identity is imported on a machine that already has one ([ADR 0018](docs/adr/0018-replacing-protecting-and-deleting-an-identity.md)).
- [x] **M05-007:** Decide the minimum strength of an export password ([ADR 0018](docs/adr/0018-replacing-protecting-and-deleting-an-identity.md)).
- [x] **M05-008:** Decide whether a person can delete their identity ([ADR 0018](docs/adr/0018-replacing-protecting-and-deleting-an-identity.md)).
- [x] **M05-009:** Build the first-run screens: making an identity, or importing one.
- [x] **M05-010:** Show a person their own identity and safety-code material in the interface.
- [x] **M05-011:** Test every encoding against fixed vectors, and fuzz the import of an exported file.
- [x] **M05-012:** Offer importing an exported identity on the screen shown when the data cannot be opened ([ADR 0017](docs/adr/0017-the-database-only-moves-forward-and-is-never-destroyed.md)).
- [x] **M05-013:** Let a person replace their identity by importing another, and delete it.

## Phase 3 — Two peers connect

Two instances of Convia, on two machines behind two routers, find each other, connect, and prove who they are.

### M06 — Protocol definitions

The messages of the protocol, in one place both sides are generated from ([ADR 0008](docs/adr/0008-the-protocol-between-peers.md)).

**Depends on:** M03.

- [x] **M06-001:** Choose the Protocol Buffers tooling for Go and for TypeScript, under ADR 0003.
- [x] **M06-002:** Keep the schemas in the repository, generate both sides in the build, and check in CI that the generated code is current.
- [x] **M06-003:** Decide how the protocol's version is numbered ([ADR 0019](docs/adr/0019-the-protocol-version-is-a-whole-number.md)).
- [x] **M06-004:** Specify the canonical encodings of everything that is signed — the handshake transcript, the rendezvous answer, invitations — as length-prefixed fields ([ADR 0009](docs/adr/0009-the-cryptography.md)).
- [x] **M06-005:** Publish test vectors for each canonical encoding.

### M07 — The rendezvous service

The small service that lets peers find each other and exchange connection metadata, and is never the authority for anything ([ADR 0005](docs/adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)).

**Depends on:** M06.

- [x] **M07-001:** Decide where the rendezvous service's code lives — this repository or another — and how it is versioned against the application.
- [x] **M07-002:** Serve WSS, and authenticate a peer with a single-use 32-byte Ed25519 challenge, valid for 30 seconds and bound to the service's address ([ADR 0009](docs/adr/0009-the-cryptography.md)).
- [x] **M07-003:** Relay offers, answers and ICE candidates between two peers, and nothing else.
- [x] **M07-004:** Accept announcements and subscriptions to opaque reachability identifiers, and tell subscribers when one is announced ([ADR 0011](docs/adr/0011-a-message-waits-with-its-sender.md), [ADR 0012](docs/adr/0012-only-contacts-can-tell-when-a-person-is-online.md), [ADR 0014](docs/adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).
- [x] **M07-005:** Keep only ephemeral state, and forget a peer's subscriptions and announcements when it disconnects.
- [x] **M07-006:** Decide the service's limits — connections, messages, subscriptions per peer — and how it answers abuse.
- [x] **M07-007:** Enforce them.
- [x] **M07-008:** Decide what the service logs and for how long, given that it sees who tries to reach whom.
- [x] **M07-009:** Test it on its own, and with two real peers.

### M08 — STUN and TURN

The fallback for peers that cannot reach each other directly ([ADR 0005](docs/adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)).

**Depends on:** M07.

- [x] **M08-001:** Decide how a peer obtains short-lived TURN credentials, without the application ever carrying a long-lived secret.
- [x] **M08-002:** Configure coturn for STUN and TURN accordingly.
- [x] **M08-003:** Issue and expire credentials that way.
- [x] **M08-004:** Run coturn and the rendezvous service together for development and tests, on one machine.

### M09 — The authenticated peer connection

One `RTCPeerConnection` per pair of peers, in the WebView, orchestrated by the core ([ADR 0006](docs/adr/0006-one-peer-connection-per-pair-in-the-webview.md), [ADR 0008](docs/adr/0008-the-protocol-between-peers.md)).

**Depends on:** M05, M07, M08.

- [x] **M09-001:** Manage the connections in the WebView: create, track and close one per pair, and carry opaque payloads between them and the core.
- [x] **M09-002:** Orchestrate signaling in the core: when to connect, to whom, with which ICE servers.
- [x] **M09-003:** Open the one reliable, ordered data channel of the first version.
- [x] **M09-004:** Negotiate the protocol's version, and tell the person when the other side needs to update.
- [x] **M09-005:** Run the handshake: nonces, the transcript of both identities, fingerprints, nonces, version and roles, signed and verified; accept nothing else before it completes.
- [x] **M09-006:** Disconnect a peer whose proof does not verify.
- [x] **M09-007:** Decide how a peer reconnects after a connection drops — how often, how long, and with what back-off.
- [x] **M09-008:** Reconnect accordingly, including after the page reloads.
- [x] **M09-009:** Decide how the transport is tested: the WebView's connection cannot be exercised by `go test`.
- [x] **M09-010:** Test the handshake in Go with the transport replaced, including every way it can be forged or replayed.
- [x] **M09-011:** Test a real connection between two instances, on one machine and across two networks.

### M10 — Invitations and contacts

How two people first find each other ([ADR 0005](docs/adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md), [ADR 0009](docs/adr/0009-the-cryptography.md), [ADR 0012](docs/adr/0012-only-contacts-can-tell-when-a-person-is-online.md)).

**Depends on:** M09.

- [x] **M10-001:** Encode and decode invitations — version, identity, suggested name, rendezvous hint, signature — as Bech32m with the prefix `convia`.
- [x] **M10-002:** Refuse an invitation whose checksum or signature does not verify, and say why.
- [x] **M10-003:** Decide whether accepting an invitation needs the inviter's consent before the two become contacts, and what the inviter sees.
- [x] **M10-004:** Add a contact from a pasted invitation, connect, and agree on the pair's secret when they first connect.
- [x] **M10-005:** Derive the pair's rotating reachability identifiers, announce them, and subscribe to them.
- [x] **M10-006:** Show the safety code of a contact, and let the person mark the contact as verified.
- [x] **M10-007:** Decide what happens when a contact's identity changes — an identity imported anew, a key that no longer matches.
- [x] **M10-008:** Decide how a person removes or blocks a contact, and what the other side learns.
- [x] **M10-009:** Build it.
- [x] **M10-010:** Fuzz the invitation decoder.
- [x] **M10-011:** Tell the person when a contact has to update Convia to talk to them, or they do ([ADR 0008](docs/adr/0008-the-protocol-between-peers.md)).

## Phase 4 — Conversations between two people

Two people write to each other, in real time or not, and both see the same conversation.

### M11 — Direct messages

The conversation itself ([ADR 0010](docs/adr/0010-a-conversation-is-kept-on-each-peer.md), [ADR 0011](docs/adr/0011-a-message-waits-with-its-sender.md)).

**Depends on:** M10.

- [x] **M11-001:** Give every message a 16-byte random identifier, its sender's sequence number and a hybrid logical clock timestamp.
- [x] **M11-002:** Order a conversation by timestamp, then identifier, identically on both peers.
- [x] **M11-003:** Store a message before showing it as sent, and keep the outbox in storage, so that waiting messages survive restarts of the application and the machine.
- [x] **M11-004:** Send waiting messages when the contact becomes reachable, and retransmit until acknowledged.
- [x] **M11-005:** Acknowledge a message only after storing it, and acknowledge again, storing nothing, a message already held.
- [x] **M11-006:** Catch up on reconnection by each sender's highest sequence number.
- [x] **M11-007:** Decide the largest message, and what happens to a larger one.
- [x] **M11-008:** Show each message as waiting, sent or delivered.
- [x] **M11-009:** Test ordering, idempotency and catching up with property-based tests: many orders of arrival, duplicates and interruptions, one result.

### M12 — The workspace and the conversation view

The Discord-like window where conversations happen ([`README.md`](README.md)).

**Depends on:** M11.

- [x] **M12-001:** Lay out the workspace: the rail, the sidebar and the conversation, from the design carried over in M01-011.
- [x] **M12-002:** List conversations, open one, and show its history.
- [x] **M12-003:** Write and send messages.
- [x] **M12-004:** Decide how messages are formatted — plain text, Markdown, and which of it — and how links are shown.
- [x] **M12-005:** Decide whether link previews exist, given that fetching one reveals the reader to the linked site.
- [x] **M12-006:** Decide whether unread counts, read receipts and typing indicators exist, and which travel between peers.
- [x] **M12-007:** Decide whether the interface shows that a contact is online, given that reachability does not have to be shown ([ADR 0014](docs/adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).
- [x] **M12-008:** Decide how a hidden Convia tells the person something arrived — Windows notifications, the icon, both — and what a notification may reveal.
- [x] **M12-009:** Build what those decisions keep.
- [x] **M12-010:** Meet WCAG 2.2 AA throughout, including keyboard navigation, as the theme's contrast test already requires of colour.
- [x] **M12-011:** Speak English and Brazilian Portuguese everywhere, with every phrase translated.

### M13 — More of a conversation

What people expect of a conversation beyond writing to each other.

**Depends on:** M12.

- [x] **M13-001:** Decide editing a message: whether, until when, and what the other side keeps of the earlier text ([ADR 0010](docs/adr/0010-a-conversation-is-kept-on-each-peer.md)).
- [x] **M13-002:** Decide deleting a message: for oneself, and asking the other side to delete it too.
- [x] **M13-003:** Decide replies and reactions.
- [x] **M13-004:** Decide sending files and images: which channel carries them, the largest, whether they wait for an offline peer, and how they are stored.
- [x] **M13-005:** Decide searching history, given that content is encrypted on disk.
- [x] **M13-006:** Build what those decisions keep.

## Phase 5 — Spaces

Discord-like spaces with channels, replicated by their members under signed authority ([ADR 0013](docs/adr/0013-a-space-is-replicated-by-its-members.md), [ADR 0014](docs/adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).

### M14 — The state of a space

The space as replicated, signed state.

**Depends on:** M11.

- [x] **M14-001:** Decide the roles and permissions a space has — what an owner, an administrator, a member and any other role may do — and how a channel's visibility is restricted to roles ([ADR 0038](docs/adr/0038-a-space-has-three-ranks-and-roles-that-open-channels.md)).
- [x] **M14-002:** Create a space with a genesis signed by its creator, and an identifier of its own.
- [x] **M14-003:** Represent every administrative change as a signed operation that names, by hash, the latest operations its author had seen.
- [x] **M14-004:** Order operations identically on every member: causes first, then timestamp, then identifier.
- [x] **M14-005:** Apply an operation only if its author held the authority it needs in the state before it; keep and show one that did not.
- [x] **M14-006:** Transfer ownership.
- [x] **M14-007:** Enforce the limit of 50 members.
- [x] **M14-008:** Test convergence with property-based tests: any order of arrival, concurrent operations and revocations, one state.
- [x] **M14-009:** Bring the space's packages under mutation testing (M01-010).

### M15 — Replicating a space

How members carry a space's content to one another.

**Depends on:** M14.

- [x] **M15-001:** Sign every group message with its author's key over every security-relevant field, encoded canonically.
- [x] **M15-002:** Verify a group message's signature whoever delivered it, and reject one that does not verify.
- [x] **M15-003:** Compare, author by author, how far each member holds a channel, and supply what another is missing.
- [x] **M15-004:** Send or synchronize content only to members allowed to read that channel at that moment.
- [x] **M15-005:** Give a new member the existing history of every channel they may read.
- [x] **M15-006:** On a removal, stop sending the removed member anything and reject their further actions.
- [x] **M15-007:** Keep the space's discovery secret per epoch, advance the epoch on every change that revokes access, and give the new secret only to current members.
- [x] **M15-008:** Decide how a person is invited into a space: the form of the invitation, who may issue one, and whether it is used once ([ADR 0039](docs/adr/0039-a-person-joins-a-space-when-an-administrator-adds-a-contact.md)).
- [x] **M15-009:** Decide how members choose which other members to connect to, so that 50 members do not each hold 49 connections ([ADR 0013](docs/adr/0013-a-space-is-replicated-by-its-members.md), [ADR 0040](docs/adr/0040-members-of-a-space-connect-to-a-few-of-one-another.md)).
- [x] **M15-010:** Build it.

### M16 — Spaces in the interface

**Depends on:** M12, M15.

- [x] **M16-001:** Show spaces in the rail, their channels in the sidebar, and a channel's conversation ([ADR 0042](docs/adr/0042-a-channel-holds-what-a-conversation-holds-but-files.md)).
- [x] **M16-002:** Show a space's members and their roles.
- [x] **M16-003:** Create and administer a space: channels, members, roles, transferring ownership.
- [x] **M16-004:** Decide moderation beyond removal — muting, deleting another's message — and how it travels ([ADR 0041](docs/adr/0041-moderators-mute-members-and-delete-their-messages.md)).
- [x] **M16-005:** Build it.
- [x] **M16-006:** Channels read by every member who sees them but written only by some roles — announcements, rules ([ADR 0038](docs/adr/0038-a-space-has-three-ranks-and-roles-that-open-channels.md)).
- [x] **M16-007:** Decide how files travel in a space: who serves them, and to whom ([ADR 0042](docs/adr/0042-a-channel-holds-what-a-conversation-holds-but-files.md), [ADR 0043](docs/adr/0043-a-spaces-files-are-served-by-whoever-holds-them.md)).
- [x] **M16-008:** Build it.

## Phase 6 — Calls and screen sharing

Voice, video and screen sharing over the peer connection ([ADR 0015](docs/adr/0015-calls-and-screen-sharing-ride-the-peer-connection.md)).

### M17 — Calls between two people

**Depends on:** M12.

- [x] **M17-001:** Verify how Wails and WebView2 handle the permission requests for the microphone, the camera and the screen, and record what was found ([media permissions](docs/platform/media-permissions.md)).
- [x] **M17-002:** Define the messages of a call's lifecycle — calling, accepting, declining, ending — in the protocol.
- [x] **M17-003:** Add and remove audio and camera tracks on the existing connection with perfect negotiation, over the authenticated data channel.
- [x] **M17-004:** Decide how long a call rings, and what the callee sees when hidden ([ADR 0044](docs/adr/0044-a-call-rings-for-30-seconds.md)).
- [x] **M17-005:** Let a person choose their microphone, camera and speakers, and mute or turn the camera off.
- [x] **M17-006:** Build the call's interface.

### M18 — Screen sharing

**Depends on:** M17.

- [ ] **M18-001:** Capture a screen or a window with `getDisplayMedia()`, and add it to the connection.
- [x] **M18-002:** Decide whether a shared screen carries the computer's sound ([ADR 0046](docs/adr/0046-a-shared-screen-may-carry-the-computers-sound.md)).
- [ ] **M18-003:** Show a shared screen to the other side, and stop sharing from either the interface or the system's own control.

### M19 — Calls with more people

**Depends on:** M15, M18.

- [ ] **M19-001:** Decide how a call exists in a space: a call started in a channel, a voice channel people join and leave as in Discord, or both.
- [ ] **M19-002:** Connect the participants of a call in a full mesh.
- [ ] **M19-003:** Enforce at most six participants while video or screen sharing is on.
- [ ] **M19-004:** Measure an audio-only call as participants are added, and set its limit from the measurement.
- [ ] **M19-005:** Build the interface of a call with several people.

## Phase 7 — Ready to be used

What makes Convia something people can install and rely on.

### M20 — Measuring

**Depends on:** M15, M19.

- [ ] **M20-001:** Measure a full space: connections, memory, processor, signaling, synchronization traffic and bandwidth at 50 members.
- [ ] **M20-002:** Measure reconnection storms and catching up after being away.
- [ ] **M20-003:** Measure how often connections need TURN, on real networks.
- [ ] **M20-004:** Revise the limits of [ADR 0013](docs/adr/0013-a-space-is-replicated-by-its-members.md) and [ADR 0015](docs/adr/0015-calls-and-screen-sharing-ride-the-peer-connection.md) from what was measured.

### M21 — Security

**Depends on:** M15, M19.

- [ ] **M21-001:** Write the threat model: what each peer, the rendezvous service, TURN and a member of a space can and cannot learn or do.
- [ ] **M21-002:** Fuzz every decoder of data from another peer or the rendezvous service.
- [ ] **M21-003:** Run mutation testing on the critical packages before each release (M01-010).
- [ ] **M21-004:** Decide whether the protocol and the cryptography are reviewed by somebody outside the project before the first public release.

### M22 — Packaging and release

**Depends on:** M12.

- [ ] **M22-001:** Decide how Convia is installed: MSI, MSIX, NSIS or another.
- [ ] **M22-002:** Decide how Convia updates itself, and how an update is verified before it runs.
- [ ] **M22-003:** Decide whether executables are code-signed, and with what certificate.
- [ ] **M22-004:** Number releases with SemVer, 0.x until the protocol and the experience are mature (CONTRIBUTING.md).
- [ ] **M22-005:** Build release artifacts in CI from a tag.
- [ ] **M22-006:** Decide what Convia does with a crash: nothing, a local report, or a report the person may choose to send.

### M23 — The project's infrastructure

The default rendezvous service and coturn the project runs, and a person's freedom to use others ([ADR 0005](docs/adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)).

**Depends on:** M07, M08.

- [ ] **M23-001:** Decide where the project's rendezvous service and coturn run, under which domain, and who pays for them.
- [ ] **M23-002:** Deploy them, with certificates that renew themselves.
- [ ] **M23-003:** Decide what is monitored, and what an outage of either means for people using Convia.
- [ ] **M23-004:** Let a person configure other infrastructure in the application, and show which they use.
- [ ] **M23-005:** Document how to run one's own rendezvous service and coturn.
- [ ] **M23-006:** Decide whether Convia needs a privacy policy for its infrastructure, and write it if so.
- [ ] **M23-007:** Decide how an operator blocks an address permanently, without the service logging who it blocked ([ADR 0021](docs/adr/0021-the-rendezvous-service-limits-every-peer.md), [ADR 0022](docs/adr/0022-the-rendezvous-service-logs-nobody.md)).
- [ ] **M23-008:** Decide coturn's quotas — bandwidth and allocations — for the project's relay.

## Later

Not planned yet, each recorded where it was left open:

- **Several devices per person**, which [ADR 0004](docs/adr/0004-a-person-is-a-key-their-application-made.md) leaves room for.
- **macOS and Linux**, which [ADR 0001](docs/adr/0001-a-go-core-in-a-wails-window-for-windows.md) defers.
- **Encryption above DTLS**, which [ADR 0009](docs/adr/0009-the-cryptography.md) reconsiders if a message could pass through anything but a session between members.
- **Link previews**, fetched only by their sender, which [ADR 0031](docs/adr/0031-link-previews-may-come-later.md) allows and leaves for later.
- **Spaces larger than 50, and calls larger than a mesh carries**, which [ADR 0013](docs/adr/0013-a-space-is-replicated-by-its-members.md) and [ADR 0015](docs/adr/0015-calls-and-screen-sharing-ride-the-peer-connection.md) limit for now.
