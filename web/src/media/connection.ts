import type { DisconnectReason as Reason, Participant } from 'livekit-client'

/*
The media plane, as this page sees it.

Everything the rest of the interface knows about a call's audio and video comes
through here, in Convia's words: who is seen, whether they are speaking, what can
be attached to an element. Nothing else in the page imports the media client, so
replacing it is work in this file.

**The media client is loaded when somebody first joins a call**, not with the
page. It is most of the weight a call adds, and most visits never join one.
*/

// Attachable is a track an element can play.
export interface Attachable {
  attach: (element: HTMLMediaElement) => HTMLMediaElement
  detach: (element: HTMLMediaElement) => HTMLMediaElement
}

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

export interface Connection {
  setMicrophone: (on: boolean) => Promise<void>
  setCamera: (on: boolean) => Promise<void>
  hangUp: () => Promise<void>
}

export interface Listeners {
  onChange: (seen: Seen[]) => void
  onEnded: (ending: Ending) => void
}

export interface Connected {
  connection: Connection
  // microphone is false when the person is in the call and nobody can hear them.
  microphone: boolean
}

/*
connect joins the media session a join session names, with the microphone on and
the camera off.

It resolves once the connection is open. A microphone that cannot be had does not
fail it: the person is in the call and can hear it, and `microphone` says that
nobody can hear them. A connection that cannot be opened rejects, and is closed
first, so nothing is left half-open.
*/
export async function connect(url: string, token: string, listeners: Listeners): Promise<Connected> {
  const { DisconnectReason, Room, RoomEvent, Track } = await import('livekit-client')

  const room = new Room({ adaptiveStream: true, dynacast: true })
  let hungUp = false

  function see(participant: Participant, local: boolean): Seen {
    const camera = participant.getTrackPublication(Track.Source.Camera)
    const microphone = participant.getTrackPublication(Track.Source.Microphone)

    return {
      identity: participant.identity,
      local,
      speaking: participant.isSpeaking,
      microphone: microphone !== undefined && !microphone.isMuted,
      camera: camera?.track !== undefined && !camera.isMuted,
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
    .on(RoomEvent.ParticipantConnected, changed)
    .on(RoomEvent.ParticipantDisconnected, changed)
    .on(RoomEvent.TrackSubscribed, changed)
    .on(RoomEvent.TrackUnsubscribed, changed)
    .on(RoomEvent.TrackMuted, changed)
    .on(RoomEvent.TrackUnmuted, changed)
    .on(RoomEvent.LocalTrackPublished, changed)
    .on(RoomEvent.LocalTrackUnpublished, changed)
    .on(RoomEvent.ActiveSpeakersChanged, changed)
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

  let microphone = true
  try {
    await room.localParticipant.setMicrophoneEnabled(true)
  } catch {
    microphone = false
  }

  /*
  A browser plays audio only after the person has done something on the page.
  Pressing "Start call" was that, and asking here is what keeps the first voice
  from being silently withheld.
  */
  void room.startAudio().catch(() => undefined)
  changed()

  return {
    microphone,
    connection: {
      async setMicrophone(on) {
        await room.localParticipant.setMicrophoneEnabled(on)
        changed()
      },
      async setCamera(on) {
        await room.localParticipant.setCameraEnabled(on)
        changed()
      },
      async hangUp() {
        hungUp = true
        await room.disconnect()
      },
    },
  }
}
