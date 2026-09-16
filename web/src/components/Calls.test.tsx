import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { CallPresence, JoinSession, RoomCall } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { ConnectionQuality, createLocalVideoTrack, DisconnectReason, Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'

vi.mock('livekit-client', () => import('../test/livekit'))

beforeEach(() => {
  Room.reset()
  window.localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

type Person = ReturnType<typeof userEvent.setup>

const standupPath = `/v1/me/rooms/${room().id}`
const design = room({ id: 'room_DESIGNREVIEW7QK4XMZP2VJH6T', name: 'Design review' })
const designPath = `/v1/me/rooms/${design.id}`

const session: JoinSession = {
  participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
  call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
  media_url: 'wss://media.convia.test',
  media_token: 'a-credential-for-one-call',
  expires_at: '2026-09-14T18:35:00.000Z',
}

const running: RoomCall = {
  id: session.call_id,
  room_id: room().id,
  status: 'active',
  created_at: '2026-09-14T18:30:00.000Z',
}

const anaInTheCall: CallPresence = {
  participant_id: session.participant_id,
  user_id: ana.user_id,
  display_name: ana.username,
  role: 'moderator',
  joined_at: '2026-09-14T18:30:00.000Z',
}

const brunoInTheCall: CallPresence = {
  participant_id: 'part_BRUNOINTHECALL7QK4XMZP2VJ',
  user_id: 'usr_BRUNOALVES7QK4XMZP2VJH6TBW',
  display_name: 'Bruno Alves',
  role: 'member',
  joined_at: '2026-09-14T18:31:00.000Z',
}

function quietRoom(server: FakeConvia, path: string) {
  return server
    .on('GET', `${path}/messages`, { body: { data: [] } })
    .on('GET', `${path}/members`, { body: { data: [] } })
}

function standup({ owned = false, calls = [] as RoomCall[] } = {}) {
  const server = new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room({ owned }), design] } })
    .on('GET', '/v1/me/calls', { body: { data: calls } })
    .on('POST', `${standupPath}/call/join`, { status: calls.length > 0 ? 200 : 201, body: session })
    .on('POST', `${standupPath}/call/leave`, { status: 204 })
    .on('GET', `${standupPath}/call/participants`, { body: { data: [anaInTheCall, brunoInTheCall] } })
    .on('DELETE', `${standupPath}/call/participants/${brunoInTheCall.user_id}`, { status: 204 })
  quietRoom(server, standupPath)
  quietRoom(server, designPath)
  return server
}

function open(server: FakeConvia) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

// preparation is getting ready to join, which the header's button opens.
async function preparation(person: Person, label = 'Start call') {
  await person.click(await screen.findByRole('button', { name: label }))
  return screen.findByRole('region', { name: 'Prepare to join' })
}

// joined gets ready and joins, the whole of pressing the button twice.
async function joined(person: Person, label = 'Start call') {
  const ready = await preparation(person, label)
  await person.click(within(ready).getByRole('button', { name: label }))
}

// connected waits for the page to open its media connection, and returns it.
async function connected(): Promise<Room> {
  return waitFor(() => {
    const media = Room.latest()
    expect(media?.connect).toHaveBeenCalled()
    return media as Room
  })
}

