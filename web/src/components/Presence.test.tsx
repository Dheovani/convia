import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Person, RoomInvitation } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { heartbeatInterval, idleAfter, readInterval } from '../state/presence'
import { Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'

vi.mock('livekit-client', () => import('../test/livekit'))

beforeEach(() => {
  Room.reset()
  window.localStorage.clear()
  window.sessionStorage.clear()
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const roomPath = `/v1/me/rooms/${room().id}`
const bruno: Person = { user_id: 'usr_BRUNOALVES7QK4XMZP2VJH6TBW', display_name: 'Bruno Alves', role: 'member' }
const eva: Person = { user_id: 'usr_EVAELSEWHERE7QK4XMZP2VJH6', display_name: 'eva#7QK4', role: 'member' }

function convia() {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
    .on('GET', `${roomPath}/messages`, { body: { data: [] } })
    .on('GET', `${roomPath}/members`, {
      body: { data: [{ user_id: ana.user_id, display_name: ana.username, role: 'owner' }, bruno, eva] },
    })
    .on('GET', '/v1/me/people', { body: { data: [] } })
    .on('GET', `${roomPath}/invitations`, { body: { data: [] } })
}

type Asserted = { path: string; state: unknown }

function assertions(server: FakeConvia): Asserted[] {
  return server.calls
    .filter((call) => call.method === 'PUT' && call.path.startsWith('/v1/me/presence/'))
    .map((call) => ({ path: call.path, state: (call.body as { state?: unknown }).state }))
}

// A heartbeat names its page in the path, so the route is matched by prefix.
function answeringPresence(server: FakeConvia): FakeConvia {
  const original = globalThis.fetch
  vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://convia.test')
    if (url.pathname.startsWith('/v1/me/presence/')) {
      server.calls.push({
        method: init?.method ?? 'GET',
        path: url.pathname,
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      })
      return Promise.resolve(
        new Response(JSON.stringify({ user_id: ana.user_id, state: 'online' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    }
    return original(input, init)
  })
  return server
}

function open(server: FakeConvia) {
  server.install()
  answeringPresence(server)
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

describe("a person's own presence", () => {
  it('is said by the page as it opens, again as a heartbeat, and withdrawn as it goes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const server = convia()
    const page = open(server)

    await waitFor(() => expect(assertions(server)).toHaveLength(1))
    const [first] = assertions(server)
    expect(first?.state).toBe('online')
    expect(first?.path).toMatch(/^\/v1\/me\/presence\/page-[0-9a-f-]+$/)

    await act(() => vi.advanceTimersByTimeAsync(heartbeatInterval))
    expect(assertions(server).length).toBeGreaterThanOrEqual(2)
    expect(new Set(assertions(server).map((each) => each.path)).size).toBe(1)

    page.unmount()
    expect(server.calls.some((call) => call.method === 'DELETE' && call.path === first?.path)).toBe(true)
  })

  it('says away once the page has gone untouched, and available again when touched', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const server = convia()
    open(server)
    await waitFor(() => expect(assertions(server)).toHaveLength(1))

    await act(() => vi.advanceTimersByTimeAsync(idleAfter + 1_000))
    await waitFor(() => expect(assertions(server).at(-1)?.state).toBe('away'))
    expect(screen.getByRole('button', { name: 'Your status: Away' })).toBeInTheDocument()

    act(() => window.dispatchEvent(new Event('pointerdown')))
    await waitFor(() => expect(assertions(server).at(-1)?.state).toBe('online'))
  })

  // Somebody listening to a call is there without touching the page.
  it('is not away while the person is in a call', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const server = convia()
      .on('GET', '/v1/me/calls', { body: { data: [] } })
      .on('GET', `${roomPath}/call/participants`, { body: { data: [] } })
      .on('POST', `${roomPath}/call/join`, {
        status: 201,
        body: {
          participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
          call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
          media_url: 'wss://media.convia.test',
          media_token: 'a-credential-for-one-call',
          expires_at: '2026-09-14T18:35:00.000Z',
        },
      })
    open(server)
    const person = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    const ready = await screen.findByRole('region', { name: 'Prepare to join' })
    await person.click(within(ready).getByRole('button', { name: 'Start call' }))
    await screen.findByRole('button', { name: 'Leave call' })

    await act(() => vi.advanceTimersByTimeAsync(idleAfter * 2))
    expect(assertions(server).map((each) => each.state)).not.toContain('away')
  })

  it('says what the person chose, and remembers it in this browser', async () => {
    const server = convia()
    open(server)
    const person = userEvent.setup()

    const avatar = await screen.findByRole('button', { name: 'Your status: Available' })
    await person.click(avatar)
    const menu = screen.getByRole('group', { name: 'Your status' })
    expect(within(menu).getByRole('button', { name: 'Available' })).toHaveAttribute('aria-pressed', 'true')

    await person.click(within(menu).getByRole('button', { name: 'Busy' }))

    await waitFor(() => expect(assertions(server).at(-1)?.state).toBe('busy'))
    expect(window.localStorage.getItem('convia.status')).toBe('busy')
    expect(screen.getByRole('button', { name: 'Your status: Busy' })).toHaveFocus()

    await person.click(screen.getByRole('button', { name: 'Your status: Busy' }))
    await person.keyboard('{Escape}')
    expect(screen.queryByRole('group', { name: 'Your status' })).not.toBeInTheDocument()
  })
})

describe('the presence of people in a room', () => {
  it('shows who is available, in words too, and reads it again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const server = convia().on('GET', '/v1/me/people/presence', {
      body: {
        data: [
          { user_id: ana.user_id, state: 'online' },
          { user_id: bruno.user_id, state: 'busy' },
        ],
      },
    })
    open(server)
    const person = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const panel = screen.getByRole('complementary', { name: 'People in Standup' })
    const rows = within(panel).getAllByRole('listitem')

    await waitFor(() => expect(within(rows[1] as HTMLElement).getByText('Busy')).toBeInTheDocument())
    expect(within(rows[0] as HTMLElement).getByText('Available')).toBeInTheDocument()
    // A visitor is not answered for, and shows nothing rather than offline.
    expect(within(rows[2] as HTMLElement).queryByText(/Available|Busy|Away|Offline/)).toBeNull()

    const asked = server.calls.find((call) => call.path.startsWith('/v1/me/people/presence'))
    expect(asked?.path).toContain(`user_id=${bruno.user_id}`)

    const before = server.calls.filter((call) => call.path.startsWith('/v1/me/people/presence')).length
    await act(() => vi.advanceTimersByTimeAsync(readInterval))
    const after = server.calls.filter((call) => call.path.startsWith('/v1/me/people/presence')).length
    expect(after).toBeGreaterThan(before)
  })
})

