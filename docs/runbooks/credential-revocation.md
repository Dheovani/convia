# Runbook: Emergency Credential Revocation

**Use this when an application credential is believed to be compromised** — leaked into a repository, a log, a screenshot, or a ticket, or presented by someone who should not hold it.

This runbook satisfies `M07-016` in [`TODO.md`](../../TODO.md). The decisions behind it are in [`authentication.md`](../authentication.md); this document is the procedure, not the rationale.

Every command below has been executed against a running instance. The observed results are stated with each step.

## The One Property That Makes This Work

**Revocation takes effect on the next request.** Convia reads the credential row on every authenticated request and derives the lifecycle state from its timestamps. There is no cache to invalidate, no denylist to propagate, and no restart to perform.

The consequence for an incident: **the moment the write commits, the key is dead.** The only remaining exposure is requests that already passed the authentication middleware, which are bounded by the 30-second write timeout in [`api-conventions.md`](../api-conventions.md).

## Decide the Blast Radius First

| You know | Use | Reversible |
| --- | --- | --- |
| Exactly which credential leaked | [Revoke one credential](#revoke-one-credential) | **No** |
| The application is compromised, not which key | [Suspend the application](#suspend-an-application) | **Yes** |
| Several keys leaked, or the issuing key did | [Revoke many at once](#revoke-many-credentials) | **No** |

**Revocation cannot be undone.** There is no operation that restores a revoked credential, and no supported way to produce one — clearing `revoked_at` by hand would create a state the service never writes. Recovery from an over-broad revocation means issuing new credentials and redeploying every client that held one.

**Suspension can be undone.** That asymmetry is the whole reason to reach for suspension first: it withdraws every key of an application at once, buys time to identify the leak, and costs a single `UPDATE` to reverse.

## Before You Start

Confirm which surface is available to you:

| Surface | Requires | Path |
| --- | --- | --- |
| Operator HTTP API | An operator key carrying `tenants:write` | `DELETE /v1/applications/{id}/credentials/{credential_id}` |
| Tenant HTTP API | A surviving application key carrying `credentials:write` | `DELETE /v1/credentials/{credential_id}` |
| `convia operator` | Database access | for operator credentials themselves |
| Direct SQL | Database access | the fallback, below |

**Prefer the operator API.** It is available in production, it audits what it did, and it needs no database session. Reach for SQL only when you cannot authenticate — no operator key survives, or the leaked key *is* the operator key and you have not yet minted a replacement.

Keep an operator key with `tenants:write` where the on-call can reach it, and confirm before you need it that it works. An incident is the wrong time to discover that the only key you have is the one that leaked.

## Revoke One Credential

Take the credential identifier from the leaked key: it is the middle segment, between `cvk_` and the secret. **The identifier alone is not sensitive**; do not paste the whole key into the incident channel.

```
cvk_2TJII6LYZUSXAHXBDCNAWMHTMV_TRALCBUFFUOBKUWWWEXZNKH2Z4
    └──── this is the credential id, prefixed cred_ ────┘
```

```bash
curl -X DELETE   "https://convia.example/v1/applications/$APP_ID/credentials/cred_2TJII6LYZUSXAHXBDCNAWMHTMV"   -H "Authorization: Bearer $OPERATOR_KEY"
```

**Expected:** `204`. A `404` means the identifier is wrong or belongs to a different application. A `403` means your operator key lacks `tenants:write`.

Revoking an already-revoked credential also answers `204` and changes nothing, so a repeated request is safe and the original revocation time is preserved.

### If you cannot authenticate

Only when no usable key survives:

```sql
UPDATE credentials
   SET revoked_at = now()
 WHERE id = 'cred_2TJII6LYZUSXAHXBDCNAWMHTMV'
   AND revoked_at IS NULL
RETURNING id, name, application_id, revoked_at;
```

`AND revoked_at IS NULL` is not optional. Without it, re-running the statement overwrites the original revocation time and destroys the forensic timeline of when the key actually stopped working. The API applies the same guard for you, which is one more reason to prefer it.

**Expected:** `UPDATE 1` and one returned row. `UPDATE 0` means the credential was already revoked or the identifier is wrong — check which before assuming you are done.

**Verified:** the key answered `200` before the write and `401` immediately after, with no restart. Other credentials of the same application were unaffected.

## Suspend an Application

This withdraws **every** credential the application holds at once, without revoking any of them.

```bash
curl -X POST "https://convia.example/v1/applications/$APP_ID/suspend"   -H "Authorization: Bearer $OPERATOR_KEY"
```

To restore service once the leak is identified and the affected keys are revoked:

```bash
curl -X POST "https://convia.example/v1/applications/$APP_ID/activate"   -H "Authorization: Bearer $OPERATOR_KEY"
```

Both need `applications:write`.

### If you cannot authenticate

```sql
UPDATE applications
   SET status = 'suspended', updated_at = now()
 WHERE id = 'app_5LU4Q3BY73HD2ANSRWRFZILIX5'
   AND status = 'active'
RETURNING id, name, status, updated_at;
```

`updated_at` must be set: the schema enforces `updated_at >= created_at`. Reactivating is the same statement with the two statuses swapped.

**Verified:** all three of the application's keys answered `401` while suspended. After reactivation the two unrevoked keys answered `200` again, and the one that had been revoked stayed `401` — suspension and revocation compose without interfering.

## Revoke Many Credentials

Revoke every credential of one application, **sparing the one you need to recover with**:

```sql
UPDATE credentials
   SET revoked_at = now()
 WHERE application_id = 'app_5LU4Q3BY73HD2ANSRWRFZILIX5'
   AND revoked_at IS NULL
   AND id <> 'cred_4CPYEQFM6SCAKJGDZK5FPLGELV'
RETURNING id, name, revoked_at;
```

Run it as a `SELECT` first. Replacing `UPDATE credentials SET revoked_at = now()` with `SELECT id, name, created_at` over the same `WHERE` clause tells you exactly what the write will hit, and costs seconds.

**Decide the break-glass key before you run this.** If you revoke everything, the application cannot issue a replacement for itself: issuing requires a key carrying `credentials:write`, and you have just revoked it. Recovery then goes through the operator API, which can mint a new key for any tenant — so make sure your operator key still works *before* you sweep.

**Verified:** the swept key answered `401`, the spared key answered `200`, and the previously revoked key kept its original `revoked_at` rather than being restamped.

## After the Write

**Expect a flood of `401`s.** Every client still holding a revoked key will retry. This is the incident resolving, not a second incident.

**Expect `429`s shortly after.** Failed attempts are budgeted per caller address at 60 per minute. A fleet retrying dead keys exhausts that budget within seconds.

> **Recover from a different address than the retrying fleet.** The budget is checked *before* the key is verified, so while an address is out of budget **a valid key from that address is refused too**. Measured under sustained failure load from one address: a valid key was refused on 9 of 10 attempts. The bucket refills at one token per second, so a lull of a second or two lets one request through — do not rely on that under load.
>
> Behind a reverse proxy, this depends on configuration. With `CONVIA_TRUSTED_PROXIES` naming the proxy network, each client is budgeted separately and a retrying fleet cannot lock out anyone else. Without it, every client shares the proxy's address and one fleet exhausts the budget for all of them — including you. Confirm which you are running before you sweep.

**Issue replacements and redeploy.** A revoked key is not reset, it is replaced. Issue a new credential with the same scopes, deploy it, and confirm traffic is using it.

## Revoking an Operator Credential

A leaked **operator** key is the worse incident: it carries authority over every tenant. It is not revoked through the endpoints above, because it belongs to no application.

```bash
convia operator list
convia operator revoke oper_2UTZFUADKBY6HL7ZR4HLKLXQJI
```

Or, holding an operator key with `operators:write`:

```bash
curl -X DELETE   "https://convia.example/v1/operator/credentials/oper_2UTZFUADKBY6HL7ZR4HLKLXQJI"   -H "Authorization: Bearer $OPERATOR_KEY"
```

**Mint the replacement before revoking the last one.** `convia operator issue <name>` needs database access and always works, but an instance with no active operator credential answers `401` on the entire operator surface until one exists. Convia warns about that state at startup; during an incident nobody is reading startup logs.

A key may revoke itself. During an incident the holder of a leaked key is often the only party able to act, and refusing would protect nothing.

**Verified:** the revoked operator key answered `200` before and `401` immediately after, while another operator key was unaffected.

## Record What You Did

**The API audits; SQL does not.** Revoking through the API writes a `credential.revoked` or `operator_credential.revoked` entry naming the credential and the actor. A direct `UPDATE` bypasses that entirely — verified during the drill, where three SQL revocations and a full suspend/reactivate cycle produced zero audit entries.

If you had to use SQL, paste the `RETURNING` output into the incident record. It is then the only trace that the revocation happened, and it names each credential by its public identifier — never its secret or digest.

The incident record should carry:

- which credentials were revoked, by identifier, and at what time;
- whether the application was suspended, and when it was reactivated;
- how the key leaked, and where it was exposed;
- which clients were redeployed with replacements.

## Known Gaps

Each of these makes this runbook harder than it should be. They are recorded here so the procedure is honest about what it is working around.

- **No audit trail for SQL revocation.** The fallback path is the one Convia does not record. Using the API avoids this entirely, which is why it is the first choice above.
- **No bulk revocation endpoint.** Revoking many of one tenant's keys means one `DELETE` per credential, so a sweep is still faster in SQL. That is the remaining reason the SQL is documented rather than deleted.
- **No `last_used_at`.** After a leak you cannot tell whether the key was actually used, from where, or when it last worked. Recorded in [`authentication.md`](../authentication.md) as needing a design rather than a column.
