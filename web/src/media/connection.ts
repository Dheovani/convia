import type {
  DisconnectReason as Reason,
  LocalAudioTrack,
  LocalVideoTrack,
  Participant,
  RemoteParticipant,
  RemoteTrackPublication,
} from 'livekit-client'

/*
The media plane, as this page sees it.

Everything the rest of the interface knows about a call's audio and video comes
through here, in Convia's words: who is seen, whether they are speaking, how well
they are connected, which devices there are, what can be attached to an element.
Nothing else in the page imports the media client, so replacing it is work in
this file.

**The media client is loaded when somebody first gets ready to join a call**, not
with the page. It is most of the weight a call adds, and most visits never join
one.
*/

// Attachable is a track an element can play.
export interface Attachable {
  attach: (element: HTMLMediaElement) => HTMLMediaElement
  detach: (element: HTMLMediaElement) => HTMLMediaElement
}

/*
Quality is how well a connection is carrying somebody, in the three states worth
saying. The media client distinguishes excellent from good, which nobody acts on.
*/
export type Quality = 'good' | 'poor' | 'lost'

/*
Seen is one person in the call, as the media server reports them.

`identity` is the participant Convia seated them as, which is how a name is
found for them. The person on this page is `local`, and carries no audio: hearing
yourself is an echo, not a feature.
*/
export interface Seen {
  identity: string
  local: boolean
  speaking: boolean
  microphone: boolean
  camera: boolean
  quality: Quality
  video?: Attachable
  audio?: Attachable
}

/*
Ending is why a connection closed without this page closing it.

`closed` is the media client closing it on its own, which it does as the page is
left. `removed` is Convia disconnecting somebody on purpose: a moderator put them
out, they lost their place in the room, or they left from another tab. `ended`
is the call itself going away. `elsewhere` is the same person joining from
another page. `lost` is everything else, and is the only one worth trying again.
*/
export type Ending = 'closed' | 'removed' | 'ended' | 'elsewhere' | 'lost'

export type DeviceKind = 'audioinput' | 'videoinput' | 'audiooutput'

export interface Device {
  id: string
  label: string
}

export interface Devices {
  audioinput: Device[]
  videoinput: Device[]
  audiooutput: Device[]
}

export const noDevices: Devices = { audioinput: [], videoinput: [], audiooutput: [] }

/*
Choice is what a person decided about their devices before joining.

An empty device identifier is the system's default, which is what somebody who
never chose gets, and what somebody whose chosen device went away falls back to.
*/
export interface Choice {
  microphone: boolean
  camera: boolean
  audioInput: string
  videoInput: string
  audioOutput: string
}

/*
Refusal is why a device could not be had, in the terms a person can act on: they
refused it, there is none, something else holds it, or it simply failed.
*/
export type Refusal = 'denied' | 'missing' | 'busy' | 'failed'

/*
Preview is the person's own camera and microphone before they join.

It is opened with the devices they chose and closed before the call opens them
again, so nothing holds a camera the call is about to ask for.
*/
export interface Preview {
  video?: Attachable
  refused: { microphone?: Refusal; camera?: Refusal }
  // level is how loud the microphone is right now, from 0 to 1.
  level: () => number
  stop: () => void
}

export interface Connection {
  setMicrophone: (on: boolean) => Promise<void>
  setCamera: (on: boolean) => Promise<void>
  switchDevice: (kind: DeviceKind, id: string) => Promise<void>
  // setReceiveVideo stops, or resumes, receiving everybody else's video.
  setReceiveVideo: (on: boolean) => void
  hangUp: () => Promise<void>
}

export interface Listeners {
  onChange: (seen: Seen[]) => void
  onEnded: (ending: Ending) => void
  onReconnecting: (reconnecting: boolean) => void
  // onArrived and onDeparted are about people who came or went after this page joined.
  onArrived: (identity: string) => void
  onDeparted: (identity: string) => void
  // onDeviceFailure is a device the call is using that stopped working.
  onDeviceFailure: (refusal: Refusal, kind?: DeviceKind) => void
}

export interface Connected {
  connection: Connection
  // microphone and camera are false when they were wanted and could not be had.
  microphone: boolean
  camera: boolean
}

type Failures = (typeof import('livekit-client'))['MediaDeviceFailure']

// refusalOf reads why a device could not be had, in the terms a person can act on.
function refusalOf(failures: Failures, error: unknown): Refusal {
  switch (failures.getFailure(error)) {
    case failures.PermissionDenied:
      return 'denied'
    case failures.NotFound:
      return 'missing'
    case failures.DeviceInUse:
      return 'busy'
    default:
      return 'failed'
  }
}

