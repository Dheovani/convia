import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'

import { api, ApiError } from '../api/client'
import type { ConviaEvent } from '../api/types'
import { inApplication, listen as listenToApplication } from '../desktop/bridge'

export type Listener = (event: ConviaEvent) => void

/*
Events is what the rest of the interface sees of the stream.

`live` is the one thing that changes how the screen behaves: while the stream is
open nothing needs to ask Convia on a timer, and while it is not, everything
asks as it did before there was a stream. Being told about something is an
improvement on asking, never a replacement for being able to ask.
*/
export interface Events {
  live: boolean
  listen: (listener: Listener) => () => void
}

/*
The default is a stream that never opens. A component rendered without the
workspace around it — in a test, or in a screen added later — therefore polls
exactly as it would if the connection had failed, rather than waiting forever
for events nothing will send.
*/
export const EventsContext = createContext<Events>({ live: false, listen: () => () => {} })

export function useEvents(): Events {
  return useContext(EventsContext)
}

// sessionEnded is the close code Convia sends when the session that opened the
// stream stops authenticating anybody. docs/events.md lists it.
const sessionEnded = 4001

// tooOld is the close code for a cursor older than the events Convia keeps.
const tooOld = 4002

/*
The wait before reconnecting starts at a second and doubles to half a minute.

Starting low is for the ordinary case, a deployment restarting an instance. The
ceiling is for the other one: every tab of every person reconnecting at once
into a Convia that is still down would be a load it does not need.
*/
const firstRetry = 1_000
const lastRetry = 30_000

/*
streamAddress is the person's stream on the origin this page came from, which is
the only origin Convia accepts a handshake from, resuming after a cursor when
there is one.
*/
export function streamAddress(location: Location, after?: string): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const resume = after === undefined ? '' : `?after=${encodeURIComponent(after)}`
  return `${scheme}//${location.host}/v1/me/events${resume}`
}

/*
useEventStream holds one connection to the person's stream for as long as the
workspace is open.

There is one per page rather than one per component. Each connection is a place
against a ceiling Convia keeps per person, and a room, its member list, and the
sidebar all want the same events.

**A refused handshake says nothing about why.** A browser hides the status of a
failed WebSocket upgrade from scripts, so an expired session looks exactly like
an unreachable server. Nothing here guesses: the stream retries, the screen falls
back to asking on a timer, and the next ordinary request is what notices a
session that is gone. The one reason Convia can still give is the close code
sent on a stream that was open, and that one is acted on.

**A reconnect resumes.** The cursor of the last event received is handed back,
so what happened while the connection was down arrives before anything new. A
cursor Convia no longer keeps closes the stream with its own code; the next
connection starts afresh, and the screens re-read as they do whenever the
stream comes back.
*/
export function useEventStream(onExpired: () => void): Events {
  const [live, setLive] = useState(false)
  const listeners = useRef(new Set<Listener>())

  const expired = useRef(onExpired)
  expired.current = onExpired

  const deliver = useCallback((event: ConviaEvent) => {
    for (const listener of listeners.current) {
      listener(event)
    }
  }, [])

  /*
  In Convia's own application the stream is not this page's to open.

  The session travels in a header there, and a page cannot set one on a
  handshake — so the application holds the connection and the events arrive
  here already read. Everything downstream is the same: the same events, in the
  same order, and the same `live` deciding whether a screen waits or asks.
  */
  useEffect(() => {
    if (!inApplication()) {
      return
    }

    return listenToApplication(
      (event) => deliver(event as ConviaEvent),
      (state) => {
        setLive(state.Live)
        if (!state.Ended) {
          return
        }
        /*
        Asked rather than assumed, exactly as the close code is in a browser:
        the answer to /me is what the rest of the interface treats as the
        authority on whether a session is gone, so both paths agree.
        */
        void api.me().catch((error: unknown) => {
          if (error instanceof ApiError && error.unauthenticated) {
            expired.current()
          }
        })
      },
    )
  }, [deliver])

  useEffect(() => {
    if (inApplication()) {
      return
    }

    let socket: WebSocket | null = null
    let retry: number | undefined
    let delay = firstRetry
    let stopped = false
    let cursor: string | undefined

    function connect() {
      const opening = new WebSocket(streamAddress(window.location, cursor))
      socket = opening

      opening.onopen = () => {
        delay = firstRetry
        setLive(true)
      }

      opening.onmessage = (message: MessageEvent) => {
        let event: ConviaEvent
        try {
          event = JSON.parse(String(message.data)) as ConviaEvent
        } catch {
          return
        }

        if (event.cursor !== undefined) {
          cursor = event.cursor
        }

        deliver(event)
      }

      opening.onclose = (closed: CloseEvent) => {
        socket = null
        setLive(false)
        if (stopped) {
          return
        }

        if (closed.code === tooOld) {
          cursor = undefined
        }

        if (closed.code === sessionEnded) {
          /*
          Asked rather than assumed. The code says the session ended, and the
          answer to /me is what the rest of the interface already treats as the
          authority on that, so both paths to the sign-in form agree.
          */
          void api.me().catch((error: unknown) => {
            if (error instanceof ApiError && error.unauthenticated) {
              expired.current()
            }
          })
        }

        retry = window.setTimeout(connect, delay)
        delay = Math.min(delay * 2, lastRetry)
      }
    }

    connect()

    return () => {
      stopped = true
      window.clearTimeout(retry)
      socket?.close()
    }
  }, [deliver])

  const listen = useCallback((listener: Listener) => {
    listeners.current.add(listener)
    return () => {
      listeners.current.delete(listener)
    }
  }, [])

  return useMemo(() => ({ live, listen }), [live, listen])
}
