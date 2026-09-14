import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, roomApi, sourceKey, type RoomSource } from '../api/client'
import type { Message } from '../api/types'
import { useEvents } from './events'

// historyInterval is how often an open room is asked for what is new while
// nothing will say what changed. See useRooms.
const historyInterval = 5_000

// window is how much history is read at once. Fifty is roughly two screens on a
// laptop, so the first paint is full and the second page is rarely needed.
const historyWindow = 50

/*
catchUpWindows bounds how far forward a room reads in one go.

Reading what is new is normally one request that comes back short. After the
stream was down for a while it may not be, and the reading continues — but not
without end, because a room that said more than five hundred things while
somebody was away is one they will scroll rather than read.
*/
const catchUpWindows = 10

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
Reader is how the open room is read, by whichever cause asked.

It is built once per room by the effect that owns the request lifecycle, and
called from the timer, from the event stream, and from reconnecting. Keeping it
in a ref is what lets those three live in effects of their own without each one
tearing down the room's history when it changes.
*/
interface Reader {
  // since reads everything newer than the newest message held.
  since: () => Promise<void>
  // at reads one message again, by its place in the room.
  at: (sequence: number) => Promise<void>
  // recent reads the newest window again, which is what shows edits and
  // withdrawals nobody was listening for.
  recent: () => Promise<void>
}

/*
useConversation reads one room and writes to it.

The room's history is requested newest-first, because that is the end a person
is looking at, and reversed here: Convia paginates backwards from the present,
and the screen reads forwards.

**A room on another installation is always read on a timer.** This page's event
stream is about rooms here: nothing announces what is said in a room elsewhere,
so there is nothing to wait for, and asking is the only way to find out.

`onRead` is called when Convia has accepted how far the person has read, which is
when the sidebar's count for this room changed.
*/
export function useConversation(
  source: RoomSource | null,
  onExpired: () => void,
  onRead: () => void = () => {},
): Conversation {
  const { live, listen } = useEvents()

  const [messages, setMessages] = useState<Message[]>([])
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)

  const expired = useRef(onExpired)
  expired.current = onExpired
  const read = useRef(onRead)
  read.current = onRead

  const key = source === null ? null : sourceKey(source)
  const remote = source?.kind === 'remote'
  const told = live && !remote

  /*
  The API is rebuilt from the key rather than taken from the source object, so
  that a parent drawing a fresh object for the same room does not restart every
  effect below.
  */
  const room = useRef(source === null ? null : roomApi(source))
  const current = useRef(key)
  if (current.current !== key) {
    current.current = key
    room.current = source === null ? null : roomApi(source)
  }

  /*
  The newest sequence seen, kept in a ref rather than in state.

  Reading what is new reads it and writes it every time. As state it would be a
  dependency of every effect that reads, so each arriving message would tear
  them down and build them again.
  */
  const newest = useRef(0)
  const reader = useRef<Reader | null>(null)

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

    const conversation = room.current
    if (key === null || conversation === null) {
      return
    }

    const controller = new AbortController()
    let mounted = true
    /*
    Nothing reads forward until the first window has arrived. Forward from
    nothing is the start of the room, and in a long history that would fetch its
    oldest messages rather than its newest.
    */
    let ready = false
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

    function refused(error: unknown) {
      if (mounted && !controller.signal.aborted) {
        fail(error)
      }
    }

    async function recent() {
      try {
        const page = await conversation!.history({ limit: historyWindow, direction: 'older' }, controller.signal)
        if (!mounted) {
          return
        }
        absorb(page.data)
        ready = true
        setFailed(false)
      } catch (error) {
        refused(error)
      } finally {
        if (mounted) {
          setLoading(false)
        }
      }
    }

    async function since() {
      if (!ready) {
        return
      }
      try {
        for (let window = 0; window < catchUpWindows; window++) {
          const page = await conversation!.history(
            { limit: historyWindow, direction: 'newer', cursor: String(newest.current) },
            controller.signal,
          )
          if (!mounted) {
            return
          }
          absorb(page.data)
          setFailed(false)
          if (page.data.length < historyWindow) {
            return
          }
        }
      } catch (error) {
        refused(error)
      }
    }

    async function at(sequence: number) {
      try {
        // The cursor is exclusive, so the message itself is the first after
        // the one before it.
        const page = await conversation!.history(
          { limit: 1, direction: 'newer', cursor: String(sequence - 1) },
          controller.signal,
        )
        if (mounted) {
          absorb(page.data)
        }
      } catch (error) {
        refused(error)
      }
    }

    reader.current = { since, at, recent }
    void recent()

    return () => {
      mounted = false
      controller.abort()
      reader.current = null
    }
  }, [key, fail])

  // Asking on a timer, whenever nothing will say what changed.
  useEffect(() => {
    if (key === null || told) {
      return
    }
    const timer = window.setInterval(() => void reader.current?.since(), historyInterval)
    return () => window.clearInterval(timer)
  }, [key, told])

  /*
  Catching up when the stream opens again.

  Whatever was said while it was down was announced to nobody, so it is read:
  forwards for what is new, then the newest window again for what was edited or
  withdrawn in the meantime.
  */
  const wasTold = useRef(told)
  useEffect(() => {
    if (told && !wasTold.current) {
      void reader.current?.since().then(() => reader.current?.recent())
    }
    wasTold.current = told
  }, [told])

  /*
  Being told.

  An event names a message and its place and carries nothing that was said, so
  each one is a read of exactly what it names: what is new after a post, and the
  one message after an edit or a withdrawal. Only rooms here are announced.
  */
  useEffect(() => {
    if (source === null || source.kind !== 'local') {
      return
    }
    const roomId = source.id
    return listen((event) => {
      if (event.data?.['room_id'] !== roomId) {
        return
      }
      if (event.type === 'message.posted') {
        void reader.current?.since()
        return
      }
      if (event.type === 'message.edited' || event.type === 'message.deleted') {
        const sequence = event.data['sequence']
        if (typeof sequence === 'number') {
          void reader.current?.at(sequence)
        }
      }
    })
    // The source object itself is not a dependency: its key is.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, listen])

  /*
  Reading is reported once the newest message has been rendered, and only ever
  forwards: Convia takes the greater of what it holds and what is marked, so a
  late request carrying an older position cannot un-read a room.
  */
  useEffect(() => {
    const conversation = room.current
    if (conversation === null || messages.length === 0) {
      return
    }
    const last = messages[messages.length - 1]
    if (last === undefined) {
      return
    }

    let active = true
    void conversation
      .markRead(last.sequence)
      .then(() => {
        if (active) {
          read.current()
        }
      })
      .catch((error: unknown) => {
        if (active) {
          fail(error)
        }
      })
    return () => {
      active = false
    }
  }, [key, messages, fail])

  const send = useCallback(
    async (body: string) => {
      const conversation = room.current
      if (conversation === null) {
        return
      }
      const message = await conversation.post(body)
      newest.current = Math.max(newest.current, message.sequence)
      setMessages((current) => merge(current, [message]))
    },
    [],
  )

  const edit = useCallback(async (messageId: string, body: string) => {
    const conversation = room.current
    if (conversation === null) {
      return
    }
    const message = await conversation.edit(messageId, body)
    setMessages((current) => merge(current, [message]))
  }, [])

  const withdraw = useCallback(async (messageId: string) => {
    const conversation = room.current
    if (conversation === null) {
      return
    }
    const message = await conversation.withdraw(messageId)
    setMessages((current) => merge(current, [message]))
  }, [])

  return { messages, loading, failed, send, edit, withdraw }
}
