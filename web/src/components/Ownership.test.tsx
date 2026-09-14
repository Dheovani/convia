import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Person } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { FakeConvia, ana, message, room } from '../test/server'

afterEach(() => vi.unstubAllGlobals())

const roomPath = `/v1/me/rooms/${room().id}`
const owner: Person = { user_id: ana.user_id, display_name: ana.username, role: 'owner' }
const bruno: Person = { user_id: 'usr_BRUNOALVES7QK4XMZP2VJH6TBW', display_name: 'Bruno Alves', role: 'member' }
const saidByBruno = message({ id: 'msg_BRUNOSAID7QK4XMZP2VJH6TBW', user_id: bruno.user_id, body: 'Off topic.' })

function open(server: FakeConvia) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

function standup({ owned }: { owned: boolean }) {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room({ owned })] } })
    .on('GET', `${roomPath}/messages`, { body: { data: [saidByBruno] } })
    .on('PUT', `${roomPath}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
    .on('GET', `${roomPath}/members`, {
      body: { data: owned ? [owner, bruno] : [{ ...owner, role: 'member' }, { ...bruno, role: 'owner' }] },
    })
    .on('GET', `${roomPath}/bans`, { body: { data: [] } })
}

describe('owning a room', () => {
  it('lets the owner rename the room', async () => {
    const server = standup({ owned: true }).on('PATCH', roomPath, {
      body: { id: room().id, name: 'Weekly standup', status: 'open', owned: true, created_at: '2026-09-13T10:02:41Z' },
    })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Room' }))
    await person.click(screen.getByRole('button', { name: 'Rename' }))
    const field = screen.getByLabelText('Room name')
    await person.clear(field)
    await person.type(field, 'Weekly standup')

    server.on('GET', '/v1/me/rooms', { body: { data: [room({ owned: true, name: 'Weekly standup' })] } })
    await person.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('heading', { name: 'Weekly standup' })).toBeInTheDocument()
    expect(server.asked('PATCH', roomPath)?.body).toEqual({ name: 'Weekly standup' })
  })

  /*
  Convia refuses a member everything an owner does, so the page leaves the
  controls out rather than offering buttons that could only fail.
  */
  it('offers a member none of it', async () => {
    open(standup({ owned: false }))
    const person = userEvent.setup()

    await screen.findByRole('heading', { name: 'Standup' })
    expect(screen.queryByRole('button', { name: 'Room' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull()

    await person.click(screen.getByRole('button', { name: 'People' }))
    const panel = await screen.findByRole('complementary', { name: 'People in Standup' })
    expect(await within(panel).findByText('· owner')).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'Ban Bruno Alves' })).toBeNull()
    expect(within(panel).queryByText('Banned')).toBeNull()
  })

  /*
  A message the owner takes down must not read as its author taking it back, so
  the tombstone says who did.
  */
  it('lets the owner take down what somebody else said, and says the owner did', async () => {
    const server = standup({ owned: true }).on('POST', `/v1/me/messages/${saidByBruno.id}/delete`, {
      body: {
        ...saidByBruno,
        body: undefined,
        deleted: true,
        deleted_at: '2026-09-05T14:09:44.201Z',
        deleted_by: 'owner',
      },
    })
    open(server)
    const person = userEvent.setup()

    await screen.findByText('Off topic.')
    await person.click(screen.getByRole('button', { name: 'Remove' }))

    expect(await screen.findByText("Removed by the room's owner.")).toBeInTheDocument()
    expect(server.asked('POST', `/v1/me/messages/${saidByBruno.id}/delete`)).toBeDefined()
  })

  it('lets the owner ban somebody, and lift the ban', async () => {
    const banPath = `${roomPath}/bans/${bruno.user_id}`
    const server = standup({ owned: true }).on('PUT', banPath, { status: 204 }).on('DELETE', banPath, { status: 204 })
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'People' }))
    const panel = await screen.findByRole('complementary', { name: 'People in Standup' })

    server
      .on('GET', `${roomPath}/members`, { body: { data: [owner] } })
      .on('GET', `${roomPath}/bans`, { body: { data: [{ user_id: bruno.user_id, display_name: 'Bruno Alves' }] } })
    await person.click(await within(panel).findByRole('button', { name: 'Ban Bruno Alves' }))

    expect(server.asked('PUT', banPath)).toBeDefined()
    await person.click(within(panel).getByText('Banned'))
    const unban = await within(panel).findByRole('button', { name: 'Unban Bruno Alves' })

    server.on('GET', `${roomPath}/bans`, { body: { data: [] } })
    await person.click(unban)

    expect(await within(panel).findByText('Nobody is banned from this room.')).toBeInTheDocument()
    expect(server.asked('DELETE', banPath)).toBeDefined()
  })
})
