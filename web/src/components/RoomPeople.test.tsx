import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Message, Person } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { FakeConvia, ana, message, room } from '../test/server'

afterEach(() => vi.unstubAllGlobals())

const herself: Person = { user_id: ana.user_id, display_name: ana.display_name }
const bruno: Person = { user_id: 'usr_BRUNOALVES7QK4XMZP2VJH6TBW', display_name: 'Bruno Alves' }
const carla: Person = { user_id: 'usr_CARLASOUZA7QK4XMZP2VJH6TBW', display_name: 'Carla Souza' }

const roomPath = `/v1/me/rooms/${room().id}`

function open(server: FakeConvia) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

function standup(messages: Message[] = []) {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
    .on('GET', `${roomPath}/messages`, { body: { data: messages } })
    .on('PUT', `${roomPath}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
}

describe('opening a room', () => {
  /*
  The request carries a name and nothing else, because Convia accepts nothing
  else from a person: an alias, metadata and a capacity are the application's
  to decide. And the room opens — a room somebody just made and then has to go
  looking for is a room they will make twice.
  */
  it('sends a name and nothing else, and opens the room', async () => {
    const weekend = room({ id: 'room_WEEKENDPLANS7QK4XMZP2VJHT', name: 'Weekend plans' })
    const server = standup()
      .on('POST', '/v1/me/rooms', {
        status: 201,
        body: { id: weekend.id, name: weekend.name, status: 'open', created_at: '2026-09-13T10:02:41Z' },
      })
      .on('GET', `/v1/me/rooms/${weekend.id}/messages`, { body: { data: [] } })
    open(server)
    const person = userEvent.setup()

    await screen.findByRole('heading', { name: 'Standup' })
    server.on('GET', '/v1/me/rooms', { body: { data: [room(), weekend] } })

    await person.click(screen.getByRole('button', { name: 'New conversation' }))
    await person.type(screen.getByLabelText('Conversation name'), 'Weekend plans')
    await person.click(screen.getByRole('button', { name: 'Create' }))

    expect(await screen.findByRole('heading', { name: 'Weekend plans' })).toBeInTheDocument()
    expect(server.asked('POST', '/v1/me/rooms')?.body).toEqual({ name: 'Weekend plans' })
  })
})

describe('adding somebody', () => {
  /*
  Who can be added comes from Convia and from nowhere else. The panel offers the
  people this person already shares a room with and are not here yet, and it has
  no field to type somebody into — which is the client half of the rule that
  discovery is a shared room rather than a lookup.
  */
  it('offers only people Convia says can be added, and nowhere to type anybody', async () => {
    const server = standup()
      .on('GET', `${roomPath}/members`, { body: { data: [herself, carla] } })
      .on('GET', '/v1/me/people', { body: { data: [bruno, carla] } })
      .on('PUT', `${roomPath}/members/${bruno.user_id}`, {
        status: 201,
        body: {
          application_id: 'app_MXHJAY4MJNX2FO22XWJ3XNCKHT',
          room_id: room().id,
          user_id: bruno.user_id,
          created_at: '2026-09-13T10:05:00Z',
        },
      })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const panel = await screen.findByRole('complementary', { name: 'People in Standup' })

    await person.click(within(panel).getByRole('button', { name: 'Show people you can add' }))

    expect(await within(panel).findByRole('button', { name: 'Add Bruno Alves' })).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'Add Carla Souza' })).toBeNull()
    expect(within(panel).queryByRole('textbox')).toBeNull()

    server.on('GET', `${roomPath}/members`, { body: { data: [herself, carla, bruno] } })
    await person.click(within(panel).getByRole('button', { name: 'Add Bruno Alves' }))

    expect(server.asked('PUT', `${roomPath}/members/${bruno.user_id}`)).toBeDefined()
    await waitFor(() =>
      expect(within(panel).queryByRole('button', { name: 'Add Bruno Alves' })).toBeNull(),
    )
    expect(within(panel).getByText('Bruno Alves')).toBeInTheDocument()
  })

  // Convia gives one answer for every reason somebody cannot be added, so the
  // interface gives one sentence rather than guessing at which it was.
  it('says one thing when somebody cannot be added', async () => {
    const server = standup()
      .on('GET', `${roomPath}/members`, { body: { data: [herself] } })
      .on('GET', '/v1/me/people', { body: { data: [bruno] } })
      .on('PUT', `${roomPath}/members/${bruno.user_id}`, {
        status: 404,
        failure: { code: 'not_found', message: 'That person is not somebody you can add to this room.' },
      })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const panel = await screen.findByRole('complementary', { name: 'People in Standup' })
    await person.click(within(panel).getByRole('button', { name: 'Show people you can add' }))
    await person.click(await within(panel).findByRole('button', { name: 'Add Bruno Alves' }))

    const complaint = await within(panel).findByRole('alert')
    expect(complaint).toHaveTextContent('Bruno Alves cannot be added to this room.')
    for (const guess of ['suspended', 'does not exist', 'stranger']) {
      expect(complaint.textContent?.toLowerCase()).not.toContain(guess)
    }
  })
})

describe('leaving', () => {
  // Leaving is the one act here a person cannot undo alone, so nothing is sent
  // until they confirm it.
  it('asks first, and then leaves', async () => {
    const server = standup()
      .on('GET', `${roomPath}/members`, { body: { data: [herself] } })
      .on('POST', `${roomPath}/leave`, { status: 204 })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const panel = await screen.findByRole('complementary', { name: 'People in Standup' })

    await person.click(within(panel).getByRole('button', { name: 'Leave this room' }))
    expect(server.asked('POST', `${roomPath}/leave`)).toBeUndefined()

    server.on('GET', '/v1/me/rooms', { body: { data: [] } })
    await person.click(within(panel).getByRole('button', { name: 'Leave' }))

    expect(await screen.findByText('Nothing is open.')).toBeInTheDocument()
    expect(server.asked('POST', `${roomPath}/leave`)).toBeDefined()
  })
})

describe('who said what', () => {
  /*
  A conversation with more than one person in it needs its speakers named. A
  message from somebody the member list does not know asks for the list again
  once — and only once, or a departed author would turn into a request loop.
  */
  it('names each speaker, and asks about a stranger only once', async () => {
    const departed = 'usr_DEPARTED7QK4XMZP2VJH6TBWMD'
    const server = standup([
      message({ id: 'msg_1', sequence: 1, user_id: ana.user_id, body: 'Morning.' }),
      message({ id: 'msg_2', sequence: 2, user_id: bruno.user_id, body: 'Morning, Ana.' }),
      message({ id: 'msg_3', sequence: 3, user_id: departed, body: 'Signing off for good.' }),
    ]).on('GET', `${roomPath}/members`, { body: { data: [herself, bruno] } })
    open(server)

    expect(await screen.findByText('Bruno Alves')).toBeInTheDocument()
    expect(screen.getByText('You')).toBeInTheDocument()
    expect(await screen.findByText('Somebody who left')).toBeInTheDocument()

    /*
    Let the page settle for a fixed number of turns before counting.

    The first version of this test counted as soon as the name appeared, which
    is before a loop has had a chance to show itself — and it passed with the
    guard removed. A loop re-reads the list on every turn, so given turns it is
    visible as a count that keeps climbing, while a correct page stops at two.
    Turns rather than milliseconds, so the answer does not depend on how fast
    the machine is.
    */
    for (let turn = 0; turn < 25; turn++) {
      await act(async () => {})
    }

    const readsOfMembers = server.calls.filter(
      (call) => call.method === 'GET' && call.path.startsWith(`${roomPath}/members`),
    )
    expect(readsOfMembers.length).toBeLessThanOrEqual(2)
  })
})
