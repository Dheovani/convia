# Convia

Convia is a standalone real-time communication platform. It is intended to provide its own user interface while also exposing stable public APIs and SDKs for products such as Orbit and Workspace Town.

Convia owns its public API and domain model. Media infrastructure, including the planned initial use of LiveKit, remains an internal implementation detail.

## Status

The project currently contains the Go backend foundation, its HTTP transport baseline, and its PostgreSQL foundation: environment-based configuration, process lifecycle management, graceful shutdown, health and readiness endpoints, request correlation identifiers, structured access logs, panic recovery, a single JSON error schema, a connection pool, and reversible schema migrations. Calls, rooms, authentication, real-time events, and media integration are intentionally not implemented yet.

The transport contract shared by every endpoint is documented in [`docs/api-conventions.md`](docs/api-conventions.md).

## API contract

The public API is specified in [`api/openapi.yaml`](api/openapi.yaml), an OpenAPI 3.0.3 document. It is authoritative: `go test` validates the document, compares it against the implemented routes in both directions, checks that the documented error codes are exactly the ones the service can return, and validates real responses against the documented schemas.

[`docs/api-compatibility.md`](docs/api-compatibility.md) governs how the contract may change: naming, additive versus breaking changes, lifecycle states, deprecation periods, idempotency, optimistic concurrency, cursor opacity, and how SDKs consume the contract.

## Prerequisites

- Go 1.26.6, as declared in `go.mod`
- Docker, for the local PostgreSQL instance and for a container build

## Run

Convia requires PostgreSQL. Start it, apply the migrations, then start the service on its development defaults (`0.0.0.0:8080`):

```sh
docker compose up -d
export CONVIA_DATABASE_URL="postgres://convia:convia@127.0.0.1:5432/convia?sslmode=disable"
go run ./cmd/convia migrate up
go run ./cmd/convia
```

Configuration is available through these environment variables:

- `CONVIA_ENVIRONMENT` selects `development` or `production` validation. The default is `development`.
- `CONVIA_HTTP_HOST` sets the HTTP bind host. The default is `0.0.0.0`.
- `CONVIA_HTTP_PORT` sets the HTTP port. The default is `8080`.
- `CONVIA_DATABASE_URL` sets the PostgreSQL connection URL. It is required and has no default.
- `CONVIA_DATABASE_MAX_CONNECTIONS` sets the pool size. The default is `10`.
- `CONVIA_DATABASE_CONNECT_TIMEOUT` bounds establishing a connection. The default is `5s`.
- `CONVIA_DATABASE_QUERY_TIMEOUT` bounds a single query. The default is `5s`.

In production, `CONVIA_DATABASE_URL` must request a verified TLS mode. [`docs/database.md`](docs/database.md) documents the database setup, the migration workflow, and the testing model.

Check the running service:

```sh
curl http://localhost:8080/health   # process liveness, never touches the database
curl http://localhost:8080/ready    # dependency readiness, 503 when PostgreSQL is unreachable
```

Operational endpoints are served outside the `/v1` public API prefix. Every response carries an `X-Request-ID` header, and every failure uses the JSON error schema:

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

Database tests are skipped unless a PostgreSQL instance is provided. Include them with:

```sh
docker compose up -d
CONVIA_TEST_DATABASE_URL="postgres://convia:convia@127.0.0.1:5432/convia?sslmode=disable" go test ./...
```

Each database test creates and drops its own database, so runs never share state.

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
