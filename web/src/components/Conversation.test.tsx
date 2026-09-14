import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Workspace } from '../screens/Workspace'
import { FakeConvia, ana, message, room, withdrawn } from '../test/server'

afterEach(() => vi.unstubAllGlobals())

function open(server: FakeConvia) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

function conversation(messages = [message()], overrides = {}) {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room(overrides)] } })
    .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: messages } })
    .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
}

/*
The property the whole session surface rests on, seen from the other end.

Convia's session routes have no author field, so writing as somebody else is
unrepresentable rather than refused. This is the client half of that: what it
sends must carry the words and nothing else, because a field Convia does not
read is a field somebody will eventually believe in.
*/
describe('what the interface sends when somebody says something', () => {
  it('sends the words and nothing else', async () => {
    const server = conversation().on('POST', `/v1/me/rooms/${room().id}/messages`, {
      status: 201,
      body: message({ id: 'msg_second', sequence: 2, body: 'On my way.' }),
    })
    open(server)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Message Standup'), 'On my way.')
    await person.click(screen.getByRole('button', { name: 'Send' }))

    await screen.findByText('On my way.')

    const sent = server.asked('POST', `/v1/me/rooms/${room().id}/messages`)
    expect(sent?.body).toEqual({ body: 'On my way.' })
  })

  /*
  What was typed survives a refusal.

  Clearing the field on submit is the common shortcut and it loses the words on
  every failure. They are the one thing on this screen that cannot be recovered
  from the server.
  */
  it('keeps what was typed when Convia refuses it', async () => {
    const server = conversation().on('POST', `/v1/me/rooms/${room().id}/messages`, {
      status: 409,
      failure: { code: 'conflict', message: 'This room is closed.' },
    })
    open(server)
    const person = userEvent.setup()

    const field = await screen.findByLabelText('Message Standup')
    await person.type(field, 'Something I would rather not retype.')
    await person.click(screen.getByRole('button', { name: 'Send' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('closed')
    expect(field).toHaveValue('Something I would rather not retype.')
  })
})

describe('a message somebody withdraws', () => {
  /*
  A withdrawn message keeps its place and loses its words. That is what Convia
  stores, and an interface that closed the gap would quietly rewrite what people
  remember reading.
  */
  it('leaves the gap where the message was', async () => {
    const server = conversation().on('POST', `/v1/me/messages/${message().id}/delete`, {
      body: withdrawn(),
    })
    open(server)
    const person = userEvent.setup()

    await screen.findByText('Standup in five minutes.')
    await person.click(screen.getByRole('button', { name: 'Withdraw' }))

    expect(await screen.findByText('This message was withdrawn.')).toBeInTheDocument()
    expect(screen.queryByText('Standup in five minutes.')).toBeNull()
  })

  // Only the author is offered the choice, because only the author has it.
  it('offers nothing to somebody who did not write it', async () => {
    open(conversation([message({ user_id: 'usr_SOMEBODYELSE2VJH6TBWMDR3YA' })]))

    await screen.findByText('Standup in five minutes.')
    expect(screen.queryByRole('button', { name: 'Withdraw' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
  })
})

describe('the sidebar', () => {
  /*
  An unread count is a number a sighted person reads from a coloured circle.
  Without a label it is a number in a circle to everybody else.
  */
  it('says what its badge means', async () => {
    open(conversation([], { unread: 3 }))

    expect(await screen.findByText('3 unread messages')).toBeInTheDocument()
  })

  it('does not say "messages" about one message', async () => {
    open(conversation([], { unread: 1 }))

    expect(await screen.findByText('1 unread message')).toBeInTheDocument()
  })

  /*
  Reading is reported from the newest message on screen. Convia takes the
  greater of what it holds and what is marked, so this can only ever move
  forwards — but it has to be sent at all, or a badge never clears.
  */
  it('tells Convia how far the conversation has been read', async () => {
    const server = conversation([message({ sequence: 7 })])
    open(server)

    await screen.findByText('Standup in five minutes.')

    // Marked from an effect after the message is drawn, so it is waited for
    // rather than expected in the same instant the text appears.
    await waitFor(() =>
      expect(server.asked('PUT', `/v1/me/rooms/${room().id}/read_state`)?.body).toEqual({
        sequence: 7,
      }),
    )
  })
})

describe('a room that is closed', () => {
  it('does not offer a composer that cannot work', async () => {
    open(conversation([], { status: 'closed' }))

    const field = await screen.findByLabelText('Message Standup')
    expect(field).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
  })
})
