# Messages

A message is something somebody said in a room.

This document covers what a message is, how a room's history is ordered and
read, what editing and deleting mean, how read state works, what streams, and
what erasure removes.

It covers **two surfaces**. An application reaches its own rooms with its key, naming which of its people is speaking. A person reaches the rooms they belong to with a session cookie, and names nobody — see *Acting as yourself*.

## A conversation is a room

There is no separate conversation. The rooms domain already calls a room *the
place* and a call *the occasion*, and a conversation held in a place over time
is what a room's messages are.

So messages hang off the **room**, not the call. That is what lets chat survive
a call ending — the history is what somebody sees when they open a room where
nothing is happening, and a call is one afternoon in a place that outlives it.

The reasoning is recorded in
[ADR 0008](adr/0008-a-conversation-is-a-room-and-its-order-comes-from-the-database.md).

## What Convia is authoritative for

| Convia decides | Whoever wrote it decides |
| --- | --- |
| the identifier | what the message says |
| the room and the owning application | |
| the position in the history | |
| when it was written, edited, deleted | |
| who the author was | |

The body is stored **without interpretation**. No markdown is parsed, no mention
is extracted, no link is fetched. That is the same stance room metadata takes,
and for the same reason: Convia cannot be wrong about a value it never reads.

## Order

Every message carries a `sequence`: a number that orders one room's history.

**It is not a timestamp, and this is the point.** Since M16 a deployment is
several instances, and each stamps `created_at` from its own clock. Two clocks
that are merely synchronised are not the same clock, so two messages written
milliseconds apart on different machines can be stamped in the wrong order — and
a chat that shows a reply above the thing it replies to is broken in a way people
notice immediately.

The sequence is allocated by PostgreSQL while the append holds the room's row
lock, which makes it the one answer two instances cannot disagree about.
`created_at` is still recorded and reported; it says when an instance handled the
request, and nothing about order.

What a client may assume:

- **Strictly increasing within a room.** A higher sequence was said later.
- **Per room.** Every room counts from 1. It is not a global clock.
- **Not dense.** There may be gaps. Erasing a message for real, rather than
  tombstoning it, leaves one, and code that assumed contiguity would break the
  day retention is implemented.

Appends to one room are serialized by that lock. That is the correct cost — they
are being given positions in a single sequence, which is inherently a serial act
— and it is per room, so two rooms never wait on each other.

## Reading history

History is read by keyset, in one of two directions, because the two readings a
client actually performs are different:

- `older` — backwards from the newest. This is opening a room.
- `newer` — forwards from a position. This is catching up after being away.

**The cursor is the sequence.** The rooms listing hides its cursor behind an
encoded `(created_at, id)` pair, because that pair is internal and a client has
no other use for it. A message's sequence is published in the message itself and
is what read state will be expressed in, so encoding it would be ceremony.

An absent cursor means the end the direction starts from: the newest message for
`older`, the very beginning for `newer`.

## The API

| Route | Scope | What it does |
| --- | --- | --- |
| `POST /v1/rooms/{room_id}/messages` | `messages:write` | say something |
| `GET /v1/rooms/{room_id}/messages` | `messages:read` | read a window of the history |
| `GET /v1/messages/{message_id}` | `messages:read` | read one message |
| `PATCH /v1/messages/{message_id}` | `messages:write` | change what it says |
| `POST /v1/messages/{message_id}/delete` | `messages:write` | withdraw it |
| `PUT /v1/rooms/{room_id}/read_state` | `messages:write` | mark a room read |
| `GET /v1/rooms/{room_id}/read_state` | `messages:read` | how far somebody read, and how much they have not |

The tenant comes from the presented credential, so no request field could name
another application's room or person.

**The two scopes are separate, and neither implies the other.** A credential
that lists rooms and reads their settings is doing administration; one that
reads their history is reading people's conversations, and a deployment that
wants the first without the second must be able to say so. Writing without
reading is a real shape too: a service that announces deployments into a room
has no business reading what people replied.

Withdrawing is a `POST` to a sub-resource rather than a `DELETE`, because the
request has to name **which person** is withdrawing and a `DELETE` carrying a
body is a shape many clients cannot send. Removing a participant is spelled the
same way for the same reason.

Posting accepts an `Idempotency-Key`. It is the operation where a retry after a
timeout is most visibly wrong: a duplicated room is an administrative annoyance,
while a message sent twice is something everybody in the room sees.

**Naming somebody else's message answers `409 conflict`, not `403 forbidden`.**
The credential was granted everything this surface can grant, so sending the
caller after a different scope would send them after something that would not
help. They named the wrong person.

## Acting as yourself

Everything above is the **application** surface: a key says which application is calling, and the request says which of its people is speaking. There is a second surface, reached with a session cookie, where a person acts as themselves.

