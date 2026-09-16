import { useEffect, useId, useRef, useState } from 'react'

import type { RoomCall, SidebarRoom } from '../api/types'
import { useLanguage, useWords } from '../i18n/language'
import type { Attachable, Device, DeviceKind, Preview, Seen } from '../media/connection'
import { useCall } from '../state/call'
import { Button, input } from './controls'
import { OnShelf } from './Recovery'

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
  const words = useWords()
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
      title={cannotStart ? words.call.cannotStart : undefined}
      onClick={() => void call.prepare(room.id)}
    >
      {running ? words.call.join : words.call.start}
    </Button>
  )
}

// CallProblem says what went wrong with the call in a room, until it is dismissed.
export function CallProblem({ roomId }: { roomId: string }) {
  const call = useCall()
  const words = useWords()

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
        {words.call.dismiss}
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
DeviceSelect chooses one device of a kind. A device the browser gives no name
is named by its kind and its place in the list.
*/
function DeviceSelect({
  kind,
  devices,
  value,
  onChoose,
}: {
  kind: DeviceKind
  devices: Device[]
  value: string
  onChoose: (kind: DeviceKind, id: string) => void
}) {
  const id = useId()
  const words = useWords()

  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-[0.75rem] font-medium text-ink-dim">
        {words.call.device(kind)}
      </label>
      <select
        id={id}
        className={`${input} py-1.5 text-[0.82rem]`}
        value={devices.some((device) => device.id === value) ? value : ''}
        onChange={(event) => onChoose(kind, event.target.value)}
      >
        <option value="">{words.call.systemDefault}</option>
        {devices.map((device, index) => (
          <option key={device.id} value={device.id}>
            {device.label || words.call.numbered(kind, index + 1)}
          </option>
        ))}
      </select>
    </div>
  )
}

