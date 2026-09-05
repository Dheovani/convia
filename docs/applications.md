# Applications and Tenancy

An **application** is Convia's unit of isolation. It represents a product that uses Convia: the standalone Convia product itself, or an external system such as an online learning platform integrating through the public API.

This document records the decisions of milestone M05 in [`TODO.md`](../TODO.md). The domain lives in [`internal/applications`](../internal/applications), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

## What an Application Is

Every tenant-scoped resource introduced later — rooms, calls, participants, credentials, webhook endpoints — belongs to exactly one application. Two applications never see each other's data, and no resource may be shared between them.

Applications are deliberately thin. Convia stores what it needs to isolate tenants and nothing more: an identifier, a display name, a lifecycle state, and timestamps. Everything a consuming product knows about its own customers stays in that product.

### Standalone Convia is an application

The standalone Convia product is represented by an ordinary first-party application record rather than by a privileged special case in the code.

This costs one row and buys a guarantee: the standalone user interface exercises the same tenancy, authorization, and rate-limiting paths that external consumers do. A bug in tenant isolation therefore fails Convia's own product first, rather than hiding until an external integration finds it. It also keeps M18's requirement — that the standalone interface uses only public Convia APIs — achievable.

## Identifiers

Public identifiers are opaque, immutable strings: the prefix `app_` followed by 26 random base32 characters, for example `app_MXHJAY4MJNX2FO22XWJ3XNCKHT`.

- **Opaque.** Clients store the identifier as a reference and must never parse it.
- **Random, not sequential.** A sequential key would reveal how many tenants exist and would let anyone guess a neighbour's identifier.
- **Immutable.** External systems record the identifier as a foreign reference, so it never changes for the life of the application, not even through a rename.
- **Prefixed.** The prefix makes an identifier self-describing in logs and support requests, and prevents a room identifier from being accepted where an application identifier belongs.

The format is enforced in three places: when Convia generates one, when the service accepts one, and by a `CHECK` constraint in PostgreSQL, so no code path can store a malformed identifier.

An identifier that is malformed, unknown, or belongs to a deleted application produces the same `404`. Distinguishing them would let anyone probe which identifiers exist.

## Lifecycle

| State | Meaning | Convia serves requests | Data retained |
| --- | --- | --- | --- |
| `active` | Normal operation | Yes | Yes |
| `suspended` | Access withdrawn without data loss | No | Yes |
| `deleted` | Gone from the API, awaiting erasure | No | Until the retention window ends |

Three states are enough for tenancy. Anything richer — trials, plans, billing states — belongs to the product that consumes Convia, not to Convia's isolation model.

**Suspension** is reversible and non-destructive. It exists for abuse response, non-payment, and incident containment: the tenant stops being served, and nothing is lost. A suspended application's existing calls are terminated when the service implements that behavior in M09; new work is refused from the moment of suspension.

**Deletion** is a soft delete. The row is retained, the application disappears from the API, and the data becomes eligible for erasure at the end of the retention window. This is what makes deletion recoverable from an operational mistake and what allows an erasure obligation to be satisfied on a defined schedule rather than instantly and irreversibly.

Cascading deletion to tenant-scoped resources is not implemented, because no tenant-scoped resource exists yet. The retention window is documented alongside the personal-data workflows in M23.

### Transitions

| From | To | Operation | Result |
| --- | --- | --- | --- |
| `active` | `suspended` | `POST /suspend` | Suspended |
| `suspended` | `active` | `POST /activate` | Active |
| `active` or `suspended` | `deleted` | `DELETE` | Deleted, row retained |
| `active` | `active` | `POST /activate` | Unchanged, success |
| `suspended` | `suspended` | `POST /suspend` | Unchanged, success |
| `deleted` | anything | any | `404`, the application has left the API |

**Every transition is repeatable.** Asking for a state an application is already in changes nothing, touches no timestamp, and reports success. A client that times out can retry without first reading the current state, and a repeated `DELETE` succeeds rather than failing.

There is deliberately no error for an "illegal" transition. A deleted application is reported as missing, exactly as an unknown identifier is, and every other combination is either a real change or a no-op. This keeps the public error vocabulary smaller and gives clients less to branch on.

## Concurrent Updates

Every response carrying an application also carries an `ETag`: an opaque version covering the identifier, name, status, and last-updated time. Any change produces a different value.

A client may send that value back as `If-Match` when renaming:

```http
PATCH /v1/applications/app_MXHJAY4MJNX2FO22XWJ3XNCKHT
If-Match: "9f2c1d4b8a3e5f60"
```

