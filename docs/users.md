# External Users and Identity Mapping

A **user** is one application's view of one of its people. Applications keep their own accounts, passwords, and profiles; Convia only needs a stable handle so that it can attribute rooms, calls, and participation to someone.

This document records the decisions of milestone M06 in [`TODO.md`](../TODO.md). The domain lives in [`internal/users`](../internal/users), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

## The Model

Convia stores two identifiers for each person:

- the **Convia user identifier**, opaque and assigned by Convia (`usr_` followed by 26 base32 characters);
- the **external subject**, the identifier the application already uses, stored opaquely and never parsed.

The external subject is scoped to the application. `(application_id, external_subject)` is unique, so one subject resolves to exactly one Convia user forever, which is what makes resolving idempotent.

Applications should pass a **stable internal identifier** rather than an email address. Convia stores whatever it receives, and an email is personal data Convia does not need in order to work. This is guidance, not enforcement: Convia cannot tell one opaque string from another.

## No Cross-Application Linking

**Convia never links users across applications.** The same human known to two applications becomes two unrelated Convia users, with different identifiers and no record connecting them.

This is a deliberate privacy decision with real consequences:

- Convia cannot become a cross-product identity graph, even accidentally, because the data to build one is never collected.
- One application cannot discover that a person also uses another application, so no application learns about another's customers.
- A subpoena, breach, or curious operator finds no linkage to expose, because none exists.

The cost is that a genuine single-sign-on across products cannot be built on Convia's user model. That belongs to an identity provider the applications share, above Convia, where the people involved can consent to it. If Convia ever needs linking, it must be an explicit, consented, separately audited feature — never a silent change to this table.

## Ownership of Attributes

| Attribute | Owner | Notes |
| --- | --- | --- |
| `id` | Convia | Opaque, immutable, assigned at creation |
| `application_id` | Convia | The owning tenant, never changes |
| `status` | Convia | Lifecycle state |
| `created_at`, `updated_at` | Convia | UTC, microsecond precision |
| `external_subject` | Application | Opaque to Convia, bounded only |
| `display_name` | Application | Optional; shown in future participant lists |
| `metadata` | Application | Stored verbatim, never interpreted |

**Convia is authoritative only for the first four.** Everything else is a copy of what the application told it, kept so that Convia can render a participant roster without calling back into the application during a call. If an application and Convia disagree about a display name, the application is right and should say so by updating the user.

There is no dedicated avatar field. An application that wants one stores its URL in `metadata`, which keeps Convia from acquiring an opinion about image hosting, sizes, or fetching. Convia never retrieves anything from a metadata value.

## Metadata Limits

Metadata is a flat map of strings, bounded in every dimension a caller could grow:

| Limit | Value |
| --- | --- |
| Entries | 16 |
| Key length | 40 characters |
| Key shape | lowercase letters, digits, and underscores, starting with a letter |
| Value length | 256 characters |
| Total size | 4096 bytes |

Nested objects and arrays are rejected at decoding, so the stored shape stays predictable and Convia never becomes general-purpose storage for an application. Keys are restricted to identifier shape so that they remain usable as log fields or export columns if metadata is ever indexed.

## Two Surfaces

An application reaches its own users under **`/v1/users`**, authenticated with its API key. The tenant comes from the key, so no application appears in the path and none can be named — see [`authentication.md`](authentication.md).

The routes nested under `/v1/applications/{application_id}/users` are the operator-facing equivalents. They behave identically and remain behind the `CONVIA_ADMIN_API` gate. The examples below use the nested form because it shows the tenant explicitly; every one of them has an authenticated counterpart without the prefix.

## Resolving an Identity

```http
POST /v1/users
Authorization: Bearer cvk_...
{"external_subject": "customer-42", "display_name": "Ada Lovelace"}
```

The operation is **create-or-resolve**, and it is idempotent by the external subject rather than by a client-supplied idempotency key. An application can call it before every session without tracking whether it has called it before. The response is `201` when the mapping was created and `200` when it already existed.

**An existing user is returned unchanged.** A routine resolve never overwrites a display name or metadata, so a correction made by an operator survives the next login. Changing stored attributes is an explicit update.

Concurrency is handled in the database, not by checking first: the insert and the lookup are a single statement, and the unique index decides which of several simultaneous callers creates the user.

