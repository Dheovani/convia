/*
Taking part in a call, as the signed-in person.

Four operations, and the only one with anything to it is joining: Convia seats
the person and answers with what to connect with, and that answer carries a
credential. Everything about keeping it out of a log is in `media.ts`; this is
where it comes from.
*/

import type { components } from './contract.js'
import { invitationFrom, type MediaInvitation } from './media.js'
import type { Convia, RequestOptions } from './transport.js'

export type Call = components['schemas']['Call']
export type Participant = components['schemas']['Participant']

type JoinAnswer = components['schemas']['JoinSession']
type Roster = { data: Participant[] }

/*
Calls is the person-facing call surface, bound to one client.

A class rather than methods on `Convia` because the transport is about requests
and this is about calls, and a transport that grew a method per domain would be
a transport nobody could read.
*/
export class Calls {
  readonly #convia: Convia

  constructor(convia: Convia) {
    this.#convia = convia
  }

  /* current reads the call happening in a room, if one is. */
  current(roomId: string, options: RequestOptions = {}): Promise<Call> {
    return this.#convia.get<Call>(`/me/rooms/${encodeURIComponent(roomId)}/call`, options)
  }

  /*
  join seats the person in the room's call and answers with what to connect
  with.

  The invitation it returns carries a credential that **does not render
  itself**, so logging what came back does not log the credential. See
  `MediaToken`.
  */
  async join(roomId: string, options: RequestOptions = {}): Promise<MediaInvitation> {
    const answered = await this.#convia.post<JoinAnswer>(
      `/me/rooms/${encodeURIComponent(roomId)}/call/join`,
      options,
    )
    return invitationFrom(answered)
  }

  /*
  leave takes the person out of the room's call.

  It answers nothing, and it is worth calling even when the connection is
  already gone: Convia learns a participant left from its media plane too, but
  later, and a roster that is right at once is the difference between a person
  disappearing and a person lingering.
  */
  leave(roomId: string, options: RequestOptions = {}): Promise<void> {
    return this.#convia.post<void>(`/me/rooms/${encodeURIComponent(roomId)}/call/leave`, options)
  }

  /* participants reads who Convia says is in the room's call. */
  async participants(roomId: string, options: RequestOptions = {}): Promise<Participant[]> {
    const answered = await this.#convia.get<Roster>(
      `/me/rooms/${encodeURIComponent(roomId)}/call/participants`,
      options,
    )
    return answered.data
  }
}
