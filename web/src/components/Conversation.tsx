import { Fragment, useEffect, useRef, useState } from 'react'

import type { Account, Message, SidebarRoom } from '../api/types'
import { useConversation } from '../state/useConversation'
import { Composer } from './Composer'
import { Button, input } from './controls'

interface ConversationProps {
  room: SidebarRoom
  account: Account
  onExpired: () => void
  onActivity: () => void
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
*/
function Entry({
  message,
  mine,
  onEdit,
  onWithdraw,
}: {
  message: Message
  mine: boolean
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
        <span className="text-ink-faint italic">This message was withdrawn.</span>
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
          <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{message.body}</span>
          {message.edited_at !== undefined && (
            <span className="text-[0.72rem] text-ink-faint" title={`Edited ${message.edited_at}`}>
              (edited)
            </span>
          )}
          {mine && (
            /*
            The actions appear on hover and on focus. Focus is the half that is
            usually forgotten, and without it they are unreachable from the
            keyboard.
            */
            <span
              className="ml-auto flex gap-1 opacity-0 transition-opacity group-hover:opacity-100
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
        </>
      )}
    </li>
  )
}

export function Conversation({ room, account, onExpired, onActivity }: ConversationProps) {
  const { messages, loading, failed, send, edit, withdraw } = useConversation(room.id, onExpired)
  const foot = useRef<HTMLDivElement>(null)

  /*
  The view follows the conversation as it grows. It is `auto` rather than
  `smooth` because a long jump animated across a hundred messages is a scroll
  somebody has to wait out.
  */
  useEffect(() => {
    foot.current?.scrollIntoView({ block: 'end' })
  }, [messages.length])

  let previousDay = ''

  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={room.name}>
      <header className="flex items-baseline gap-3 border-b border-line px-5 py-3">
        <h2 className="m-0 font-display text-base font-semibold">{room.name}</h2>
        {room.alias !== undefined && (
          <span className="font-mono text-[0.78rem] text-ink-faint">{room.alias}</span>
        )}
        {room.status === 'closed' && (
          <span
            className="ml-auto rounded-full bg-surface-raised px-2 py-0.5 text-[0.68rem]
              tracking-[0.06em] text-ink-faint uppercase"
          >
            closed
          </span>
        )}
      </header>

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
            previousDay = today

            return (
              <Fragment key={message.id}>
                {opensADay && (
                  <li
                    className="mt-4 mb-2 flex items-center gap-3 text-[0.72rem] tracking-[0.06em]
                      text-ink-faint uppercase before:h-px before:flex-1 before:bg-line
                      before:content-[''] after:h-px after:flex-1 after:bg-line after:content-['']"
                    aria-hidden="true"
                  >
                    <span>{today}</span>
                  </li>
                )}
                <Entry
                  message={message}
                  mine={message.user_id === account.user_id}
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
    </section>
  )
}
