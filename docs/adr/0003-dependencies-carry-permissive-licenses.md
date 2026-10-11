# 0003 — Dependencies carry permissive licenses

## Status

Accepted

## Date

2026-10-11

## Context

Convia is source-available under the PolyForm Noncommercial license, and its owner reserves the right to license it commercially ([`LICENSE.md`](../../LICENSE.md)). A dependency under a copyleft or restrictive license could constrain either, and judging each one legally, case by case, is a cost nobody should pay by default.

## Decision

**Only dependencies under permissive licenses are used**: MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, ISC, 0BSD, MIT-0, BlueOak-1.0.0 and CC0-1.0. GPL, LGPL, AGPL, SSPL, MPL and every other copyleft or restrictive license are refused, unless a record here decides otherwise for a named dependency. The Licenses check in CI enforces the list.

**One exception, for tools that only build the interface.** Two licenses outside the list reach the repository through tools that are never distributed in Convia:

- `lightningcss`, under MPL-2.0, a dependency of Vite 8 and of Tailwind CSS 4 alike;
- `caniuse-lite`, under CC-BY-4.0, a database of browser support that Vite's toolchain reads.

MPL-2.0 and CC-BY-4.0 are therefore allowed **for development dependencies only**. Everything compiled into the binary or shipped in the interface stays on the list, and CI checks the two apart.

## Consequences

- Adding a dependency means checking its license first; CI refuses one that is not on the list.
- The exception covers those two licenses in development dependencies and nothing else: an MPL-2.0 or CC-BY-4.0 package that would be shipped is refused like any other.
