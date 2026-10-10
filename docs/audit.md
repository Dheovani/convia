# The audit trail

**The trail says who did what to what, and on whose authority.** It is the record an operator reads after something has happened: a tenant suspended, a key minted, somebody banned from a room, a call ended in the middle of a meeting.

The domain lives in [`internal/audit`](../internal/audit), the operator's way in is [`internal/audit/reading`](../internal/audit/reading), the endpoint is `GET /v1/audit` in [`api/openapi.yaml`](../api/openapi.yaml), and the table is created by migration `00027`.

## Why it is a table

Until `M21`, an audit entry was a structured log line. A log line is good at being read while an incident is happening and bad at every question asked afterwards: it lasts as long as whoever ships the logs decided, it cannot be searched by what it was about, and nothing in Convia can tell whether it arrived.

It was also **wrong**. Applications, credentials and users wrote `actor=unauthenticated` on every line, beside a comment promising the actor would become the real principal "once credentials exist in M07". `M07` closed long ago. Every one of those lines named the wrong authority for a change only an authenticated caller can make.

**The event journal is not the trail and could not become it.** It is pruned after a day, because its job is to let a disconnected reader catch up, and it records which application an event belongs to and no actor at all.

## What an entry holds

| Field | What it is |
| --- | --- |
| `action` | what happened, as `noun.verb` — the event's name where an event announced the same thing |
| `actor_kind`, `actor_id` | the authority, and whose it was |
| `application_id` | the tenant it touched, if it touched one |
| `subject_kind`, `subject_id` | what was acted on |
| `reason` | why, in the actor's words, where the route required one |
| `details` | the few facts nothing else in the entry can say |
| `request_id` | the request, which joins the entry to the access log and the trace |

**There is no before and no after.** A copy of what changed would be a second copy of every table, kept by code that cannot know when one of them changes meaning, and the first thing anybody trusted about it would be the part most likely to be wrong.

`details` exists for one reason: moving from log lines to rows must not lose what the lines already said. A credential's entry carries the scopes it was minted with, because "a key was issued" and "a key that can write every user was issued" are different findings. It is bounded to ten short values and 2 KB, and it never holds content, anything an application composed, or a secret.

## Who acted

There is one kind for each caller Convia verifies, plus Convia itself.

| Kind | Identifier | Who |
| --- | --- | --- |
| `operator` | operator credential | somebody with authority over the installation |
| `application` | application credential | a tenant's own key |
| `person` | account | somebody signed in to Convia's own product |
| `guest` | invitation | somebody holding an invitation and nothing else |
| `peer` | account on another installation | a visitor, vouched for by their home |
| `system` | none | Convia, on its own evidence |

**No service names the actor.** The middleware that verified a credential records who it verified, and the trail reads it from the request. A service that could name its own actor could name somebody else's. The two exceptions are the routes where the service *is* the verification: registering and signing in happen on public routes, the credential is the password, and the account domain is what checked it — so that is where the person is recorded.

`system` is what an entry says when nobody presented a credential: a call ended because the media plane reported the last connection gone, a person forgotten at the end of the retention window, a key minted by `convia operator issue`, whose authority is access to the database.

A visitor acting through their home is recorded as `peer`, not `person`, even where they resolve to a local session. What this installation verified is a signature from another one, and the trail says what was verified.

## Saying why

**The operations somebody will later ask about require a reason.** On the operator surface, suspending a tenant or a user, deleting one, revoking a key, minting one, ending a call and removing somebody from it all refuse a request without a `Convia-Reason` header, before anything is done:

```
POST /v1/applications/app_.../suspend
Authorization: Bearer cvo_...
Convia-Reason: Chargeback fraud reported in ticket 4411
```

The reason is stored beside the entry and comes back from `GET /v1/audit`. It is at most 500 characters once trimmed, and a control character is refused, so one reason cannot pretend to be two lines of a log.

**Which operations ask is the route's decision, not the service's.** The same service suspends a tenant whoever asks, and a reason is a question for the person holding the key, so the route's middleware asks, refuses without it, and puts it beside the actor where the trail reads it. A service cannot invent a reason any more than it can invent an actor, and a route off the operator surface marked to ask stops the process at startup: an application acting on its own data owes nobody an explanation.

**Deleting something takes naming it twice.** The five operator deletions — an application, a user, a room, an application's key, an operator key — also require `Convia-Confirm` to repeat the identifier in the path. It catches what a key cannot: a script with the wrong variable in a path, a command re-run against the wrong tenant. A missing or different confirmation is refused, and nothing is deleted.

**Re-authenticating is deliberately not asked for.** An operator key is presented on every request, so asking for it again proves nothing the first presentation did not. What would prove more is a second factor or a second person, and both need operators to be people Convia knows rather than holders of a key; who administers an installation is `M34-003`'s question.

The command line acts with the authority of the database rather than a key, and records no reason.

## Written with the change

An entry is written **in the same transaction as the change it describes.** A change that rolls back leaves no entry, and an entry that cannot be written fails the change: an installation that suspended a tenant and lost the record of who did it has the state without the accountability, and an operator reading the trail could not tell that from nobody having done it.

Three places record after the fact instead, and log rather than refuse when the trail cannot be written. Each is a change another domain already committed, where refusing would tell somebody they are not in a room or a call they are in: redeeming an invitation, accepting a room invitation from another installation, and joining a room elsewhere. The trail still holds the half an operator asks about — the participant joining, the room gaining a member.

## What is not recorded

- **Who joins and leaves a room.** That is the application's ordinary data, announced as it happens. The trail holds moderation — bans, lifted bans, ownership handed over — and not the roster.
- **Signing in, and a refused attempt.** The durable record of a sign-in is the session it began. Every failed attempt written to a table nothing prunes would hand anybody who can reach the form a way to fill the disk. Both are still logged.
- **Forgetting somebody's memberships during erasure.** Recording each room an erased person had been in would keep exactly what erasure exists to remove.
- **A webhook's address.** It commonly carries a token in its query string, and the trail is readable over every tenant. The endpoint's identifier leads to it, through routes that already decide who may read it.

## Reading it

```
GET /v1/audit?application_id=app_...&since=2026-10-10T14:00:00Z
Authorization: Bearer cvo_...
```

Every filter is optional and they narrow together: `application_id`, `actor_kind`, `actor_id`, `subject_kind`, `subject_id`, `action`, `since`, `until`. With none, the answer is the whole trail, newest first, which is where somebody with no lead starts. A window that ends before it begins is refused, and so is an instant that cannot be read, because answering with everything would let the mistake read as an answer.

**Reading the trail is `audit:read`, and nothing else grants it.** Every other operator scope is bounded by a kind of thing; the trail holds everything every tenant's key ever did. An operator who can list applications has a directory, and one who can read the trail has the history.

**Nothing writes an entry from outside.** There is no route for it, and there will not be one: an operator who could append to the trail could write a history that did not happen.

## How long it is kept

**Indefinitely, for now, and on purpose.** An entry names identifiers and never content, so it says nothing about a person beyond which identifiers were involved, and those name rows that erasure removes. An entry naming something that no longer exists is a correct entry: the table has no foreign keys, so the record that a tenant was deleted survives the tenant.

Whether that is the right period under LGPD is one of the questions `M23-018` asks qualified counsel. A retention window is a pruner and a configuration value; what it cannot be is a guess.
