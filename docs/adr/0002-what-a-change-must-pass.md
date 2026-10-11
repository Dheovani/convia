# 0002 — What a change must pass before it is merged

## Status

Accepted

## Date

2026-10-11

## Context

A change is merged into `main` only by pull request, with one approving review from somebody other than its author. What the review cannot see — formatting, types, vulnerabilities, licenses, a build that no longer compiles — has to be checked every time, by the same rules for everybody.

## Decision

**Every pull request must pass these checks** ([`ci.yml`](../../.github/workflows/ci.yml), [`codeql.yml`](../../.github/workflows/codeql.yml)):

| Check | What it runs |
| --- | --- |
| Go | `gofmt`, `go vet`, Staticcheck and the Go tests |
| Frontend | Prettier, ESLint, the TypeScript typecheck and the Vitest tests |
| Windows build | `wails build` for `windows/amd64` |
| Vulnerabilities | `govulncheck` and `npm audit` |
| Licenses | every dependency against [ADR 0003](0003-dependencies-carry-permissive-licenses.md) |
| Secrets | gitleaks over the whole history |
| CodeQL | Go and TypeScript |

**Tests are required of relevant code, not of a coverage percentage.** No global coverage threshold is set: a number invites tests written to move it. Mutation testing is kept, aimed at the packages where a missed bug is most costly — identity, cryptography, the protocol, message synchronization and networking — and run periodically and before releases rather than on every pull request.

**Dependencies are updated by Dependabot, weekly**, for Go modules, npm packages and the GitHub Actions the workflows use.

## Consequences

- The checks are named in the branch protection of `main`; a check renamed here must be renamed there.
- The mutation testing workflow is added once a critical package exists to aim it at ([`ROADMAP.md`](../../ROADMAP.md), M01-010).
- The review rule is not lowered or bypassed for convenience: a pull request waits for its reviewer.
