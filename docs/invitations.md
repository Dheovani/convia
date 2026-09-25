# Invitations

An **invitation** is permission for one person to join one call, held by the person it was sent to. It is the last of the three ideas [`participants.md`](participants.md) keeps apart, and the last to be built.

The domain lives in [`internal/invitations`](../internal/invitations), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

## Why it waited

**An invitation is only authorization when the party presenting it is not the party that granted it.**

Before M13 there was no such party. The application backend created an invitation with its own key and later joined somebody with the same key, so Convia would have been keeping a ledger of a decision the application made and could have made without telling Convia. Expiry would have been a note the application could ignore; revocation would have withdrawn nothing.

M13 introduced a client that presents something Convia issued. An invitation now sits on that same footing: it is a credential, held by the invitee, that Convia checks. Expiry and revocation are rules Convia enforces against somebody who cannot simply choose to ignore them.

## It is a credential

An invitation is a third family of Convia key, alongside an application's `cvk_` and an operator's `cvo_`:

- it is a random secret Convia stores **only as a digest**, and cannot show again;
- it is presented as `Authorization: Bearer cvi_...`;
- it is refused on its shape when offered to the wrong surface, before any lookup;
- it carries no scopes, because what it permits — one person, one call, one role — is fixed when it is issued.

The token is returned exactly once, in the response that created it. A lost invitation is not recoverable; issue another.

## The shape of the flow

```
application                         invitee's client
     |                                     |
     |  POST /v1/calls/{id}/invitations    |
     |------------------------------------>|
     |  <- token (once)                    |
     |                                     |
     |  ...sends the token, somehow...     |
     |------------------------------------>|
     |                                     |
     |                                     |  POST /v1/invitation/redeem
     |                                     |  Authorization: Bearer cvi_...
     |                                     |------------------------------> Convia
     |                                     |  <- participation + credential
```

How the token reaches the person is the application's business. Convia issues it and verifies it; it sends no mail and renders no page.

## Guests

An invitation may name nobody. Whoever holds it then takes part as a **guest**: somebody Convia has no user for.

```jsonc
POST /v1/calls/{call_id}/invitations
{ "guest": true, "role": "member" }
```

`guest` is required rather than inferred from a missing `user_id`. A request that simply forgot to name a person must not quietly become a way into the call for anyone holding the link, so naming neither is refused and naming both is refused.

**Convia learns nothing about a guest.** No name, no address, no identity of any kind. A guest participation is identified by the invitation it was redeemed with, and the application — which sent that invitation — is the only party that knows who is behind it. A roster already refuses to carry a display name for known users; a guest is not the place to start collecting one.

That identity does all the work an account would:

- **one invitation is one presence**, so a guest whose connection dropped returns to the seat they had rather than appearing twice;
- **two guests are two people**, because they hold different invitations;
- **capacity counts them**, because capacity is a property of the room and not of how somebody got in;
- **removal is terminal**, because the participation their invitation maps to is terminal — and that participation is exactly what they would present again.

A guest's role travels with their invitation, which is the only way it could: the application does not know the participation identifier until the guest has already arrived, so promoting them afterwards would be a race with their own arrival.

### Why the guest path is unreachable from the tenant API

Seating a guest cannot verify the invitation it is handed. `internal/invitations` depends on `internal/participants`, so depending back would be an import cycle, and `AdmitGuest` therefore trusts its caller completely.

What makes that safe is that the only caller is the one which has already verified the invitation. `AdmitGuest` is deliberately **absent from the interface** the authorization wrapper and both HTTP handlers consume, so there is no route, no scope, and no application-facing operation that reaches it — and `Admission`, which the ordinary join route does accept, has no field that could name an invitation. Two tests assert both, so an application cannot seat a guest by naming an invitation it does not hold, one that was revoked, or one belonging to somebody else.

## What redeeming does

Redeeming produces a participation **and** the credential to connect with, in one response. That is not a convenience: the holder has an invitation and not an API key, so a redemption that produced only a participation would leave them in a call they could not reach.

**None of the rules about who may be in a call are restated here.** Capacity, a suspended user, and a removal that must not be undone are all decided by [`internal/participants`](../internal/participants), which is where they live. In particular, somebody a moderator put out of a call cannot walk back in through a link they were sent earlier, and no code in this package had to know that.

**Redeeming again is expected.** Somebody whose connection dropped opens the same link again and arrives at the participation they already had, with a fresh credential — joining is idempotent by the person. What stops an invitation is time, a revocation, or having declined it.

## States

The state is **derived, never stored**. An expiry that had to be written into a row would need something to write it, and a sweeper that fell behind would leave invitations reading as usable after they had stopped being usable.

| State | Meaning |
| --- | --- |
| `pending` | Waiting to be used |
| `redeemed` | Used at least once, and still works |
| `expired` | Ran out of time |
| `declined` | The invitee said no. Terminal |
| `revoked` | The application withdrew it. Terminal |

The order of precedence is the order of finality. **Revocation outranks everything, including a redemption that already happened**, because the ordinary reason to withdraw a link is that it reached somebody it should not have. Declining outranks expiry, because it says something the clock does not.

Every invitation expires. One without an expiry would be a permanent way into a conversation, and the schema refuses it. The default is a day, the maximum thirty; a longer lifetime is refused rather than quietly clamped, so an application never believes something untrue about links it has already sent.

## Withdrawing versus removing

Revoking an invitation stops it being used. **It does not remove anyone already in the call.** Somebody who joined is a participant, and putting them out is a decision about a participant — authorized against a moderator's role, recorded in the audit trail — rather than about the piece of paper they arrived with. Use the participant endpoints for that.

Declining is the invitee's own act and is deliberately not the same record as the application withdrawing the invitation. Confusing the two would be wrong about who changed their mind. Declining after redeeming is refused, because the person is already in the call.

## Failure answers one thing

Expired, revoked, and declined are reported to the holder as **one** answer: the invitation can no longer be used.

The holder is not the party that issued it. Distinguishing the three would let anyone with a dead link learn whether it was deliberately withdrawn, whether it merely aged out, or whether somebody said no on their behalf. The application sees all of it on its own invitation, where it belongs.

## Scopes

| Scope | Permits |
| --- | --- |
| `invitations:read` | Reading the application's invitations |
| `invitations:write` | Issuing and withdrawing them |

They are separate from `participants:*` on purpose. An invitation is a credential that leaves Convia, so an integration trusted to manage a roster is not thereby allowed to mint ways into a call.

## Privacy and audit

An invitation names a person by their Convia user identifier and nothing else, for the same reason a roster does: the application owns who they are.

Issuing, redeeming, revoking, and declining are audited with the invitation, the call, the application, the person, and the resulting state. **The secret is never in a log**, and neither is anything the application wrote — an invitation carries no free text, so there is nothing in it that could say something about the person it was sent to. A test asserts the secret stays out.

Of the four, only declining is also delivered live. It is the invitee's own decision and nothing else observes it, while issuing and withdrawing are the application's own acts and redeeming already arrives as somebody joining. [`events.md`](events.md) records the reasoning for each.

Suspending an application withdraws every invitation it issued, immediately and without anyone hunting down outstanding links. That is the same guarantee an application key already has, for the same reason.

## What is deliberately not built

- **Delivery.** Convia does not send the invitation anywhere. Mail, links, and pages belong to the application, or to Convia's own client.
- **Rate limits on issuing** (`M13-008`). Every write endpoint is equally exposed to a caller holding a valid key; a general per-tenant limit is the right shape rather than one bolted to this endpoint.