The rename then applies only while the application still carries that version. If someone else changed it first, the request is rejected with `412 precondition_failed` and **nothing is modified** — the client re-reads and decides what to do.

The check is enforced inside the update statement itself, not by reading and then hoping, so a change arriving between the read and the write is refused as well.

`If-Match` is **optional** here. A lost rename is visible and easy to repair, so requiring the header on every call would cost more than it protects. `*` and weak validators are treated as no condition, because neither identifies an exact revision. The lifecycle actions do not accept `If-Match`: `suspend`, `activate`, and `delete` express an intended end state, so the last request wins by design.

## Display Metadata and Security

The `name` is display metadata, owned by whoever administers the application.

It carries **no security meaning**. Authentication and authorization attach to credentials, introduced in M07, never to a name. Renaming an application therefore cannot change what it is permitted to do, and two applications may share a name without either gaining access to the other.

Keeping the two separate is what allows a display name to be freely editable while credentials remain tightly controlled, rotated, and revocable.

Names are validated rather than trusted: surrounding whitespace is insignificant and removed, the result must be between 1 and 120 characters, and control characters are rejected because a name is rendered in user interfaces and written to logs.

## Audit

Security-relevant changes to an application emit an audit event as a structured log entry, correlated by request ID:

```json
{"msg":"audit event","event":"application.created","application_id":"app_...","actor":"unauthenticated","request_id":"..."}
```

Two limitations are deliberate and temporary:

- **The actor is a placeholder.** Convia has no authentication yet, so it cannot name who performed an action. Once M07 lands, the actor becomes the authenticated principal.
- **Audit entries are logs, not a queryable trail.** They are an operational record. Durable, searchable, access-controlled audit storage is M21's work.

## The Administrative API

| Operation | Endpoint |
| --- | --- |
| Create | `POST /v1/applications` |
| List | `GET /v1/applications` |
| Retrieve | `GET /v1/applications/{application_id}` |
| Rename | `PATCH /v1/applications/{application_id}` |
| Suspend | `POST /v1/applications/{application_id}/suspend` |
| Activate | `POST /v1/applications/{application_id}/activate` |
| Delete | `DELETE /v1/applications/{application_id}` |

**They have no authentication yet.** Convia gains credentials in M07, which depends on this milestone. Until then, anyone able to reach the port could create and enumerate tenants, so the endpoints are:

- **disabled by default.** They are served only when `CONVIA_ADMIN_API=enabled`;
- **refused in production.** Starting with `CONVIA_ENVIRONMENT=production` and the administrative API enabled is a startup failure, not a warning;
- **announced at startup.** Enabling them logs a warning naming the exposed endpoints.

Enable them on a local instance only, for as long as the work requires.

### Bootstrapping the first application

There is no chicken-and-egg problem to solve: the first application is created through the same endpoint as every other one, during the window in which the administrative API is enabled.

```sh
set -a && . ./.env && set +a
docker compose up -d
go run ./cmd/convia migrate up

CONVIA_ADMIN_API=enabled go run ./cmd/convia &

curl -sS -X POST http://localhost:8080/v1/applications \
  -H 'Content-Type: application/json' \
  -d '{"name":"Convia"}'
```

Record the returned identifier: it is the standalone product's application, and it cannot be recovered by name later without listing applications.

Once M07 introduces credentials, this procedure is replaced by a bootstrap that also issues the first administrative credential, and the `CONVIA_ADMIN_API` gate is removed in favour of authentication and administrative scopes.

## Pagination

`GET /v1/applications` returns applications newest first, using the cursor pagination defined in [`api-conventions.md`](api-conventions.md) and governed by [`api-compatibility.md`](api-compatibility.md).

Paging is keyset based on `(created_at, id)` rather than offset based, so a page stays correct while applications are being created. The `applications_created_at_id_idx` index matches that ordering exactly. Deleted applications are never returned.

An oversized `limit` is rejected rather than silently reduced, so a client never believes it received every result it asked for. A malformed cursor is rejected rather than treated as the first page.

## Not Yet Implemented

- idempotent creation through `Idempotency-Key`, which needs durable key storage and lands with the first resource that requires it in M08;
- cascading deletion to tenant-scoped resources, and the erasure job that acts at the end of the retention window;
- authentication, authorization, and per-application credentials, which are M07;
- cross-tenant isolation of *other* resources, which becomes testable once a tenant-scoped resource exists in M08;
- durable audit storage and administrative search, which are M21.
