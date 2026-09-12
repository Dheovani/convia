import { useCallback, useEffect, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { Message } from '../api/types'

// historyInterval is how often an open room is asked for what is new. See the
// note in useRooms: a person has no event stream to subscribe to yet.
const historyInterval = 5_000

// window is how much history is read at once. Fifty is roughly two screens on a
// laptop, so the first paint is full and the second page is rarely needed.
const historyWindow = 50

interface Conversation {
  messages: Message[]
  loading: boolean
  failed: boolean
  send: (body: string) => Promise<void>
  edit: (messageId: string, body: string) => Promise<void>
  withdraw: (messageId: string) => Promise<void>
}

/*
merge folds one message into the list, in sequence order.

Every operation on this surface answers with the message it produced, so the
list is updated from the response rather than reloaded. Editing and withdrawing
keep a message's sequence, so the same fold covers all three: a message already
present is replaced where it is, and a new one is appended.
*/
function merge(messages: Message[], arriving: Message[]): Message[] {
  if (arriving.length === 0) {
    return messages
  }

  const byId = new Map(messages.map((message) => [message.id, message]))
  for (const message of arriving) {
    byId.set(message.id, message)
  }

  return [...byId.values()].sort((left, right) => left.sequence - right.sequence)
}

/*
useConversation reads one room and writes to it.

The room's history is requested newest-first, because that is the end a person
is looking at, and reversed here: Convia paginates backwards from the present,
and the screen reads forwards.
*/
export function useConversation(roomId: string | null, onExpired: () => void): Conversation {
  const [messages, setMessages] = useState<Message[]>([])
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)

  const expired = useRef(onExpired)
  expired.current = onExpired

  /*
  The newest sequence seen, kept in a ref rather than in state.

  Polling reads it and writes it on every tick. As state it would be a
  dependency of the effect that polls, so every arriving message would tear the
  interval down and build a new one.
  */
  const newest = useRef(0)

  const fail = useCallback((error: unknown) => {
    if (error instanceof ApiError && error.unauthenticated) {
      expired.current()
      return
    }
    setFailed(true)
  }, [])

  useEffect(() => {
    setMessages([])
    newest.current = 0

    if (roomId === null) {
      return
    }

    const controller = new AbortController()
    let live = true
    setLoading(true)
    setFailed(false)

    function absorb(arriving: Message[]) {
      if (arriving.length === 0) {
        return
      }
      for (const message of arriving) {
        newest.current = Math.max(newest.current, message.sequence)
      }
      setMessages((current) => merge(current, arriving))
    }

    async function first() {
      try {
        const page = await api.history(
          roomId as string,
          { limit: historyWindow, direction: 'older' },
          controller.signal,
        )
        if (!live) {
          return
        }
        absorb(page.data)
        setFailed(false)
      } catch (error) {
        if (live && !controller.signal.aborted) {
          fail(error)
        }
      } finally {
        if (live) {
          setLoading(false)
        }
      }
    }

    async function since() {
      if (newest.current === 0) {
        return
      }
      try {
        const page = await api.history(
          roomId as string,
          { limit: historyWindow, direction: 'newer', cursor: String(newest.current) },
          controller.signal,
        )
        if (live) {
          absorb(page.data)
        }
      } catch (error) {
        if (live && !controller.signal.aborted) {
          fail(error)
        }
      }
    }

    void first()
    const timer = window.setInterval(() => void since(), historyInterval)

    return () => {
      live = false
      controller.abort()
      window.clearInterval(timer)
    }
  }, [roomId, fail])

  /*
  Reading is reported once the newest message has been rendered, and only ever
  forwards: Convia takes the greater of what it holds and what is marked, so a
  late request carrying an older position cannot un-read a room.
  */
  useEffect(() => {
    if (roomId === null || messages.length === 0) {
      return
    }
    const last = messages[messages.length - 1]
    if (last === undefined) {
      return
    }

    let live = true
    void api.markRead(roomId, last.sequence).catch((error: unknown) => {
      if (live) {
        fail(error)
      }
    })
    return () => {
      live = false
    }
  }, [roomId, messages, fail])

  const send = useCallback(
    async (body: string) => {
      if (roomId === null) {
        return
      }
      const message = await api.post(roomId, body)
      newest.current = Math.max(newest.current, message.sequence)
      setMessages((current) => merge(current, [message]))
    },
    [roomId],
  )

  const edit = useCallback(async (messageId: string, body: string) => {
    const message = await api.edit(messageId, body)
    setMessages((current) => merge(current, [message]))
  }, [])

  const withdraw = useCallback(async (messageId: string) => {
    const message = await api.withdraw(messageId)
    setMessages((current) => merge(current, [message]))
  }, [])

  return { messages, loading, failed, send, edit, withdraw }
}
