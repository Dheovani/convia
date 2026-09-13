import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { SidebarRoom } from '../api/types'

/*
sidebarInterval is how often the room list is asked again while the event
stream is not open.

Fifteen seconds is slow enough to be unnoticeable in load and fast enough that a
badge is never stale for long. While the stream is open the list is read when an
event says it changed, and not on a timer at all.
*/
const sidebarInterval = 15_000

interface Rooms {
  rooms: SidebarRoom[]
  loading: boolean
  failed: boolean
  refresh: () => void
  /*
  remember and forget change the list before Convia is asked again.

  Opening a room and leaving one both change what the sidebar should show, and
  waiting for the next read would draw the old list for a moment after the
  person already acted. The read that follows reconciles either way.
  */
  remember: (room: SidebarRoom) => void
  forget: (roomId: string) => void
}

// byIdentifier keeps a locally remembered room where Convia would list it, so
// the row does not jump when the next read arrives.
function byIdentifier(left: SidebarRoom, right: SidebarRoom): number {
  if (left.id === right.id) {
    return 0
  }
  return left.id < right.id ? -1 : 1
}

/*
useRooms keeps the sidebar current.

`onExpired` is called when Convia says the session is gone, which is the one
failure that is not this view's to report: the whole interface has to go back to
the sign-in form, and a stale room list behind it would be somebody else's.

`live` says whether the event stream is open. The list is read again whenever it
changes, which is what catches up on anything that happened while the stream was
down — an event is not stored for a connection that was not there to receive it.
*/
export function useRooms(onExpired: () => void, live = false): Rooms {
  const [rooms, setRooms] = useState<SidebarRoom[]>([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)

  const expired = useRef(onExpired)
  expired.current = onExpired

  const [reloads, setReloads] = useState(0)
  const refresh = useCallback(() => setReloads((count) => count + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    let active = true

    async function load() {
      try {
        const page = await api.rooms(controller.signal)
        if (!active) {
          return
        }
        setRooms(page.data)
        setFailed(false)
      } catch (error) {
        if (!active || controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setFailed(true)
      } finally {
        if (active) {
          setLoading(false)
        }
      }
    }

    void load()
    const timer = live ? undefined : window.setInterval(() => void load(), sidebarInterval)

    return () => {
      active = false
      controller.abort()
      window.clearInterval(timer)
    }
  }, [reloads, live])

  const remember = useCallback((room: SidebarRoom) => {
    setRooms((current) =>
      current.some((known) => known.id === room.id)
        ? current
        : [...current, room].sort(byIdentifier),
    )
  }, [])

  const forget = useCallback((roomId: string) => {
    setRooms((current) => current.filter((known) => known.id !== roomId))
  }, [])

  return { rooms, loading, failed, refresh, remember, forget }
}