/*
listDevices lists the microphones, cameras and speakers this browser offers.

It does not ask for permission. Before permission is given a browser lists
devices without names, so each is named by its kind and position until the
preview has been allowed to open one. Choosing where sound plays is offered only
where the browser can do it.
*/
export async function listDevices(): Promise<Devices> {
  const { Room, supportsAudioOutputSelection } = await import('livekit-client')

  const listed = (devices: MediaDeviceInfo[]): Device[] =>
    devices
      .filter((device) => device.deviceId !== '')
      .map((device) => ({ id: device.deviceId, label: device.label }))

  const [audioinput, videoinput, audiooutput] = await Promise.all([
    Room.getLocalDevices('audioinput', false),
    Room.getLocalDevices('videoinput', false),
    supportsAudioOutputSelection() ? Room.getLocalDevices('audiooutput', false) : Promise.resolve([]),
  ])

  return {
    audioinput: listed(audioinput),
    videoinput: listed(videoinput),
    audiooutput: listed(audiooutput),
  }
}

/*
openPreview opens the devices a person chose, so they can see and hear what the
call will get before joining it.

This is where the browser asks for permission, when the person has decided to
join rather than the moment they open a room. A device that cannot be had does not
fail the preview: it is reported, and the person may still join without it.
*/
export async function openPreview(choice: Choice): Promise<Preview> {
  const { createAudioAnalyser, createLocalAudioTrack, createLocalVideoTrack, MediaDeviceFailure } =
    await import('livekit-client')

  const refused: Preview['refused'] = {}

  let audio: LocalAudioTrack | undefined
  if (choice.microphone) {
    try {
      audio = await createLocalAudioTrack(choice.audioInput === '' ? {} : { deviceId: choice.audioInput })
    } catch (error) {
      refused.microphone = refusalOf(MediaDeviceFailure, error)
    }
  }

  let video: LocalVideoTrack | undefined
  if (choice.camera) {
    try {
      video = await createLocalVideoTrack(choice.videoInput === '' ? {} : { deviceId: choice.videoInput })
    } catch (error) {
      refused.camera = refusalOf(MediaDeviceFailure, error)
    }
  }

  const analyser = audio === undefined ? undefined : createAudioAnalyser(audio)
  let stopped = false

  return {
    ...(video === undefined ? {} : { video }),
    refused,
    level: () => (analyser === undefined || stopped ? 0 : analyser.calculateVolume()),
    stop() {
      if (stopped) {
        return
      }
      stopped = true
      void analyser?.cleanup().catch(() => undefined)
      audio?.stop()
      video?.stop()
    },
  }
}

