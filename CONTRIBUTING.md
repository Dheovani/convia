# Contributing

## What is accepted

**Issues and discussions are welcome.** Code from outside the project is not accepted for now: a contribution would have to be licensed in a way that preserves the owner's right to license Convia commercially ([`LICENSE.md`](LICENSE.md)), and the agreement that does that — a contributor license agreement — will be introduced before code from anybody else is.

## How a change is made

1. **Read [`README.md`](README.md) and [`AGENTS.md`](AGENTS.md)** before any work. They define the product and the rules.
2. **Work on a branch**, never on `main`.
3. **Write commits as [Conventional Commits](https://www.conventionalcommits.org/)**: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `build:`, `ci:`, `chore:`.
4. **Open a pull request.** It is merged after every check of [ADR 0002](docs/adr/0002-what-a-change-must-pass.md) passes and somebody other than its author approves it. The rule is not lowered or bypassed for convenience.

## Versions

Convia follows [Semantic Versioning](https://semver.org/): `0.x` while it is being developed, and `1.0.0` once its protocol and its experience are mature.

## Building and checking locally

You need Go (the version in `go.mod`), Node.js 24 and the Wails v2 CLI at the version `go.mod` requires.

```text
wails dev                                  # the application, reloading as it changes
wails build -platform windows/amd64        # build/bin/convia.exe

cd frontend
npm ci
npm run format:check && npm run lint && npm run typecheck && npm test

cd ..
go vet ./... && go test ./...
```

`go vet` and `go test` need `frontend/dist` to exist, because the binary embeds the built interface: run `npm run build` in `frontend` once, or create the directory.

Convia keeps its files in `%LocalAppData%\Convia` and runs once per directory. To run two peers on one machine, give the second its own: `CONVIA_HOME=C:\convia-second build\bin\convia.exe`.
