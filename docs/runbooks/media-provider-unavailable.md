# Media provider unavailable

**Alert:** starting calls answered `503 unavailable`, or `the media plane refused to realize a call`, `a media session was not released` or `a participant who is out of a call was not disconnected` in the log.

**What happened:** the media provider's control API — LiveKit's room service — is not answering, answering slowly, or answering `5xx`. This page is about Convia's side of that: what still works, and what the provider's own outage costs the people in calls is the provider's to say.

## What still works, and what does not

| | while the provider is away | when it is back |
| --- | --- | --- |
| Messages, rooms, members, presence, events, sign-in | **Unaffected.** None of it asks the provider. | — |
| Starting a call | `503 unavailable`, at once; the call is recorded as ended, so the room is free and a retry starts a new one. | Within about a second. |
| Joining a running call | **Works.** A credential is signed by Convia and asks the provider nothing. Whether the person then connects is up to the provider. | — |
| Leaving, being removed, ending a call | **Works**, and Convia's record changes at once. Asking the provider to disconnect the person or delete the room is best-effort and waits at most a second. | Rooms not deleted are reclaimed by the provider ten minutes after they empty. |

**A provider that hangs costs one short wait a second, not one a request.** After it fails to answer, Convia stops asking it for a second and answers at once with what it would have answered after the timeout, then lets one request find out whether it is back. See [`internal/circuit`](../../internal/circuit/circuit.go), and [`docs/load.md`](../load.md#when-the-media-provider-degrades) for what an outage measured like before and after.

## What to do

1. **Confirm it is the provider.** From an instance's host, `curl -s -o /dev/null -w '%{http_code}\n' "$CONVIA_LIVEKIT_URL"` answering, while Convia logs failures, points at the network or the credentials between them; `livekit answered 401` in the log is a key the provider does not accept, which no waiting will fix — see [`rotating-secrets.md`](rotating-secrets.md).
2. **Bring the provider back.** Convia needs nothing done: it asks again by itself. Do not restart Convia for this; it changes nothing and closes every open event stream.
3. **Afterwards, little needs reconciling by hand.** Calls that could not start were ended, and rooms that could not be deleted are reclaimed by the provider once they empty. **A person who could not be disconnected may still be connected to the media** of a call Convia records them as out of: Convia closes such a connection when the provider reports it, which it does when they connect, not while they stay. Search the log for `a participant who is out of a call was not disconnected`; where it matters — somebody removed from a call — remove them on the provider itself (`lk room participants remove`, with the log line's `call_id` as the room and its `participant_id` as the identity), because removing them again in Convia changes nothing and so asks nothing.
