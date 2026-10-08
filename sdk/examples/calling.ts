/*
Joining a call without knowing what carries it.

**Nothing here names a media plane**, and that is the rule rather than a
preference: an installation's media infrastructure is its own, a client that
knew which one it was talking to would break when somebody changed it, and a
package that depended on one would make every consumer ship it.

So the consumer is handed an address and a credential, and gives them to
whatever client their installation needs.
*/

import { Calls, longestWait, usable, type Convia, type MediaInvitation } from '../src/index.js'

/* Whatever the installation's media plane needs. Not this package's business. */
type Connect = (url: string, token: string) => Promise<void>

/*
joinAndConnect seats the person and hands the instructions on.

`reveal()` is called once, where the credential is presented. Every call to it
is a place the credential escapes its wrapper, so there should be very few, and
each should be connecting rather than storing or reporting.
*/
export async function joinAndConnect(
  convia: Convia,
  roomId: string,
  connect: Connect,
): Promise<MediaInvitation> {
  const invitation = await new Calls(convia).join(roomId)

  if (!usable(invitation)) {
    // Between Convia answering and this running, the credential stopped being
    // accepted. Joining again is the only thing that produces a new one.
    throw new Error('The credential expired before it was used.')
  }

  // The address is used exactly as given: a deployment commonly reaches its
  // media infrastructure over a private network, so one derived from Convia's
  // own address works in development and fails in production.
  await connect(invitation.url, invitation.token.reveal())

  return invitation
}

/*
logging shows what a consumer may safely write down.

The whole invitation is the thing somebody logs, not the credential alone, and
it is safe: every way a value ends up in a log answers `[redacted]`.
*/
export function logging(invitation: MediaInvitation, write: (line: string) => void): void {
  write(JSON.stringify(invitation))
  write(`joined ${invitation.callId} as ${invitation.participantId}`)
}

/*
refreshingBefore schedules a rejoin while the current credential still stands.

`longestWait` is never negative, which matters here: a negative delay makes
`setTimeout` fire at once, and a credential that has already expired would turn
a refresh into a loop.

**The expiry bounds when a connection may be opened, not how long it may last**,
so this is for joining again, not for tearing down a call that is working.
*/
export function refreshingBefore(
  invitation: MediaInvitation,
  margin: number,
  rejoin: () => void,
): ReturnType<typeof setTimeout> {
  const left = longestWait(invitation)
  return setTimeout(rejoin, Math.max(0, left - margin))
}
