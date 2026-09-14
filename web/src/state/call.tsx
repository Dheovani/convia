import { createContext, useContext, useEffect, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { CallPresence } from '../api/types'
import { connect, type Connection, type Ending, type Seen } from '../media/connection'
import type { Listener } from './events'

export type Phase = 'idle' | 'joining' | 'joined'

interface Snapshot {
  // roomId is the room whose call this person is in, or joining.
  roomId: string | null
  phase: Phase
  seen: Seen[]
  // names are the people in the call by participant, which is who the media server says they are.
  names: ReadonlyMap<string, CallPresence>
  microphone: boolean
  camera: boolean
  // problem is the last thing worth telling the person, about the room it names.
  problem: { roomId: string; message: string } | null
}

export interface CallSession extends Snapshot {
  join: (roomId: string) => Promise<void>
  leave: () => Promise<void>
  remove: (userId: string) => Promise<void>
  setMicrophone: (on: boolean) => Promise<void>
  setCamera: (on: boolean) => Promise<void>
  dismiss: () => void
}

const quiet: Snapshot = {
  roomId: null,
  phase: 'idle',
  seen: [],
  names: new Map(),
  microphone: false,
  camera: false,
  problem: null,
}

/*
The default is a call nobody can join. A component rendered outside the workspace
— in a test, or a screen added later — shows no call rather than failing.
*/
export const CallContext = createContext<CallSession>({
  ...quiet,
  join: async () => {},
  leave: async () => {},
  remove: async () => {},
  setMicrophone: async () => {},
  setCamera: async () => {},
  dismiss: () => {},
})

export function useCall(): CallSession {
  return useContext(CallContext)
}

/*
The words come from the status, never from Convia's own prose, for the reason the
sign-in form gives.
*/
function explainJoin(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 403:
        return 'You cannot join this call. You may have been taken out of it.'
      case 404:
        return 'This room is no longer available.'
      case 409:
        return 'This call cannot be joined right now. A closed room does not start new calls.'
      case 503:
        return 'Calls cannot be held here right now.'
    }
  }
  return 'The call could not be joined. Try again.'
}

function explainRemoval(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 403:
        return "Only the room's owner can take somebody out of the call."
      case 404:
        return 'That person is no longer in the call.'
      case 409:
        return 'Join the call to take somebody out of it.'
    }
  }
  return 'That person could not be taken out of the call.'
}

const endings: Record<Exclude<Ending, 'lost' | 'closed'>, string> = {
  removed: 'You were taken out of the call.',
  ended: 'The call ended.',
  elsewhere: 'You joined this call somewhere else, so it closed here.',
}

