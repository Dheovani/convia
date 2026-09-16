import { vi } from 'vitest'

/*
A stand-in for the media client, driven by the test.

It connects to nothing and opens no device. What it keeps is what the interface
asked of it — where it connected, with which credential and which devices, which
devices it turned on — and it lets a test say what the browser and the media
server would: permission refused, somebody arrived, a connection weakened or
closed. Only the parts of the client the page uses are here, under the names the
client exports.
*/

type Listener = (...values: unknown[]) => void

export const RoomEvent = {
  ParticipantConnected: 'participantConnected',
  ParticipantDisconnected: 'participantDisconnected',
  TrackSubscribed: 'trackSubscribed',
  TrackUnsubscribed: 'trackUnsubscribed',
  TrackMuted: 'trackMuted',
  TrackUnmuted: 'trackUnmuted',
  LocalTrackPublished: 'localTrackPublished',
  LocalTrackUnpublished: 'localTrackUnpublished',
  ActiveSpeakersChanged: 'activeSpeakersChanged',
  ConnectionQualityChanged: 'connectionQualityChanged',
  TrackPublished: 'trackPublished',
  MediaDevicesError: 'mediaDevicesError',
  Reconnecting: 'reconnecting',
  Reconnected: 'reconnected',
  Disconnected: 'disconnected',
} as const

export const Track = {
  Source: { Camera: 'camera', Microphone: 'microphone' },
  Kind: { Audio: 'audio', Video: 'video' },
} as const

export const ConnectionQuality = {
  Excellent: 'excellent',
  Good: 'good',
  Poor: 'poor',
  Lost: 'lost',
  Unknown: 'unknown',
} as const

export const DisconnectReason = {
  UNKNOWN_REASON: 0,
  CLIENT_INITIATED: 1,
  DUPLICATE_IDENTITY: 2,
  PARTICIPANT_REMOVED: 4,
  ROOM_DELETED: 5,
  SIGNAL_CLOSE: 9,
  ROOM_CLOSED: 10,
} as const

// MediaDeviceFailure reads a refusal the way the real client does: by the error's name.
export const MediaDeviceFailure = {
  PermissionDenied: 'PermissionDenied',
  NotFound: 'NotFound',
  DeviceInUse: 'DeviceInUse',
  Other: 'Other',
  getFailure(error: unknown): string {
    const name = error instanceof DOMException ? error.name : ''
    switch (name) {
      case 'NotAllowedError':
        return 'PermissionDenied'
      case 'NotFoundError':
        return 'NotFound'
      case 'NotReadableError':
        return 'DeviceInUse'
      default:
        return 'Other'
    }
  },
}

// FakeTrack is a device the page opened, which it can attach and must stop.
export class FakeTrack {
  readonly kind: string
  readonly deviceId: string | undefined
  attach = vi.fn((element: unknown) => element)
  detach = vi.fn((element: unknown) => element)
  stop = vi.fn()

  constructor(kind: string, deviceId: string | undefined) {
    this.kind = kind
    this.deviceId = deviceId
  }
}

function refuse(device: 'microphone' | 'camera') {
  const name = Room.refusals[device]
  if (name !== undefined) {
    throw new DOMException('refused', name)
  }
}

export const createLocalAudioTrack = vi.fn(async (options?: { deviceId?: string }) => {
  refuse('microphone')
  const track = new FakeTrack('audio', options?.deviceId)
  Room.opened.push(track)
  return track
})

export const createLocalVideoTrack = vi.fn(async (options?: { deviceId?: string }) => {
  refuse('camera')
  const track = new FakeTrack('video', options?.deviceId)
  Room.opened.push(track)
  return track
})

export const createAudioAnalyser = () => ({ calculateVolume: () => 0.4, analyser: {}, cleanup: async () => {} })

export const supportsAudioOutputSelection = () => true

function attachable() {
  return { attach: vi.fn((element: unknown) => element), detach: vi.fn((element: unknown) => element) }
}

class Publication {
  readonly kind: string
  isMuted = false
  track: ReturnType<typeof attachable> | undefined = attachable()

  constructor(kind: string) {
    this.kind = kind
  }

  // setSubscribed is receiving the track or not; a track nobody receives has nothing to attach.
  setSubscribed = vi.fn((on: boolean) => {
    this.track = on ? attachable() : undefined
  })
}

export class Participant {
  identity: string
  isSpeaking = false
  connectionQuality: string = ConnectionQuality.Excellent
  private readonly publications = new Map<string, Publication>()

  constructor(identity: string) {
    this.identity = identity
  }

