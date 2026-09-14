import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { RoomCall } from '../api/types'
import type { Listener } from './events'

// callsInterval is how often the running calls are asked again while the event
// stream is not open, for the reason the room list gives.
const callsInterval = 15_000

interface Calls {
  calls: RoomCall[]
  refresh: () => void
}

/*
useCalls keeps the list of calls running in this person's rooms.

`call.started` and `call.ended` say when to read it again. A read that fails for
any reason but a session that is gone keeps what was there: an old list of calls
is a smaller wrong than an empty one, and the next read corrects it.
*/
export function useCalls(
  onExpired: () => void,
  live: boolean,
  listen: (listener: Listener) => () => void,
): Calls {
  const [calls, setCalls] = useState<RoomCall[]>([])

  const expired = useRef(onExpired)
  expired.current = onExpired

  const [reloads, setReloads] = useState(0)
  const refresh = useCallback(() => setReloads((count) => count + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    let active = true

    async function load() {
      try {
        const running = await api.calls(controller.signal)
        if (active) {
          setCalls(running.data)
        }
      } catch (error) {
        if (active && error instanceof ApiError && error.unauthenticated) {
          expired.current()
        }
      }
    }

    void load()
    const timer = live ? undefined : window.setInterval(() => void load(), callsInterval)

    return () => {
      active = false
      controller.abort()
      window.clearInterval(timer)
    }
  }, [reloads, live])

  useEffect(
    () =>
      listen((event) => {
        if (event.type === 'call.started' || event.type === 'call.ended') {
          refresh()
        }
      }),
    [listen, refresh],
  )

  return { calls, refresh }
}
