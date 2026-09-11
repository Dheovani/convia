# Messages

A message is something somebody said in a room.

This document covers what a message is, how a room's history is ordered and
read, and what editing and deleting mean. What it does **not** yet cover is who
may write or read one: that is per-person authorization, and it is being built
separately.

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

## Bounds

| | |
| --- | --- |
| Body | 1–4000 characters, counted as characters rather than bytes |
| Page size | 25 by default, 100 at most — a larger limit is **refused**, not clamped |

The body bound is a storage bound rather than a product opinion. Messages are
the most abundant row Convia will ever hold, and a field with no ceiling is one
an application can turn into a blob store.

**A line break is allowed, and a message is the first field in Convia where one
is.** Every other caller-supplied text — an alias, a name, a metadata value — is
a label, and a control character in a label is either a mistake or an attempt to
make two values render identically. A message is meant to hold more than one
line, so newline, carriage return and tab are what it may carry; every other
control character is still refused.

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
| **Read state and unread counts** | Next, and the sequence is the shape it will take. |
| **`message.*` events** | Next. Which of them are durable is a decision of its own, since M17 established that not every event should be redelivered. |
| **Per-person authorization** | The first real use of a session principal, and the largest remaining question. Migration `00009` records that Convia does not model room membership because it held no credentials for an application's people — **M18 changed that premise** for the first-party application, and the consequence is unresolved. |
| **Reactions, threads, replies, typing indicators** | Not decided. None are needed by the interface shell. |
| **Search** | Not decided. It is a different index and a different cost model. |
