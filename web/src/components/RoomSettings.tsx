import { useId, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { SidebarRoom } from '../api/types'
import { useWords } from '../i18n/language'
import { Button, input } from './controls'

interface RoomSettingsProps {
  room: SidebarRoom
  onChanged: () => void
  // onDeleted is called once the room is gone.
  onDeleted: () => void
  onExpired: () => void
}

/*
RoomSettings is what the owner of a room may do to the room itself: rename it,
close or reopen it, and delete it.

Only the owner is shown it. Convia refuses the same acts to anybody else, so
leaving the controls out for them is a courtesy and not the protection.

Deleting asks first. It takes the room away from everybody in it, and nobody in
the room can undo it.
*/
export function RoomSettings({ room, onChanged, onDeleted, onExpired }: RoomSettingsProps) {
  const [open, setOpen] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [name, setName] = useState(room.name)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const panel = useId()
  const field = useId()
  const trigger = useRef<HTMLButtonElement>(null)
  const words = useWords()
  const said = words.roomSettings

  function dismiss() {
    setOpen(false)
    setRenaming(false)
    setConfirming(false)
    setFailure(null)
  }

  async function run(act: () => Promise<unknown>, after: () => void) {
    setBusy(true)
    setFailure(null)
    try {
      await act()
      after()
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onExpired()
        return
      }
      setFailure(
        error instanceof NetworkError ? words.common.unreachable : said.failed,
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative">
      <Button
        ref={trigger}
        size="small"
        aria-expanded={open}
        aria-controls={panel}
        onClick={() => (open ? dismiss() : setOpen(true))}
      >
        {said.open}
      </Button>

      {open && (
        <div
          id={panel}
          role="group"
          aria-label={said.label(room.name)}
          // Escape closes the menu and puts the keyboard back where it was.
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              dismiss()
              trigger.current?.focus()
            }
          }}
          className="absolute right-0 z-10 mt-2 flex w-72 max-w-[calc(100vw-2rem)] flex-col gap-3 rounded-md
            border border-line bg-surface-raised p-3 shadow-lg"
        >
          {renaming ? (
            <form
              className="flex flex-col gap-2"
              onSubmit={(event) => {
                event.preventDefault()
                const written = name.trim()
                if (written === '' || busy) {
                  return
                }
                void run(
                  () => api.renameRoom(room.id, written),
                  () => {
                    onChanged()
                    dismiss()
                  },
                )
              }}
            >
              <label htmlFor={field} className="text-[0.78rem] text-ink-dim">
                {said.name}
              </label>
              <input
                id={field}
                className={input}
                autoFocus
                maxLength={120}
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
              <div className="flex gap-2">
                <Button tone="primary" size="small" type="submit" disabled={busy || name.trim() === ''}>
                  {busy ? words.common.saving : words.common.save}
                </Button>
                <Button size="small" disabled={busy} onClick={() => setRenaming(false)}>
                  {words.common.cancel}
                </Button>
              </div>
            </form>
          ) : confirming ? (
            <div className="flex flex-col gap-2">
              <p className="m-0 text-[0.85rem]">{said.confirmDelete(room.name)}</p>
              <div className="flex gap-2">
                <Button
                  tone="primary"
                  size="small"
                  disabled={busy}
                  onClick={() => void run(() => api.deleteRoom(room.id), onDeleted)}
                >
                  {busy ? said.deleting : said.delete}
                </Button>
                <Button size="small" disabled={busy} onClick={() => setConfirming(false)}>
                  {said.keep}
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex flex-col items-start gap-1">
              <Button
                size="small"
                onClick={() => {
                  setName(room.name)
                  setRenaming(true)
                }}
              >
                {said.rename}
              </Button>
              {room.status === 'closed' ? (
                <Button size="small" disabled={busy} onClick={() => void run(() => api.reopenRoom(room.id), onChanged)}>
                  {said.reopen}
                </Button>
              ) : (
                <Button size="small" disabled={busy} onClick={() => void run(() => api.closeRoom(room.id), onChanged)}>
                  {said.close}
                </Button>
              )}
              <Button size="small" onClick={() => setConfirming(true)}>
                {said.deleteRoom}
              </Button>
            </div>
          )}

          {failure !== null && (
            <p className="m-0 text-[0.78rem] text-danger" role="alert">
              {failure}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
