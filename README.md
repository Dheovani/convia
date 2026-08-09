# Convia

Convia is a standalone real-time communication platform. It is intended to provide its own user interface while also exposing stable public APIs and SDKs for products such as Orbit and Workspace Town.

Convia owns its public API and domain model. Media infrastructure, including the planned initial use of LiveKit, remains an internal implementation detail.

## Status

The project currently contains only the Go backend foundation: environment-based HTTP configuration, process lifecycle management, graceful shutdown, and a health endpoint. Calls, rooms, authentication, persistence, real-time events, and media integration are intentionally not implemented yet.

## Prerequisites

- Go 1.26.5, as declared in `go.mod`
- Docker, optionally, for a container build

## Run

Start the service with development defaults (`0.0.0.0:8080`):

```sh
go run ./cmd/convia
```

Configuration is available through these environment variables:

- `CONVIA_HTTP_HOST` sets the HTTP bind host. The default is `0.0.0.0`.
- `CONVIA_HTTP_PORT` sets the HTTP port. The default is `8080`.

Check the running service:

```sh
curl http://localhost:8080/health
```

## Test

```sh
go test ./...
```

## Build

Build the Go executable:

```sh
go build -o convia ./cmd/convia
```

Build the container image:

```sh
docker build -t convia .
```

## Continuous integration

GitHub Actions validates the project through three workflows:

- `CI` validates workflow files, checks formatting, runs `go vet` and Staticcheck, executes tests with race detection and coverage, and builds every package.
- `Security` runs Go vulnerability analysis and CodeQL on pushes, pull requests, a weekly schedule, and manual requests.
- `Container` builds the production image, verifies its non-root user, and smoke tests the health endpoint.

Workflow actions are pinned to full commit SHAs. Dependabot checks GitHub Actions and Go module updates every week.

The detailed development roadmap and current progress are tracked in [`TODO.md`](TODO.md).
