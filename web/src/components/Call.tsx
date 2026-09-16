import { useEffect, useId, useRef, useState } from 'react'

import type { RoomCall, SidebarRoom } from '../api/types'
import type { Attachable, Device, DeviceKind, Preview, Refusal, Seen } from '../media/connection'
import { useCall } from '../state/call'
import { Button, input } from './controls'

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  const first = parts[0]?.[0] ?? '?'
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : ''
  return (first + last).toUpperCase()
}

/*
CallButton starts getting ready to start or join a room's call.

It says which: "Join call" when the room is holding one, "Start call" when it is
not. A closed room keeps a call it was holding but does not start one, so the
button is offered disabled there rather than refused after it is pressed. It is
absent while this person is getting ready for the room's call or is in it,
because what is below has the controls.
*/
export function CallButton({ room, running }: { room: SidebarRoom; running: boolean }) {
  const call = useCall()
  const button = useRef<HTMLButtonElement>(null)
  const hidden = call.roomId === room.id || call.preparing === room.id

  /*
  When getting ready is cancelled, or the call is over, the keyboard comes back
  here, where it was before, rather than to the top of the page.
  */
  const wasHidden = useRef(hidden)
  useEffect(() => {
    if (wasHidden.current && !hidden) {
      button.current?.focus()
    }
    wasHidden.current = hidden
  }, [hidden])

  if (hidden) {
    return null
  }

  const cannotStart = !running && room.status !== 'open'

  return (
    <Button
      ref={button}
      size="small"
      tone="primary"
      disabled={cannotStart}
      title={cannotStart ? 'A closed room does not start new calls.' : undefined}
      onClick={() => void call.prepare(room.id)}
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
        className="min-h-6 cursor-pointer rounded-sm px-1.5 text-[0.75rem] text-ink-faint hover:text-ink"
        onClick={call.dismiss}
      >
        Dismiss
      </button>
    </div>
  )
}

// usePlayed attaches a track to an element for as long as both exist.
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

/*
refusals explain a device that could not be had in terms a person can act on.

A refused permission is the one that needs directions, because the browser will
not ask again on its own: it has to be allowed from the site's settings, which
every browser puts beside the address.
*/
const refusals: Record<Refusal, (device: string) => string> = {
  denied: (device) =>
    `Convia is not allowed to use your ${device}. Allow it from the site settings beside the address bar, then try again.`,
  missing: (device) => `No ${device} was found.`,
  busy: (device) => `Your ${device} is being used by another app.`,
  failed: (device) => `Your ${device} could not be started.`,
}

function DeviceSelect({
  kind,
  label,
  devices,
  value,
  onChoose,
}: {
  kind: DeviceKind
  label: string
  devices: Device[]
  value: string
  onChoose: (kind: DeviceKind, id: string) => void
}) {
  const id = useId()

  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-[0.75rem] font-medium text-ink-dim">
        {label}
      </label>
      <select
        id={id}
        className={`${input} py-1.5 text-[0.82rem]`}
        value={devices.some((device) => device.id === value) ? value : ''}
        onChange={(event) => onChoose(kind, event.target.value)}
      >
        <option value="">System default</option>
        {devices.map((device) => (
          <option key={device.id} value={device.id}>
            {device.label}
          </option>
        ))}
      </select>
    </div>
  )
}

// Devices lists the devices a person chooses between, and where sound plays when the browser allows it.
function Devices({ onChoose }: { onChoose: (kind: DeviceKind, id: string) => void }) {
  const call = useCall()

  return (
    <div className="grid gap-2 sm:grid-cols-3">
      <DeviceSelect
        kind="audioinput"
        label="Microphone"
        devices={call.devices.audioinput}
        value={call.choice.audioInput}
        onChoose={onChoose}
      />
      <DeviceSelect
        kind="videoinput"
        label="Camera"
        devices={call.devices.videoinput}
        value={call.choice.videoInput}
        onChoose={onChoose}
      />
      {call.devices.audiooutput.length > 0 && (
        <DeviceSelect
          kind="audiooutput"
          label="Speaker"
          devices={call.devices.audiooutput}
          value={call.choice.audioOutput}
          onChoose={onChoose}
        />
      )}
    </div>
  )
}

