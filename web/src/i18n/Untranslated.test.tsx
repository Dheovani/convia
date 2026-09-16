import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import type { CallPresence, JoinSession, Person } from '../api/types'
import { ConnectionQuality, Room } from '../test/livekit'
import { marked, markedLanguage, unmarked } from '../test/language'
import { FakeConvia, ana, message, room, withdrawn } from '../test/server'
import { LanguageContext } from './language'

vi.mock('livekit-client', () => import('../test/livekit'))

beforeEach(() => {
  Room.reset()
  window.localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

/*
Every screen these tests reach is rendered in a language whose words are all
marked, and nothing unmarked may be on it. A string written straight into a
component fails here, rather than being found in English by the first person who
reads the interface in another language.
*/

const said = marked

function speaking() {
  return render(
    <LanguageContext.Provider value={markedLanguage}>
      <App />
    </LanguageContext.Provider>,
  )
}

const standupPath = `/v1/me/rooms/${room().id}`
const bruno: Person = { user_id: 'usr_BRUNOALVES7QK4XMZP2VJH6TBW', display_name: 'Bruno Alves', role: 'member' }
const carla: Person = { user_id: 'usr_CARLADIAS7QK4XMZP2VJH6TBWM', display_name: 'Carla Dias' }
const closedRoom = room({ id: 'room_CLOSEDROOM7QK4XMZP2VJH6TB', name: 'Archive', status: 'closed' })

const session: JoinSession = {
  participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
  call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
  media_url: 'wss://media.convia.test',
  media_token: 'a-credential-for-one-call',
  expires_at: '2026-09-14T18:35:00.000Z',
}

const brunoInTheCall: CallPresence = {
  participant_id: 'part_BRUNOINTHECALL7QK4XMZP2VJ',
  user_id: bruno.user_id,
  display_name: bruno.display_name,
  role: 'member',
  joined_at: '2026-09-14T18:31:00.000Z',
}

// What people wrote, which is theirs and not the catalogue's.
const data = [
  'Convia',
  'Standup',
  'Archive',
  'Weekly sync',
  'convia.elsewhere.test',
  'Standup in five minutes.',
  'Off topic.',
  'Bruno Alves',
  'Carla Dias',
  'ana',
  ana.handle,
  'Headset',
  'A',
  'BA',
  /^\d{1,2}:\d{2} [AP]M$/,
  new Date(message().created_at).toLocaleDateString('en', { weekday: 'long', month: 'long', day: 'numeric' }),
]

function expectAllMarked() {
  expect(unmarked(document.body, data)).toEqual([])
}

function workspace() {
  return new FakeConvia()
    .on('GET', '/v1/me', { body: ana })
    .on('GET', '/v1/me/rooms', { body: { data: [room({ owned: true, unread: 3 }), closedRoom] } })
    .on('GET', '/v1/me/remote-rooms', {
      body: {
        data: [
          {
            id: 'rrm_7KQZP4XN2VJH6TBWMDR3YAFC5E',
            name: 'Weekly sync',
            home: 'https://convia.elsewhere.test',
            user_id: 'usr_ANATHERE7KQZP4XN2VJH6TBWMD',
          },
        ],
      },
    })
    .on('GET', '/v1/me/calls', {
      body: { data: [{ id: session.call_id, room_id: room().id, status: 'active', created_at: '2026-09-14T18:30:00.000Z' }] },
    })
    .on('GET', `${standupPath}/messages`, {
      body: {
        data: [
          message(),
          message({ id: 'msg_BRUNOSAID7QK4XMZP2VJH6TBW', sequence: 2, user_id: bruno.user_id, body: 'Off topic.' }),
          { ...withdrawn(), id: 'msg_WITHDRAWN7QK4XMZP2VJH6TBW', sequence: 3 },
        ],
      },
    })
    .on('PUT', `${standupPath}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 3, unread: 0 },
    })
    .on('GET', `${standupPath}/members`, {
      body: { data: [{ user_id: ana.user_id, display_name: ana.username, role: 'owner' }, bruno] },
    })
    .on('GET', `${standupPath}/bans`, { body: { data: [] } })
    .on('GET', '/v1/me/people', { body: { data: [bruno, carla] } })
    .on('POST', `${standupPath}/call/join`, { status: 200, body: session })
    .on('POST', `${standupPath}/call/leave`, { status: 204 })
    .on('GET', `${standupPath}/call/participants`, {
      body: {
        data: [
          { ...brunoInTheCall, participant_id: session.participant_id, user_id: ana.user_id, display_name: 'ana' },
          brunoInTheCall,
        ],
      },
    })
}

describe('every word on screen comes from the catalogue', () => {
  it('when signing in and creating an account', async () => {
    new FakeConvia()
      .on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
      .install()
    speaking()
    const person = userEvent.setup()

    await screen.findByRole('button', { name: said.signIn.signIn })
    expectAllMarked()

    await person.click(screen.getByRole('button', { name: said.signIn.wantAccount }))
    await person.type(screen.getByLabelText(said.signIn.username), '!')
    await person.click(screen.getByRole('button', { name: said.signIn.createAccount }))

    expect(await screen.findByRole('alert')).toHaveTextContent(said.signIn.badUsername)
    expectAllMarked()
  })

  it('in a room its owner reads, with the people and the room menu open', async () => {
    workspace().install()
    speaking()
    const person = userEvent.setup()

    await screen.findByText('Off topic.')
    await screen.findByText(said.conversation.withdrawn)
    expectAllMarked()

    await person.click(screen.getByRole('button', { name: said.conversation.people }))
    await screen.findByRole('button', { name: said.people.addNamed('Carla Dias') })
    await screen.findByText(said.people.nobodyBanned)
    expectAllMarked()

    await person.click(screen.getByRole('button', { name: said.roomSettings.open }))
    await person.click(screen.getByRole('button', { name: said.roomSettings.deleteRoom }))
    expectAllMarked()
  })

  it('while getting ready for a call, in it, and in the list of calls', async () => {
    Room.devices = [
      { deviceId: 'mic-headset', kind: 'audioinput', label: 'Headset' },
      { deviceId: 'mic-unnamed', kind: 'audioinput', label: '' },
    ]
    Room.refusals = { camera: 'NotReadableError' }
    workspace().install()
    speaking()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: said.call.join }))
    const ready = await screen.findByRole('region', { name: said.call.prepare })
    await person.click(within(ready).getByRole('button', { name: said.call.cameraOn }))
    await within(ready).findByRole('option', { name: said.call.numbered('audioinput', 2) })
    await within(ready).findByText(said.call.refused('busy', 'videoinput'))
    expectAllMarked()

    Room.refusals = {}
    await person.click(within(ready).getByRole('button', { name: said.call.join }))
    const media = await waitFor(() => {
      const opened = Room.latest()
      expect(opened?.connect).toHaveBeenCalled()
      return opened as Room
    })
    const stage = await screen.findByRole('region', { name: said.call.stage })
    await within(stage).findByRole('button', { name: said.call.leave })

    act(() => media.arrive(brunoInTheCall.participant_id))
    await within(stage).findByText('Bruno Alves')
    act(() => media.weaken(brunoInTheCall.participant_id, ConnectionQuality.Poor))
    act(() => media.weaken('local', ConnectionQuality.Poor))
    await within(stage).findAllByText(said.call.weakConnection)
    await within(stage).findByText(said.call.weak)
    await person.click(within(stage).getByRole('button', { name: said.call.devices }))
    expectAllMarked()

    await person.click(screen.getByRole('button', { name: said.rail.calls }))
    await screen.findByRole('region', { name: said.call.current })
    await screen.findByText(new RegExp(said.call.since('').replace(/[⟦⟧]/g, '').trim()))
    expectAllMarked()
  })
})
