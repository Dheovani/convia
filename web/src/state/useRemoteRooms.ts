import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { RemoteRoom } from '../api/types'

interface RemoteRooms {
  remoteRooms: RemoteRoom[]
  remember: (room: RemoteRoom) => void
  forget: (remoteRoomId: string) => void
}

/*
useRemoteRooms keeps the list of rooms this person is in on other installations.

It is read once and not polled. The list is a set of pointers this installation
keeps, and it changes only when this person joins or leaves one — both of which
happen on this page, which updates it directly. Nothing about it is fetched from
the other installations, so there is nothing there to wait for either.

A failure to read it leaves it empty. The person's own rooms are the ones that
must work; rooms elsewhere appearing a reload later is a lesser failure than a
sidebar that reports an error about something they may not use.
*/
export function useRemoteRooms(onExpired: () => void): RemoteRooms {
  const [remoteRooms, setRemoteRooms] = useState<RemoteRoom[]>([])

  const expired = useRef(onExpired)
  expired.current = onExpired

  useEffect(() => {
    const controller = new AbortController()

    api
      .remoteRooms(controller.signal)
      .then((page) => setRemoteRooms(page.data))
      .catch((error: unknown) => {
        if (!controller.signal.aborted && error instanceof ApiError && error.unauthenticated) {
          expired.current()
        }
      })

    return () => controller.abort()
  }, [])

  const remember = useCallback((room: RemoteRoom) => {
    setRemoteRooms((current) => [...current.filter((known) => known.id !== room.id), room])
  }, [])

  const forget = useCallback((remoteRoomId: string) => {
    setRemoteRooms((current) => current.filter((known) => known.id !== remoteRoomId))
  }, [])

  return { remoteRooms, remember, forget }
}
