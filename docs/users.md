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

## Resolving an Identity

```http
POST /v1/applications/{application_id}/users
{"external_subject": "customer-42", "display_name": "Ada Lovelace"}
```

The operation is **create-or-resolve**, and it is idempotent by the external subject rather than by a client-supplied idempotency key. An application can call it before every session without tracking whether it has called it before. The response is `201` when the mapping was created and `200` when it already existed.

**An existing user is returned unchanged.** A routine resolve never overwrites a display name or metadata, so a correction made by an operator survives the next login. Changing stored attributes is an explicit update.

Concurrency is handled in the database, not by checking first: the insert and the lookup are a single statement, and the unique index decides which of several simultaneous callers creates the user while the others read the row it wrote. A test drives twelve concurrent callers and asserts that exactly one reports a creation and all twelve receive the same identifier.

## Isolation

Every read and write is scoped to the owning application. The store deliberately offers **no method that finds a user by identifier alone**, so a query cannot cross a tenant boundary by omission — the application is part of the lookup, not a filter someone might forget to apply.

A user identifier that is unknown, malformed, deleted, or **belongs to another application** produces the same `404`. Distinguishing them would let a caller confirm that an identifier exists somewhere else in Convia.

Requests for an application Convia does not serve are refused before the users table is touched at all.

## Lifecycle and Erasure

Users carry the same three states as applications: `active`, `suspended`, `deleted`. Suspension withdraws access while retaining data; deletion removes the user from the API and retains the row for the erasure window.

The `(application_id, external_subject)` mapping is unique **regardless of status**, including while a user is deleted. A deleted subject therefore cannot be silently resurrected by resolving it again, and cannot point at two Convia users. Erasure at the end of the retention window frees the subject.

Data export and erasure boundaries follow from ownership:

- **Convia can erase** everything in this table: the identifier, the mapping, the display name, and the metadata.
- **Convia cannot erase** the application's own copy of that person, which lives in the application's database. An erasure request that reaches Convia satisfies Convia's part only, and the application remains the controller for its own records.
- **An export** of a Convia user contains exactly the columns above. Convia holds no additional profile, contact detail, or history beyond the rooms and calls the person took part in, which are exported with those resources.

Audit records deliberately omit the external subject and display name. They identify the change by Convia's own identifiers, so the audit trail stays useful without accumulating personal data in the logs.

## The Standalone Product

Convia's own product is an application like any other ([`applications.md`](applications.md)), so its accounts map through exactly this model: the standalone product holds the account, and its user identifiers become external subjects of the first-party application.

This keeps one code path for participation. When the standalone interface is built in M18, it authenticates its own users, resolves them here, and joins calls through the same endpoints an external integration uses.

## Not Yet Implemented

- updating a display name or metadata, and the lifecycle transitions, which follow immediately;
- authentication, which will let an application address its own users without naming itself in the path;
- the erasure job that acts at the end of the retention window;
- suspension enforcement during calls, which needs the call domain in M09.
