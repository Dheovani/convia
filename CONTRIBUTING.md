# Contributing to Convia

Thank you for your interest in Convia. This document describes how to propose a change and what a change is expected to contain before it can be merged.

Convia is source-available rather than open source: it is free for personal and other noncommercial use, and commercial rights are reserved. Read [Licensing of Contributions](#licensing-of-contributions) before you invest time in a change.

## Before You Start

- Read [`AGENTS.md`](AGENTS.md). It is the authoritative engineering guide: architecture boundaries, Go style, error handling, testing, and the rules about which infrastructure may be introduced and when.
- Read [`TODO.md`](TODO.md). Work follows the roadmap, and every task has a stable ID such as `M02-005`. Reference that ID in your branch, commit, and pull request.
- Read [`docs/api-conventions.md`](docs/api-conventions.md) before touching anything that serves HTTP.
- For a substantial change, open an issue first and agree on the approach. A pull request that introduces a database, a dependency, or an abstraction that the current milestone does not require will be asked to justify it or to shrink.

## Development Environment

- Go, at the version declared in [`go.mod`](go.mod). The Go toolchain downloads the exact patch release automatically.
- Docker, optionally, to build and smoke test the container image.
- No database or media server is required yet; the service runs standalone.

```sh
go run ./cmd/convia
curl http://localhost:8080/health
```

## Local Checks

Run these before opening a pull request. They mirror what CI enforces:

```sh
gofmt -l .        # must print nothing
go vet ./...
go test ./...
go build ./...
```

CI additionally runs the race detector, Staticcheck, `govulncheck`, and Actionlint. To match it locally:

```sh
go test -count=1 -race -shuffle=on ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
```

The race detector requires cgo and a C compiler. If your machine has neither, say so in the pull request and rely on CI for that check.

Actionlint runs ShellCheck against the inline scripts in workflow files. The GitHub runner may ship an older ShellCheck than your machine, so a workflow change that passes locally can still fail in CI.

## Change Expectations

A change is ready for review when:

- it is focused on one roadmap item, and unrelated cleanups are in separate commits or separate pull requests;
- new behavior has tests, and a bug fix has a regression test;
- errors are returned with context rather than logged and swallowed;
- public HTTP behavior follows `docs/api-conventions.md`, including the shared error schema;
- no LiveKit, database, or other infrastructure concept leaks into a public contract;
- no credential, key, token, or environment-specific address is committed;
- documentation is updated when the change alters documented behavior or architecture;
- `TODO.md` is updated in the same pull request when it completes or advances a roadmap item.

All documentation, identifiers, comments, and commit messages are written in English.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat: add request correlation identifiers to HTTP responses
fix: reject unsupported content types on JSON endpoints
docs: record reverse-proxy assumptions
test: cover panic recovery in the middleware chain
chore: bump the Go toolchain to a patched release
```

Use the imperative mood, keep the subject under 72 characters, and describe the reasoning in the body when the change is not obvious. Reference the roadmap task ID when one applies.

## Pull Requests

1. Branch from `main`. Name branches after the work, for example `m02-http-baseline` or `fix/health-content-type`.
2. Keep the branch current with `main` before requesting a review.
3. Fill in the pull request template honestly, including the sections that do not apply.
4. Make sure every required check passes.

### Required checks

Branch protection on `main` requires these checks:

| Workflow    | Check                        | What it covers                                                             |
| ----------- | ---------------------------- | -------------------------------------------------------------------------- |
| `CI`        | `Validate Go project`        | Actionlint, formatting, `go vet`, Staticcheck, race tests, coverage, build |
| `Security`  | `Go vulnerability scan`      | `govulncheck` against the module and the Go toolchain                       |
| `Container` | `Build and smoke test image` | Image build, non-root runtime user, containerized health check              |

`Security / CodeQL analysis` is opt-in: it runs only when the repository variable `ENABLE_CODE_SCANNING` is set to `true`, because publishing results requires a public repository or GitHub Advanced Security. Add it to the required checks once it is enabled.

## Security Issues

Do not report vulnerabilities through issues or pull requests. Follow [`SECURITY.md`](SECURITY.md).

## Licensing of Contributions

By contributing, you agree that:

- your contribution is your own work, or you have the right to submit it;
- your contribution is licensed to the project and to its users under the [PolyForm Noncommercial License 1.0.0](LICENSE.md), the same terms that cover Convia; and
- you grant the maintainer a perpetual, worldwide, irrevocable, royalty-free right to use, reproduce, modify, and distribute your contribution, and to license it under other terms, including commercial terms, as part of Convia.

This last point exists because Convia reserves its commercial rights. Without it, the maintainer could not offer a commercial license covering the whole project. If you are not comfortable granting it, please open an issue to discuss your change instead of submitting it.

This section describes the project's intent in plain language. It is not legal advice, and it may be replaced by a formal contributor license agreement.
