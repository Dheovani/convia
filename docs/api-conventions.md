# HTTP API Conventions

This document defines the transport contract shared by every Convia HTTP endpoint. It records the decisions of milestone M02 in [`TODO.md`](../TODO.md) so that later milestones add endpoints instead of re-deciding transport behavior.

The behavior described here is implemented by `internal/api` (the wire contract) and `internal/server` (server construction, middleware, and routing).

## Versioning and route layout

- The public API is served under the `/v1` prefix, exposed as `api.Prefix`. A new prefix is introduced only for a breaking revision of the whole surface; additive changes stay in `/v1`.
- Operational endpoints are served outside the prefix and are not part of the public API. `GET /health` is the only current example. Operational routes are for operators and deployment infrastructure, so public API policies, including future authentication, must be applied to the versioned prefix rather than to the whole server.
- Route registration goes through the route builder in `internal/server/routes.go`, which guarantees that unknown paths and unsupported methods answer with the error schema below instead of the plain-text defaults of `net/http`.

## Media type

Requests and responses that carry a body use `application/json`. Responses always set `Content-Type: application/json`. Endpoints that accept a body reject any other media type with `415 Unsupported Media Type`.

## Success responses

- A single resource is returned directly, without an envelope. Wrapping every resource adds indirection without adding information.
- A collection is returned as an object with a `data` array so that pagination metadata can accompany it. Collections are never returned as a bare JSON array.
- `204 No Content` is used when an operation has no representation to return.

## Error responses

Every failure, including failures produced by middleware and routing, uses one schema:

```json
{
  "error": {
    "code": "not_found",
    "message": "The requested resource does not exist.",
    "request_id": "MXHJAY4MJNX2FO22XWJ3XNCKHT"
  }
}
```

- `code` is stable and machine-readable. Clients branch on `code`, never on `message`.
- `message` is a human-readable sentence for developers. It may change without notice and must never contain internal error text, stack traces, or infrastructure details.
- `request_id` correlates the failure with server logs.

Adding a code is an additive change. Changing the meaning of an existing code is breaking.

| Code                     | Status | Meaning                                                                |
| ------------------------ | ------ | ---------------------------------------------------------------------- |
| `invalid_request`        | 400    | Valid JSON that violates the endpoint contract, such as an unknown field |
| `malformed_json`         | 400    | Body that is not valid JSON, or a value with an unexpected JSON type    |
| `not_found`              | 404    | Unknown route or missing resource                                       |
| `method_not_allowed`     | 405    | Known route addressed with an unsupported method; `Allow` is returned   |
| `conflict`               | 409    | Valid request that the current state of the resource cannot satisfy      |
| `precondition_failed`    | 412    | `If-Match` no longer describes the stored resource; nothing was modified |
| `unsupported_media_type` | 415    | Body sent without an `application/json` content type                    |
| `payload_too_large`      | 413    | Body above the accepted size limit                                      |
| `internal_error`         | 500    | Unexpected server-side condition                                        |

`conflict` differs from `precondition_failed`: a precondition failure means the client acted on a stale copy and should re-read and retry, while a conflict means the request cannot be satisfied in the current state at all, so repeating it unchanged will not help.

## Request correlation

- Every request and response carries an `X-Request-ID` header.
- A client-supplied value is reused when it is non-empty, at most 128 bytes, and limited to letters, digits, `-`, `_`, `.`, and `:`. Any other value is replaced by a generated identifier so that logs and headers cannot be poisoned by untrusted input.
- The identifier is available to handlers through `api.RequestIDFromContext`, is returned in the response header, is included in error bodies, and is attached to the structured access log of the request.

## Request bodies

Endpoints that accept JSON decode through `api.DecodeJSON`, which enforces the same rules everywhere:

- the body is bounded to 1 MiB; an endpoint that needs more must opt in explicitly rather than raising the shared limit;
- unknown fields are rejected instead of ignored, so that a misspelled field never fails silently;
- the body must contain exactly one JSON value;
- an empty body is rejected;
- decoder errors are translated into the public error schema and are never returned verbatim.

## Timeouts and limits

The HTTP server sets a 5-second header timeout, a 15-second read timeout, a 30-second write timeout, a 60-second idle timeout, and a 1 MiB header limit. Long-lived connections, such as the control-plane WebSocket planned in M14, cannot use the server-wide write timeout and will require per-connection deadlines when that milestone starts.

Graceful shutdown drains in-flight requests within 10 seconds, configured in `cmd/convia`.

## Panics

A panic in a handler is recovered, logged with the request ID, method, path, panic value, and stack, and reported to the client as a generic `internal_error`. Stack traces and panic values are never sent to clients. `http.ErrAbortHandler` keeps its documented meaning and is propagated to `net/http`.

## Timestamps

Timestamps are strings in UTC RFC 3339 format with millisecond precision and a `Z` suffix, for example `2026-09-05T14:04:56.154Z`. Timestamp fields end with `_at`. Applications and users return `created_at` and `updated_at` in this format.

## Public identifiers

Public identifiers are opaque, immutable strings and must never expose database internals such as sequential keys. Each resource type uses a short prefix followed by a random component, for example `room_01H9Z2...`. Clients must treat identifiers as opaque and must not parse them. No resource exists yet; the rule applies to the first one introduced.

## Pagination

List endpoints use cursor pagination:

- `limit` sets the page size, defaults to 25, and is capped at 100. A larger value is rejected with `invalid_request`.
- `cursor` carries an opaque continuation token returned by a previous response. Clients must not construct or decode cursors.
- Responses return `{"data": [...], "next_cursor": "..."}`, with `next_cursor` omitted or null on the last page.

Offset pagination is not offered, because it is unstable while items are being created. No list endpoint exists yet; these bounds apply to the first one.

## Reverse proxies

Convia currently trusts no forwarded headers. `X-Forwarded-For`, `X-Forwarded-Proto`, and `Forwarded` are ignored, and the client address in logs is the peer address of the connection. A deployment behind a proxy must terminate TLS at the proxy and must not rely on Convia interpreting forwarded headers. Trusted-proxy configuration will be added when a deployment topology requires it, and it must be explicit rather than enabled by default.

## CORS

No CORS headers are sent. Browser access from another origin is not supported yet. The policy will be decided in M18, when the origin model of the standalone web application is known.

## Endpoint implementation checklist

When adding a public endpoint:

1. Register it under `api.Prefix` through the route builder, choosing the method that matches the semantics of the operation.
2. Keep the handler thin: decode, delegate to a service, encode. Business rules belong outside transport code.
3. Decode request bodies with `api.DecodeJSON` and validate the decoded value explicitly.
4. Return failures with `api.WriteFailure` or `api.WriteError` using an existing code where one fits; introduce a new code only when clients must distinguish the case.
5. Return successes with `api.Write`, following the envelope policy above.
6. Scope every read and write to the authenticated application once tenancy exists, and add a test proving that another tenant is denied.
7. Return timestamps and identifiers in the documented formats.
8. Apply the documented pagination fields and bounds to list endpoints.
9. Add tests for the success path and for every failure path the endpoint can produce.
10. Describe the endpoint in [`api/openapi.yaml`](../api/openapi.yaml) in the same change, reusing the shared parameters, headers, and error responses. The contract test fails when the specification and the implementation disagree.
11. Follow [`api-compatibility.md`](api-compatibility.md) when the change touches an operation that already exists, and update this document when the endpoint introduces a new convention.
