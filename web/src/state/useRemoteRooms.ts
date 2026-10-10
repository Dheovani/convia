import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { RemoteRoom } from '../api/types'

interface RemoteRooms {
  remoteRooms: RemoteRoom[]
  refresh: () => void
  remember: (room: RemoteRoom) => void
  forget: (remoteRoomId: string) => void
}

/*
useRemoteRooms keeps the list of rooms this person is in on other installations.

It is read again rather than polled. Each row carries what its home said about
it — how much is unread, and whether a call is running — and a pointer can go
without anybody here doing anything, when a home takes this person out of the
room. So the list is read again whenever the stream carries something about a
room, which is the same signal the rooms here are read again on, and nothing
asks on a timer.

**A change made here outranks a read already on its way.** Forgetting a room or
joining one changes the list at once, and a read that left before that change
would land after it carrying the list from before -- putting back the room just
forgotten, or taking away the one just joined, until the next read. So every
change here abandons the read in flight and reads again, and the answer the
list ends with was asked for after the change.

A failure to read it leaves what was there. An old list is a smaller wrong than
an empty one, and the next read corrects it: the person's own rooms are the ones
that must work, and a sidebar that reports an error about something they may not
use is worse than a badge that is a moment out of date.
*/
export function useRemoteRooms(onExpired: () => void): RemoteRooms {
  const [remoteRooms, setRemoteRooms] = useState<RemoteRoom[]>([])

  const expired = useRef(onExpired)
  expired.current = onExpired

  const [reloads, setReloads] = useState(0)
  const refresh = useCallback(() => setReloads((count) => count + 1), [])

  useEffect(() => {
    const controller = new AbortController()

    api
      .remoteRooms(controller.signal)
      .then((page) => {
        // Abandoned reads are dropped here as well as cancelled, because an
        // answer can already be in hand when the abort arrives.
        if (!controller.signal.aborted) {
          setRemoteRooms(page.data)
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted && error instanceof ApiError && error.unauthenticated) {
          expired.current()
        }
      })

    return () => controller.abort()
  }, [reloads])

  const remember = useCallback(
    (room: RemoteRoom) => {
      setRemoteRooms((current) => [...current.filter((known) => known.id !== room.id), room])
      refresh()
    },
    [refresh],
  )

  const forget = useCallback(
    (remoteRoomId: string) => {
      setRemoteRooms((current) => current.filter((known) => known.id !== remoteRoomId))
      refresh()
    },
    [refresh],
  )

  return { remoteRooms, refresh, remember, forget }
}
