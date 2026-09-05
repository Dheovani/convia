# Convia

Convia is a standalone real-time communication platform. It is intended to provide its own user interface while also exposing stable public APIs and SDKs for products such as Orbit and Workspace Town.

Convia owns its public API and domain model. Media infrastructure, including the planned initial use of LiveKit, remains an internal implementation detail.

## Status

The project currently contains the Go backend foundation and its HTTP transport baseline: environment-based configuration, process lifecycle management, graceful shutdown, a health endpoint, request correlation identifiers, structured access logs, panic recovery, and a single JSON error schema. Calls, rooms, authentication, persistence, real-time events, and media integration are intentionally not implemented yet.

The transport contract shared by every endpoint is documented in [`docs/api-conventions.md`](docs/api-conventions.md).

## Prerequisites

- Go 1.26.6, as declared in `go.mod`
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

The health endpoint is operational rather than public, so it is served outside the `/v1` public API prefix. Every response carries an `X-Request-ID` header, and every failure uses the JSON error schema:

```sh
curl -i http://localhost:8080/v1/rooms
```

```json
{
  "error": {
    "code": "not_found",
    "message": "The requested resource does not exist.",
    "request_id": "MXHJAY4MJNX2FO22XWJ3XNCKHT"
  }
}
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
- `Security` runs Go vulnerability analysis on pushes, pull requests, a weekly schedule, and manual requests.
- `Container` builds the production image, verifies its non-root user, and smoke tests the health endpoint.

The `Security` workflow also contains a CodeQL job with extended security queries. Publishing CodeQL results requires code scanning, which needs a public repository or GitHub Advanced Security, so the job is opt-in: enable code scanning in the repository settings, then set the repository variable `ENABLE_CODE_SCANNING` to `true`.

Workflow actions are pinned to full commit SHAs. Dependabot checks GitHub Actions and Go module updates every week.

The detailed development roadmap and current progress are tracked in [`TODO.md`](TODO.md).

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) describes the development workflow, the local checks, and what a change is expected to contain. [`AGENTS.md`](AGENTS.md) is the authoritative engineering guide. Vulnerabilities must be reported privately, as described in [`SECURITY.md`](SECURITY.md).

## License

Convia is source-available, not open source. It is licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE.md).

- **Free for noncommercial use.** Personal use, hobby projects, private study, experimentation, and use by charities, schools, public research organizations, and government institutions are all permitted. Self-host it, modify it, and share your changes.
- **Commercial rights are reserved.** Using Convia in or for a commercial product or service requires a separate license from the copyright holder. Open a discussion in the repository to request one.

Contributions are accepted under the terms described in [`CONTRIBUTING.md`](CONTRIBUTING.md).
