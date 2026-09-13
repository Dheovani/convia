import { useState } from 'react'

import { ApiError, NetworkError } from '../api/client'
import type { SidebarRoom } from '../api/types'
import { Button, input } from './controls'

interface SidebarProps {
  rooms: SidebarRoom[]
  selected: string | null
  loading: boolean
  onSelect: (roomId: string) => void
  onCreate: (name: string) => Promise<void>
}

function explain(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError) {
    return error.status === 403 ? 'You cannot open rooms right now.' : error.message
  }
  return 'The room could not be opened.'
}

/*
NewRoom asks for a name, and only a name.

That is not a simplification of a fuller form. Convia accepts nothing else from a
person opening a room — an alias, metadata and a capacity are the application's
to decide — so a field for any of them would be a field whose every value is
refused.
*/
function NewRoom({ onCreate, onDone }: { onCreate: (name: string) => Promise<void>; onDone: () => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    const written = name.trim()
    if (written === '' || busy) {
      return
    }

    setBusy(true)
    setFailure(null)
    try {
      await onCreate(written)
      onDone()
    } catch (error) {
      // What was typed stays, for the reason the composer keeps a message.
      setFailure(explain(error))
      setBusy(false)
    }
  }

  return (
    <form
      className="mx-1 mb-3 flex flex-col gap-2"
      onSubmit={submit}
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          onDone()
        }
      }}
    >
      <label className="sr-only" htmlFor="new-room-name">
        Conversation name
      </label>
      <input
        id="new-room-name"
        className={input}
        autoFocus
        maxLength={120}
        placeholder="Name it"
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      {failure !== null && (
        <p className="m-0 text-[0.78rem] text-danger" role="alert">
          {failure}
        </p>
      )}
      <div className="flex gap-2">
        <Button tone="primary" size="small" type="submit" disabled={busy || name.trim() === ''}>
          {busy ? 'Opening…' : 'Create'}
        </Button>
        <Button size="small" disabled={busy} onClick={onDone}>
          Cancel
        </Button>
      </div>
    </form>
  )
}

/*
The sidebar is the second zone: which conversation.

Every row carries its unread count, because Convia answers both in one request.
A count fetched per row would be a request per line on screen, and this is the
view that is redrawn most.
*/
export function Sidebar({ rooms, selected, loading, onSelect, onCreate }: SidebarProps) {
  const [creating, setCreating] = useState(false)

  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
      <div className="mx-2 mb-3 flex items-center justify-between gap-2">
        <h2
          className="m-0 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint
            uppercase"
        >
          Conversations
        </h2>
        {!creating && (
          <button
            type="button"
            className="cursor-pointer rounded-sm px-1.5 py-0.5 text-[0.75rem] font-medium text-ink-dim
              hover:bg-surface-hover hover:text-ink"
            aria-label="New conversation"
            onClick={() => setCreating(true)}
          >
            + New
          </button>
        )}
      </div>

      {creating && <NewRoom onCreate={onCreate} onDone={() => setCreating(false)} />}

      {loading && rooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] text-ink-faint">Loading…</p>
      ) : rooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] leading-relaxed text-ink-faint">
          You are not in any conversation yet. Open one, or ask somebody to add you to theirs.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
          {rooms.map((room) => (
            <li key={room.id}>
              <button
                type="button"
                className="flex w-full cursor-pointer items-center gap-2 rounded-md px-3 py-2
                  text-left text-ink-dim transition-colors hover:bg-surface-hover hover:text-ink
                  aria-[current=true]:bg-accent-soft aria-[current=true]:text-ink"
                aria-current={room.id === selected ? 'true' : undefined}
                onClick={() => onSelect(room.id)}
              >
                <span className="flex-1 truncate">{room.name}</span>
                {room.unread > 0 && (
                  <span
                    className="min-w-5 flex-none rounded-full bg-accent px-2 py-px text-center
                      text-[0.7rem] font-semibold text-on-accent"
                  >
                    <span className="sr-only">
                      {room.unread} unread {room.unread === 1 ? 'message' : 'messages'}
                    </span>
                    <span aria-hidden="true">{room.unread > 99 ? '99+' : room.unread}</span>
                  </span>
                )}
                {room.status === 'closed' && (
                  <span
                    className="flex-none text-[0.65rem] tracking-[0.06em] text-ink-faint uppercase"
                    title="This room is closed"
                  >
                    closed
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