const bia: RoomInvitation = {
  id: 'rin_BIA7KQZP4XN2VJH6TBWMDR3YAF',
  link: 'https://convia.example/invitations/rin_BIA7KQZP4XN2VJH6TBWMDR3YAF',
  invitee: 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH',
  expires_at: '2026-09-15T14:04:56.000Z',
}

describe('the invitations a person made', () => {
  it('are listed until they are withdrawn', async () => {
    const server = convia()
      .on('GET', `${roomPath}/invitations`, { body: { data: [bia] } })
      .on('DELETE', `/v1/me/room-invitations/${bia.id}`, { status: 204 })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const waiting = await screen.findByText('Waiting to join')
    const list = waiting.nextElementSibling as HTMLElement
    expect(within(list).getByText(bia.invitee)).toBeInTheDocument()
    expect(within(list).getByText(/^until /)).toBeInTheDocument()

    await person.click(within(list).getByRole('button', { name: `Copy the link for ${bia.invitee}` }))
    expect(await navigator.clipboard.readText()).toBe(bia.link)

    server.on('GET', `${roomPath}/invitations`, { body: { data: [] } })
    await person.click(within(list).getByRole('button', { name: `Withdraw the invitation for ${bia.invitee}` }))

    expect(server.asked('DELETE', `/v1/me/room-invitations/${bia.id}`)).toBeDefined()
    await waitFor(() => expect(screen.queryByText('Waiting to join')).not.toBeInTheDocument())
  })

  it('can be withdrawn just after it was made', async () => {
    const server = convia()
      .on('POST', `${roomPath}/invitations`, { status: 201, body: bia })
      .on('DELETE', `/v1/me/room-invitations/${bia.id}`, { status: 204 })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    await person.type(screen.getByLabelText('Handle'), bia.invitee)
    server.on('GET', `${roomPath}/invitations`, { body: { data: [bia] } })
    await person.click(screen.getByRole('button', { name: 'Invite' }))

    expect(await screen.findByLabelText('Invitation link')).toHaveValue(bia.link)
    // The one just made is shown once, above, rather than again in the list.
    expect(screen.queryByText('Waiting to join')).not.toBeInTheDocument()

    server.on('GET', `${roomPath}/invitations`, { body: { data: [] } })
    await person.click(screen.getByRole('button', { name: `Withdraw the invitation for ${bia.invitee}` }))

    await waitFor(() => expect(screen.queryByLabelText('Invitation link')).not.toBeInTheDocument())
    expect(server.asked('DELETE', `/v1/me/room-invitations/${bia.id}`)).toBeDefined()
  })

  it('says when they could not be read', async () => {
    open(
      convia().on('GET', `${roomPath}/invitations`, {
        status: 503,
        failure: { code: 'unavailable', message: 'no' },
      }),
    )
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))

    expect(await screen.findByText('The invitations you made could not be read.')).toBeInTheDocument()
  })
})
