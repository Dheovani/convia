import { useEffect, useRef } from 'react'

import type { RoomCall, SidebarRoom } from '../api/types'
import type { Attachable, Seen } from '../media/connection'
import { useCall } from '../state/call'
import { Button } from './controls'

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  const first = parts[0]?.[0] ?? '?'
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : ''
  return (first + last).toUpperCase()
}

/*
CallButton starts or joins a room's call from its header.

It says which: "Join call" when the room is holding one, "Start call" when it is
not. A closed room keeps a call it was holding but does not start one, so the
button is offered disabled there rather than refused after it is pressed. It is
absent while this person is in the room's call, because the stage below has the
controls.
*/
export function CallButton({ room, running }: { room: SidebarRoom; running: boolean }) {
  const call = useCall()

  if (call.roomId === room.id) {
    return null
  }

  const cannotStart = !running && room.status !== 'open'

  return (
    <Button
      size="small"
      tone="primary"
      disabled={cannotStart}
      title={cannotStart ? 'A closed room does not start new calls.' : undefined}
      onClick={() => void call.join(room.id)}
    >
      {running ? 'Join call' : 'Start call'}
    </Button>
  )
}

// CallProblem says what went wrong with the call in a room, until it is dismissed.
export function CallProblem({ roomId }: { roomId: string }) {
  const call = useCall()

  if (call.problem === null || call.problem.roomId !== roomId) {
    return null
  }

  return (
    <div className="flex items-center gap-3 border-b border-line px-5 py-2 text-[0.82rem]" role="alert">
      <span className="flex-1 text-danger">{call.problem.message}</span>
      <button
        type="button"
        className="cursor-pointer rounded-sm px-1 text-[0.75rem] text-ink-faint hover:text-ink"
        onClick={call.dismiss}
      >
        Dismiss
      </button>
    </div>
  )
}

// Played attaches a track to an element for as long as both exist.
function usePlayed<Element extends HTMLMediaElement>(track: Attachable | undefined) {
  const element = useRef<Element>(null)

  useEffect(() => {
    const target = element.current
    if (target === null || track === undefined) {
      return
    }
    track.attach(target)
    return () => {
      track.detach(target)
    }
  }, [track])

  return element
}

function Tile({
  person,
  name,
  onRemove,
}: {
  person: Seen
  name: string
  onRemove?: () => void
}) {
  const video = usePlayed<HTMLVideoElement>(person.camera ? person.video : undefined)

  return (
    <li
      className={`relative flex aspect-video min-w-0 flex-col justify-end overflow-hidden rounded-md
        bg-surface-raised ${person.speaking ? 'ring-2 ring-accent' : ''}`}
    >
      {person.camera && person.video !== undefined ? (
        // Muted because a person's sound plays through the call's own audio, once.
        <video ref={video} className="absolute inset-0 size-full object-cover" autoPlay playsInline muted />
      ) : (
        <span
          className="grid flex-1 place-items-center font-display text-xl font-semibold text-ink-dim"
          aria-hidden="true"
        >
          {initials(name)}
        </span>
      )}
      <span className="relative flex items-center gap-2 bg-surface-deep/80 px-2 py-1 text-[0.75rem]">
        <span className="min-w-0 flex-1 truncate">{name}</span>
        {!person.microphone && <span className="flex-none text-ink-faint">muted</span>}
        {onRemove !== undefined && (
          <button
            type="button"
            className="flex-none cursor-pointer rounded-sm px-1 text-ink-faint hover:bg-surface-hover hover:text-ink"
            aria-label={`Take ${name} out of the call`}
            onClick={onRemove}
          >
            Remove
          </button>
        )}
      </span>
    </li>
  )
}