/*
connect joins the media session a join session names, with the devices and the
microphone and camera the person chose.

It resolves once the connection is open. A microphone or camera that cannot be
had does not fail it: the person is in the call, and `microphone` and `camera`
say what they are without. A connection that cannot be opened rejects, and is
closed first, so nothing is left half-open.
*/
export async function connect(url: string, token: string, choice: Choice, listeners: Listeners): Promise<Connected> {
  const { ConnectionQuality, DisconnectReason, MediaDeviceFailure, Room, RoomEvent, Track } =
    await import('livekit-client')

  const room = new Room({
    adaptiveStream: true,
    dynacast: true,
    ...(choice.audioInput === '' ? {} : { audioCaptureDefaults: { deviceId: choice.audioInput } }),
    ...(choice.videoInput === '' ? {} : { videoCaptureDefaults: { deviceId: choice.videoInput } }),
    ...(choice.audioOutput === '' ? {} : { audioOutput: { deviceId: choice.audioOutput } }),
  })

  let hungUp = false
  // settled is false while the connection opens, so the people already in the call are not announced as arriving.
  let settled = false
  // receiveVideo is false while the person has chosen to hear the call without seeing it.
  let receiveVideo = true

  function qualityOf(participant: Participant): Quality {
    switch (participant.connectionQuality) {
      case ConnectionQuality.Poor:
        return 'poor'
      case ConnectionQuality.Lost:
        return 'lost'
      default:
        return 'good'
    }
  }

  function see(participant: Participant, local: boolean): Seen {
    const camera = participant.getTrackPublication(Track.Source.Camera)
    const microphone = participant.getTrackPublication(Track.Source.Microphone)

    return {
      identity: participant.identity,
      local,
      speaking: participant.isSpeaking,
      microphone: microphone !== undefined && !microphone.isMuted,
      camera: camera?.track !== undefined && !camera.isMuted,
      quality: qualityOf(participant),
      ...(camera?.track === undefined ? {} : { video: camera.track }),
      ...(local || microphone?.track === undefined ? {} : { audio: microphone.track }),
    }
  }

  function changed() {
    if (hungUp) {
      return
    }
    listeners.onChange([
      see(room.localParticipant, true),
      ...Array.from(room.remoteParticipants.values(), (participant) => see(participant, false)),
    ])
  }

  function endingFor(reason: Reason | undefined): Ending {
    switch (reason) {
      case DisconnectReason.CLIENT_INITIATED:
        return 'closed'
      case DisconnectReason.PARTICIPANT_REMOVED:
        return 'removed'
      case DisconnectReason.ROOM_DELETED:
      case DisconnectReason.ROOM_CLOSED:
        return 'ended'
      case DisconnectReason.DUPLICATE_IDENTITY:
        return 'elsewhere'
      default:
        return 'lost'
    }
  }

  room
    .on(RoomEvent.ParticipantConnected, (participant: RemoteParticipant) => {
      changed()
      if (settled && !hungUp) {
        listeners.onArrived(participant.identity)
      }
    })
    .on(RoomEvent.ParticipantDisconnected, (participant: RemoteParticipant) => {
      changed()
      if (settled && !hungUp) {
        listeners.onDeparted(participant.identity)
      }
    })
    .on(RoomEvent.TrackSubscribed, changed)
    .on(RoomEvent.TrackPublished, (publication: RemoteTrackPublication) => {
      if (!receiveVideo && publication.kind === Track.Kind.Video) {
        publication.setSubscribed(false)
      }
      changed()
    })
    .on(RoomEvent.MediaDevicesError, (error: Error, kind?: MediaDeviceKind) => {
      if (!hungUp) {
        listeners.onDeviceFailure(refusalOf(MediaDeviceFailure, error), kind)
      }
    })
    .on(RoomEvent.TrackUnsubscribed, changed)
    .on(RoomEvent.TrackMuted, changed)
    .on(RoomEvent.TrackUnmuted, changed)
    .on(RoomEvent.LocalTrackPublished, changed)
    .on(RoomEvent.LocalTrackUnpublished, changed)
    .on(RoomEvent.ActiveSpeakersChanged, changed)
    .on(RoomEvent.ConnectionQualityChanged, changed)
    .on(RoomEvent.Reconnecting, () => {
      if (!hungUp) {
        listeners.onReconnecting(true)
      }
    })
    .on(RoomEvent.Reconnected, () => {
      if (!hungUp) {
        listeners.onReconnecting(false)
        changed()
      }
    })
    .on(RoomEvent.Disconnected, (reason?: Reason) => {
      if (!hungUp) {
        hungUp = true
        listeners.onEnded(endingFor(reason))
      }
    })

  try {
    await room.connect(url, token)
  } catch (error) {
    hungUp = true
    await room.disconnect()
    throw error
  }
  settled = true

  let microphone = false
  if (choice.microphone) {
    try {
      await room.localParticipant.setMicrophoneEnabled(true)
      microphone = true
    } catch {
      microphone = false
    }
  }

  let camera = false
  if (choice.camera) {
    try {
      await room.localParticipant.setCameraEnabled(true)
      camera = true
    } catch {
      camera = false
    }
  }

  /*
  A browser plays audio only after the person has done something on the page.
  Pressing "Join call" was that, and asking here is what keeps the first voice
  from being silently withheld.
  */
  void room.startAudio().catch(() => undefined)
  changed()

  return {
    microphone,
    camera,
    connection: {
      async setMicrophone(on) {
        await room.localParticipant.setMicrophoneEnabled(on)
        changed()
      },
      async setCamera(on) {
        await room.localParticipant.setCameraEnabled(on)
        changed()
      },
      async switchDevice(kind, id) {
        // The system's default is named "default" to a browser, and "" to this page.
        if (!(await room.switchActiveDevice(kind, id === '' ? 'default' : id))) {
          throw new Error('the device could not be used')
        }
        changed()
      },
      setReceiveVideo(on) {
        receiveVideo = on
        for (const participant of room.remoteParticipants.values()) {
          for (const publication of participant.videoTrackPublications.values()) {
            publication.setSubscribed(on)
          }
        }
        changed()
      },
      async hangUp() {
        hungUp = true
        await room.disconnect()
      },
    },
  }
}
