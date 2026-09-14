import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, roomApi, sourceKey, type RoomSource } from '../api/client'
import type { Person } from '../api/types'
import { useEvents } from './events'

interface Members {
  members: Person[]
  loaded: boolean
  failed: boolean
  reload: () => void
}

/*
useMembers reads who is in a room, by name.

It does not poll. Who is in a room changes far less often than what is said in
it, so it is read when the conversation opens, when somebody looks at the list,
after somebody is added, when the event stream says somebody joined or left, and
when a message arrives from somebody it does not know — which is how a newcomer
is still noticed while the stream is down, and in a room on another
installation, which the stream never mentions.

A failure here is not the conversation's failure. Names make a conversation
easier to read; their absence does not make it unreadable, so this reports
`failed` and leaves the rest of the screen alone.
*/
export function useMembers(source: RoomSource, onExpired: () => void): Members {
  const { listen } = useEvents()

  const [members, setMembers] = useState<Person[]>([])
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const [reloads, setReloads] = useState(0)

  const expired = useRef(onExpired)
  expired.current = onExpired

  const reload = useCallback(() => setReloads((count) => count + 1), [])

  const key = sourceKey(source)
  const { kind, id } = source

  useEffect(() => {
    const controller = new AbortController()
    let active = true

    roomApi({ kind, id } as RoomSource)
      .members(controller.signal)
      .then((page) => {
        if (!active) {
          return
        }
        setMembers(page.data)
        setLoaded(true)
        setFailed(false)
      })
      .catch((error: unknown) => {
        if (!active || controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setFailed(true)
      })

    return () => {
      active = false
      controller.abort()
    }
  }, [kind, id, reloads])

  useEffect(() => {
    if (kind !== 'local') {
      return
    }
    return listen((event) => {
      const membership = event.type === 'room.member_added' || event.type === 'room.member_removed'
      if (membership && event.subject.id === id) {
        reload()
      }
    })
  }, [key, kind, id, listen, reload])

  return { members, loaded, failed, reload }
}
