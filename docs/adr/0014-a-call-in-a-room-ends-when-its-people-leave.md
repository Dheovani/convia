# ADR 0014 — A call in a room ends when its people leave

**Status:** Accepted
**Date:** 2026-09-14
**Milestone:** M18 — Standalone Web Application

## Context

Convia has had calls since M09, participants since M10, a media plane since M12 and credentials to connect with since M13, all for applications: an application starts a call, admits its people, hands them credentials, and ends the call when it decides. None of it was reachable by a person signed in to Convia's own product, and none of it knew when somebody's browser simply went away.

`M18-004` brings calls to Convia's own product. The product owner decided the shape before anything was built:

- **any member of a room starts a call in it**, and starting a call puts the person who started it in it;
- **nobody ends a call for everybody**: it ends when its last participant has left;
- a person starting or ending a call is recorded as the actor **`person`**, and a call Convia ends without being asked — the media server reported that the last connection went away, or the room was deleted — as **`system`**;
- the first delivery carries **audio and video**, and a person joins with the microphone on and the camera off;
- **the room's owner joins as the call's moderator** and may take somebody out of it, who is disconnected at once and cannot come back to that call;
- **deleting a room ends its call**, whoever deleted it;
- **losing one's place in a room** — leaving it, being removed or banned — takes one out of its call;
- **the media server says when somebody's connection went away**, rather than the page reporting that it is still there;
- in the interface, **the call is in the room**, the Calls destination lists the calls running in the person's rooms, and **a call goes on while its person reads another room**;
- **visitors from other installations join calls later**, in `M33-002`.

## Decision

### Starting and joining are one request

`POST /v1/me/rooms/{room_id}/call/join` seats the person in the room's call and starts one if there is none, answering `201` when it started it and `200` when it joined one. The calls domain keeps starting and joining apart, and rightly for an application, which starts a call and admits people on its own schedule; for a person pressing a button they are one act, and a call its starter is not in would be a call nobody is in.

Two people starting at once are settled by the one-call-per-room index M09 already has: the one who loses joins the call the other started. Joining again while in the call returns the same participation with a fresh credential, which is how a page that lost its connection gets back in.

There is no route to end a call. `POST .../call/leave` takes the person out, and ends the call when they were the last.

### Whether a call is empty is decided under the call's lock

A call ends when nobody is left in it, and somebody may be joining at that instant. `calls.Store.EndIfEmpty` locks the call's row, counts who is present in a second statement, and ends it only if nobody is. Joining takes the same lock before admitting anybody, so the two serialize: the person arriving finds the call running and keeps it running, or finds it over and starts a new one.

The count is a second statement rather than a condition on the update because PostgreSQL re-checks an update's condition only against a row that changed while it waited, and a lock is not a change — a subquery in the condition would judge the room by the roster from before the person who joined. A test races a join against the last leave twenty times.

**Only a call in a room a person opened ends this way.** An application decides when its conversations end, and an empty call is an ordinary state for one.

### The media server reports; Convia checks

A person whose browser crashes, or whose network drops, never asks to leave. Something has to notice, and the product owner chose the media server's reports over the page asserting that it is still there: the media server is what actually knows who is connected, a heartbeat from the page would be a second account that could disagree with it, and only the media server can say that a call nobody ever connected to has lapsed.

This is `M12-009` and `M12-010`. The media server sends signed reports to `POST /media/reports`, a route outside the versioned API, served only when a media plane is configured. The adapter verifies the signature — a token made with the API secret, pinned to one algorithm and to this deployment's key, carrying a digest of the body — before reading anything, and translates the three kinds Convia acts on into `media.Report`. Everything else the server says is received and ignored.

**A report is evidence, not an instruction.** It is checked against Convia's own record:

- **somebody connected who is not in the call** is disconnected. A credential outlives the participation it was issued for, so somebody taken out of a call could otherwise connect again with the one they still hold;
- **somebody whose connection went away** is recorded as having left **only once the media server confirms they are not connected**. Reloading a page opens a new connection under the same identity before the old one is reported gone, and believing the report alone would take a person out of a call they are sitting in, and could end it around them. A question the media server cannot answer fails the report, so it is sent again rather than guessed at;
- **a session that is gone** ends a call in a room a person opened, and records everybody still present as having left. It is what ends a call nobody ever arrived at.

An application's participants are recorded as having left when the media server reports it, because that is true of them too; an application's calls are never ended by a report.

### A room tells its call when it changes

Deleting a room ends its call, and taking somebody's place in a room takes them out of its call. The rooms package cannot import the calls or participants packages, which import it, so it declares what it needs — `RoomDeleted` and `MemberGone` — and the participants service is attached after construction by `rooms.Service.InformCalls`, before anything is served.

Both are best-effort, like announcing an event: the room has already changed, and nothing a call does may undo that. A call that should have ended and did not is ended when the media server reports its session finished.

Membership decides who is in a call only in a room a person opened. An application admits people to its calls on its own authority and never had to make them members first.

### Being put out of a call means being disconnected

Before this, removing a participant ended the participation and stopped new credentials, and the person stayed connected until they chose to go. The media boundary gains two operations — close somebody's connection, and ask whether they have one — and every departure Convia records closes the connection: leaving, being removed, and losing one's place in the room, on every surface. The moderation token that does it is scoped to the one room, and a real server is shown refusing it in another.

### The page may reach the media server, and nothing else new

The page's `connect-src` names the media server's WebSocket address and the same host over HTTP, exactly, and nothing when there is no media server. `Permissions-Policy` allows the camera and microphone to the page itself only.

The media client is loaded when somebody first joins a call, not with the page. It is most of the weight a call adds, and most visits never join one.

## Consequences

- **Reports have to reach Convia.** A media server configured without the webhook to `/media/reports` leaves a crashed browser's participation standing, and a call in a person's room running until somebody leaves it. `docs/media.md` says how to configure it, and the local Compose file does.
- An application's call now sees its participants leave when their connections go away, and `Leave` and `Remove` now disconnect. Neither changes the contract; both make it true of the media.
- Deleting a room ends its call for applications as well as people.
- A moderator moderates from inside the call. An owner who is not in it is told to join it, and an ownership that passes during a call does not change anybody's role in it until they join the next one.
- A person taken out of a call can join the room's next call, which is a new one; they cannot return to that call.
- A lost connection is retried once by the page; anything Convia or the media server did on purpose is said, and not undone.
- Device selection, a preview before joining, clearer handling of refused permissions and degraded networks are `M18-005` to `M18-012`. Visitors are `M33-002`.