// Devices lists the devices a person chooses between, and where sound plays when the browser allows it.
export function Devices({ onChoose }: { onChoose: (kind: DeviceKind, id: string) => void }) {
  const call = useCall()

  return (
    <div className="grid gap-2 sm:grid-cols-3">
      <DeviceSelect
        kind="audioinput"
        devices={call.devices.audioinput}
        value={call.choice.audioInput}
        onChoose={onChoose}
      />
      <DeviceSelect
        kind="videoinput"
        devices={call.devices.videoinput}
        value={call.choice.videoInput}
        onChoose={onChoose}
      />
      {call.devices.audiooutput.length > 0 && (
        <DeviceSelect
          kind="audiooutput"
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
  const words = useWords()

  useEffect(() => {
    const timer = window.setInterval(() => setLevel(preview.level()), 100)
    return () => window.clearInterval(timer)
  }, [preview])

  return <meter className="h-2 w-full" min={0} max={1} value={level} aria-label={words.call.level} />
}

/*
usePreviewFor lets go of the devices a preview opened for `holder` when whatever
showed it goes away, so a camera is never left on by a screen nobody sees.
*/
export function usePreviewFor(holder: string) {
  const call = useCall()
  const cancel = useRef(call.cancelPreparing)
  cancel.current = call.cancelPreparing
  useEffect(() => () => cancel.current(holder), [holder])
}

/*
OwnPicture is the person's own camera while a preview is open, mirrored as a
person expects to see themselves.
*/
export function OwnPicture({ holder }: { holder: string }) {
  const call = useCall()
  const said = useWords().call
  const { choice, preview } = call
  const video = usePlayed<HTMLVideoElement>(call.preparing === holder ? preview?.video : undefined)

  return (
    <div className="relative aspect-video w-full overflow-hidden rounded-md bg-surface-raised md:w-64 md:flex-none">
      {choice.camera && preview?.video !== undefined ? (
        // Muted: this is only the picture.
        <video ref={video} className="size-full -scale-x-100 object-cover" autoPlay playsInline muted />
      ) : (
        <span className="grid size-full place-items-center text-[0.8rem] text-ink-faint">
          {preview === null ? said.startingDevices : said.cameraOff}
        </span>
      )}
    </div>
  )
}

// OwnLevel is how loud the microphone is while a preview has it.
export function OwnLevel() {
  const { choice, preview } = useCall()
  if (!choice.microphone || preview === null || preview.refused.microphone !== undefined) {
    return null
  }
  return <Level preview={preview} />
}

// PreviewRefusals says which device a preview could not have, and offers to try again.
export function PreviewRefusals() {
  const call = useCall()
  const said = useWords().call
  const refused = call.preview?.refused ?? {}

  if (refused.microphone === undefined && refused.camera === undefined) {
    return null
  }

  return (
    <div className="flex flex-col gap-1 text-[0.8rem] text-danger" role="alert">
      {refused.microphone !== undefined && <p className="m-0">{said.refused(refused.microphone, 'audioinput')}</p>}
      {refused.camera !== undefined && <p className="m-0">{said.refused(refused.camera, 'videoinput')}</p>}
      <div>
        <Button size="small" onClick={() => void call.choose({})}>
          {said.tryAgain}
        </Button>
      </div>
    </div>
  )
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
  const words = useWords()
  const said = words.call

  // Leaving the room lets go of the camera and microphone the preview holds.
  usePreviewFor(room.id)

  if (call.preparing !== room.id) {
    return null
  }

  const { choice } = call

  return (
    <section aria-label={said.prepare} className="border-b border-line bg-surface-sunken px-3 py-3 md:px-5">
      <div className="flex flex-col gap-3 md:flex-row">
        <OwnPicture holder={room.id} />

        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <OwnLevel />

          <div className="flex flex-wrap gap-2">
            <Button
              size="small"
              aria-pressed={choice.microphone}
              onClick={() => void call.choose({ microphone: !choice.microphone })}
            >
              {choice.microphone ? said.microphoneOff : said.microphoneOn}
            </Button>
            <Button size="small" aria-pressed={choice.camera} onClick={() => void call.choose({ camera: !choice.camera })}>
              {choice.camera ? said.cameraOffAction : said.cameraOn}
            </Button>
          </div>

          <Devices onChoose={(kind, id) => void call.switchDevice(kind, id)} />

          <PreviewRefusals />

          <div className="mt-1 flex gap-2">
            <Button tone="primary" size="small" autoFocus onClick={() => void call.join(room.id)}>
              {running ? said.join : said.start}
            </Button>
            <Button size="small" onClick={() => call.cancelPreparing(room.id)}>
              {words.common.cancel}
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
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
  const words = useWords()
  const said = words.call

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
            {person.quality === 'lost' ? said.lostConnection : said.weakConnection}
          </span>
        )}
        {!person.microphone && <span className="flex-none text-ink-faint">{said.muted}</span>}
        {onRemove !== undefined && (
          <button
            type="button"
            className="min-h-6 flex-none cursor-pointer rounded-sm px-1.5 text-ink-faint hover:bg-surface-hover hover:text-ink"
            aria-label={said.takeOut(name)}
            onClick={onRemove}
          >
            {words.common.remove}
          </button>
        )}
      </span>
    </li>
  )
}

/*
CallStage is a room's call, in the room: who is in it, how well they are
connected, and the controls.

The room's owner and moderators are the call's moderators, so they see Remove
on everybody else. Anybody the media server shows before Convia has named them is shown as
joining, and named when the list is read again.
*/
export function CallStage({ room, moderator }: { room: SidebarRoom; moderator: boolean }) {
  const call = useCall()
  const [choosing, setChoosing] = useState(false)
  const panel = useId()
  const words = useWords()
  const said = words.call

  if (call.roomId !== room.id) {
    return null
  }

  const self = call.seen.find((person) => person.local)

  return (
    <section aria-label={said.stage} className="border-b border-line bg-surface-sunken px-3 py-3 md:px-5">
      {call.phase === 'joining' && call.seen.length === 0 && (
        <p className="mt-0 mb-3 text-[0.85rem] text-ink-faint" role="status">
          {said.joiningCall}
        </p>
      )}

      {call.reconnecting ? (
        <p className="mt-0 mb-3 text-[0.82rem] text-ink-dim" role="status">
          {said.reconnecting}
        </p>
      ) : (
        self !== undefined &&
        self.quality !== 'good' && (
          <p className="mt-0 mb-3 text-[0.82rem] text-ink-dim" role="status">
            {said.weak}
          </p>
        )
      )}

      {call.audioOnly ? (
        <div className="mt-0 mb-3 flex flex-wrap items-center gap-2 text-[0.82rem] text-ink-dim" role="status">
          <span>{said.audioOnly}</span>
          <Button size="small" onClick={call.resumeVideo}>
            {said.resumeVideo}
          </Button>
        </div>
      ) : (
        call.offerAudioOnly && (
          <div className="mt-0 mb-3 flex flex-wrap items-center gap-2 text-[0.82rem] text-ink-dim" role="status">
            <span>{said.weakForAWhile}</span>
            <Button size="small" onClick={() => void call.goAudioOnly()}>
              {said.goAudioOnly}
            </Button>
            <Button size="small" onClick={call.declineAudioOnly}>
              {said.notNow}
            </Button>
          </div>
        )
      )}

      <ul
        className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2 p-0"
        aria-label={said.people}
      >
        {call.seen.map((person) => {
          const present = call.names.get(person.identity)
          const name = person.local ? words.common.you : present?.display_name || said.joiningPerson
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
          <div className="mt-3 flex flex-wrap gap-2" role="group" aria-label={said.controls}>
            <Button size="small" aria-pressed={call.microphone} onClick={() => void call.setMicrophone(!call.microphone)}>
              {call.microphone ? said.mute : said.unmute}
            </Button>
            <Button size="small" aria-pressed={call.camera} onClick={() => void call.setCamera(!call.camera)}>
              {call.camera ? said.stopCamera : said.startCamera}
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
              {said.devices}
            </Button>
            <Button size="small" className="ml-auto" onClick={() => void call.leave()}>
              {said.leave}
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
CallNotices says who arrived and who left, wherever this person is on the page,
among the toasts at the bottom right.

It is a polite live region, so a screen reader says each one once without
interrupting, and each goes away on its own after a few seconds.
*/
export function CallNotices() {
  const call = useCall()
  const words = useWords()

  return (
    <OnShelf>
      <div role="status" aria-live="polite" aria-label={words.call.notices} className="flex flex-col items-end gap-1">
        {call.notices.map((notice) => (
          <p key={notice.id} className="m-0 rounded-md border border-line bg-surface-raised px-3 py-1.5 text-[0.8rem]">
            {notice.text}
          </p>
        ))}
      </div>
    </OnShelf>
  )
}

/*
CallBar is the call this person is in, while they look at something else.

It names the room, and offers the two things somebody reading elsewhere wants:
going back to the call, and leaving it.
*/
export function CallBar({ roomName, onReturn }: { roomName: string; onReturn: () => void }) {
  const call = useCall()
  const said = useWords().call

  return (
    <div
      role="region"
      aria-label={said.current}
      className="flex items-center gap-3 border-b border-line bg-accent-soft px-4 py-2 text-[0.82rem]"
    >
      <span className="min-w-0 flex-1 truncate">
        {call.phase === 'joining' ? said.joiningIn : said.inCallIn}
        <strong className="font-semibold">{roomName}</strong>
        {call.reconnecting && <span className="text-ink-dim">{said.reconnectingShort}</span>}
      </span>
      <Button size="small" onClick={onReturn}>
        {said.return}
      </Button>
      <Button size="small" onClick={() => void call.leave()}>
        {said.leave}
      </Button>
    </div>
  )
}

function since(timestamp: string, formatting: string): string {
  const at = new Date(timestamp)
  if (Number.isNaN(at.getTime())) {
    return ''
  }
  return at.toLocaleTimeString(formatting, { hour: '2-digit', minute: '2-digit' })
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
  const { words, formatting } = useLanguage()
  const said = words.call
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
        {said.list}
      </h2>
      {named.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] leading-relaxed text-ink-faint">{said.noneRunning}</p>
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
                <span className="flex-none text-[0.7rem] text-ink-faint">
                  {said.since(since(running.created_at, formatting))}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