/*
useCallSession holds the one call this page is in.

It lives in the workspace rather than in a conversation, because a call goes on
while its person reads another room: the conversation that shows it can go away
and the call must not.

A join is an attempt, numbered. Everything an attempt learns later — a
connection opening, a person arriving, a connection closing — is dropped if a
newer attempt, or leaving, has happened since, so a slow answer about the call
somebody already left cannot pull them back into it.
*/
export function useCallSession(onExpired: () => void, listen: (listener: Listener) => () => void): CallSession {
  const [state, setState] = useState<Snapshot>(quiet)

  const connection = useRef<Connection | null>(null)
  const current = useRef<string | null>(null)
  const attempt = useRef(0)
  const asked = useRef(new Set<string>())

  const expired = useRef(onExpired)
  expired.current = onExpired

  function patch(change: Partial<Snapshot>) {
    setState((was) => ({ ...was, ...change }))
  }

  function unauthenticated(error: unknown): boolean {
    if (error instanceof ApiError && error.unauthenticated) {
      expired.current()
      return true
    }
    return false
  }

  async function readNames(roomId: string) {
    try {
      const page = await api.callParticipants(roomId)
      if (current.current === roomId) {
        patch({ names: new Map(page.data.map((present) => [present.participant_id, present])) })
      }
    } catch (error) {
      unauthenticated(error)
    }
  }

  /*
  open takes a seat in a room's call and connects to it, reporting whether it
  did.

  A seat Convia gave that the media server could not be reached for is given
  back, so the call is not held open by somebody who never arrived.
  */
  async function open(roomId: string): Promise<boolean> {
    const mine = ++attempt.current
    current.current = roomId
    asked.current = new Set()
    patch({ roomId, phase: 'joining', seen: [], names: new Map(), problem: null })

    let seated = false
    try {
      const session = await api.joinCall(roomId)
      seated = true
      if (mine !== attempt.current) {
        return false
      }

      const opened = await connect(session.media_url, session.media_token, {
        onChange: (seen) => {
          if (mine === attempt.current) {
            patch({ seen })
          }
        },
        onEnded: (ending) => {
          if (mine === attempt.current) {
            void ended(roomId, ending)
          }
        },
      })

      if (mine !== attempt.current) {
        await opened.connection.hangUp()
        return false
      }

      connection.current = opened.connection
      patch({
        phase: 'joined',
        microphone: opened.microphone,
        camera: false,
        problem: opened.microphone
          ? null
          : { roomId, message: 'Your microphone is not available, so nobody can hear you.' },
      })
      void readNames(roomId)
      return true
    } catch (error) {
      if (mine !== attempt.current || unauthenticated(error)) {
        return false
      }

      if (seated) {
        void api.leaveCall(roomId).catch(() => undefined)
      }

      current.current = null
      setState({
        ...quiet,
        problem: {
          roomId,
          message: seated ? "The call's media server could not be reached." : explainJoin(error),
        },
      })
      return false
    }
  }

  /*
  ended is a connection closing that this page did not close.

  Only a lost connection is tried again, once: Convia hands the same seat back
  with a new credential. Anything Convia or the media server did on purpose is
  said, and not undone.
  */
  async function ended(roomId: string, ending: Ending) {
    connection.current = null

    /*
    The media client closing its own connection is the page being left, and there
    is nothing to say to somebody who is leaving and nothing to rejoin. The media
    server reports the connection gone, which is how Convia learns of it.
    */
    if (ending === 'closed') {
      attempt.current++
      current.current = null
      setState(quiet)
      return
    }

    if (ending === 'lost') {
      if (!(await open(roomId))) {
        setState((was) => (was.problem !== null ? was : { ...quiet, problem: { roomId, message: 'The connection to the call was lost.' } }))
      }
      return
    }

    attempt.current++
    current.current = null
    setState({ ...quiet, problem: { roomId, message: endings[ending] } })
  }

  async function join(roomId: string) {
    if (current.current === roomId) {
      return
    }
    if (current.current !== null) {
      await leave()
    }
    await open(roomId)
  }

  async function leave() {
    const roomId = current.current
    const opened = connection.current

    attempt.current++
    connection.current = null
    current.current = null
    setState(quiet)

    /*
    Convia is told before the connection closes, and the order matters. The
    media server reports a closed connection too, and a report that reaches
    Convia first is evidence that the person went away without asking — so the
    call would be recorded as ended by nobody when this person asked to leave
    it. Convia closes the connection itself as it records the leaving; hanging
    up here is for when Convia could not be reached.
    */
    if (roomId !== null) {
      await api.leaveCall(roomId).catch((error: unknown) => {
        unauthenticated(error)
      })
    }
    await opened?.hangUp().catch(() => undefined)
  }

  async function remove(userId: string) {
    const roomId = current.current
    if (roomId === null) {
      return
    }
    try {
      await api.removeFromCall(roomId, userId)
      await readNames(roomId)
    } catch (error) {
      if (!unauthenticated(error)) {
        patch({ problem: { roomId, message: explainRemoval(error) } })
      }
    }
  }

  async function setMicrophone(on: boolean) {
    const opened = connection.current
    const roomId = current.current
    if (opened === null || roomId === null) {
      return
    }
    try {
      await opened.setMicrophone(on)
      patch({ microphone: on, problem: null })
    } catch {
      patch({ microphone: false, problem: { roomId, message: 'Your microphone is not available.' } })
    }
  }

  async function setCamera(on: boolean) {
    const opened = connection.current
    const roomId = current.current
    if (opened === null || roomId === null) {
      return
    }
    try {
      await opened.setCamera(on)
      patch({ camera: on, problem: null })
    } catch {
      patch({ camera: false, problem: { roomId, message: 'Your camera is not available.' } })
    }
  }

  /*
  Who is in the call is read again when Convia says it changed, and when the
  media server shows somebody Convia has not named yet — once per person, for
  the reason a conversation asks for its member list once per stranger.
  */
  const reread = useRef(readNames)
  reread.current = readNames

  useEffect(
    () =>
      listen((event) => {
        const roomId = current.current
        if (roomId !== null && event.type.startsWith('participant.') && event.data?.['room_id'] === roomId) {
          void reread.current(roomId)
        }
      }),
    [listen],
  )

  useEffect(() => {
    const roomId = state.roomId
    if (roomId === null) {
      return
    }
    const strangers = state.seen.filter(
      (person) => !person.local && !state.names.has(person.identity) && !asked.current.has(person.identity),
    )
    if (strangers.length === 0) {
      return
    }
    for (const person of strangers) {
      asked.current.add(person.identity)
    }
    void reread.current(roomId)
  }, [state.seen, state.names, state.roomId])

  // A page that goes away — signing out — hangs up rather than leaving a connection behind.
  useEffect(
    () => () => {
      attempt.current++
      void connection.current?.hangUp()
    },
    [],
  )

  return {
    ...state,
    join,
    leave,
    remove,
    setMicrophone,
    setCamera,
    dismiss: () => patch({ problem: null }),
  }
}
