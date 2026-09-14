import { useState } from 'react'

import { ApiError, NetworkError, sourceKey } from '../api/client'
import type { InvitationLook, RemoteRoom, SidebarRoom } from '../api/types'
import { Button, input } from './controls'

interface SidebarProps {
  rooms: SidebarRoom[]
  remoteRooms: RemoteRoom[]
  selected: string | null
  loading: boolean
  onSelect: (key: string) => void
  onCreate: (name: string) => Promise<void>
  onLook: (link: string) => Promise<InvitationLook>
  onJoin: (link: string) => Promise<void>
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
explainLink says why a link could not be looked at or joined.

The words come from the status and never from the other installation's prose,
which this page did not write and cannot vouch for.
*/
function explainLink(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return 'That is not a Convia invitation link.'
      case 404:
        return 'That invitation cannot be used. It may have expired, been used, or been meant for somebody else.'
      case 409:
        return 'You are already in that room.'
      case 503:
        return 'The Convia that link points to could not be reached. Try again in a moment.'
    }
  }
  return 'That link could not be used. Try again.'
}

// hostOf is the part of a home a person recognizes.
function hostOf(home: string): string {
  try {
    return new URL(home).host
  } catch {
    return home
  }
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
JoinByLink takes a link somebody was sent, says what it is for, and joins it.

It looks before it joins, deliberately. A link is an address anybody can make,
and the only way to know which room it leads to — and who sent it — is to ask
the installation it names; joining without showing that first would be
accepting a stranger's room on the strength of a URL.
*/
function JoinByLink({
  onLook,
  onJoin,
  onDone,
}: {
  onLook: (link: string) => Promise<InvitationLook>
  onJoin: (link: string) => Promise<void>
  onDone: () => void
}) {
  const [link, setLink] = useState('')
  const [look, setLook] = useState<InvitationLook | null>(null)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  async function examine(event: React.FormEvent) {
    event.preventDefault()
    if (link.trim() === '' || busy) {
      return
    }
    setBusy(true)
    setFailure(null)
    try {
      setLook(await onLook(link.trim()))
    } catch (error) {
      setFailure(explainLink(error))
    } finally {
      setBusy(false)
    }
  }

  async function accept() {
    setBusy(true)
    setFailure(null)
    try {
      await onJoin(link.trim())
      onDone()
    } catch (error) {
      setFailure(explainLink(error))
      setBusy(false)
    }
  }

  return (
    <div className="mx-1 mb-3 flex flex-col gap-2">
      {look === null ? (
        <form className="flex flex-col gap-2" onSubmit={examine}>
          <label className="sr-only" htmlFor="invitation-link">
            Invitation link
          </label>
          <input
            id="invitation-link"
            className={input}
            autoFocus
            placeholder="Paste the link you were sent"
            value={link}
            onChange={(event) => setLink(event.target.value)}
          />
          <div className="flex gap-2">
            <Button tone="primary" size="small" type="submit" disabled={busy || link.trim() === ''}>
              {busy ? 'Looking…' : 'Look'}
            </Button>
            <Button size="small" disabled={busy} onClick={onDone}>
              Cancel
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex flex-col gap-2 rounded-md border border-line bg-surface-raised p-3">
          <p className="m-0 text-[0.85rem]">
            <strong className="font-semibold">{look.room_name}</strong>
          </p>
          <p className="m-0 text-[0.75rem] leading-relaxed text-ink-dim">
            Invited by {look.inviter}, on {hostOf(look.home)}.
          </p>
          <div className="flex gap-2">
            <Button tone="primary" size="small" disabled={busy} onClick={() => void accept()}>
              {busy ? 'Joining…' : 'Join'}
            </Button>
            <Button size="small" disabled={busy} onClick={onDone}>
              Cancel
            </Button>
          </div>
        </div>
      )}
      {failure !== null && (
        <p className="m-0 text-[0.78rem] text-danger" role="alert">
          {failure}
        </p>
      )}
    </div>
  )
}

const rowClass =
  'flex w-full cursor-pointer items-center gap-2 rounded-md px-3 py-2 text-left text-ink-dim ' +
  'transition-colors hover:bg-surface-hover hover:text-ink ' +
  'aria-[current=true]:bg-accent-soft aria-[current=true]:text-ink'

const headingClass =
  'm-0 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint uppercase'

// The header's actions are icons because they share a line with its title at the
// sidebar's width. Each keeps its words, as its name and as its tooltip.
const actionClass =
  'grid size-7 flex-none cursor-pointer place-items-center rounded-md text-ink-dim transition-colors ' +
  'hover:bg-surface-hover hover:text-ink'

const iconProps = {
  'aria-hidden': true,
  viewBox: '0 0 16 16',
  className: 'size-4',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.5,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const

/*
The sidebar is the second zone: which conversation.

Every row carries its unread count, because Convia answers both in one request.
A count fetched per row would be a request per line on screen, and this is the
view that is redrawn most.

Rooms on other installations are listed apart, with where they live, and without
a count: asking every one of their homes on every redraw is the request-per-row
shape again, across the network.
*/
export function Sidebar({
  rooms,
  remoteRooms,
  selected,
  loading,
  onSelect,
  onCreate,
  onLook,
  onJoin,
}: SidebarProps) {
  const [creating, setCreating] = useState(false)
  const [joining, setJoining] = useState(false)

  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
      <div className="mx-2 mb-3 flex min-h-7 items-center justify-between gap-2">
        <h2 className={`${headingClass} min-w-0 truncate`}>Conversations</h2>
        {!creating && !joining && (
          <span className="flex flex-none gap-0.5">
            <button
              type="button"
              className={actionClass}
              aria-label="Join with a link"
              title="Join with a link"
              onClick={() => setJoining(true)}
            >
              <svg {...iconProps}>
                <path d="M6.75 9.25a2.75 2.75 0 0 0 3.9 0l2-2a2.75 2.75 0 0 0-3.9-3.9l-.6.6" />
                <path d="M9.25 6.75a2.75 2.75 0 0 0-3.9 0l-2 2a2.75 2.75 0 0 0 3.9 3.9l.6-.6" />
              </svg>
            </button>
            <button
              type="button"
              className={actionClass}
              aria-label="New conversation"
              title="New conversation"
              onClick={() => setCreating(true)}
            >
              <svg {...iconProps}>
                <path d="M8 3.25v9.5M3.25 8h9.5" />
              </svg>
            </button>
          </span>
        )}
      </div>

      {creating && <NewRoom onCreate={onCreate} onDone={() => setCreating(false)} />}
      {joining && <JoinByLink onLook={onLook} onJoin={onJoin} onDone={() => setJoining(false)} />}

      {loading && rooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] text-ink-faint">Loading…</p>
      ) : rooms.length === 0 && remoteRooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] leading-relaxed text-ink-faint">
          You are not in any conversation yet. Open one, or join one with a link somebody sent you.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
          {rooms.map((room) => {
            const key = sourceKey({ kind: 'local', id: room.id })
            return (
              <li key={key}>
                <button
                  type="button"
                  className={rowClass}
                  aria-current={key === selected ? 'true' : undefined}
                  onClick={() => onSelect(key)}
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
            )
          })}
        </ul>
      )}

      {remoteRooms.length > 0 && (
        <section aria-label="Rooms on other Convias" className="mt-5">
          <h2 className={`${headingClass} mx-2 mb-2`}>Elsewhere</h2>
          <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
            {remoteRooms.map((room) => {
              const key = sourceKey({ kind: 'remote', id: room.id })
              return (
                <li key={key}>
                  <button
                    type="button"
                    className={rowClass}
                    aria-current={key === selected ? 'true' : undefined}
                    onClick={() => onSelect(key)}
                  >
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate">{room.name}</span>
                      <span className="truncate text-[0.68rem] text-ink-faint">{hostOf(room.home)}</span>
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>
        </section>
      )}
    </div>
  )
}
