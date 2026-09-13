import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import type { ConviaEvent } from '../api/types'
import { Workspace } from '../screens/Workspace'
import { FakeConvia, ana, message, room } from '../test/server'
import { FakeSocket } from '../test/socket'
import { streamAddress } from './events'

afterEach(() => vi.unstubAllGlobals())

const history = `/v1/me/rooms/${room().id}/messages`
const newer = `${history}?limit=50&cursor=1&direction=newer`

function conversation(): FakeConvia {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
    .on('GET', history, { body: { data: [message()] } })
    .on('GET', newer, { body: { data: [] } })
    .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
}

function event(type: string, subject: ConviaEvent['subject'], data: Record<string, unknown>): ConviaEvent {
  return {
    id: 'evt_2QF7XKN4VJH6TBWMDR3YAC5EZP',
    version: 1,
    type,
    occurred_at: '2026-09-05T14:05:00.000Z',
    application_id: message().application_id,
    subject,
    data,
  }
}

// connected waits for the interface to dial the stream, and lets it open.
async function connected(): Promise<FakeSocket> {
  const socket = await waitFor(() => {
    const latest = FakeSocket.latest()
    if (latest === undefined) {
      throw new Error('the interface never opened the stream')
    }
    return latest
  })
  act(() => socket.open())
  return socket
}

function count(server: FakeConvia, method: string, path: string): number {
  return server.calls.filter((call) => call.method === method && call.path === path).length
}

/*
settle lets whatever the interface does on its own finish, so that what a test
sees next was caused by what it did next.

It waits for the requests to stop rather than for a fixed time. Opening the
stream sets off a chain — the room is read again, marked read, and the sidebar
read after a gathering delay — and a fixed wait shorter than that chain lets its
last read land after the test has changed a route, where it passes for the
event's work. A mutation test found exactly that.
*/
async function settle(server: FakeConvia): Promise<void> {
  for (;;) {
    const before = server.calls.length
    await act(() => new Promise((resolve) => setTimeout(resolve, 350)))
    if (server.calls.length === before) {
      return
    }
  }
}

describe('the stream', () => {
  /*
  The page is served from the origin it listens on, and Convia refuses a
  handshake from any other. Building the address from anything but the page's own
  location — a configured host, a hard-coded port — would work in development and
  be refused in production.
  */
  it('listens on the origin the page came from', () => {
    expect(streamAddress({ protocol: 'https:', host: 'convia.example' } as Location)).toBe(
      'wss://convia.example/v1/me/events',
    )
    expect(streamAddress({ protocol: 'http:', host: 'localhost:5173' } as Location)).toBe(
      'ws://localhost:5173/v1/me/events',
    )
  })
})