| Route | What it does |
| --- | --- |
| `GET /v1/me/rooms` | the rooms I am in, with what I have not read |
| `GET /v1/me/rooms/{room_id}/messages` | read a room I am in |
| `POST /v1/me/rooms/{room_id}/messages` | say something |
| `GET /v1/me/rooms/{room_id}/read_state` | how far I have read |
| `PUT /v1/me/rooms/{room_id}/read_state` | mark a room read |
| `PATCH /v1/me/messages/{message_id}` | change what I said |
| `POST /v1/me/messages/{message_id}/delete` | withdraw it, or take down anybody's message in a room I own |

### There is no author field, and that is the whole point

An application names which of its people is speaking because it is acting on their behalf and is the only party that knows. A person cannot name anybody, because naming somebody would mean naming somebody else.

So the request body carries what was written and nothing else. Writing as another person is not validated against and refused — **there is nowhere to put it**. That is the same move M18 made with the tenant: a bug of this shape is unrepresentable rather than merely unlikely. The schema forbids the field too, so a client that sends one is told its request is invalid rather than having it quietly ignored; being ignored would be worse, because the message would be attributed correctly while the caller believed otherwise.

### Membership decides, and a stranger is told the room is not there

This is the first place a session principal decides anything, and what it decides against is [room membership](rooms.md#membership).

**A room this person is not in answers `404 not found`, never `403 forbidden`.** A refusal that separates "not yours" from "does not exist" confirms to somebody outside a conversation that the conversation is happening, which is most of what they were asking. The application surface is entitled to that distinction and gets it; a person is not.

Editing and withdrawing are the exception, and the asymmetry is deliberate: they check **authorship**, not membership. Somebody who wrote a message was in the room when they wrote it, and having been removed since does not hand their own words to anybody else.

**The owner of a room can take down anybody's message in it** ([ADR 0013](adr/0013-a-room-a-person-opens-has-an-owner.md)). The tombstone's `deleted_by` is `owner` rather than `author`, and never names the person, so the room does not read the removal as its author taking the words back. A member who is not the owner is refused with the `409` they have always received. Taking down a message already withdrawn changes nothing, including who withdrew it. A room an application created has no owner, so nobody takes down anybody else's message there.

### The sidebar

`GET /v1/me/rooms` answers with the rooms and the unread counts together, because a sidebar is redrawn constantly and a request per row on the screen is the shape that makes an interface feel slow. Convia counts every room's unread in one statement and resolves every room in one more, rather than once per row.

A membership whose room has been deleted is skipped rather than reported. Rooms are deleted softly and memberships outlive them until erasure, so it is an ordinary state — and a sidebar that failed because one row had been deleted would be a whole screen lost to a room nobody can open anyway.

### A session still grants no authority over a tenant

Nothing here converts a session into an application key. The shortcut would let anybody who signed in act with the first-party application's full authority, outliving their session and their account, and [ADR 0007](adr/0007-a-session-is-a-person-not-a-tenants-authority.md) records why it stays closed.

Responses carry `Cache-Control: no-store` and `Vary: Cookie`. These bodies are one person's conversations, and a cache that served one to somebody else would be the worst failure this surface could have.

## Authorship

An author is **a user, or a guest carrying nothing but the invitation they
redeemed** — and exactly one of the two. The shape is taken from `participants`
deliberately: it is the same question about the same people, and answering it
two ways is how a roster and a history would come to disagree about who was in
the room.

Convia learns nothing new about a guest. No name travels with a message; the
application knows who it sent the invitation to.

Attribution is checked before anything is written. A user must be one of this
application's and must not be suspended or deleted. A guest must be an
invitation this application issued, and one that names nobody — an invitation
issued *for* a user is that user's, and accepting it as a guest identity would
let the same person appear in a room under two different names.

## Editing and deleting

**Editing** replaces the body and records `edited_at`. Convia records *that* a
message was edited, not what it said before: keeping the previous text would
double the storage of the most abundant row in the system and create a second
copy to find when somebody asks to be erased.

**Deleting** leaves a tombstone. The row stays, keeps its position, and loses its
body. Two reasons:

- A history that closed over the hole would move every later message's position,
  and a client paging through it would silently skip whatever slid past its
  cursor.
- "This message was deleted" is what a reader expects to see. Silently vanishing
  text is more confusing than an acknowledged absence.

Deleting twice is the same outcome as deleting once. A retried request is not a
failure.

**Only the author changes what they said.** This is not a configurable
permission — editing another person's words is not a thing anybody can be
granted, so it is refused by the domain rather than by a policy. An edit also
cannot follow a deletion: the author already decided to withdraw it.

## Read state

How far somebody has read in a room.

**It is durable, and presence is not, and that difference is the decision.**
Presence is a claim with an expiry, honestly described as "this was true a
moment ago". Read state is a fact about something a person did: it does not
decay, and a badge that came back because a laptop closed would be wrong in a
way people notice immediately. So it lives in PostgreSQL beside the messages it
points at, not in the Redis that holds presence.

It is a **position, not a set**. Convia records the furthest sequence reached,
never which messages were read — the set would be the size of the history times
the number of readers, and nothing has ever needed to know that message 12 was
read while 11 was not.

**It only ever moves forward.** A mark at or behind the stored position is
accepted and changes nothing rather than being refused: two devices report
independently and out of order, and a phone finishing a second after the laptop
must not pull the badge backwards. The reply is therefore the position that now
holds, which is not always the one that was sent. A position beyond the room's
newest message is refused, because accepting it would make the unread count
negative and silently suppress messages the client never received.

`unread` is derived from the position on every read rather than stored, so the
two cannot drift apart. It excludes two things, and both are product decisions:

- **The person's own messages.** Nobody has an unread message from themselves,
  and a badge that counted them would light up as you typed.
- **Withdrawn messages.** A tombstone is the absence of something to read.

Somebody who has never opened a room has no row. That is not an error: they have
read nothing, `sequence` is `0` — a position nothing occupies, since messages
count from one — and the whole history is unread.

**Only users have read state.** A guest has no Convia user, their stint is one
call, and they do not come back to find a badge waiting.

## Events

Three types stream: `message.posted`, `message.edited`, `message.deleted`.

They stream for the reason rooms and users do not. A room being renamed is the
application telling itself what it just did; a message is the application's
*other* instances learning what one of them did, and the people they hold
connections for are waiting on it. Since M16 a backend is several processes, and
the one that handled a post is almost never the one holding the socket of the
person who needs to see it.

**An event carries no body.** It names the message, the room, the position and
the author; what was said is read back through the API by a caller that holds
the scope to read it. Events reach every webhook destination an application
registered, which are endpoints outside Convia, and copying conversations into
them would be a leak dressed as a subscription.

Carrying no body is also what makes these safe to deliver **durably**, unlike
presence. A retried delivery that arrives late says only that a message changed,
so a subscriber that re-reads gets the current text rather than an older one it
would write over the newer.

## Retention and erasure

Convia keeps a room's history for as long as the room exists. There is no cap on
how many messages a room may hold: truncating a conversation nobody asked to
truncate is worse than a large table, and the operator who wants a smaller one
deletes rooms.

**Erasing a person redacts what they wrote rather than removing it.** The row
keeps its place in the room's order and loses its body and its author. The two
alternatives are both worse:

- *Deleting their messages* takes the conversation away from the people still in
  it. Every reply left behind answers something that is no longer there, and a
  history full of holes is a worse record for everyone else than one with an
  anonymous author.
- *Clearing a name* erases nothing, because the name never lived here. The link
  to the person is the thing Convia holds.

Messages already withdrawn are included: a tombstone still names who wrote it,
and that link is exactly what erasure is for. Read positions go too — where
somebody had read names a person and a room they were in.

This follows the boundary [`users.md`](users.md) already draws: Convia erases
everything in its own tables, and cannot erase the application's copy of the
same person.

**Nothing calls erasure yet, and that is the honest state.** M06 named the
missing piece — there is no job that acts at the end of a retention window — so
deletion stays soft everywhere in Convia and erasure is a capability rather than
a schedule. This is the messages half of the work that job will do, built so the
job has something correct to call.

## Bounds

| | |
| --- | --- |
| Body | 1–4000 characters, counted as characters rather than bytes |
| Page size | 25 by default, 100 at most — a larger limit is **refused**, not clamped |
| Request body | 1 MiB, which is the transport cap every JSON route shares |
| History per room | unbounded, deliberately |
| Rate | **not bounded per caller.** See below. |

A test asserts the first three cannot contradict one another: a message at the
documented limit must not be refused by the transport before it is validated,
and must not be accepted by the domain and rejected by the column.

**There is no request rate limit in Convia**, for messages or anything else. The
budget that exists covers *failed authentication* only. `M13-008` is where the
general one belongs, and the note M18 made about per-account limiting applies
here too: doing it properly across several instances needs shared state, which
the Redis of M16 now makes possible.

## What a closed room does

A closed room keeps its history and takes nothing new. Closing is reversible and
lossless — what stops is anything being added, which is what makes closing mean
something. A deleted room is reported as missing, because it is gone from the
API and a caller must not learn that an identifier once named something.

## Nothing said reaches the log

The audit trail records that somebody wrote in a room, which message it was,
which room, and when. **It never records what was said.** The log is shipped,
retained, and read by people who are not in the room; putting conversations into
it would undo the point of having rooms.

This is asserted against the real log a running instance writes, not against
intent.

## Not built, and why

| | |
| --- | --- |
| **Attachments** | Need object storage, an upload path, a content-scanning story and a retention story. None exist. A message that could carry a file but not delete it would be worse than one that cannot carry a file. |
| **Per-person authorization** | The first real use of a session principal, and the largest remaining question. Migration `00009` records that Convia does not model room membership because it held no credentials for an application's people — **M18 changed that premise** for the first-party application, and the consequence is unresolved. |
| **Reactions, threads, replies, typing indicators** | Not decided. None are needed by the interface shell. |
| **Search** | Not decided. It is a different index and a different cost model. |
