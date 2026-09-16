import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { JoinSession } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'

vi.mock('livekit-client', () => import('../test/livekit'))

/*
The keyboard and the screen reader, held to WCAG 2.2 AA as the product owner set.

These are the checks a person makes by hand when they cannot use a mouse, written
down: that there is a way past the lists, that what opens can be closed with
Escape, that the keyboard lands somewhere useful when something appears and comes
back to where it was when it goes, and that every control says what it is.
*/

const roomPath = `/v1/me/rooms/${room().id}`

const session: JoinSession = {
  participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
  call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
  media_url: 'wss://media.convia.test',
  media_token: 'a-credential-for-one-call',
  expires_at: '2026-09-14T18:35:00.000Z',
}

function workspace({ owned = false } = {}) {
  new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room({ owned })] } })
    .on('GET', `${roomPath}/messages`, { body: { data: [] } })
    .on('GET', `${roomPath}/members`, { body: { data: [] } })
    .on('GET', '/v1/me/calls', { body: { data: [] } })
    .on('POST', `${roomPath}/call/join`, { status: 201, body: session })
    .on('POST', `${roomPath}/call/leave`, { status: 204 })
    .on('GET', `${roomPath}/call/participants`, { body: { data: [] } })
    .install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

beforeEach(() => Room.reset())
afterEach(() => vi.unstubAllGlobals())

describe('the keyboard', () => {
  it('can go past the lists straight to the conversation', async () => {
    workspace()
    await screen.findByRole('heading', { name: 'Standup' })

    const skip = screen.getByRole('link', { name: 'Skip to the conversation' })
    expect(skip).toHaveAttribute('href', '#conversation')

    const conversation = screen.getByRole('main')
    expect(conversation).toHaveAttribute('id', 'conversation')
    expect(conversation).toHaveAttribute('tabindex', '-1')
  })

  it('closes the room menu with Escape and goes back to its button', async () => {
    workspace({ owned: true })
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Room' }))
    const menu = screen.getByRole('group', { name: 'Settings for Standup' })
    await person.tab()
    expect(within(menu).getByRole('button', { name: 'Rename' })).toHaveFocus()

    await person.keyboard('{Escape}')

    expect(screen.queryByRole('group', { name: 'Settings for Standup' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Room' })).toHaveFocus()
  })

  it('lands on joining when getting ready, and goes back to the call button on cancelling', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    const ready = await screen.findByRole('region', { name: 'Prepare to join' })
    expect(within(ready).getByRole('button', { name: 'Start call' })).toHaveFocus()

    await person.keyboard('{Tab}')
    await person.click(within(ready).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.getByRole('button', { name: 'Start call' })).toHaveFocus())
  })

  it('goes back to the call button when the call is left', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    await person.keyboard('{Enter}')
    await person.click(await screen.findByRole('button', { name: 'Leave call' }))

    await waitFor(() => expect(screen.getByRole('button', { name: 'Start call' })).toHaveFocus())
  })
})

describe('a screen reader', () => {
  it('is told what every control in a call is, and who is in it', async () => {
    workspace()
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    await person.keyboard('{Enter}')

    const controls = await screen.findByRole('group', { name: 'Call controls' })
    for (const name of ['Mute', 'Start camera', 'Devices', 'Leave call']) {
      expect(within(controls).getByRole('button', { name })).toBeInTheDocument()
    }
    expect(within(controls).getByRole('button', { name: 'Mute' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('list', { name: 'People in the call' })).toBeInTheDocument()
  })

  it('is told the page is Convia, and what each zone is', async () => {
    workspace()

    expect(await screen.findByRole('heading', { level: 1, name: 'Convia' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Convia' })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'Conversations' })).toBeInTheDocument()
    expect(screen.getByRole('status', { name: 'Call notices' })).toBeInTheDocument()
  })
})
