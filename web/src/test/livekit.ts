import { vi } from 'vitest'

/*
A stand-in for the media client, driven by the test.

It connects to nothing. What it keeps is what the interface asked of it — where it
connected, with which credential, which devices it turned on — and it lets a test
say what the media server would: somebody arrived, the connection closed and why.
Only the parts of the client the page uses are here, under the names the client
exports, so a page that reached for anything else would fail to type-check
against the real one and fail to run against this.
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
  Disconnected: 'disconnected',
} as const

export const Track = { Source: { Camera: 'camera', Microphone: 'microphone' } } as const

export const DisconnectReason = {
  UNKNOWN_REASON: 0,
  CLIENT_INITIATED: 1,
  DUPLICATE_IDENTITY: 2,
  PARTICIPANT_REMOVED: 4,
  ROOM_DELETED: 5,
  SIGNAL_CLOSE: 9,
  ROOM_CLOSED: 10,
} as const

class Publication {
  isMuted = false
  track = { attach: vi.fn((element: unknown) => element), detach: vi.fn((element: unknown) => element) }
}

export class Participant {
  identity: string
  isSpeaking = false
  private readonly publications = new Map<string, Publication>()

  constructor(identity: string) {
    this.identity = identity
  }

  getTrackPublication(source: string): Publication | undefined {
    return this.publications.get(source)
  }

  publish(source: string) {
    this.publications.set(source, new Publication())
  }

  unpublish(source: string) {
    this.publications.delete(source)
  }
}

class LocalParticipant extends Participant {
  setMicrophoneEnabled = vi.fn(async (on: boolean) => {
    if (on && Room.microphoneRefused) {
      throw new DOMException('Permission denied', 'NotAllowedError')
    }
    if (on) {
      this.publish(Track.Source.Microphone)
    } else {
      this.unpublish(Track.Source.Microphone)
    }
  })

  setCameraEnabled = vi.fn(async (on: boolean) => {
    if (on) {
      this.publish(Track.Source.Camera)
    } else {
      this.unpublish(Track.Source.Camera)
    }
  })
}

export class Room {
  static made: Room[] = []
  // microphoneRefused makes every room's microphone unavailable, as a denied permission does.
  static microphoneRefused = false

  static latest(): Room | undefined {
    return Room.made[Room.made.length - 1]
  }

  static reset() {
    Room.made = []
    Room.microphoneRefused = false
  }

  readonly localParticipant = new LocalParticipant('local')
  readonly remoteParticipants = new Map<string, Participant>()
  private readonly listeners = new Map<string, Listener[]>()

  constructor() {
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

  connect = vi.fn(async (_url: string, _token: string) => {})

  disconnect = vi.fn(async () => {
    this.emit(RoomEvent.Disconnected, DisconnectReason.CLIENT_INITIATED)
  })

  startAudio = vi.fn(async () => {})

  // arrive is somebody connecting to the call, speaking with their microphone on.
  arrive(identity: string) {
    const participant = new Participant(identity)
    participant.publish(Track.Source.Microphone)
    this.remoteParticipants.set(identity, participant)
    this.emit(RoomEvent.ParticipantConnected, participant)
  }

  // close is the media server closing the connection, for a reason it gives.
  close(reason: number) {
    this.emit(RoomEvent.Disconnected, reason)
  }
}
