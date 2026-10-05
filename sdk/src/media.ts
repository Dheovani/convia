/*
What to connect with, without saying what to connect with it.

**Nothing here names a media plane**, and that is the rule rather than a
preference: an installation's media infrastructure is its own, a client that
knew which one it was talking to would break when somebody changed it, and a
package that depended on one would make every consumer ship it.

So this carries an address and a credential, and the consumer hands them to
whatever client their installation's media plane needs. The SDK's part is the
three things that are easy to get wrong about a short-lived bearer credential:
not logging it, knowing when it has stopped working, and not mistaking it for
something that can be stored.
*/

import type { components } from './contract.js'

export type JoinSession = components['schemas']['JoinSession']

/* What a credential renders as everywhere except where it is presented. */
const redacted = '[redacted]'

/*
MediaToken is a bearer credential that does not render itself.

The contract says it is never logged, never stored, never shared. Saying so is
not enough: `console.log(invitation)` is one keystroke, a crash reporter walks
whatever it is handed, and a token in a log is a token anybody with the log can
present. So it renders as `[redacted]` to everything that stringifies a value,
and the real one comes out of `reveal`.

**Every call to `reveal` is a place the credential escapes**, so there should be
very few, and each should be connecting rather than storing or reporting.
*/
export class MediaToken {
  readonly #value: string

  constructor(value: string) {
    this.#value = value
  }

  /* Hides it from string concatenation and from template literals. */
  toString(): string {
    return redacted
  }

  /*
  Hides it from JSON.stringify, which does not consult toString.

  This is the one that matters most: a structured logger serialises its
  arguments, and an invitation handed to one would otherwise carry the
  credential into the log whole.
  */
  toJSON(): string {
    return redacted
  }

  /* Hides it from console.log in Node, which consults neither of the above. */
  [Symbol.for('nodejs.util.inspect.custom')](): string {
    return redacted
  }

  /*
  Hides it from `+` and from anything coercing to a primitive, including the
  `==` comparisons somebody writes while debugging.
  */
  [Symbol.toPrimitive](): string {
    return redacted
  }

  // reveal produces the credential, for presenting it and nothing else.
  reveal(): string {
    return this.#value
  }
}

/*
MediaInvitation is one person's instructions for joining one call.

`url` is used as given. It is frequently not the address Convia itself answers
on, because a deployment commonly reaches its media infrastructure over a
private network, so deriving one from Convia's own address is how a client works
in development and fails in production.
*/
export interface MediaInvitation {
  /* The participation these instructions belong to, and the identity the
  person appears under to other clients in the same call. */
  participantId: string

  /* The call being joined. */
  callId: string

  /* The address to connect to, used exactly as given. */
  url: string

  /* The credential to present, which does not render itself. */
  token: MediaToken

  /*
  When the credential stops being accepted.

  **This bounds when a connection may be opened, not how long it may last.** A
  connection already established is unaffected, so a client that tears one down
  at this moment is ending a call for no reason.
  */
  expiresAt: Date
}

/*
invitationFrom reads what Convia answered into something that will not leak.

It is a function rather than a constructor because what it takes is the
contract's shape, which is data off a socket, and what it returns is this
package's: the boundary is worth naming.
*/
export function invitationFrom(answered: JoinSession): MediaInvitation {
  return {
    participantId: answered.participant_id,
    callId: answered.call_id,
    url: answered.media_url,
    token: new MediaToken(answered.media_token),
    expiresAt: new Date(answered.expires_at),
  }
}

/*
usable reports whether the credential would still be accepted for opening a
connection.

The clock is a parameter so that a caller can ask about a moment other than now
-- and so this can be tested without waiting.
*/
export function usable(invitation: MediaInvitation, at: Date = new Date()): boolean {
  return at.getTime() < invitation.expiresAt.getTime()
}

/*
longestWait is how long the credential is still good for, in milliseconds, and
zero once it is not.

Never negative: a caller scheduling a refresh from this would otherwise schedule
it in the past, and `setTimeout` runs a negative delay immediately -- which
turns an expired credential into a loop rather than a refresh.
*/
export function longestWait(invitation: MediaInvitation, at: Date = new Date()): number {
  return Math.max(0, invitation.expiresAt.getTime() - at.getTime())
}
