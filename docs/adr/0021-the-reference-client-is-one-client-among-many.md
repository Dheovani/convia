# ADR 0021 — The reference client is one client among many

**Status:** Accepted
**Date:** 2026-09-27
**Milestones:** M19 — TypeScript Client SDK; M35 — Desktop Application

## Context

Convia is a real-time communication system. **Its job is the communication, not an interface to it.** Anybody may write their own client — free for noncommercial use, commercial rights reserved, as `LICENSE.md` says — and the product owner's own other products, Orbit and Workspace Town, are each meant to write one and consume Convia privately.

The interface in `web/`, and the desktop application that embeds it, are **the reference client**: what an ordinary person opens when they have not written their own. That has been the intent from the start. It has never been written down, and two things followed from that.

The first is [ADR 0019](0019-a-session-travels-in-a-cookie-or-a-header.md)'s whole existence. `M18` was recorded in the roadmap as a "standalone web application" and built on that premise — a session in a cookie, an interface served from Convia's own origin, an SDK aimed at browsers — and `M35` was spent correcting what followed. A decision nobody wrote down is a decision that gets made again, differently, by whoever reads the record next.

The second is subtler and is why this ADR exists now rather than at `V1`. **"The reference client is one client among many" is currently an intention rather than a fact.** The interface is compiled into the service binary with `go:embed`; the service serves it as a page; the two are built, tested and released together. Nothing would break if the reference client quietly came to depend on something no third party could have. Nobody would notice for a year.

Measuring it was the surprise. The desktop binary links exactly four things of Convia's: the public error vocabulary (`internal/api` — `ErrorCode`, `ErrorBody`, `Prefix`, `WriteError`), the event vocabulary (`internal/events`, which [ADR 0020](0020-the-event-vocabulary-is-separate-from-its-delivery.md) had already made import nothing), the interface bundle it embeds, and its own code under `internal/desktop`. It touches no domain, no store, no database. **The dependency direction is already right** — partly on purpose, from the two-binary tripwire and ADR 0020, and partly by luck.

## Decision

**Convia and its reference client are separate products, and they will be separate repositories.** This repository becomes Convia alone: the control plane, its API, and nothing that draws a window.

The move happens at `V1`, not now. Two repositories cost a release cadence and a contract between them, and today a change to `/v1/me/...` and to the interface that consumes it land in one commit and are checked by one run. Paying that before the API is stable would be paying it for nothing.

**What does not wait is the preparation, because it is `M19` and it is worth doing anyway.** The two vocabularies the client shares as Go source — errors and events — become something published, and the reference client consumes that rather than `internal/`. `M19` then has a real first consumer instead of being written in the vacuum left by products that do not exist yet, and the split becomes `git mv`.

The rule that decides every case in between: **the reference client may use only what a third party could use.** Where that is inconvenient, the inconvenience is the finding.

## Consequences

- **The platform claim becomes falsifiable.** Today it is an intention nothing tests. Once the reference client builds from published contracts alone, a dependency it should not have stops the build instead of going unnoticed.
- **Convia stops serving a page.** `internal/web`, the content-security policy naming the media server exactly, and the page-versus-application duality all leave with the client. What remains is an API and the `go:embed` of nothing.
- **The technology the client is built with stops being Convia's concern.** Wails, Tauri, Qt, Electron or a thing not yet written: it consumes an API.
- **The personal surface does not move.** `/v1/me/...` is Convia's, not the client's — a person signing in, their rooms, their messages, their calls are all control plane, and every client needs them. The duplication between `tenant_http.go` and `session_http.go` in `rooms`, `messages` and `participants` is unaffected by this decision and remains its own question.
- **Convia loses its end-to-end proof, and has to replace it.** The five Playwright journeys in `web/e2e` are the only thing that drives a real browser against a real Convia and a real LiveKit. They go with the client. `M35-012` already asks what replaces them; this makes the answer load-bearing rather than tidy, and `M33-010` — two installations against each other in CI — becomes the shape of the proof that stays.
- **Licensing reads per artifact.** The service and the reference client are licensed the same way today and can diverge without either being rewritten.