  getTrackPublication(source: string): Publication | undefined {
    return this.publications.get(source)
  }

  publish(source: string) {
    this.publications.set(source, new Publication(source === Track.Source.Camera ? Track.Kind.Video : Track.Kind.Audio))
  }

  unpublish(source: string) {
    this.publications.delete(source)
  }

  get videoTrackPublications(): Map<string, Publication> {
    return new Map([...this.publications].filter(([, publication]) => publication.kind === Track.Kind.Video))
  }
}

class LocalParticipant extends Participant {
  setMicrophoneEnabled = vi.fn(async (on: boolean) => {
    if (on) {
      refuse('microphone')
      this.publish(Track.Source.Microphone)
    } else {
      this.unpublish(Track.Source.Microphone)
    }
  })

  setCameraEnabled = vi.fn(async (on: boolean) => {
    if (on) {
      refuse('camera')
      this.publish(Track.Source.Camera)
    } else {
      this.unpublish(Track.Source.Camera)
    }
  })
}

export class Room {
  static made: Room[] = []
  // opened is every device a preview opened, so a test can ask whether it was let go.
  static opened: FakeTrack[] = []
  // refusals makes a device unavailable, named as the browser's error would be.
  static refusals: { microphone?: string; camera?: string } = {}
  static devices: { deviceId: string; kind: string; label: string }[] = []
  // alreadyInCall are the people the media server introduces while the connection opens.
  static alreadyInCall: string[] = []

  static latest(): Room | undefined {
    return Room.made[Room.made.length - 1]
  }

  static reset() {
    Room.made = []
    Room.opened = []
    Room.refusals = {}
    Room.devices = []
    Room.alreadyInCall = []
    createLocalAudioTrack.mockClear()
    createLocalVideoTrack.mockClear()
  }

  static getLocalDevices = vi.fn(async (kind: string) => Room.devices.filter((device) => device.kind === kind))

  readonly options: unknown
  readonly localParticipant = new LocalParticipant('local')
  readonly remoteParticipants = new Map<string, Participant>()
  private readonly listeners = new Map<string, Listener[]>()

  constructor(options?: unknown) {
    this.options = options
    Room.made.push(this)
  }

  on(event: string, listener: Listener): this {
    this.listeners.set(event, [...(this.listeners.get(event) ?? []), listener])
    return this
  }

  emit(event: string, ...values: unknown[]) {
    for (const listener of this.listeners.get(event) ?? []) {
      listener(...values)
    }
  }

  connect = vi.fn(async (_url: string, _token: string) => {
    for (const identity of Room.alreadyInCall) {
      this.arrive(identity)
    }
  })

  disconnect = vi.fn(async () => {
    this.emit(RoomEvent.Disconnected, DisconnectReason.CLIENT_INITIATED)
  })

  switchActiveDevice = vi.fn(async (_kind: string, _deviceId: string) => true)

  startAudio = vi.fn(async () => {})

  // arrive is somebody connecting to the call, speaking with their microphone on.
  arrive(identity: string) {
    const participant = new Participant(identity)
    participant.publish(Track.Source.Microphone)
    this.remoteParticipants.set(identity, participant)
    this.emit(RoomEvent.ParticipantConnected, participant)
  }

  // depart is somebody's connection to the call going away.
  depart(identity: string) {
    const participant = this.remoteParticipants.get(identity)
    this.remoteParticipants.delete(identity)
    this.emit(RoomEvent.ParticipantDisconnected, participant)
  }

  // weaken is the media server saying how well somebody is connected; `local` is this page.
  weaken(identity: string, quality: string) {
    const participant = identity === 'local' ? this.localParticipant : this.remoteParticipants.get(identity)
    if (participant !== undefined) {
      participant.connectionQuality = quality
      this.emit(RoomEvent.ConnectionQualityChanged, quality, participant)
    }
  }

  // showVideo is somebody already in the call turning their camera on.
  showVideo(identity: string) {
    const participant = this.remoteParticipants.get(identity)
    if (participant === undefined) {
      return
    }
    participant.publish(Track.Source.Camera)
    this.emit(RoomEvent.TrackPublished, participant.getTrackPublication(Track.Source.Camera), participant)
  }

  // failDevice is a device the call is using failing, named as the browser's error would be.
  failDevice(name: string, kind?: string) {
    this.emit(RoomEvent.MediaDevicesError, new DOMException('failed', name), kind)
  }

  // close is the media server closing the connection, for a reason it gives.
  close(reason: number) {
    this.emit(RoomEvent.Disconnected, reason)
  }
}
