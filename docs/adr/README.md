# Architecture decision records

Each record explains why an architectural decision was made, so that nobody has to infer it from the code (AGENTS.md, rules 3 and 4). [`ROADMAP.md`](../../ROADMAP.md) describes what is built; these describe why it is built that way.

A record is `NNNN-title.md`, numbered in order and never renumbered, written in English, with five sections: **Status** (Proposed, Accepted, or Superseded by a later record), **Date**, **Context**, **Decision** and **Consequences**. A decision is changed by a new record that supersedes the old one, not by editing it.

| Record | Decision |
| --- | --- |
| [0001](0001-a-go-core-in-a-wails-window-for-windows.md) | A Go core in a Wails v2 window with a React interface, for Windows first |
| [0002](0002-what-a-change-must-pass.md) | What a change must pass before it is merged |
| [0003](0003-dependencies-carry-permissive-licenses.md) | Dependencies carry permissive licenses |
| [0004](0004-a-person-is-a-key-their-application-made.md) | A person is a key their own application made |
| [0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md) | Peers connect over WebRTC, helped by a rendezvous service and TURN |
| [0006](0006-one-peer-connection-per-pair-in-the-webview.md) | One peer connection per pair of peers, in the WebView |
| [0007](0007-closing-the-window-hides-convia.md) | Closing the window hides Convia |
| [0008](0008-the-protocol-between-peers.md) | The protocol between peers |
| [0009](0009-the-cryptography.md) | The cryptography |
| [0010](0010-a-conversation-is-kept-on-each-peer.md) | A conversation is kept on each peer, in the same order on both |
| [0011](0011-a-message-waits-with-its-sender.md) | A message waits with its sender until both peers are online |
| [0012](0012-only-contacts-can-tell-when-a-person-is-online.md) | Only contacts can tell when a person is online |
| [0013](0013-a-space-is-replicated-by-its-members.md) | A space is replicated by its members, under signed authority |
| [0014](0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md) | Members of a space share a secret to find one another |
| [0015](0015-calls-and-screen-sharing-ride-the-peer-connection.md) | Calls and screen sharing ride the peer connection |
| [0016](0016-how-convia-runs-on-windows.md) | How Convia runs on Windows |
| [0017](0017-the-database-only-moves-forward-and-is-never-destroyed.md) | The database only moves forward, and is never destroyed |
| [0018](0018-replacing-protecting-and-deleting-an-identity.md) | Replacing, protecting and deleting an identity |
| [0019](0019-the-protocol-version-is-a-whole-number.md) | The protocol's version is a whole number |
| [0020](0020-the-rendezvous-service-lives-beside-the-application.md) | The rendezvous service lives beside the application |
| [0021](0021-the-rendezvous-service-limits-every-peer.md) | The rendezvous service limits every peer |
| [0022](0022-the-rendezvous-service-logs-nobody.md) | The rendezvous service logs nobody |
| [0023](0023-the-rendezvous-service-hands-out-turn-credentials.md) | The rendezvous service hands out TURN credentials |
| [0024](0024-peers-connect-on-demand-and-reconnect-when-they-can.md) | Peers connect on demand, and reconnect when they can |
| [0025](0025-the-transport-is-tested-in-three-layers.md) | The transport is tested in three layers |
| [0026](0026-a-contact-needs-the-inviters-consent.md) | A contact needs the inviter's consent, and a request expires |
| [0027](0027-a-new-key-is-a-new-person.md) | A new key is a new person |
| [0028](0028-removing-or-blocking-a-contact-is-silent.md) | Removing or blocking a contact is silent |
