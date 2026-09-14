# Participants

A **participant** is one person's presence in one call. The person is usually one of the application's Convia users; a guest, who has none, is identified by the invitation they redeemed instead. This document records the decisions of milestone M10 in [`TODO.md`](../TODO.md). The domain lives in [`internal/participants`](../internal/participants), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

> **No media provider appears anywhere here.** Whether someone may join is a Convia decision. Handing them a media token is a later and separate one, which is what "join authorization independent of media token issuance" means.

## Three Ideas, Kept Apart

M10 begins by distinguishing three things that are easy to confuse.

| | What it would mean | Where it stands |
| --- | --- | --- |
| **Room membership** | Who belongs to a place over time | **Not modelled.** Convia holds no credentials for an application's people, so it could not enforce a policy about who belongs; the application already knows. Decided in [`rooms.md`](rooms.md) and unchanged. |
| **Invitation** | Permission to join that has not been used yet | **Built**, once M13 made it enforceable. See [`invitations.md`](invitations.md). |
| **Participation** | Who actually joined, in what role, and how they left | **This document.** |

### Why invitations waited for M13

An invitation is only authorization when the party presenting it is not the party that granted it.

Before M13 the application backend created an invitation with its own key and later joined somebody with the same key. Convia would have been checking the application's homework against itself — a ledger of a decision the application made and could have made without telling Convia. That is the same reasoning that kept room membership out of M08.

M13 changed the shape: a **join session** is presented by a client, not by the backend that issued it. An invitation now sits on that footing, so expiry and revocation are rules Convia enforces rather than notes the application may ignore. See [`invitations.md`](invitations.md).

Guest participation still waits, and for a different reason than it used to: it changes this table's central invariant — every participant names a Convia user — and that deserves its own migration rather than being bolted onto invitations.

## Lifecycle

```
        join                     leave
   ───────────▶ joined ──────────────────▶ left
                   │
                   │  remove
                   └──────────────────────▶ removed ──╳── cannot rejoin
```

Three states, and all three are reachable.

**`removed` is not a reason attached to `left`.** It is terminal with a policy of its own: **someone a moderator put out cannot rejoin that call**. If they could, removing them would mean nothing. That is the whole justification for a third state, and it is why the distinction is not folded into a `reason` field the way a call's ending is.

A removal is bounded to the conversation it happened in. It is not a ban on the application's service, so the same person may join a different call.

**Leaving is repeatable.** Leaving twice succeeds and changes nothing, so a client retrying after a timeout is never punished, and someone who was removed is never quietly converted into someone who left.

## Joining Is Idempotent by the Person

`POST /v1/calls/{call_id}/participants` answers `201` when it admitted someone and `200` when they were already there.

This is the reconnection story, and it needs no idempotency key. A client whose network dropped and came back **is the same person**; a roster showing them twice would be wrong in a way users notice immediately. The rule is enforced by a partial unique index rather than by a check:

```sql
CREATE UNIQUE INDEX participants_call_user_present_key
    ON participants (call_id, user_id) WHERE status = 'joined';
```

Departed rows do not participate in the index, so a person may leave and come back later and the call keeps **both stints** in its history.

## Capacity

`max_participants` is the room's, because the room is where an application decided how many people belong in a place. The call is where it is counted. This is what [`rooms.md`](rooms.md) said would happen once there were participants to count.

**Capacity is enforced transactionally.** Joining takes a row lock on the call, then counts, then inserts. Counting and inserting without the lock would let two joins both see the last seat free and both take it, and no amount of application-level checking closes that window. A test drives ten simultaneous arrivals at a three-seat room and asserts exactly three get in.

The call's own state is read under the same lock, which is also what stops a conversation ending half way through someone joining it. A participant only ever exists inside a call, so the call is the consistency boundary the transaction is drawn around.

**Capacity bounds the present, not the history.** Only people still in the call occupy it, which is what lets a small room host a long conversation people come and go from.

## Roles

Two roles: **`moderator`** and **`member`**. A moderator may remove participants and change roles; a member may not.

**Roles about media are deliberately absent.** Publishing, subscribing, sharing a screen — these describe capabilities of a media plane that does not exist yet, so Convia could not enforce them, and a role that promises what nothing enforces is worse than no role at all. They arrive when the thing that enforces them does.

**An omitted role is `member`.** Defaulting to the lesser of the two is the only safe direction: a mistyped field must never hand someone the authority to remove other people. Changing a role is different — the role must be stated, because an omitted field there would silently demote someone.

### What Convia actually enforces

**Convia does not decide whether the application may remove someone.** It already may, on its own authority, over its own calls. Naming an acting participant in `by` is what invokes the check:

> What Convia decides is whether the participant the application named was entitled to — because Convia is what holds the roster.

That is a real rule, not a rubber stamp. The application asserts *who is acting*, exactly as it asserts every other identity; Convia enforces a rule about state only it holds. Three things are checked, and each can be wrong on its own: the acting participant must be one of this application's, must be **in the same call** as the person being acted on, and must be **present** and a moderator. A moderator who left has no authority, and a moderator from another call cannot lend theirs.

A participant acting beyond their role answers `403 forbidden`, deliberately with a different message from a refused credential: presenting a different key would not help, and neither would being granted a scope. Being made a moderator would.

## What an Operator May Do

