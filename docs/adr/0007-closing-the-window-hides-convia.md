# 0007 — Closing the window hides Convia

## Status

Accepted

## Date

2026-10-13

## Context

A peer's connections live as long as its WebView does ([ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md)). If closing the window ended the process, Convia would stop receiving anything the moment its window was closed — a surprise in an application people keep for talking to each other.

## Decision

**Closing the window hides Convia in the Windows notification area, and it keeps running.** Its connections stay open and it keeps receiving while hidden. Clicking its icon there shows the window again. **Quitting is an explicit action**, offered from the icon's menu, and it is the one way the process ends on purpose.

## Consequences

- Wails v2 has no notification-area icon of its own, so one is added with a library chosen when it is implemented, under [ADR 0003](0003-dependencies-carry-permissive-licenses.md).
- A hidden Convia that receives something has to be able to tell the person; how it notifies them is decided with the interface.
- Still open: whether Convia starts with Windows.
