# 0008 — A conversation is a room, and its order comes from the database

**Status:** accepted
**Date:** 2026-09-11
**Milestone:** M31 — Messaging and Conversations

## Context

The user interface specification is built around chat, and Convia had no
messaging domain — not in the code, and nowhere in M00–M30. Building one raised
two questions that had to be answered before any table existed, because
everything after them inherits the answer.

**The first is what a conversation is.** Convia already has rooms and calls, and
the rooms package draws the line between them in one sentence: the room is the
place, the call is the occasion. A third noun could have been introduced beside
them, with messages hanging off it.

**The second is what "before" means.** Since M16 a deployment is several
instances behind a load balancer. Each one stamps `created_at` from its own
clock, and two clocks that are merely synchronised are not the same clock. Two
messages written milliseconds apart on different instances can be stamped in
the wrong order, and a chat that shows replies above the thing they reply to is
broken in a way people notice immediately.

## Decision

**A conversation is a room.** There is no `conversations` table. Messages carry
`room_id`, and a room's messages are its history.

**Order comes from a per-room `sequence`, allocated by PostgreSQL under the
room's row lock.** `created_at` is retained and reported, but it is a record of
when an instance handled the request, not the ordering. The sequence is
published to clients, because read state will be expressed in it.

## Consequences

### Why not a conversations table

A `conversations` row one-to-one with a `rooms` row is a second name for one
thing. Every read would join it to learn nothing, and every feature after this
one would have to decide which of the two nouns it attaches to — a decision with
no right answer, made repeatedly.

The stronger argument is about lifetime. Chat has to survive a call ending: the
history is what somebody sees when they open a room where nothing is happening.
Attaching messages to a call would have made chat as ephemeral as the occasion,
and attaching them to a third entity would have left that entity with exactly
the lifetime a room already has.

**Messages therefore hang off the room, not the call.** A call is one afternoon
in a place that outlives it.

### Why not order by time

The clock argument above is the whole of it. What makes it worth an ADR is that
`created_at` is *almost* good enough, which is what makes it dangerous: it will
be right in every test on one machine, and wrong occasionally in production
under load, which is the worst failure schedule a design can have.

A sequence is also what read state needs. "This person has read up to 41" is a
single comparison against an integer; the same statement against a timestamp
has to reason about a message that arrives carrying an earlier stamp than one
already read, which is exactly what clock skew produces.

The sequence is **strictly increasing within a room** and is deliberately **not**
promised to be dense. Erasing a message for real, rather than tombstoning it,
leaves a hole, and a client that assumed contiguity would break the day
retention is implemented.

### Why the room's row lock, and not an optimistic retry

The first implementation allocated the position inside the insert —
`max(sequence) + 1` over the room, in the statement that writes the row — and
relied on the unique index to reject a collision, retrying up to five times.

**It was measured and it does not work.** Of sixteen simultaneous appends to one
room, nine or ten failed. Every writer that loses a collision re-reads the same
highest sequence as every other loser, so they collide again; the herd does not
disperse, it re-forms.

Appends now take `SELECT status FROM rooms ... FOR UPDATE` and hold it through
the insert. This follows the boundary `participants` already draws around a call
when it admits somebody, and for the same reason: a message's position is a fact
about the room, so locking the room is reading the state that owns the ordering
rather than reaching into an unrelated domain.

The unique index stays. It is no longer the arbiter of a race — it is the
assertion that the lock is doing its job, and an append that trips it is
reported rather than retried, because a retry would paper over an ordering
guarantee that had already been broken.

Locking the room closes a second race for free: closing a room takes the same
row lock, so a message cannot land in a room that was open when the service
checked and closed before the insert.

The cost is that appends to one room are serialized. That is the correct cost —
they are being given positions in a single sequence, which is inherently a
serial act — and it is per room, so two rooms never wait on each other.

### What this does not decide

**Who may read or write.** Authorization is per-person, it is the first real use
of a session principal, and it is deliberately separate from what a message is.
Until it exists, the messages domain checks only that attribution is truthful:
that a named author exists, belongs to this application, and is somebody Convia
still serves.

**Room membership.** Migration `00009` records that Convia does not model it,
and gives the reason: Convia held no credentials for an application's people, so
it could not enforce a policy about who belongs. **M18 changed that premise** for
the first-party application, where Convia now does hold the person's credential.
The consequence is not resolved here, and it is the question per-person
authorization has to answer first.