describe('getting ready to join', () => {
  it('shows the microphone before joining, and joins only when asked', async () => {
    const server = standup()
    open(server)
    const person = userEvent.setup()

    const ready = await preparation(person)

    expect(await within(ready).findByRole('meter', { name: 'Microphone level' })).toBeInTheDocument()
    expect(Room.made).toHaveLength(0)
    expect(server.asked('POST', `${standupPath}/call/join`)).toBeUndefined()

    await person.click(within(ready).getByRole('button', { name: 'Start call' }))

    const media = await connected()
    expect(media.connect).toHaveBeenCalledWith(session.media_url, session.media_token)
    expect(screen.queryByRole('region', { name: 'Prepare to join' })).not.toBeInTheDocument()
  })

  // The preview lets go of the devices so the call can open them again.
  it('stops the preview when the person joins or cancels', async () => {
    open(standup())
    const person = userEvent.setup()

    const ready = await preparation(person)
    await waitFor(() => expect(Room.opened).toHaveLength(1))

    await person.click(within(ready).getByRole('button', { name: 'Cancel' }))

    expect(Room.opened[0]?.stop).toHaveBeenCalled()
    expect(await screen.findByRole('button', { name: 'Start call' })).toBeInTheDocument()
  })

  it('remembers in this browser whether the camera starts on', async () => {
    const first = open(standup())
    const person = userEvent.setup()

    const ready = await preparation(person)
    await person.click(within(ready).getByRole('button', { name: 'Turn camera on' }))
    await waitFor(() => expect(createLocalVideoTrack).toHaveBeenCalled())
    await person.click(within(ready).getByRole('button', { name: 'Start call' }))

    const media = await connected()
    await waitFor(() => expect(media.localParticipant.setCameraEnabled).toHaveBeenCalledWith(true))

    first.unmount()
    open(standup())

    const again = await preparation(person)
    expect(within(again).getByRole('button', { name: 'Turn camera off' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('joins with the microphone the person chose', async () => {
    Room.devices = [
      { deviceId: 'mic-built-in', kind: 'audioinput', label: 'Built-in microphone' },
      { deviceId: 'mic-headset', kind: 'audioinput', label: 'Headset' },
    ]
    open(standup())
    const person = userEvent.setup()

    const ready = await preparation(person)
    const microphone = await within(ready).findByRole('combobox', { name: 'Microphone' })
    await waitFor(() => expect(within(microphone).getByRole('option', { name: 'Headset' })).toBeInTheDocument())
    await person.selectOptions(microphone, 'mic-headset')
    await person.click(within(ready).getByRole('button', { name: 'Start call' }))

    const media = await connected()
    expect(media.options).toMatchObject({ audioCaptureDefaults: { deviceId: 'mic-headset' } })
  })

  it('explains a refused microphone, and still lets the person join', async () => {
    Room.refusals = { microphone: 'NotAllowedError' }
    open(standup())
    const person = userEvent.setup()

    const ready = await preparation(person)

    expect(await within(ready).findByText(/Convia is not allowed to use your microphone/)).toBeInTheDocument()
    expect(within(ready).getByRole('button', { name: 'Try again' })).toBeInTheDocument()

    await person.click(within(ready).getByRole('button', { name: 'Start call' }))

    expect(await screen.findByText('Your microphone is not available, so nobody can hear you.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Leave call' })).toBeInTheDocument()
  })

  it('says a camera held by another app is busy', async () => {
    Room.refusals = { camera: 'NotReadableError' }
    open(standup())
    const person = userEvent.setup()

    const ready = await preparation(person)
    await person.click(within(ready).getByRole('button', { name: 'Turn camera on' }))

    expect(await within(ready).findByText('Your camera is being used by another app.')).toBeInTheDocument()
  })
})

describe('calls', () => {
  it('starts a call in a quiet room and connects with what Convia issued', async () => {
    const server = standup()
    open(server)
    const person = userEvent.setup()

    await joined(person)

    const media = await connected()
    expect(media.connect).toHaveBeenCalledWith(session.media_url, session.media_token)
    expect(server.asked('POST', `${standupPath}/call/join`)).toBeDefined()

    const stage = await screen.findByRole('region', { name: 'Call' })
    expect(within(stage).getByText('You')).toBeInTheDocument()
    expect(await within(stage).findByRole('button', { name: 'Leave call' })).toBeInTheDocument()
  })

  // A person joins speaking and unseen, and turns the camera on deliberately.
  it('joins with the microphone on and the camera off', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()

    await waitFor(() => expect(media.localParticipant.setMicrophoneEnabled).toHaveBeenCalledWith(true))
    expect(media.localParticipant.setCameraEnabled).not.toHaveBeenCalled()

    await person.click(await screen.findByRole('button', { name: 'Start camera' }))
    expect(media.localParticipant.setCameraEnabled).toHaveBeenCalledWith(true)
  })

  it('joins the call a room is already holding', async () => {
    open(standup({ calls: [running] }))
    const person = userEvent.setup()

    await joined(person, 'Join call')
    await connected()
  })

  /*
  Convia is told first. The media server reports a closed connection too, and a
  report that arrived before the request would record the call as ended by
  nobody's asking, which a real server was seen doing.
  */
  it('leaving tells Convia and then hangs up', async () => {
    const server = standup()
    open(server)
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()

    let toldFirst: boolean | undefined
    media.disconnect.mockImplementation(async () => {
      toldFirst = server.asked('POST', `${standupPath}/call/leave`) !== undefined
    })

    await person.click(await screen.findByRole('button', { name: 'Leave call' }))

    await waitFor(() => expect(media.disconnect).toHaveBeenCalled())
    expect(toldFirst).toBe(true)
    expect(await screen.findByRole('button', { name: 'Start call' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Call' })).not.toBeInTheDocument()
  })

  it('switches the microphone in the middle of a call', async () => {
    Room.devices = [{ deviceId: 'mic-headset', kind: 'audioinput', label: 'Headset' }]
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()

    await person.click(await screen.findByRole('button', { name: 'Devices' }))
    const microphone = await screen.findByRole('combobox', { name: 'Microphone' })
    await waitFor(() => expect(within(microphone).getByRole('option', { name: 'Headset' })).toBeInTheDocument())
    await person.selectOptions(microphone, 'mic-headset')

    await waitFor(() => expect(media.switchActiveDevice).toHaveBeenCalledWith('audioinput', 'mic-headset'))
  })

  it("lets the room's owner take somebody out of the call", async () => {
    const server = standup({ owned: true })
    open(server)
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    act(() => media.arrive(brunoInTheCall.participant_id))

    await person.click(await screen.findByRole('button', { name: 'Take Bruno Alves out of the call' }))

    await waitFor(() =>
      expect(server.asked('DELETE', `${standupPath}/call/participants/${brunoInTheCall.user_id}`)).toBeDefined(),
    )
  })

  // Convia refuses a member, so the page does not offer what could only fail.
  it('offers a member no way to take anybody out', async () => {
    open(standup({ owned: false }))
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    act(() => media.arrive(brunoInTheCall.participant_id))

    expect(await within(await screen.findByRole('region', { name: 'Call' })).findByText('Bruno Alves')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /out of the call/ })).not.toBeInTheDocument()
  })

  it('keeps the call while another room is read, with a way back', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()

    await person.click(screen.getByRole('button', { name: 'Design review' }))

    const bar = await screen.findByRole('region', { name: 'Current call' })
    expect(within(bar).getByText('Standup')).toBeInTheDocument()
    expect(media.disconnect).not.toHaveBeenCalled()

    await person.click(within(bar).getByRole('button', { name: 'Return' }))

    expect(await screen.findByRole('region', { name: 'Call' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Current call' })).not.toBeInTheDocument()
  })

  it('says why a call could not be started', async () => {
    const server = standup().on('POST', `${standupPath}/call/join`, {
      status: 503,
      failure: { code: 'unavailable', message: 'Whatever the server happened to say.' },
    })
    open(server)
    const person = userEvent.setup()

    await joined(person)

    expect(await screen.findByText('Calls cannot be held here right now.')).toBeInTheDocument()
    expect(screen.queryByText('Whatever the server happened to say.')).not.toBeInTheDocument()
    expect(Room.latest()).toBeUndefined()
  })

  it('says so when somebody is taken out of the call', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    await screen.findByRole('button', { name: 'Leave call' })

    act(() => media.close(DisconnectReason.PARTICIPANT_REMOVED))

    expect(await screen.findByText('You were taken out of the call.')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Call' })).not.toBeInTheDocument()
  })

  // A lost connection is the one ending worth trying again, with a new credential.
  it('rejoins once when the connection is lost', async () => {
    const server = standup()
    open(server)
    const person = userEvent.setup()

    await joined(person)
    const first = await connected()
    await screen.findByRole('button', { name: 'Leave call' })

    act(() => first.close(DisconnectReason.SIGNAL_CLOSE))

    await waitFor(() => expect(Room.made).toHaveLength(2))
    expect(server.calls.filter((call) => call.path === `${standupPath}/call/join`)).toHaveLength(2)
  })

  /*
  The media client closes its own connection as a page is left. Rejoining then
  would seat somebody who is going away, so it ends quietly instead.
  */
  it('neither rejoins nor complains when the media client closes the connection itself', async () => {
    const server = standup()
    open(server)
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    await screen.findByRole('button', { name: 'Leave call' })

    act(() => media.close(DisconnectReason.CLIENT_INITIATED))

    expect(await screen.findByRole('button', { name: 'Start call' })).toBeInTheDocument()
    expect(Room.made).toHaveLength(1)
    expect(server.calls.filter((call) => call.path === `${standupPath}/call/join`)).toHaveLength(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('lists the calls running in my rooms and opens one', async () => {
    open(standup({ calls: [running] }))
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Calls' }))
    await person.click(await screen.findByRole('button', { name: /Standup/ }))

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Join call' })).toBeInTheDocument()
  })

  it('does not start a call in a closed room', async () => {
    const server = standup()
    server.on('GET', '/v1/me/rooms', { body: { data: [room({ status: 'closed' }), design] } })
    open(server)

    expect(await screen.findByRole('button', { name: 'Start call' })).toBeDisabled()
  })
})

describe('what a call says as it goes', () => {
  it('says when the connection is being got back', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    const stage = await screen.findByRole('region', { name: 'Call' })

    act(() => media.emit('reconnecting'))
    expect(await within(stage).findByText('Reconnecting to the call…')).toBeInTheDocument()

    act(() => media.emit('reconnected'))
    await waitFor(() => expect(within(stage).queryByText('Reconnecting to the call…')).not.toBeInTheDocument())
  })

  it('says when this person’s own connection is weak', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()

    act(() => media.weaken('local', ConnectionQuality.Poor))

    expect(await screen.findByText('Your connection is weak. Others may not hear or see you well.')).toBeInTheDocument()
  })

  it('marks somebody whose connection is weak or lost', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    act(() => media.arrive(brunoInTheCall.participant_id))
    const stage = await screen.findByRole('region', { name: 'Call' })
    await within(stage).findByText('Bruno Alves')

    act(() => media.weaken(brunoInTheCall.participant_id, ConnectionQuality.Poor))
    expect(await within(stage).findByText('weak connection')).toBeInTheDocument()

    act(() => media.weaken(brunoInTheCall.participant_id, ConnectionQuality.Lost))
    expect(await within(stage).findByText('connection lost')).toBeInTheDocument()
  })

  // Joining a call is not everybody in it arriving.
  it('does not announce the people already in the call as arriving', async () => {
    Room.alreadyInCall = [brunoInTheCall.participant_id]
    open(standup({ calls: [running] }))
    const person = userEvent.setup()

    await joined(person, 'Join call')
    await connected()

    const stage = await screen.findByRole('region', { name: 'Call' })
    expect(await within(stage).findByText('Bruno Alves')).toBeInTheDocument()
    expect(
      within(screen.getByRole('status', { name: 'Call notices' })).queryByText(/joined the call/),
    ).not.toBeInTheDocument()
  })

  it('says who joined and who left, for everybody listening', async () => {
    open(standup())
    const person = userEvent.setup()

    await joined(person)
    const media = await connected()
    const notices = screen.getByRole('status', { name: 'Call notices' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Leave call' })).toBeInTheDocument())

    act(() => media.arrive(brunoInTheCall.participant_id))
    expect(await within(notices).findByText('Bruno Alves joined the call.')).toBeInTheDocument()

    act(() => media.depart(brunoInTheCall.participant_id))
    expect(await within(notices).findByText('Bruno Alves left the call.')).toBeInTheDocument()
  })
})
