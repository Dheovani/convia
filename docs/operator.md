# Operating Convia

**This is what somebody running an installation does when something is wrong, and what they may not do.** It is the operator surface as one document: who owns what, which key to hold for which job, and where each kind of incident goes next.

The rationale for each control is elsewhere and linked; this is the map.

## Who owns what

| What | Who decides about it | The operator may |
| --- | --- | --- |
| Tenants, their lifecycle | the operator | create, rename, suspend, restore, delete |
| A tenant's keys | the tenant, and the operator on its behalf | issue, revoke, read without the secret |
| A tenant's users | the tenant | resolve, read, update, suspend, restore, delete |
| Rooms, calls, participants | the tenant | read what Convia decided; open, change, close, reopen or delete a room; end a call; remove somebody |
| What an application wrote — names, metadata, reasons | the tenant | **nothing: it is never shown** |
| Where a tenant's webhooks go | the tenant | read, send a delivery again |
| Operator keys | the operator | issue, revoke |
| The audit trail | nobody | read it, with `audit:read` |
| The database | whoever deploys Convia | everything, and nothing of it is recorded |

The last row is the escalation of last resort, and it is the one place the controls below do not reach. A change made with SQL is not in the [audit trail](audit.md), carries no reason, and was confirmed by nobody.

## Holding the right key

A key carries exactly the scopes it was minted with, and minting cannot exceed the scopes of the key that mints. So a key per job is cheap, and it is the whole of least privilege here — there are no roles to configure. See [`authentication.md`](authentication.md).

| The job | Scopes |
| --- | --- |
| Answering a tenant's support question | `applications:read tenants:read` |
| Responding to an incident in a tenant | `applications:read applications:write tenants:read tenants:write` |
| Investigating who did something | `audit:read` |
| Administering operators | `operators:read operators:write` |

The first key comes from `convia operator issue`, which needs database access. Keep one key with `operators:write` that nobody uses day to day, so that revoking a compromised key never leaves the installation with none — a Convia with no active operator key answers `401` on the whole operator surface.

## Saying why, and saying what

Operations somebody will later ask about require `Convia-Reason`: suspending or deleting a tenant or a user, revoking or minting a key, deleting a room, ending a call, removing somebody, sending a webhook again. The five deletions also require `Convia-Confirm` to repeat the identifier in the path. Both are refused with `400` before anything is done. See [`audit.md`](audit.md#saying-why).

Write the reason for whoever reads the trail in six months: the ticket, the incident, what was observed. "cleanup" answers nothing.

## Incidents, and where they go

| What you are told | First | Then |
| --- | --- | --- |
| A tenant's key leaked | [revoke it](runbooks/credential-revocation.md#revoke-one-credential) | read `GET /v1/audit?actor_id=cred_...` for what it did |
| A tenant is compromised, key unknown | [suspend the tenant](runbooks/credential-revocation.md#suspend-an-application) | revoke and reissue, then restore |
| An operator key leaked | revoke it with another operator key | read `GET /v1/audit?actor_id=oper_...` |
| A call must stop now | `POST /v1/applications/{id}/calls/{call_id}/end` | everybody in it is disconnected |
| Somebody must be out of a call | `POST /v1/applications/{id}/participants/{participant_id}/remove` | their connection is closed |
| A tenant's receiver missed events | `GET /v1/applications/{id}/deliveries` | [send them again](webhooks.md#sending-something-again) |
| Requests are failing or slow | [`runbooks/failing-requests.md`](runbooks/failing-requests.md) | |
| Clients report gaps in what they are told | [`runbooks/subscribers-falling-behind.md`](runbooks/subscribers-falling-behind.md) | |
| Presence shows people who left | [`runbooks/presence-not-lapsing.md`](runbooks/presence-not-lapsing.md) | |
| Data was lost | [`runbooks/restoring-from-a-backup.md`](runbooks/restoring-from-a-backup.md) | |

**What an operator sees is what Convia decided, and never what an application wrote.** A room is its identifier, state and capacity, not its name; a call is who started and ended it and when, not what it was about. That is deliberate and it is enough to act on: the tenant knows what its room is called, and asking them is cheaper than holding their words. See [`threat-model.md`](threat-model.md).

## Escalating

1. **A tenant** reports a problem with its own data or keys. An operator with a support key reads; if something must change, an incident key acts, with a reason.
2. **An operator** who cannot resolve it through the API escalates to **whoever deploys Convia**, who holds database access. Anything they change directly must be written down by hand — what, why, when, who — because the trail will not hold it; [`runbooks/credential-revocation.md`](runbooks/credential-revocation.md) says what to capture for the one direct change the runbooks describe.
3. **A suspected compromise of the installation itself**, rather than of one tenant, starts with revoking every operator key but the spare and reading the trail for what they did.

Named on-call ownership and severity levels are not defined here, because they belong to an organisation running Convia rather than to Convia; `M26-013` is where they are written once there is one.
