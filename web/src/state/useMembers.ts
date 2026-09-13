import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { Person } from '../api/types'

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
after somebody is added, and when a message arrives from somebody it does not
know — which is how a newcomer is noticed without asking every few seconds.

A failure here is not the conversation's failure. Names make a conversation
easier to read; their absence does not make it unreadable, so this reports
`failed` and leaves the rest of the screen alone.
*/
export function useMembers(roomId: string, onExpired: () => void): Members {
  const [members, setMembers] = useState<Person[]>([])
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const [reloads, setReloads] = useState(0)

  const expired = useRef(onExpired)
  expired.current = onExpired

  const reload = useCallback(() => setReloads((count) => count + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    let live = true

    api
      .members(roomId, controller.signal)
      .then((page) => {
        if (!live) {
          return
        }
        setMembers(page.data)
        setLoaded(true)
        setFailed(false)
      })
      .catch((error: unknown) => {
        if (!live || controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setFailed(true)
      })

    return () => {
      live = false
      controller.abort()
    }
  }, [roomId, reloads])

  return { members, loaded, failed, reload }
}
