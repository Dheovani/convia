# Convia

Convia is a standalone real-time communication platform. It is intended to provide its own user interface while also exposing stable public APIs and SDKs for products such as Orbit and Workspace Town.

Convia owns its public API and domain model. Media infrastructure, including the planned initial use of LiveKit, remains an internal implementation detail.

## Status

The project currently contains the Go backend foundation, its HTTP transport baseline, its PostgreSQL foundation, and four domain resources: environment-based configuration, process lifecycle management, graceful shutdown, health and readiness endpoints, request correlation identifiers, structured access logs, panic recovery, a single JSON error schema, a connection pool, reversible schema migrations, and endpoints for applications, their users, their API credentials, their rooms, the calls held in them, and who takes part. A narrow internal boundary separates all of it from whatever will eventually transport audio and video.

The tenant-facing API is authenticated. An application presents an opaque API key carrying explicit scopes, and Convia takes the tenant from that key rather than from the request, so `/v1/users` and `/v1/credentials` act on the caller's own data and nothing else. [`docs/authentication.md`](docs/authentication.md) documents the threat model and the credential lifecycle; [`docs/runbooks/credential-revocation.md`](docs/runbooks/credential-revocation.md) is the procedure for withdrawing a leaked key.

The operator surface is authenticated too, by a separate kind of key. An operator credential (`cvo_`) administers Convia itself — creating tenants, suspending them, issuing their first keys — and lives in its own table with its own scopes, so an application key can never reach it. The first one is created with `convia operator issue`, because issuing one over the API requires presenting one.

Rooms exist: an application can create durable rooms addressed by an alias it chose, or anonymous ones for a single occasion, and manage their lifecycle under `/v1/rooms`. A creation can carry an `Idempotency-Key`, so retrying after a timeout produces no second room. See [`docs/rooms.md`](docs/rooms.md).

Calls exist: an application can start a conversation in one of its rooms, end it, and read the history of what has happened there, under `/v1/calls`. A room holds one call at a time, and starting one can carry an `Idempotency-Key`. See [`docs/calls.md`](docs/calls.md).

Participants exist: an application can admit its people to a call, read who is there, promote a moderator, and remove someone. Joining is idempotent by the person, so a reconnection never duplicates anyone, and a room's capacity is enforced when people arrive. See [`docs/participants.md`](docs/participants.md).

People can also be invited rather than admitted. An invitation is a credential the invitee holds and presents themselves, which is what lets Convia enforce its expiry and its withdrawal instead of merely recording them; redeeming one produces a place in the call and the means to connect, in a single request. See [`docs/invitations.md`](docs/invitations.md).

A media plane exists behind that boundary: a call asks LiveKit for the room its conversation happens in, and releases it when the call ends. A client joins through Convia alone: one endpoint returns a short-lived credential for one person in one call, and no external consumer ever integrates with a media provider directly. Convia still runs perfectly well with no media plane configured at all. See [`docs/media.md`](docs/media.md).

Control events stream: an application opens one WebSocket at `/v1/events` and is told what happened while it is still news — a call starting or ending, a roster changing, an invitation declined. The stream carries nothing upstream, so it cannot become a way to push media, and a credential is told only about the things it could already have read. Nothing is stored, and a subscriber that falls behind is disconnected with a reason rather than quietly losing an event. A deployment running more than one instance sets `CONVIA_REDIS_URL` and events are carried between them; one instance needs nothing, and an unreachable Redis narrows the stream rather than failing anything. See [`docs/events.md`](docs/events.md).

People sign in to Convia's own product with a fourth family of credential, `cvs_`, which lives only in a cookie and **carries no authority over a tenant** — a session proves who somebody is, and nothing anywhere turns one into an application key. Passwords are argon2id where every other Convia secret is a plain digest, because the rule is chosen by where the entropy came from. Every sign-in failure answers identically, and an unknown address is hashed against a decoy so the timing does not answer either. Accounts are created by an operator: open registration needs email verification, which needs a mailer Convia does not have. See [`docs/sessions.md`](docs/sessions.md).

Presence is the one advisory thing Convia holds: an application heartbeats for each of a person's devices, Convia aggregates them into one answer and expires it on a clock that is never the caller's. Whether somebody is *in a call* is a different, durable question and is deliberately not a field on it. Presence streams as `presence.changed` and is the one event type Convia refuses to deliver by webhook, because a redelivered presence report arrives after it stopped being true. See [`docs/presence.md`](docs/presence.md).

Webhooks exist for what a client must not miss: an application registers a destination, Convia signs every delivery with a secret shown once, retries on a published schedule, disables a receiver that has stopped answering, and keeps a readable record of every attempt. It refuses to connect to anything that is not the public internet, checked at the socket on every attempt rather than at the hostname once. See [`docs/webhooks.md`](docs/webhooks.md).

[`docs/applications.md`](docs/applications.md) explains the tenancy model and the bootstrap procedure.

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
cp .env.example .env
set -a && . ./.env && set +a
docker compose up -d
go run ./cmd/convia migrate up
go run ./cmd/convia
```

Both API surfaces require a credential, so a fresh instance needs one operator key before it can do anything. Issuing one over the API requires presenting one, so the first is minted against the database:

```sh
go run ./cmd/convia operator issue "bootstrap"
```

The secret is printed once and is not stored. `convia operator list` and `convia operator revoke <id>` manage the rest. An instance with no active operator credential still serves the tenant API and warns at startup that nobody can administer it.

Convia reads configuration from the process environment rather than from a file, so `.env` has to be loaded into the shell as shown above. `.env` is ignored by Git and must never hold a production credential.

Configuration is available through these environment variables:

- `CONVIA_ENVIRONMENT` selects `development` or `production` validation. The default is `development`.
- `CONVIA_HTTP_HOST` sets the HTTP bind host. The default is `0.0.0.0`.
- `CONVIA_HTTP_PORT` sets the HTTP port. The default is `8080`.
- `CONVIA_TRUSTED_PROXIES` names the networks whose `X-Forwarded-For` header Convia believes, as comma-separated CIDR blocks or bare addresses. The default is empty, which trusts nothing. Set it before deploying behind a reverse proxy.
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
- `Security` runs Go vulnerability analysis and CodeQL with extended security queries on pushes, pull requests, a weekly schedule, and manual requests.
- `Container` builds the production image, verifies its non-root user, and smoke tests the health and readiness endpoints against a real PostgreSQL instance.

Workflow actions are pinned to full commit SHAs. Dependabot checks GitHub Actions and Go module updates every week.

The detailed development roadmap and current progress are tracked in [`TODO.md`](TODO.md).

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) describes the development workflow, the local checks, and what a change is expected to contain. [`AGENTS.md`](AGENTS.md) is the authoritative engineering guide. Vulnerabilities must be reported privately, as described in [`SECURITY.md`](SECURITY.md).

## License

Convia is source-available, not open source. It is licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE.md).

- **Free for noncommercial use.** Personal use, hobby projects, private study, experimentation, and use by charities, schools, public research organizations, and government institutions are all permitted. Self-host it, modify it, and share your changes.
- **Commercial rights are reserved.** Using Convia in or for a commercial product or service requires a separate license from the copyright holder. Open a discussion in the repository to request one.

Contributions are accepted under the terms described in [`CONTRIBUTING.md`](CONTRIBUTING.md).
