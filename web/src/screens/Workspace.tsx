import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api/client'
import type { Account } from '../api/types'
import { Conversation } from '../components/Conversation'
import { Rail, type Mode } from '../components/Rail'
import { Sidebar } from '../components/Sidebar'
import { EventsContext, useEventStream } from '../state/events'
import { useRooms } from '../state/useRooms'

/*
refreshDelay gathers a burst of events into one read of the sidebar.

A busy room announces a message every moment, and the sidebar only needs to know
that something changed since it last looked. A quarter of a second is below
what anybody notices and above the gap between events in a burst.
*/
const refreshDelay = 250

/*
Workspace is the three zones: where in Convia, which conversation, and the
conversation itself.

The zones are a grid rather than nested flex boxes because they are peers — the
rail does not contain the sidebar, and the sidebar does not contain the stage —
and a layout that says so is one that can be rearranged for a narrow screen by
changing the grid alone, which is exactly what happens below `md`.

Narrow is the base and wide is the variant, because that is the direction
Tailwind's breakpoints run. The narrow layout is deliberately the crude one: the
room list sits above the conversation and is reached by scrolling rather than
from behind a drawer, because a drawer is a navigation model and this milestone
has not decided on one. What it must not do is overflow sideways.

It also holds the event stream, one for the page, and hands it down through
context: the sidebar, the open room, and its member list all listen to the same
connection rather than each opening one.
*/
export function Workspace({
  account,
  onSignedOut,
}: {
  account: Account
  onSignedOut: () => void
}) {
  const [mode, setMode] = useState<Mode>('chat')
  const [selected, setSelected] = useState<string | null>(null)

  const stream = useEventStream(onSignedOut)
  const { live, listen } = stream
  const { rooms, loading, failed, refresh, remember, forget } = useRooms(onSignedOut, live)

  const pending = useRef<number | undefined>(undefined)
  const refreshSoon = useCallback(() => {
    if (pending.current !== undefined) {
      return
    }

    pending.current = window.setTimeout(() => {
      pending.current = undefined
      refresh()
    }, refreshDelay)
  }, [refresh])

  useEffect(() => () => window.clearTimeout(pending.current), [])

  /*
  What the sidebar hears.

  Anything said or any change of membership may change a row, so each one asks
  for the list again, gathered. Losing one's own place is the exception worth
  acting on at once: the room goes before the read, and if it was open, nothing
  stays open that the person can no longer read.
  */
  useEffect(
    () =>
      listen((event) => {
        const aboutARoom = event.type.startsWith('message.') || event.type.startsWith('room.member_')
        if (!aboutARoom) {
          return
        }
        if (event.type === 'room.member_removed' && event.data?.['user_id'] === account.user_id) {
          const roomId = event.subject.id
          forget(roomId)
          setSelected((current) => (current === roomId ? null : current))
        }
        refreshSoon()
      }),
    [listen, account.user_id, forget, refreshSoon],
  )

  /*
  Something has to be open, and the first room is the only defensible guess
  until the interface remembers where somebody was.

  Only an empty choice is filled. A chosen room that is momentarily missing from
  the list — opened a moment before the read that will include it, or read by a
  request that started before it existed — is left chosen, and the conversation
  reappears when the list catches up rather than jumping somewhere else first.
  */
  useEffect(() => {
    if (selected === null && rooms.length > 0) {
      setSelected(rooms[0]?.id ?? null)
    }
  }, [rooms, selected])

  async function create(name: string) {
    const room = await api.createRoom(name)
    remember({ id: room.id, name: room.name, status: room.status, unread: 0 })
    setSelected(room.id)
    refresh()
  }

  async function leave(roomId: string) {
    await api.leave(roomId)
    const next = rooms.find((room) => room.id !== roomId)?.id ?? null
    forget(roomId)
    setSelected(next)
    refresh()
  }

  async function signOut() {
    try {
      await api.signOut()
    } finally {
      /*
      The interface returns to the sign-in form whatever the server said. The
      cookie is cleared by the response when there was one, and a sign-out that
      failed to reach Convia should still not leave somebody's conversations on
      the screen.
      */
      onSignedOut()
    }
  }

  const room = rooms.find((candidate) => candidate.id === selected) ?? null

  return (
    <EventsContext.Provider value={stream}>
      <div
        className="grid h-full grid-cols-[var(--rail-width)_minmax(0,1fr)]
          grid-rows-[auto_minmax(0,1fr)] bg-surface
          md:grid-cols-[var(--rail-width)_var(--sidebar-width)_minmax(0,1fr)] md:grid-rows-1"
      >
        <Rail
          mode={mode}
          onMode={setMode}
          displayName={account.display_name}
          onSignOut={() => void signOut()}
        />

        <aside
          className="col-start-2 row-start-1 flex max-h-[34vh] min-h-0 flex-col border-b border-line
            bg-surface-deep md:max-h-none md:border-r md:border-b-0"
          aria-label="Conversations"
        >
          <Sidebar
            rooms={rooms}
            selected={selected}
            loading={loading}
            onSelect={setSelected}
            onCreate={create}
          />
          {failed && (
            <p className="m-0 border-t border-line px-4 py-2 text-[0.75rem] text-ink-faint" role="status">
              Convia could not be reached. Retrying.
            </p>
          )}
        </aside>

        <main className="col-start-2 row-start-2 flex min-h-0 min-w-0 md:col-start-3 md:row-start-1">
          {room === null ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-1 text-ink-dim">
              <p className="m-0">Nothing is open.</p>
              <p className="m-0 text-[0.85rem] text-ink-faint">
                Pick a conversation on the left, or open a new one.
              </p>
            </div>
          ) : (
            <Conversation
              key={room.id}
              room={room}
              account={account}
              onExpired={onSignedOut}
              onActivity={refreshSoon}
              onLeave={leave}
            />
          )}
        </main>
      </div>
    </EventsContext.Provider>
  )
}
