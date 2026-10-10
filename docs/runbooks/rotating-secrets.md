# Runbook: Rotating secrets and signing keys

**Use this to replace a secret on a schedule, or because it may have been exposed.** Every secret Convia holds or checks is listed below with how to replace it and what the replacement costs.

This is `M23-006`. For a key believed compromised *right now*, start with [`credential-revocation.md`](credential-revocation.md): revoking first and rotating after is the order when somebody else may be using it.

## What there is to rotate

| Secret | Who holds it | Rotating it costs | Procedure |
| --- | --- | --- | --- |
| Operator key (`cvo_`) | an operator | nothing, with an overlap | [below](#an-operator-key) |
| Application key (`cvk_`) | a tenant's servers | nothing, with an overlap | [below](#an-application-key) |
| Webhook signing secret | Convia and a tenant's receiver | nothing, if the receiver is taught first | [below](#a-webhook-signing-secret) |
| LiveKit API key and secret | Convia and LiveKit | nothing, with the overlap Convia supports | [below](#the-livekit-api-key-and-secret) |
| Redis password | Convia and Redis | nothing, with Redis's own overlap | [below](#the-redis-password) |
| PostgreSQL password | Convia and PostgreSQL | **a short window of refused connections** | [below](#the-postgresql-password) |
| A person's session (`cvs_`) | their device | they sign in again | [below](#sessions) |
| A person's identity key | sealed by their password | **cannot be rotated** | [below](#identity-keys) |

TLS certificates belong to whatever terminates HTTPS in front of Convia, and are rotated there.

## An operator key

Every operator key expires within 90 days, and Convia warns at startup two weeks before the last working one runs out. Rotate before that:

1. Issue the replacement with the same scopes, over the API with a key holding `operators:write`, or with `convia operator issue <name> [scope...]`.
2. Move whatever uses the old key to the new one.
3. Revoke the old one, saying why: `DELETE /v1/operator/credentials/{id}` with `Convia-Reason` and `Convia-Confirm`.

Keep a spare with `operators:write` that nobody uses day to day. Revoking the last working key leaves the operator surface answering `401` until `convia operator issue` mints another, which needs the database.

## An application key

The same overlap, done by the tenant with its own `credentials:write` key or by an operator on its behalf. [`authentication.md`](../authentication.md#lifecycle) has the three steps; the point of them is that both keys work at once, so a fleet is never between two keys.

## A webhook signing secret

`POST /v1/webhooks/{endpoint_id}/rotate` answers with the new secret once, and **the old one stops at that moment**: a rotation exists because a secret may be out, and a grace period would be a grace period for whoever has it. So the receiver is taught first:

1. Change the receiver to accept a signature made with either secret.
2. Rotate, and give the receiver the new one.
3. Remove the old secret from the receiver.

A delivery in flight during step 2 may arrive signed with either. See [`webhooks.md`](../webhooks.md#rotating-a-secret).

## The LiveKit API key and secret

**One pair signs two things in opposite directions.** Convia signs the credentials it hands to clients and its calls to LiveKit's API with it; LiveKit signs the reports it sends Convia about who connected and who left with it. Swapping it in one place before the other would refuse one of the two, and a refused report is a person Convia keeps listing in a call they left.

So Convia accepts reports signed with the pair being retired, for as long as it is told to, and never signs anything with it:

1. **Teach LiveKit the new pair beside the old one**, so it accepts both. LiveKit holds a map of keys, and the new one is a second entry in it:

   ```yaml
   keys:
     oldkey: old-secret
     newkey: new-secret
   ```

   Restart LiveKit. Nothing has changed for Convia yet.

2. **Move Convia to the new pair, and name the old one as previous:**

   ```
   CONVIA_LIVEKIT_API_KEY=newkey
   CONVIA_LIVEKIT_API_SECRET=new-secret
   CONVIA_LIVEKIT_PREVIOUS_API_KEY=oldkey
   CONVIA_LIVEKIT_PREVIOUS_API_SECRET=old-secret
   ```

   Restart Convia. Credentials are minted with the new pair from now on, and reports are believed whichever of the two signed them. Convia refuses to start with half of a previous pair, or with a previous key equal to the current one.

3. **Move LiveKit's reports to the new key**: `webhook.api_key: newkey`, and restart LiveKit.

4. **Wait at least five minutes**, which is how long a credential Convia minted before step 2 can still be used to open a connection. Connections already open are unaffected either way.

5. **Remove the old pair from both**: its entry in LiveKit's keys, and the two `PREVIOUS` variables. Restart both.

In production the secret must be at least 32 characters, and that is checked for the new one; the previous one is not re-checked, because it is on its way out.

## The Redis password

Redis lets one user hold two passwords at once, which is the whole procedure:

1. `ACL SETUSER convia >new-password` adds the new password beside the old one.
2. Put the new password in `CONVIA_REDIS_URL` and restart Convia.
3. `ACL SETUSER convia <old-password` removes the old one.

A Convia that cannot reach Redis does not fail requests: presence narrows back to one instance and the failure is reported loudly, so even a mistake here is not an outage. See [`events.md`](../events.md).

## The PostgreSQL password

**This is the one rotation with a gap.** PostgreSQL holds one password per role, and Convia opens new connections as its pool needs them, so between changing the password and restarting Convia with the new one, any new connection is refused. Connections already open keep working.

1. Put the new `CONVIA_DATABASE_URL` where the deployment keeps it, without restarting.
2. `ALTER ROLE convia PASSWORD 'new-password';`
3. Restart Convia at once.

Do it when traffic is low, and do steps 2 and 3 together. Rotating without the gap needs two login roles that can both act on Convia's tables, and Convia's migrations create tables owned by whichever role ran them, so that arrangement is not set up today. It belongs with how an installation is deployed, `M26`.

## Sessions

A session token is 130 random bits, stored only as a digest, so a copy of the database does not hold anything that signs a person in. There is nothing to rotate as a fleet. A person who thinks a device was taken signs out everywhere (`DELETE /v1/sessions`), which also happens when they change their password; an operator stops somebody signing in with `convia account suspend <account-id>`.

There is no command that ends every session on an installation at once.

## Identity keys

**A person's identity key cannot be rotated, by design.** An account is named by the fingerprint of its key, and other installations know the person by it — so a new key is a new account, not a new key for the old one. Changing a password seals the same key under the new one. Why, and what that costs, is [ADR 0011](../adr/0011-an-account-is-local-and-its-identifier-is-its-key.md).