describe('being told rather than asking', () => {
  /*
  The whole point of M18-018. While the stream is open the room is not asked on
  a timer, so a message that appears within a second of its event can only have
  been fetched because the event said so.
  */
  it('reads what was just said when Convia says it was said', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()
    await settle(server)

    server.on('GET', newer, {
      body: { data: [message({ id: 'msg_second', sequence: 2, body: 'On my way.' })] },
    })
    act(() =>
      socket.deliver(
        event('message.posted', { type: 'message', id: 'msg_second' }, { room_id: room().id, sequence: 2 }),
      ),
    )

    expect(await screen.findByText('On my way.')).toBeInTheDocument()
  })

  it('reads a message again when Convia says it changed', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()
    await settle(server)

    server.on('GET', `${history}?limit=1&cursor=0&direction=newer`, {
      body: { data: [message({ body: 'Standup in ten minutes.', edited_at: '2026-09-05T14:06:00.000Z' })] },
    })
    act(() =>
      socket.deliver(
        event('message.edited', { type: 'message', id: message().id }, { room_id: room().id, sequence: 1 }),
      ),
    )

    expect(await screen.findByText('Standup in ten minutes.')).toBeInTheDocument()
  })

  // An event about a room that is not open changes the sidebar, and nothing
  // else: the open conversation must not read anything for it.
  it('leaves the open room alone when the news is about another', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()
    await settle(server)

    const before = count(server, 'GET', newer)
    act(() =>
      socket.deliver(
        event('message.posted', { type: 'message', id: 'msg_elsewhere' }, { room_id: 'room_OTHER', sequence: 9 }),
      ),
    )
    await settle(server)

    expect(count(server, 'GET', newer)).toBe(before)
  })

  /*
  Somebody else added this person to a room. The response went to them, so the
  only way this sidebar learns it is the event — and it must, without a reload.
  */
  it('shows a room this person was just added to', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()
    await settle(server)

    server.on('GET', '/v1/me/rooms', {
      body: { data: [room(), room({ id: 'room_RETRO4XN2VJH6TBWMDR3YAFC5E', name: 'Retro' })] },
    })
    act(() =>
      socket.deliver(
        event('room.member_added', { type: 'room', id: 'room_RETRO4XN2VJH6TBWMDR3YAFC5E' }, { user_id: ana.user_id }),
      ),
    )

    expect(await screen.findByText('Retro')).toBeInTheDocument()
  })

  /*
  A person removed from the room they are looking at must not go on looking at
  it, whatever the next read of the sidebar says or whether it arrives. The read
  fails here, so that only the event can be what closed the room.
  */
  it('closes a room this person no longer belongs to', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByRole('heading', { name: 'Standup' })
    const socket = await connected()
    await settle(server)

    server.on('GET', '/v1/me/rooms', {
      status: 503,
      failure: { code: 'unavailable', message: 'Convia is briefly unavailable.' },
    })
    act(() =>
      socket.deliver(event('room.member_removed', { type: 'room', id: room().id }, { user_id: ana.user_id })),
    )

    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Standup' })).toBeNull())
    expect(screen.getByText('Nothing is open.')).toBeInTheDocument()
  })

  // Somebody joining the open room is read as it happens, which is how their
  // first message arrives already carrying their name.
  it('reads who is here again when somebody joins', async () => {
    const members = `/v1/me/rooms/${room().id}/members`
    const server = conversation().on('GET', members, {
      body: { data: [{ user_id: ana.user_id, display_name: ana.username }] },
    })
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()
    await settle(server)

    const before = count(server, 'GET', `${members}?limit=100`)
    act(() =>
      socket.deliver(
        event('room.member_added', { type: 'room', id: room().id }, { user_id: 'usr_BEA4XN2VJH6TBWMDR3YAFC5E7' }),
      ),
    )

    await waitFor(() => expect(count(server, 'GET', `${members}?limit=100`)).toBe(before + 1))
  })

  /*
  A type this build has never heard of is ignored, as the contract asks. An
  interface that threw on one would break the day Convia added a sixth.
  */
  it('ignores what it does not understand', async () => {
    const server = conversation()
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    await screen.findByText('Standup in five minutes.')
    const socket = await connected()

    act(() => socket.deliver(event('call.started', { type: 'call', id: 'call_1' }, { room_id: room().id })))
    act(() => socket.onmessage?.(new MessageEvent('message', { data: 'not json' })))
    await settle(server)

    expect(screen.getByText('Standup in five minutes.')).toBeInTheDocument()
  })
})

describe('a stream that ends', () => {
  /*
  Convia closes a stream with 4001 when its session stops authenticating — the
  person signed out everywhere, say, from another device. A refused reconnect
  would never say why, so this close is the one chance to leave the screen.
  */
  it('returns to the sign-in form when the session is gone', async () => {
    const server = new FakeConvia()
      .on('GET', '/v1/me', { body: ana })
      .on('GET', '/v1/me/rooms', { body: { data: [] } })
    server.install()
    render(<App />)

    await screen.findByText('Nothing is open.')
    const socket = await connected()

    server.on('GET', '/v1/me', {
      status: 401,
      failure: { code: 'unauthenticated', message: 'The request did not carry a usable session.' },
    })
    act(() => socket.close(4001))

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  // Any other ending is a connection problem rather than a verdict on the
  // session, and the interface tries again.
  it('reconnects after an ordinary close', async () => {
    const server = new FakeConvia().on('GET', '/v1/me/rooms', { body: { data: [] } })
    server.install()
    render(<Workspace account={ana} onSignedOut={() => {}} />)

    const socket = await connected()
    act(() => socket.close(1001))

    await waitFor(() => expect(FakeSocket.opened.length).toBe(2), { timeout: 2_000 })
    expect(server.asked('GET', '/v1/me')).toBeUndefined()
  })
})