A caller that loses that race does not necessarily see the winner's row in the same statement. PostgreSQL evaluates the whole statement against the snapshot it took before the insert began waiting for the winner to commit, so a row committed during that wait is invisible to the lookup and the statement resolves nothing at all. That is a lost race and not a missing user, so the statement is repeated against a new snapshot, which does include the winner's row.

Two tests cover this. One drives twelve concurrent callers and asserts that exactly one reports a creation and all twelve receive the same identifier. The other holds a user uncommitted until a resolve is demonstrably blocked on it, which reproduces the lost race on every run rather than by luck.

## Isolation

Every read and write is scoped to the owning application. The store deliberately offers **no method that finds a user by identifier alone**, so a query cannot cross a tenant boundary by omission — the application is part of the lookup, not a filter someone might forget to apply.

A user identifier that is unknown, malformed, deleted, or **belongs to another application** produces the same `404`. Distinguishing them would let a caller confirm that an identifier exists somewhere else in Convia.

Requests for an application Convia does not serve are refused before the users table is touched at all.

## Updating Attributes

```http
PATCH /v1/applications/{application_id}/users/{user_id}
{"display_name": "Ada King"}
```

Changing what the application owns is an **explicit** operation, separate from resolving, so a routine login can never overwrite a correction.

The update is **partial**: only the fields present in the body change, and an omitted field keeps its stored value. An explicitly empty value is a change rather than an omission — `"display_name": ""` clears the name, and `"metadata": {}` clears the metadata. A body that changes nothing is rejected rather than costing a write and an audit record.

**Metadata is replaced, not merged.** The object sent becomes the stored object, which is how a key is removed. Merging would make removal impossible without a second convention, and would leave the stored shape dependent on the order of requests.

Send the `ETag` from a previous read as `If-Match` to make the change conditional. The version is checked before the write and repeated inside the `UPDATE` itself, so a change arriving in between is refused with `412` too. The header is optional: without it, the last write wins.

## Lifecycle and Erasure

Users carry the same three states as applications: `active`, `suspended`, `deleted`. Suspension withdraws access while retaining data; deletion removes the user from the API and retains the row for the erasure window. Both transitions are idempotent — repeating one changes nothing, reports success, and records no second audit event.

```http
POST /v1/applications/{application_id}/users/{user_id}/suspend
POST /v1/applications/{application_id}/users/{user_id}/activate
DELETE /v1/applications/{application_id}/users/{user_id}
```

**Suspension keeps the mapping.** Resolving a suspended subject returns that user with its status, so the application can see the state it set rather than accidentally creating a second user for the same person.

**Deletion is terminal until erasure.** A deleted user cannot be updated or activated, and is reported as missing by every read.

The `(application_id, external_subject)` mapping is unique **regardless of status**, including while a user is deleted. So a deleted subject stays reserved: resolving it again is refused with `409 conflict` rather than reviving the user or creating a second one. Reviving would let a routine login undo a deletion the application asked for, and returning the deleted user would contradict every other endpoint, which reports it as missing. Erasure at the end of the retention window frees the subject.

The reservation is scoped to the application, as everything else is: deleting a subject in one application leaves the same subject in another untouched.

Data export and erasure boundaries follow from ownership:

- **Convia can erase** everything in this table: the identifier, the mapping, the display name, and the metadata.
- **Convia cannot erase** the application's own copy of that person, which lives in the application's database. An erasure request that reaches Convia satisfies Convia's part only, and the application remains the controller for its own records.
- **An export** of a Convia user contains exactly the columns above. Convia holds no additional profile, contact detail, or history beyond the rooms and calls the person took part in, which are exported with those resources.

Audit records deliberately omit the external subject and display name. They identify the change by Convia's own identifiers, so the audit trail stays useful without accumulating personal data in the logs.

## The Standalone Product

Convia's own product is an application like any other ([`applications.md`](applications.md)), so its accounts map through exactly this model: the standalone product holds the account, and its user identifiers become external subjects of the first-party application.

This keeps one code path for participation. When the standalone interface is built in M18, it authenticates its own users, resolves them here, and joins calls through the same endpoints an external integration uses.

## Not Yet Implemented

- the erasure job that acts at the end of the retention window, which is what eventually frees a deleted subject;
- suspension enforcement during calls, which needs the call domain in M09. Suspension currently records the state and withdraws nothing, because there is no call to withdraw from yet.
