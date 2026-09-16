import { createContext, useContext, useEffect, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { CallPresence } from '../api/types'
import {
  connect,
  listDevices,
  noDevices,
  openPreview,
  type Choice,
  type Connection,
  type DeviceKind,
  type Devices,
  type Ending,
  type Preview,
  type Seen,
} from '../media/connection'
import type { Listener } from './events'
import { defaultChoice, remember, remembered } from './preferences'

export type Phase = 'idle' | 'joining' | 'joined'

// Notice is something that happened in the call, said once and then gone.
export interface Notice {
  id: number
  text: string
}

// noticeLifetime is how long somebody arriving or leaving stays said on screen.
const noticeLifetime = 5_000

interface Snapshot {
  // roomId is the room whose call this person is in, or joining.
  roomId: string | null
  phase: Phase
  seen: Seen[]
  // names are the people in the call by participant, which is who the media server says they are.
  names: ReadonlyMap<string, CallPresence>
  microphone: boolean
  camera: boolean
  // reconnecting is true while the media client is getting a dropped connection back.
  reconnecting: boolean
  // problem is the last thing worth telling the person, about the room it names.
  problem: { roomId: string; message: string } | null
  notices: Notice[]
  // preparing is the room whose call this person is getting ready to join.
  preparing: string | null
  preview: Preview | null
  choice: Choice
  devices: Devices
}

export interface CallSession extends Snapshot {
  prepare: (roomId: string) => Promise<void>
  cancelPreparing: (roomId: string) => void
  choose: (change: Partial<Choice>) => Promise<void>
  join: (roomId: string) => Promise<void>
  leave: () => Promise<void>
  remove: (userId: string) => Promise<void>
  setMicrophone: (on: boolean) => Promise<void>
  setCamera: (on: boolean) => Promise<void>
  switchDevice: (kind: DeviceKind, id: string) => Promise<void>
  refreshDevices: () => Promise<void>
  dismiss: () => void
}

type CallPart = Pick<Snapshot, 'roomId' | 'phase' | 'seen' | 'names' | 'microphone' | 'camera' | 'reconnecting'>

const outOfCall: CallPart = {
  roomId: null,
  phase: 'idle',
  seen: [],
  names: new Map(),
  microphone: false,
  camera: false,
  reconnecting: false,
}

/*
The default is a call nobody can join. A component rendered outside the workspace
— in a test, or a screen added later — shows no call rather than failing.
*/
export const CallContext = createContext<CallSession>({
  ...outOfCall,
  problem: null,
  notices: [],
  preparing: null,
  preview: null,
  choice: defaultChoice,
  devices: noDevices,
  prepare: async () => {},
  cancelPreparing: () => {},
  choose: async () => {},
  join: async () => {},
  leave: async () => {},
  remove: async () => {},
  setMicrophone: async () => {},
  setCamera: async () => {},
  switchDevice: async () => {},
  refreshDevices: async () => {},
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

// unavailable says which device the person wanted and is in the call without.
function unavailable(roomId: string, wanted: Choice, got: { microphone: boolean; camera: boolean }) {
  if (wanted.microphone && !got.microphone) {
    return { roomId, message: 'Your microphone is not available, so nobody can hear you.' }
  }
  if (wanted.camera && !got.camera) {
    return { roomId, message: 'Your camera is not available, so nobody can see you.' }
  }
  return null
}

function nameOf(present: CallPresence): string {
  return present.display_name || 'Somebody'
}

/*
useCallSession holds the one call this page is in, and getting ready to join one.

It lives in the workspace rather than in a conversation, because a call goes on
while its person reads another room: the conversation that shows it can go away
and the call must not.

A join is an attempt, numbered, and so is opening a preview. Everything an attempt
learns later — a connection opening, a person arriving, a device being granted —
is dropped if a newer attempt, or leaving, has happened since, so a slow answer
about the call somebody already left cannot pull them back into it.
*/
export function useCallSession(onExpired: () => void, listen: (listener: Listener) => () => void): CallSession {
  const [state, setState] = useState<Snapshot>(() => ({
    ...outOfCall,
    problem: null,
    notices: [],
    preparing: null,
    preview: null,
    choice: remembered(),
    devices: noDevices,
  }))

  const connection = useRef<Connection | null>(null)
  const current = useRef<string | null>(null)
  const attempt = useRef(0)
  const asked = useRef(new Set<string>())

  const preparing = useRef<string | null>(null)
  const preview = useRef<Preview | null>(null)
  const preparation = useRef(0)
  const choice = useRef(state.choice)

  const names = useRef<ReadonlyMap<string, CallPresence>>(new Map())
  const awaiting = useRef(new Set<string>())
  const noticeCount = useRef(0)

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

  function notify(text: string) {
    const id = ++noticeCount.current
    setState((was) => ({ ...was, notices: [...was.notices, { id, text }].slice(-3) }))
    window.setTimeout(() => {
      setState((was) => ({ ...was, notices: was.notices.filter((notice) => notice.id !== id) }))
    }, noticeLifetime)
  }

  function outOfTheCall(problem: Snapshot['problem']) {
    connection.current = null
    current.current = null
    names.current = new Map()
    awaiting.current = new Set()
    patch({ ...outOfCall, problem })
  }

  async function readNames(roomId: string) {
    try {
      const page = await api.callParticipants(roomId)
      if (current.current !== roomId) {
        return
      }

      const named = new Map(page.data.map((present) => [present.participant_id, present]))
      names.current = named
      patch({ names: named })

      for (const identity of [...awaiting.current]) {
        const present = named.get(identity)
        if (present !== undefined) {
          awaiting.current.delete(identity)
          notify(`${nameOf(present)} joined the call.`)
        }
      }
    } catch (error) {
      unauthenticated(error)
    }
  }

  /*
  Somebody arriving is said by name. The media server shows them before Convia's
  list of who is in the call may name them, so an unnamed arrival waits for the
  list to be read again rather than being said as nobody.
  */
  function arrived(identity: string) {
    const present = names.current.get(identity)
    if (present !== undefined) {
      notify(`${nameOf(present)} joined the call.`)
      return
    }
    awaiting.current.add(identity)
    if (current.current !== null) {
      void readNames(current.current)
    }
  }

  function departed(identity: string) {
    if (awaiting.current.delete(identity)) {
      return
    }
    const present = names.current.get(identity)
    if (present !== undefined) {
      notify(`${nameOf(present)} left the call.`)
    }
  }

  async function refreshDevices() {
    try {
      patch({ devices: await listDevices() })
    } catch {
      // The list keeps what it had.
    }
  }

  function closePreview() {
    preparation.current++
    preview.current?.stop()
    preview.current = null
  }

  async function showPreview() {
    const mine = ++preparation.current
    preview.current?.stop()
    preview.current = null
    patch({ preview: null })

    const opened = await openPreview(choice.current)
    if (mine !== preparation.current) {
      opened.stop()
      return
    }

    preview.current = opened
    patch({ preview: opened })
    await refreshDevices()
  }

  /*
  prepare opens the preview for a room's call. Nothing is joined, and nothing is
  asked of Convia, until the person presses join.
  */
  async function prepare(roomId: string) {
    if (current.current === roomId) {
      return
    }
    preparing.current = roomId
    patch({ preparing: roomId, problem: null })
    await showPreview()
  }

  function cancelPreparing(roomId: string) {
    if (preparing.current !== roomId) {
      return
    }
    preparing.current = null
    closePreview()
    patch({ preparing: null, preview: null })
  }

  /*
  choose changes what the person wants and remembers it in this browser. While they
  are getting ready, the preview is opened again with the new choice, so what they
  see is what the call will get.
  */
  async function choose(change: Partial<Choice>) {
    const next: Choice = { ...choice.current, ...change }
    choice.current = next
    remember(next)
    patch({ choice: next })

    if (preparing.current !== null) {
      await showPreview()
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
    awaiting.current = new Set()
    names.current = new Map()
    patch({ roomId, phase: 'joining', seen: [], names: new Map(), problem: null, reconnecting: false })

    let seated = false
    try {
      const session = await api.joinCall(roomId)
      seated = true
      if (mine !== attempt.current) {
        return false
      }

      const wanted = choice.current
      const opened = await connect(session.media_url, session.media_token, wanted, {
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
        onReconnecting: (reconnecting) => {
          if (mine === attempt.current) {
            patch({ reconnecting })
          }
        },
        onArrived: (identity) => {
          if (mine === attempt.current) {
            arrived(identity)
          }
        },
        onDeparted: (identity) => {
          if (mine === attempt.current) {
            departed(identity)
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
        camera: opened.camera,
        problem: unavailable(roomId, wanted, opened),
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

      outOfTheCall({
        roomId,
        message: seated ? "The call's media server could not be reached." : explainJoin(error),
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
      outOfTheCall(null)
      return
    }

    if (ending === 'lost') {
      if (!(await open(roomId))) {
        setState((was) =>
          was.problem !== null ? was : { ...was, problem: { roomId, message: 'The connection to the call was lost.' } },
        )
      }
      return
    }

    attempt.current++
    outOfTheCall({ roomId, message: endings[ending] })
  }

  // join closes the preview, which holds the devices the call is about to open, and joins.
  async function join(roomId: string) {
    if (current.current === roomId) {
      return
    }
    if (preparing.current !== null) {
      preparing.current = null
      closePreview()
      patch({ preparing: null, preview: null })
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
    outOfTheCall(null)

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

  /*
  Turning the microphone or camera on and off inside a call changes that call and
  nothing else. What a call starts with is decided before joining, where the choice
  is remembered; a mute in the middle of one is not a preference.
  */
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
  switchDevice changes a device, in the call when there is one, and remembers it:
  somebody who picked their headset mid-call wants it next time too.
  */
  async function switchDevice(kind: DeviceKind, id: string) {
    const opened = connection.current
    const roomId = current.current
    if (opened !== null && roomId !== null) {
      try {
        await opened.switchDevice(kind, id)
      } catch {
        patch({ problem: { roomId, message: 'That device could not be used.' } })
        return
      }
    }

    const change: Partial<Choice> =
      kind === 'audioinput' ? { audioInput: id } : kind === 'videoinput' ? { videoInput: id } : { audioOutput: id }
    await choose(change)
  }

  /*
  Who is in the call is read again when Convia says it changed, and when the
  media server shows somebody Convia has not named yet — once per person, for
  the reason a conversation asks for its member list once per stranger.
  */
  const latest = useRef({ readNames, refreshDevices, closePreview })
  latest.current = { readNames, refreshDevices, closePreview }

  useEffect(
    () =>
      listen((event) => {
        const roomId = current.current
        if (roomId !== null && event.type.startsWith('participant.') && event.data?.['room_id'] === roomId) {
          void latest.current.readNames(roomId)
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
    void latest.current.readNames(roomId)
  }, [state.seen, state.names, state.roomId])

  // A device plugged in or taken out changes the lists the person chooses from.
  useEffect(() => {
    const media = navigator.mediaDevices
    const changed = () => void latest.current.refreshDevices()
    media?.addEventListener('devicechange', changed)
    return () => media?.removeEventListener('devicechange', changed)
  }, [])

  // A page that goes away — signing out — hangs up and lets go of its devices.
  useEffect(
    () => () => {
      attempt.current++
      latest.current.closePreview()
      void connection.current?.hangUp()
    },
    [],
  )

  return {
    ...state,
    prepare,
    cancelPreparing,
    choose,
    join,
    leave,
    remove,
    setMicrophone,
    setCamera,
    switchDevice,
    refreshDevices,
    dismiss: () => patch({ problem: null }),
  }
}
