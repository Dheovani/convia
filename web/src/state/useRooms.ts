import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { SidebarRoom } from '../api/types'

/*
sidebarInterval is how often the room list is asked again.

Convia has a real-time event stream, but it is on the surface an *application*
reaches with its key, not the one a person reaches with a cookie: there is no
way for this page to subscribe to its own rooms yet. Until there is, the sidebar
polls, and the interval is the honest cost of that — fifteen seconds is slow
enough to be unnoticeable in load and fast enough that a badge is never stale
for long.
*/
const sidebarInterval = 15_000

interface Rooms {
  rooms: SidebarRoom[]
  loading: boolean
  failed: boolean
  refresh: () => void
}

/*
useRooms keeps the sidebar current.

`onExpired` is called when Convia says the session is gone, which is the one
failure that is not this view's to report: the whole interface has to go back to
the sign-in form, and a stale room list behind it would be somebody else's.
*/
export function useRooms(onExpired: () => void): Rooms {
  const [rooms, setRooms] = useState<SidebarRoom[]>([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)

  const expired = useRef(onExpired)
  expired.current = onExpired

  const [reloads, setReloads] = useState(0)
  const refresh = useCallback(() => setReloads((count) => count + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    let live = true

    async function load() {
      try {
        const page = await api.rooms(controller.signal)
        if (!live) {
          return
        }
        setRooms(page.data)
        setFailed(false)
      } catch (error) {
        if (!live || controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setFailed(true)
      } finally {
        if (live) {
          setLoading(false)
        }
      }
    }

    void load()
    const timer = window.setInterval(() => void load(), sidebarInterval)

    return () => {
      live = false
      controller.abort()
      window.clearInterval(timer)
    }
  }, [reloads])

  return { rooms, loading, failed, refresh }
}
