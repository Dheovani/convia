# Roadmap

What Convia builds, in order: phases, the milestones in each, and the tasks in each milestone. Every task has an ID that never changes, `M01-001`, so it can be referred to from commits, pull requests and records. Why things are built the way they are is in [`docs/adr`](docs/adr/README.md), not here.

A task is ticked when it is done, and nothing else is written about it. A task that needs a decision first says so, and waits for it (AGENTS.md, rule 3).

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
- [x] **M01-011:** Carry over the interface's design from before the reset: the theme, the mark and the icon, the shared controls, and the choice of language.
- [ ] **M01-010:** Run mutation testing periodically and before releases, on the critical packages, once the first of them exists.

### M02 — The decisions before the first conversation

Nothing that two peers do together can be built before these are decided ([`README.md`](README.md)). Each is decided by the product owner and recorded as an ADR.

- [ ] **M02-001:** Decide identity, and how a peer authenticates another.
- [ ] **M02-002:** Decide how peers discover each other, how they connect across NAT, and which auxiliary infrastructure, if any, exists.
- [ ] **M02-003:** Decide the protocol between peers.
- [ ] **M02-004:** Decide the cryptography.
- [ ] **M02-005:** Decide how a conversation is stored locally, ordered and synchronized.
- [ ] **M02-006:** Decide what happens to a message sent to a peer that is offline.
- [ ] **M02-007:** Decide how voice, video and screen sharing are negotiated between peers.
- [ ] **M02-008:** Decide groups and conversations with more than two people.

## Phase 2 — Two peers talk

Its milestones are written once M02's decisions are, because what they contain depends on them.

## Later

- **Packaging and release:** the installer, automatic updates and code signing, decided in a milestone of their own once there is a public distribution.