/*
CallStage is a room's call, in the room: who is in it, and the controls.

The room's owner is the call's moderator, so the owner sees Remove on everybody
else. Anybody the media server shows before Convia has named them is shown as
joining, and named when the list is read again.
*/
export function CallStage({ room, moderator }: { room: SidebarRoom; moderator: boolean }) {
  const call = useCall()

  if (call.roomId !== room.id) {
    return null
  }

  return (
    <section aria-label="Call" className="border-b border-line bg-surface-sunken px-3 py-3 md:px-5">
      {call.phase === 'joining' && call.seen.length === 0 && (
        <p className="mt-0 mb-3 text-[0.85rem] text-ink-faint" role="status">
          Joining the call…
        </p>
      )}

      <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2 p-0">
        {call.seen.map((person) => {
          const present = call.names.get(person.identity)
          const name = person.local ? 'You' : (present?.display_name || 'Joining…')
          return (
            <Tile
              key={person.identity}
              person={person}
              name={name}
              {...(moderator && !person.local && present !== undefined
                ? { onRemove: () => void call.remove(present.user_id) }
                : {})}
            />
          )
        })}
      </ul>

      {call.phase === 'joined' && (
        <div className="mt-3 flex flex-wrap gap-2" role="group" aria-label="Call controls">
          <Button size="small" aria-pressed={call.microphone} onClick={() => void call.setMicrophone(!call.microphone)}>
            {call.microphone ? 'Mute' : 'Unmute'}
          </Button>
          <Button size="small" aria-pressed={call.camera} onClick={() => void call.setCamera(!call.camera)}>
            {call.camera ? 'Stop camera' : 'Start camera'}
          </Button>
          <Button size="small" className="ml-auto" onClick={() => void call.leave()}>
            Leave call
          </Button>
        </div>
      )}
    </section>
  )
}

function Speaker({ track }: { track: Attachable }) {
  const element = usePlayed<HTMLAudioElement>(track)
  return <audio ref={element} autoPlay hidden />
}

/*
CallAudio plays the call wherever this person is on the page.

It is rendered once, by the workspace, and not by the stage: a call goes on while
its person reads another room, and so does its sound.
*/
export function CallAudio() {
  const call = useCall()

  return (
    <>
      {call.seen.map((person) =>
        person.local || person.audio === undefined ? null : <Speaker key={person.identity} track={person.audio} />,
      )}
    </>
  )
}

/*
CallBar is the call this person is in, while they look at something else.

It names the room, and offers the two things somebody reading elsewhere wants:
going back to the call, and leaving it.
*/
export function CallBar({ roomName, onReturn }: { roomName: string; onReturn: () => void }) {
  const call = useCall()

  return (
    <div
      role="region"
      aria-label="Current call"
      className="flex items-center gap-3 border-b border-line bg-accent-soft px-4 py-2 text-[0.82rem]"
    >
      <span className="min-w-0 flex-1 truncate">
        {call.phase === 'joining' ? 'Joining the call in ' : 'In a call in '}
        <strong className="font-semibold">{roomName}</strong>
      </span>
      <Button size="small" onClick={onReturn}>
        Return
      </Button>
      <Button size="small" onClick={() => void call.leave()}>
        Leave call
      </Button>
    </div>
  )
}

function since(timestamp: string): string {
  const at = new Date(timestamp)
  if (Number.isNaN(at.getTime())) {
    return ''
  }
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

/*
CallsList is the Calls destination: the calls running in this person's rooms.

Choosing one opens its room, where the call is joined like any other. A call in a
room the list of rooms does not know yet is left out until the rooms are read
again, because a row with no name is not something anybody can choose.
*/
export function CallsList({
  calls,
  rooms,
  onOpen,
}: {
  calls: RoomCall[]
  rooms: SidebarRoom[]
  onOpen: (roomId: string) => void
}) {
  const named = calls.flatMap((running) => {
    const room = rooms.find((candidate) => candidate.id === running.room_id)
    return room === undefined ? [] : [{ running, room }]
  })

  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
      <h2
        className="mx-2 mt-0 mb-3 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint
          uppercase"
      >
        Calls
      </h2>
      {named.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] leading-relaxed text-ink-faint">
          No call is running in your rooms. Start one from a conversation.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
          {named.map(({ running, room }) => (
            <li key={running.id}>
              <button
                type="button"
                className="flex w-full cursor-pointer items-center gap-2 rounded-md px-3 py-2 text-left
                  text-ink-dim transition-colors hover:bg-surface-hover hover:text-ink"
                onClick={() => onOpen(room.id)}
              >
                <span className="flex-1 truncate">{room.name}</span>
                <span className="flex-none text-[0.7rem] text-ink-faint">since {since(running.created_at)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