An operator may **read a roster and remove someone from it, and nothing else**.

That is the same line the call domain draws, for the same reason. Removing is administration: it is the lever an operator needs when someone must be put out of a conversation and the application cannot do it. Admitting someone, promoting them, or recording that they left is not — it would put Convia in the position of arranging a conversation nobody asked it to arrange.

An operator cannot use the `by` field. An operator acts from outside the conversation, so there is no moderator to check or to credit, and accepting the field would let a caller believe a check ran that did not. The request is refused rather than silently ignored.

The absences are asserted by a test, because the natural instinct of anyone extending this surface will be to fill them in.

## Isolation and Privacy

Every operation is scoped to one application. Reaching another application's participant answers `404`, not `403` — a `403` would confirm it exists. A roster for a call that is not the caller's is **missing**, not empty.

A person named on a join must be one of the caller's own users, and must be active: a suspended person cannot be let into a conversation, or the suspension would be decorative. Another tenant's user is as unknown as one that never existed.

**A roster names people only by their Convia user identifier.** No display name appears in it. A roster is read by more callers and stored in more places than a user record is; copying a name into every entry would put it where nobody asked for it, and would duplicate something the application owns and can change — a corrected name would then be right in one place and stale in the other. A caller that wants a name reads the user, where it lives and stays current. A contract test asserts the published schema carries only identifiers and states.

Scopes are `participants:read` and `participants:write` on the tenant surface, and `tenants:read` / `tenants:write` on the operator surface. **`calls:write` does not imply `participants:write`**: a key granted to start and end conversations was not granted to decide who is in them.

`participants:read` is also what governs roster events on the live stream, so a key that cannot read a roster is not told about one changing either. See [`events.md`](events.md).

## Listing

`GET /v1/calls/{call_id}/participants` returns a call's roster newest first, using the cursor pagination defined in [`api-conventions.md`](api-conventions.md).

**Everyone is returned, including those who left**, because a roster is also a record of who was there. `status=joined` answers who is present now.

## Joining the Conversation

Everything above records **who may take part**. `POST /v1/participants/{participant_id}/session` is what makes a call audible: it returns an address and a short-lived credential for one person in one call.

The application asks on its client's behalf and passes the answer on. The client never holds the application's API key and never speaks to Convia's media infrastructure, which is what lets that infrastructure be replaced without any external consumer noticing.

**Convia decides afresh on every request.** Someone who left, was removed, whose user was suspended, or whose call has ended receives nothing, however recently they were admitted.

**Leaving and being removed close the connection too.** Since `M18-004` the media plane is told: the person's connection is closed as the departure is recorded, and one who connects again with a credential they still hold is reported and disconnected again. See [`media.md`](media.md#what-the-media-server-reports).

**A connection going away is recorded as leaving.** When the media server reports that somebody's connection went away and confirms they are not connected some other way, they are recorded as having left. An application's call is not ended by that, even when nobody is left in it.

**The credential expires in five minutes**, and the trade is deliberate. It is presented once, to open a connection, and the connection outlives it: nothing forces anyone out when it expires. What the short life bounds is how long a copy taken from a log, a crash report, or a device somebody no longer has can still be used to walk into a conversation. The cost is that a client which loses its connection after expiry cannot reconnect with the same credential and has to ask for another — one request against an endpoint the application already calls.

Sending an `Idempotency-Key` is worth it here in a way it is not for the operations above. Those are idempotent by nature; this one mints something new every time, so a request that timed out would otherwise leave a usable credential behind that nobody ever received.

A deployment configured with no media plane answers `503 unavailable`. That is not a fault: a Convia running only the control plane is supported, and every other participant operation works in it.

The response names no provider. A contract test asserts the schema publishes only Convia-owned connection data, and an end-to-end test confirms a real media server accepts what Convia issues. See [`media.md`](media.md).

## Audit

Joining, leaving, removal, a role change, and issuing a connection credential are audited. The record names the participant, the call, the application, the person, the new state and role, and the removing authority.

**The removal reason is not recorded.** It is composed by the application and may say something about the person removed — `harassed-another-attendee` is a reason someone could plausibly write. A test asserts it stays out.

**The credential is not recorded either.** The audit trail says that one was issued and to whom, which is what an incident needs to know. Writing the credential itself into a durable, widely readable artefact would turn a five-minute secret into a long-lived one. A test asserts that too.

## Not Yet Implemented

- **Presence beyond a call is still out**, see below. What is no longer missing is guest participation: somebody with no Convia user takes part by redeeming an invitation, identified by that invitation and nothing else. See [`invitations.md`](invitations.md).
- **Rate limits on issuing credentials** (`M13-008`). Every write endpoint is equally exposed to a caller holding a valid key, so limiting only this one would be arbitrary. It belongs with a general per-tenant limit rather than here.
- **Media capabilities.** What a participant may do with audio, video, or a screen is uniform today: everyone admitted may publish and subscribe, because that is what a call is. The role vocabulary will grow when a distinction exists that is worth enforcing — a listener-only room, or screen sharing in M28. A moderator deliberately gets no extra media permission: moderation is a control-plane decision, and a client able to act directly on the media plane would bypass Convia's authorization and audit trail.
- **Presence beyond a call.** Whether someone is online, away, or busy is not participation and does not belong here.
