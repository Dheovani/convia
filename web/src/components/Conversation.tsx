import { Fragment, useEffect, useId, useMemo, useRef, useState } from 'react'

import type { RoomSource } from '../api/client'
import type { Account, Message, Person, SidebarRoom } from '../api/types'
import { useConversation } from '../state/useConversation'
import { useMembers } from '../state/useMembers'
import { Composer } from './Composer'
import { Button, input } from './controls'
import { label, RoomPeople } from './RoomPeople'
import { RoomSettings } from './RoomSettings'

interface ConversationProps {
  source: RoomSource
  room: SidebarRoom
  account: Account
  /*
  selfId is who this person is in the room. In a room here it is their user;
  in a room on another installation it is the user they became there, which is
  the only way their own messages can be recognized.
  */
  selfId: string
  // home is set for a room on another installation.
  home?: string
  onExpired: () => void
  onActivity: () => void
  onLeave: () => Promise<void>
  // onForget is set for a room on another installation.
  onForget?: () => Promise<void>
  // onRoomChanged asks for the room to be read again after its owner changed it.
  onRoomChanged: () => void
  // onRoomDeleted is set for a room here, which its owner can delete.
  onRoomDeleted?: () => void
}

function when(timestamp: string): string {
  const at = new Date(timestamp)
  if (Number.isNaN(at.getTime())) {
    return ''
  }
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function day(timestamp: string): string {
  const at = new Date(timestamp)
  if (Number.isNaN(at.getTime())) {
    return ''
  }
  return at.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' })
}

const row = 'flex items-baseline gap-3 rounded-sm px-2 py-[3px]'
const stamp = 'w-[3.2rem] flex-none font-mono text-[0.72rem] text-ink-faint'

/*
Entry is one message.

A withdrawn message keeps its place and loses its words, which is what Convia
stores: the record that something was said and taken back is part of the
conversation, and collapsing the gap would silently rewrite what people
remember reading.

The author's name is shown when it changes, and read out every time. Repeating
it on each line of a run is noise to somebody looking; omitting it from a line is
a line with no speaker to somebody listening.

A message the room's owner took down says so, so that nobody reads it as its
author taking the words back.
*/
function Entry({
  message,
  name,
  showName,
  mine,
  removable,
  onEdit,
  onWithdraw,
}: {
  message: Message
  name: string
  showName: boolean
  mine: boolean
  // removable is somebody else's message in a room this person owns.
  removable: boolean
  onEdit: (body: string) => Promise<void>
  onWithdraw: () => Promise<void>
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(message.body ?? '')
  const [busy, setBusy] = useState(false)

  if (message.deleted) {
    return (
      <li className={row}>
        <span className={stamp}>{when(message.created_at)}</span>
        <span className="text-ink-faint italic">
          {message.deleted_by === 'owner' ? "Removed by the room's owner." : 'This message was withdrawn.'}
        </span>
      </li>
    )
  }

  return (
    <li className={`${row} group hover:bg-surface-raised`}>
      <span className={stamp}>{when(message.created_at)}</span>

      {editing ? (
        <form
          className="flex flex-1 gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            const written = draft.trim()
            if (written === '' || busy) {
              return
            }
            setBusy(true)
            void onEdit(written)
              .then(() => setEditing(false))
              .finally(() => setBusy(false))
          }}
        >
          <label className="sr-only" htmlFor={`edit-${message.id}`}>
            Edit message
          </label>
          <input
            id={`edit-${message.id}`}
            className={`${input} flex-1`}
            value={draft}
            autoFocus
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                setEditing(false)
                setDraft(message.body ?? '')
              }
            }}
          />
          <Button type="submit" size="small" disabled={busy}>
            Save
          </Button>
          <Button
            size="small"
            onClick={() => {
              setEditing(false)
              setDraft(message.body ?? '')
            }}
          >
            Cancel
          </Button>
        </form>
      ) : (
        <>
          <div className="min-w-0 flex-1">
            {showName ? (
              <p className="m-0 text-[0.8rem] font-semibold text-ink">{name}</p>
            ) : (
              <span className="sr-only">{name}: </span>
            )}
            <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{message.body}</span>
            {message.edited_at !== undefined && (
              <span className="ml-1 text-[0.72rem] text-ink-faint" title={`Edited ${message.edited_at}`}>
                (edited)
              </span>
            )}
          </div>
          {mine && (
            /*
            The actions appear on hover and on focus. Focus is the half that is
            usually forgotten, and without it they are unreachable from the
            keyboard.
            */
            <span
              className="flex flex-none gap-1 opacity-0 transition-opacity group-hover:opacity-100
                focus-within:opacity-100"
            >
              <button
                type="button"
                className="cursor-pointer rounded-sm px-1 py-0.5 text-[0.72rem] text-ink-faint
                  hover:bg-surface-hover hover:text-ink"
                onClick={() => {
                  setDraft(message.body ?? '')
                  setEditing(true)
                }}
              >
                Edit
              </button>
              <button
                type="button"
                className="cursor-pointer rounded-sm px-1 py-0.5 text-[0.72rem] text-ink-faint
                  hover:bg-surface-hover hover:text-ink"
                onClick={() => void onWithdraw()}
              >
                Withdraw
              </button>
            </span>
          )}
          {removable && (
            <span
              className="flex flex-none gap-1 opacity-0 transition-opacity group-hover:opacity-100
                focus-within:opacity-100"
            >
              <button
                type="button"
                className="cursor-pointer rounded-sm px-1 py-0.5 text-[0.72rem] text-ink-faint
                  hover:bg-surface-hover hover:text-ink"
                onClick={() => void onWithdraw()}
              >
                Remove
              </button>
            </span>
          )}
        </>
      )}
    </li>
  )
}