// Level is how loud the microphone is, read a few times a second while it is shown.
function Level({ preview }: { preview: Preview }) {
  const [level, setLevel] = useState(0)

  useEffect(() => {
    const timer = window.setInterval(() => setLevel(preview.level()), 100)
    return () => window.clearInterval(timer)
  }, [preview])

  return <meter className="h-2 w-full" min={0} max={1} value={level} aria-label="Microphone level" />
}

/*
CallPreparation is getting ready to join a room's call: seeing yourself, hearing
that the microphone works, choosing devices, and deciding whether to start with
the microphone and camera on.

Nothing is joined until the person presses join. What they chose is remembered in
this browser for the next call. A device that cannot be had is explained, and the
person may join without it.
*/
export function CallPreparation({ room, running }: { room: SidebarRoom; running: boolean }) {
  const call = useCall()
  const video = usePlayed<HTMLVideoElement>(call.preparing === room.id ? call.preview?.video : undefined)

  // Leaving the room lets go of the camera and microphone the preview holds.
  const cancel = useRef(call.cancelPreparing)
  cancel.current = call.cancelPreparing
  const roomId = room.id
  useEffect(() => () => cancel.current(roomId), [roomId])

  if (call.preparing !== room.id) {
    return null
  }

  const { choice, preview } = call
  const refused = preview?.refused ?? {}

  return (
    <section aria-label="Prepare to join" className="border-b border-line bg-surface-sunken px-3 py-3 md:px-5">
      <div className="flex flex-col gap-3 md:flex-row">
        <div className="relative aspect-video w-full overflow-hidden rounded-md bg-surface-raised md:w-64 md:flex-none">
          {choice.camera && preview?.video !== undefined ? (
            // Mirrored, as a person expects to see themselves, and muted: this is only the picture.
            <video ref={video} className="size-full -scale-x-100 object-cover" autoPlay playsInline muted />
          ) : (
            <span className="grid size-full place-items-center text-[0.8rem] text-ink-faint">
              {preview === null ? 'Starting your devices…' : 'Your camera is off'}
            </span>
          )}
        </div>

        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {choice.microphone && preview !== null && refused.microphone === undefined && <Level preview={preview} />}

          <div className="flex flex-wrap gap-2">
            <Button
              size="small"
              aria-pressed={choice.microphone}
              onClick={() => void call.choose({ microphone: !choice.microphone })}
            >
              {choice.microphone ? 'Turn microphone off' : 'Turn microphone on'}
            </Button>
            <Button size="small" aria-pressed={choice.camera} onClick={() => void call.choose({ camera: !choice.camera })}>
              {choice.camera ? 'Turn camera off' : 'Turn camera on'}
            </Button>
          </div>

          <Devices onChoose={(kind, id) => void call.switchDevice(kind, id)} />

          {(refused.microphone !== undefined || refused.camera !== undefined) && (
            <div className="flex flex-col gap-1 text-[0.8rem] text-danger" role="alert">
              {refused.microphone !== undefined && <p className="m-0">{refusals[refused.microphone]('microphone')}</p>}
              {refused.camera !== undefined && <p className="m-0">{refusals[refused.camera]('camera')}</p>}
              <div>
                <Button size="small" onClick={() => void call.choose({})}>
                  Try again
                </Button>
              </div>
            </div>
          )}

          <div className="mt-1 flex gap-2">
            <Button tone="primary" size="small" autoFocus onClick={() => void call.join(room.id)}>
              {running ? 'Join call' : 'Start call'}
            </Button>
            <Button size="small" onClick={() => call.cancelPreparing(room.id)}>
              Cancel
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}

const qualityWords = { poor: 'weak connection', lost: 'connection lost' } as const

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
        {person.quality !== 'good' && (
          <span className={`flex-none ${person.quality === 'lost' ? 'text-danger' : 'text-ink-faint'}`}>
            {qualityWords[person.quality]}
          </span>
        )}
        {!person.microphone && <span className="flex-none text-ink-faint">muted</span>}
        {onRemove !== undefined && (
          <button
            type="button"
            className="min-h-6 flex-none cursor-pointer rounded-sm px-1.5 text-ink-faint hover:bg-surface-hover hover:text-ink"
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
CallStage is a room's call, in the room: who is in it, how well they are
connected, and the controls.

The room's owner is the call's moderator, so the owner sees Remove on everybody
else. Anybody the media server shows before Convia has named them is shown as
joining, and named when the list is read again.
*/
export function CallStage({ room, moderator }: { room: SidebarRoom; moderator: boolean }) {
  const call = useCall()
  const [choosing, setChoosing] = useState(false)
  const panel = useId()

  if (call.roomId !== room.id) {
    return null
  }

  const self = call.seen.find((person) => person.local)

  return (
    <section aria-label="Call" className="border-b border-line bg-surface-sunken px-3 py-3 md:px-5">
      {call.phase === 'joining' && call.seen.length === 0 && (
        <p className="mt-0 mb-3 text-[0.85rem] text-ink-faint" role="status">
          Joining the call…
        </p>
      )}

      {call.reconnecting ? (
        <p className="mt-0 mb-3 text-[0.82rem] text-ink-dim" role="status">
          Reconnecting to the call…
        </p>
      ) : (
        self !== undefined &&
        self.quality !== 'good' && (
          <p className="mt-0 mb-3 text-[0.82rem] text-ink-dim" role="status">
            Your connection is weak. Others may not hear or see you well.
          </p>
        )
      )}

      {call.audioOnly ? (
        <div className="mt-0 mb-3 flex flex-wrap items-center gap-2 text-[0.82rem] text-ink-dim" role="status">
          <span>Audio only: video is paused to spare your connection.</span>
          <Button size="small" onClick={call.resumeVideo}>
            Turn video back on
          </Button>
        </div>
      ) : (
        call.offerAudioOnly && (
          <div className="mt-0 mb-3 flex flex-wrap items-center gap-2 text-[0.82rem] text-ink-dim" role="status">
            <span>Your connection has been weak for a while.</span>
            <Button size="small" onClick={() => void call.goAudioOnly()}>
              Continue with audio only
            </Button>
            <Button size="small" onClick={call.declineAudioOnly}>
              Not now
            </Button>
          </div>
        )
      )}

      <ul
        className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2 p-0"
        aria-label="People in the call"
      >
        {call.seen.map((person) => {
          const present = call.names.get(person.identity)
          const name = person.local ? 'You' : present?.display_name || 'Joining…'
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
        <>
          <div className="mt-3 flex flex-wrap gap-2" role="group" aria-label="Call controls">
            <Button size="small" aria-pressed={call.microphone} onClick={() => void call.setMicrophone(!call.microphone)}>
              {call.microphone ? 'Mute' : 'Unmute'}
            </Button>
            <Button size="small" aria-pressed={call.camera} onClick={() => void call.setCamera(!call.camera)}>
              {call.camera ? 'Stop camera' : 'Start camera'}
            </Button>
            <Button
              size="small"
              aria-expanded={choosing}
              aria-controls={panel}
              onClick={() => {
                const opening = !choosing
                setChoosing(opening)
                if (opening) {
                  void call.refreshDevices()
                }
              }}
            >
              Devices
            </Button>
            <Button size="small" className="ml-auto" onClick={() => void call.leave()}>
              Leave call
            </Button>
          </div>
          {choosing && (
            <div id={panel} className="mt-3">
              <Devices onChoose={(kind, id) => void call.switchDevice(kind, id)} />
            </div>
          )}
        </>
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
CallNotices says who arrived and who left, wherever this person is on the page.

It is a polite live region, so a screen reader says each one once without
interrupting, and each goes away on its own after a few seconds.
*/
export function CallNotices() {
  const call = useCall()

  return (
    <div
      role="status"
      aria-live="polite"
      aria-label="Call notices"
      className="pointer-events-none fixed right-4 bottom-4 z-10 flex flex-col items-end gap-1"
    >
      {call.notices.map((notice) => (
        <p key={notice.id} className="m-0 rounded-md border border-line bg-surface-raised px-3 py-1.5 text-[0.8rem]">
          {notice.text}
        </p>
      ))}
    </div>
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
        {call.reconnecting && <span className="text-ink-dim"> — reconnecting…</span>}
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
