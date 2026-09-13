# 0010 — A person's stream is authorized per room, by rooms the broker holds

**Status:** Accepted
**Date:** 2026-09-13
**Milestone:** M18

## Context

Convia's interface polled. The sidebar asked every fifteen seconds and an open room every five, because the event stream was on the surface an application reaches with its key and a person holding a session had nothing to subscribe to. [ADR 0009](0009-convia-serves-its-own-interface-from-its-own-origin.md) recorded that as a gap the interface exposed, and `M18-018` noted the part that made it more than wiring: **a person's stream is authorized per room rather than per tenant, so it is not the existing handler with a different verifier.**

An application's stream is authorized once. The scopes on its key say which types it receives, the key names the tenant, and neither changes while the connection is open. A person's entitlement is the set of rooms they are in, and that set is exactly what changes while a stream is open: somebody adds them, or they leave in another tab. Whatever decides delivery has to follow it.

Membership changes were not announced at all (`M18-020`), which is the other half of the same problem. A person added to a room by somebody else had no way to find out until the sidebar asked again, and neither did a stream.

## Decision

**Each person's stream holds the set of rooms it covers, and the broker checks an event against that set as it delivers.**

- The set is **read before the upgrade**, from the database, so a failure is still an HTTP answer.
- It is **kept current by the stream's own events**. Membership changes are now announced as `room.member_added` and `room.member_removed`, and when one is about the stream's person the broker updates the set as it delivers it. Such an event is delivered if the person was in the room before it or is in it after, so both being added and being removed arrive.
- It is **read again every minute**, and the session is authenticated again at the same moment. A session that has ended closes the stream with `4001`.
- A read that races a membership change is reconciled: changes about the person that arrive while the read is running are kept and applied on top of it. Each change states whether the person is in a room rather than describing a difference, so applying them in order to a read taken at any moment in between gives the state after the last one.

The stream carries only the types a person could already read — messages and members — and leaves out `correlation_id`, which names somebody else's request. People's streams have ceilings of their own, apart from applications'.

## Alternatives

**Ask the database per event.** The most obviously correct answer, and the wrong one here. An event is delivered under the broker's lock to every stream at once, and `Publish` is documented as unable to block or fail, because a slow subscriber must never become a slow API. A query per event per subscriber would put the database inside every message posted, multiplied by every tab open on the tenant.

**Deliver every tenant event to a person's goroutine and filter there.** It keeps queries off the publish path, but the queue fills with other people's conversations: a busy first-party application would end every person's stream as having fallen behind, and each of them would briefly hold metadata about rooms they are not in.

**A channel per room, subscribed to per stream.** A reasonable shape for a larger system, and one this deployment does not need. It moves the same membership problem into subscription management — a stream still has to learn it was added somewhere in order to subscribe — and adds a structure per room to a broker that holds nothing by design.

**Keep polling.** It works, and it was the honest cost while there was nothing to subscribe to. It is also a request every five seconds per open tab to learn, almost always, that nothing happened.

## Consequences

- **A window of up to a minute** is stated rather than hidden. Within it a stream can still carry events about a room its person has just left, when the removal happened on another instance and its event was lost or delayed, or about a session that has just ended. What travels is identifiers; reading what they point at checks membership and the session again on every request.
- Rechecking costs a few indexed reads per open stream per minute. At the people's ceiling on one instance that is a few hundred reads a second, which is the figure to revisit if the ceiling is raised.
- Authenticating again counts as using the session, as a request does. An open tab keeps its session from going idle, which is what polling every few seconds already did.
- Signing out does not end a stream immediately; it ends within the minute. Ending it at once would mean the sessions domain reaching into the broker, and the minute is the same bound the rest of this design accepts.
- A person's streams do not count against their application's eight, so the first-party application's backend cannot be refused its stream by the tabs of its own people. They do share an instance with every tenant, so they have an instance ceiling of their own.
- The WebSocket handshake is held to the exact-origin check despite being a GET. It is the first GET on the session surface that is not exempt, and the reason is written where the check is.
- The application's stream still verifies its key once. Revoking a key does not reach a stream already open; that gap predates this decision, and `docs/sessions.md` lists it.

## Related

- [ADR 0003 — A one-directional, in-process control event stream](0003-a-one-directional-in-process-control-event-stream.md)
- [ADR 0007 — A session is a person, not a tenant's authority](0007-a-session-is-a-person-not-a-tenants-authority.md)
- [ADR 0009 — Convia serves its own interface, from its own origin](0009-convia-serves-its-own-interface-from-its-own-origin.md)
- [`docs/events.md`](../events.md)
