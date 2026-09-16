import { useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { PresenceState } from '../api/types'
import { rememberedStatus, rememberStatus, type Status } from './preferences'

/*
heartbeatInterval is how often a page says its person is still there. It is well
inside the lifetime each heartbeat asks for, so a missed one or two change nothing.
*/
export const heartbeatInterval = 25_000

/*
idleAfter is how long a page may go untouched before it says its person is away.
A page in a call is never idle: somebody listening is there without touching it.
*/
export const idleAfter = 5 * 60_000

// readInterval is how often the presence of the people on screen is read again.
export const readInterval = 20_000

const deviceKey = 'convia.device'

/*
deviceId names this page as a device. It is kept for the tab rather than the
browser, so two tabs are two devices and closing one takes only itself away, and
a reload is the same device rather than a new one.
*/
function deviceId(): string {
  try {
    const kept = window.sessionStorage.getItem(deviceKey)
    if (kept !== null) {
      return kept
    }
    const made = `page-${crypto.randomUUID()}`
    window.sessionStorage.setItem(deviceKey, made)
    return made
  } catch {
    return `page-${crypto.randomUUID()}`
  }
}

const interactions = ['pointerdown', 'pointermove', 'keydown', 'wheel', 'touchstart'] as const

/*
useOwnPresence says where this person is, from this page, for as long as it is
open, and lets them choose.

What is said is the person's choice, except that a page nobody has touched for a
while says away when they chose available. Busy and away are said as chosen, and
the choice is kept in this browser.
*/
export function useOwnPresence(onExpired: () => void, inCall: boolean) {
  const [status, setStatus] = useState<Status>(rememberedStatus)
  const [idle, setIdle] = useState(false)
  const device = useRef(deviceId())

  const expired = useRef(onExpired)
  expired.current = onExpired
  const calling = useRef(inCall)
  calling.current = inCall

  // Any interaction makes the page active again, and starts the wait for idleness afresh.
  useEffect(() => {
    let timer: number | undefined
    const wake = () => {
      setIdle(false)
      window.clearTimeout(timer)
      timer = window.setTimeout(() => setIdle(!calling.current), idleAfter)
    }
    wake()
    for (const kind of interactions) {
      window.addEventListener(kind, wake, { passive: true })
    }
    return () => {
      window.clearTimeout(timer)
      for (const kind of interactions) {
        window.removeEventListener(kind, wake)
      }
    }
  }, [])

  useEffect(() => {
    if (inCall) {
      setIdle(false)
    }
  }, [inCall])

  const said: Exclude<PresenceState, 'offline'> = status === 'online' && idle ? 'away' : status

  useEffect(() => {
    const id = device.current
    const tell = () =>
      void api.assertPresence(id, said).catch((error: unknown) => {
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
        }
      })
    tell()
    const timer = window.setInterval(tell, heartbeatInterval)
    return () => window.clearInterval(timer)
  }, [said])

  // A page that closes, or signs out, says so rather than waiting for the timer.
  useEffect(() => {
    const id = device.current
    const leave = () => void api.withdrawPresence(id).catch(() => undefined)
    window.addEventListener('pagehide', leave)
    return () => {
      window.removeEventListener('pagehide', leave)
      leave()
    }
  }, [])

  function choose(next: Status) {
    rememberStatus(next)
    setStatus(next)
  }

  return { status, said, choose }
}

/*
usePresenceOf reads the presence of the people on screen, and reads it again
every few seconds while they are. Somebody Convia does not answer for — a visitor,
or somebody no longer sharing a room — is absent from what comes back.
*/
export function usePresenceOf(userIds: readonly string[]): ReadonlyMap<string, PresenceState> {
  const [states, setStates] = useState<ReadonlyMap<string, PresenceState>>(new Map())
  const key = [...new Set(userIds)].sort().join(',')

  useEffect(() => {
    if (key === '') {
      setStates(new Map())
      return
    }
    const ids = key.split(',')
    let controller = new AbortController()

    const read = () => {
      controller.abort()
      controller = new AbortController()
      api.peoplePresence(ids, controller.signal).then(
        (list) => setStates(new Map(list.data.map((presence) => [presence.user_id, presence.state]))),
        () => undefined,
      )
    }
    read()
    const timer = window.setInterval(read, readInterval)
    return () => {
      window.clearInterval(timer)
      controller.abort()
    }
  }, [key])

  return states
}