export function Conversation({
  source,
  room,
  selfId,
  home,
  onExpired,
  onActivity,
  onLeave,
  onForget,
  onRoomChanged,
  onRoomDeleted,
}: ConversationProps) {
  const { messages, loading, failed, send, edit, withdraw } = useConversation(source, onExpired, onActivity)
  const {
    members,
    loaded: membersLoaded,
    failed: membersFailed,
    reload: reloadMembers,
  } = useMembers(source, onExpired)

  const [showPeople, setShowPeople] = useState(false)
  const panel = useId()
  const foot = useRef<HTMLDivElement>(null)

  const byId = useMemo(() => new Map(members.map((person) => [person.user_id, person])), [members])

  /*
  A message from somebody the member list does not know asks for the list again,
  once per person.

  It is how somebody added a moment ago gets a name without the list being read
  every few seconds. The set is what bounds it: somebody who is still unknown
  after the list was read again has left, and asking a third time would not
  change that.
  */
  const asked = useRef(new Set<string>())
  useEffect(() => {
    if (!membersLoaded) {
      return
    }
    const unknown = messages
      .map((message) => message.user_id)
      .filter(
        (userId): userId is string =>
          userId !== undefined &&
          userId !== selfId &&
          !byId.has(userId) &&
          !asked.current.has(userId),
      )
    if (unknown.length === 0) {
      return
    }
    for (const userId of unknown) {
      asked.current.add(userId)
    }
    reloadMembers()
  }, [messages, membersLoaded, byId, selfId, reloadMembers])

  function nameOf(message: Message): string {
    if (message.user_id === undefined) {
      return 'A guest'
    }
    if (message.user_id === selfId) {
      return 'You'
    }
    const person: Person | undefined = byId.get(message.user_id)
    if (person !== undefined) {
      return label(person)
    }
    return membersLoaded ? 'Somebody who left' : 'Somebody'
  }

  /*
  The view follows the conversation as it grows. It is `auto` rather than
  `smooth` because a long jump animated across a hundred messages is a scroll
  somebody has to wait out.
  */
  useEffect(() => {
    foot.current?.scrollIntoView({ block: 'end' })
  }, [messages.length])

  // Only a room here has an owner who can act from this page.
  const owner = room.owned && onRoomDeleted !== undefined

  let previousDay = ''
  let previousAuthor: string | undefined

  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={room.name}>
      <header className="flex items-center gap-3 border-b border-line px-5 py-3">
        <h2 className="m-0 font-display text-base font-semibold">{room.name}</h2>
        {room.alias !== undefined && (
          <span className="font-mono text-[0.78rem] text-ink-faint">{room.alias}</span>
        )}
        {room.status === 'closed' && (
          <span
            className="rounded-full bg-surface-raised px-2 py-0.5 text-[0.68rem] tracking-[0.06em]
              text-ink-faint uppercase"
          >
            closed
          </span>
        )}
        <Button
          size="small"
          className="ml-auto"
          aria-expanded={showPeople}
          aria-controls={panel}
          onClick={() => {
            const opening = !showPeople
            setShowPeople(opening)
            if (opening) {
              reloadMembers()
            }
          }}
        >
          People
        </Button>
        {owner && onRoomDeleted !== undefined && (
          <RoomSettings room={room} onChanged={onRoomChanged} onDeleted={onRoomDeleted} onExpired={onExpired} />
        )}
      </header>

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-5">
            {loading && messages.length === 0 && (
              <p className="mt-0 mb-3 text-[0.85rem] text-ink-faint">Loading…</p>
            )}
            {failed && (
              <p className="mt-0 mb-3 text-[0.85rem] text-danger" role="alert">
                This conversation could not be read. Convia will try again.
              </p>
            )}
            {!loading && !failed && messages.length === 0 && (
              <p className="mt-0 mb-3 text-[0.85rem] text-ink-faint">
                Nothing has been said here yet.
              </p>
            )}

            <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
              {messages.map((message) => {
                const today = day(message.created_at)
                const opensADay = today !== previousDay
                const changesAuthor = message.user_id !== previousAuthor
                previousDay = today
                previousAuthor = message.deleted ? undefined : message.user_id

                return (
                  <Fragment key={message.id}>
                    {opensADay && (
                      <li
                        className="mt-4 mb-2 flex items-center gap-3 text-[0.72rem] tracking-[0.06em]
                          text-ink-faint uppercase before:h-px before:flex-1 before:bg-line
                          before:content-[''] after:h-px after:flex-1 after:bg-line
                          after:content-['']"
                        aria-hidden="true"
                      >
                        <span>{today}</span>
                      </li>
                    )}
                    <Entry
                      message={message}
                      name={nameOf(message)}
                      showName={opensADay || changesAuthor}
                      mine={message.user_id === selfId}
                      removable={owner && message.user_id !== selfId}
                      onEdit={(body) => edit(message.id, body)}
                      onWithdraw={() => withdraw(message.id)}
                    />
                  </Fragment>
                )
              })}
            </ul>
            <div ref={foot} />
          </div>

          <Composer
            roomName={room.name}
            disabled={room.status !== 'open'}
            onSend={async (body) => {
              await send(body)
              onActivity()
            }}
          />
        </div>

        {showPeople && (
          <RoomPeople
            id={panel}
            room={room}
            selfId={selfId}
            {...(home === undefined ? {} : { home })}
            members={members}
            membersFailed={membersFailed}
            onChanged={reloadMembers}
            onLeave={onLeave}
            {...(onForget === undefined ? {} : { onForget })}
            onExpired={onExpired}
          />
        )}
      </div>
    </section>
  )
}
