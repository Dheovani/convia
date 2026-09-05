# Database

PostgreSQL is Convia's source of truth for durable data. This document records the decisions of milestone M04 in [`TODO.md`](../TODO.md) and the operational rules that follow from them.

## Decisions

**Driver: `github.com/jackc/pgx/v5`, used directly through `pgxpool`.** pgx is the actively maintained PostgreSQL driver for Go, and using it directly rather than through `database/sql` keeps access to PostgreSQL types, native prepared statements, and pool statistics that `database/sql` hides. Migrations are the one exception: they run through `database/sql` with pgx's `stdlib` adapter, because the migration tool expects that interface.

**Migrations: `github.com/pressly/goose/v3`, embedded in the binary.** goose is reversible, runs plain SQL files with no code generation, and can be driven as a library, which lets the same code path serve the CLI and the integration tests. Migration files are embedded with `go:embed`, so the binary carries the exact schema it was built against and the container image needs no extra files.

**Migrations never run implicitly at startup.** A process that migrates on boot makes a rolling deployment race with itself and makes schema changes invisible in a deployment log. An operator or a deployment step runs `convia migrate up` explicitly.

## Configuration

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CONVIA_DATABASE_URL` | Yes | none | PostgreSQL connection URL |
| `CONVIA_DATABASE_MAX_CONNECTIONS` | No | `10` | Maximum pooled connections, 1 to 500 |
| `CONVIA_DATABASE_CONNECT_TIMEOUT` | No | `5s` | Bound on establishing a connection |
| `CONVIA_DATABASE_QUERY_TIMEOUT` | No | `5s` | Default bound for a single query |
| `CONVIA_ENVIRONMENT` | No | `development` | `development` or `production` |

The URL is validated at startup: it must use the `postgres` or `postgresql` scheme and name a host and a database. **In production it must also set `sslmode` to `require`, `verify-ca`, or `verify-full`**, because PostgreSQL's default negotiation silently accepts a plaintext connection. Development accepts `sslmode=disable` so that a local container needs no certificates.

The connection URL carries the password, so it is never logged and never included in an error message. Startup logs the host, port, database name, and pool size only.

Connection failure at startup is fatal. A process that starts without a usable database would accept traffic it cannot serve.

## Local Development

```sh
docker compose up -d
export CONVIA_DATABASE_URL="postgres://convia:convia@127.0.0.1:5432/convia?sslmode=disable"
go run ./cmd/convia migrate up
go run ./cmd/convia
```

The credentials in `docker-compose.yml` are development-only.

Migration commands:

```sh
go run ./cmd/convia migrate up      # apply every pending migration
go run ./cmd/convia migrate down    # revert the most recent migration
go run ./cmd/convia migrate status  # report applied and pending migrations
```

## Migration Conventions

Migrations live in [`internal/database/migrations`](../internal/database/migrations).

- The filename is `NNNNN_snake_case_description.sql`, numbered sequentially from `00001`, with no gaps. If two branches claim the same number, renumber before merging.
- Every file has a `-- +goose Up` section and a `-- +goose Down` section. A migration without a working `Down` is not accepted unless the roadmap records why recovery is forward-only.
- **Comments in SQL files use `--`.** goose splits statements on semicolons and mis-parses a `/* */` block that contains one, so block comments are not safe here even though Go code in this repository prefers them.
- **An applied migration is never edited.** Once a migration has run anywhere beyond a developer's machine, correcting it means adding a new migration.
- One migration makes one coherent schema change. Data backfills that can take minutes belong in their own migration so a failure is easy to locate.
- Statements that PostgreSQL cannot run inside a transaction, such as `CREATE INDEX CONCURRENTLY`, need goose's `-- +goose NO TRANSACTION` annotation and must be reviewed for partial-failure behavior.

### Reversibility

`convia migrate down` reverts the most recent migration. It exists to recover a failed deployment while the previous schema is still correct.

A rollback cannot restore data that a migration destroyed. Dropping a column is irreversible in practice, so a change that removes data is split: stop writing the column, deploy, and only remove it in a later release once the previous version is no longer running.

## Query Timeouts and Cancellation

Every query takes a `context.Context`. Request-scoped work inherits the request's context, so a client that disconnects stops the work it caused. `CONVIA_DATABASE_QUERY_TIMEOUT` bounds queries that have no tighter deadline of their own.

Cancellation reaches PostgreSQL: pgx cancels the in-flight query rather than only abandoning the Go call, which is what keeps a stalled database from exhausting the pool. This behavior is covered by an integration test.

## Health and Readiness

`GET /health` reports process liveness and never touches the database, so a database outage does not make an orchestrator restart healthy processes.

`GET /ready` checks the database with a two-second bound and answers `503` when it is unreachable, which removes the instance from a load balancer while the process keeps running. The response names the failing dependency but never explains the failure; the reason goes to the logs, correlated by request ID.

## Testing

Database tests live in `internal/database` and are skipped unless `CONVIA_TEST_DATABASE_URL` is set, so `go test ./...` works without infrastructure. CI always sets it.

```sh
docker compose up -d
CONVIA_TEST_DATABASE_URL="postgres://convia:convia@127.0.0.1:5432/convia?sslmode=disable" go test ./...
```

Each test creates its own database named `convia_test_<random>`, runs migrations into it, and drops it afterwards. Tests therefore never share state, never depend on execution order, and never touch a developer's own data. The URL must belong to a role that may create databases.

CI additionally applies, reverts, and reapplies every migration against an empty database, so a migration that only works against an already-migrated schema fails the build.

## Schema

### `applications`

An application is a tenant of Convia: the standalone product itself, or an external product integrating through the public API. Every tenant-scoped resource added later references it.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `TEXT` | Primary key. Opaque public identifier, `app_` followed by 26 base32 characters |
| `name` | `TEXT` | Display name, 1 to 120 characters |
| `status` | `TEXT` | `active`, `suspended`, or `deleted` |
| `created_at` | `TIMESTAMPTZ` | |
| `updated_at` | `TIMESTAMPTZ` | Never earlier than `created_at` |

The invariants are enforced by `CHECK` constraints rather than by application code alone, so a bug in a future service cannot write a row that violates them. Identifiers are random rather than sequential so that no public identifier reveals how many tenants exist or allows guessing another tenant's ID.

The domain rules that govern transitions between these states are implemented in M05, together with the repository and the administrative endpoints.

## Backup and Recovery

These expectations must be met before Convia serves production traffic, and are not yet implemented:

- automated daily base backups with continuous WAL archiving, giving point-in-time recovery;
- backups encrypted at rest and stored outside the database host's failure domain;
- a documented retention window, with restore tested against the oldest retained backup;
- a restore drill performed and timed before launch, and repeated after any change to the backup topology, since an untested backup is not a backup;
- migrations paired with a recovery plan, because restoring a backup taken before a migration also reverts the schema.

M26 owns the operational implementation of these points.
