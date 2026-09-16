import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { JoinSession } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'

vi.mock('livekit-client', () => import('../test/livekit'))

const standupPath = `/v1/me/rooms/${room().id}`
const design = room({ id: 'room_DESIGNREVIEW7QK4XMZP2VJH6T', name: 'Design review' })

const session: JoinSession = {
  participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
  call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
  media_url: 'wss://media.convia.test',
  media_token: 'a-credential-for-one-call',
  expires_at: '2026-09-14T18:35:00.000Z',
}

// screenOf pretends the screen is narrow or wide, which is all useNarrow asks.
function screenOf(narrow: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: narrow,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }))
}

function workspace() {
  const server = new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room(), design] } })
    .on('GET', '/v1/me/calls', { body: { data: [] } })
    .on('POST', `${standupPath}/call/join`, { status: 201, body: session })
    .on('POST', `${standupPath}/call/leave`, { status: 204 })
    .on('GET', `${standupPath}/call/participants`, { body: { data: [] } })
  for (const path of [standupPath, `/v1/me/rooms/${design.id}`]) {
    server.on('GET', `${path}/messages`, { body: { data: [] } }).on('GET', `${path}/members`, { body: { data: [] } })
  }
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

beforeEach(() => Room.reset())
afterEach(() => vi.unstubAllGlobals())

describe('a narrow screen', () => {
  beforeEach(() => screenOf(true))

  it('shows the list first, and a conversation in its place', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Standup' }))

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(screen.queryByRole('complementary', { name: 'Conversations' })).not.toBeInTheDocument()

    await person.click(screen.getByRole('button', { name: 'Back to conversations' }))

    expect(await screen.findByRole('complementary', { name: 'Conversations' })).toBeInTheDocument()
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
  })

  it('opens nothing until a conversation is chosen', async () => {
    workspace()

    expect(await screen.findByRole('button', { name: 'Standup' })).toBeInTheDocument()
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Skip to the conversation' })).not.toBeInTheDocument()
  })

  it('keeps the rail at hand, and shows the list of the destination chosen', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Standup' }))
    await screen.findByRole('heading', { name: 'Standup' })

    await person.click(within(screen.getByRole('navigation', { name: 'Convia' })).getByRole('button', { name: 'Calls' }))

    expect(await screen.findByText('No call is running in your rooms. Start one from a conversation.')).toBeInTheDocument()
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
  })

  it('shows the call a person is in above the list, and goes back to it', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Standup' }))
    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    const ready = await screen.findByRole('region', { name: 'Prepare to join' })
    await person.click(within(ready).getByRole('button', { name: 'Start call' }))
    await screen.findByRole('button', { name: 'Leave call' })

    await person.click(screen.getByRole('button', { name: 'Back to conversations' }))

    const bar = await screen.findByRole('region', { name: 'Current call' })
    expect(within(screen.getByRole('complementary', { name: 'Conversations' })).getByRole('region', { name: 'Current call' })).toBe(bar)

    await person.click(within(bar).getByRole('button', { name: 'Return' }))

    expect(await screen.findByRole('region', { name: 'Call' })).toBeInTheDocument()
  })
})

describe('a wide screen', () => {
  beforeEach(() => screenOf(false))

  it('shows the list and the conversation side by side, with no way back to need', async () => {
    workspace()

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'Conversations' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Back to conversations' })).not.toBeInTheDocument()
  })
})
