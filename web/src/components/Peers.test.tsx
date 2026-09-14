import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Workspace } from '../screens/Workspace'
import { FakeConvia, ana, message, room } from '../test/server'

afterEach(() => vi.unstubAllGlobals())

const invitationId = 'rin_7KQZP4XN2VJH6TBWMDR3YAFC5E'
const remoteId = 'rrm_7KQZP4XN2VJH6TBWMDR3YAFC5E'
// Who ana is at the other installation, which is not who she is here.
const anaThere = 'usr_ANATHERE7KQZP4XN2VJH6TBWMD'

function open(server: FakeConvia) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={() => {}} />)
}

function roomHere() {
  return new FakeConvia()
    .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
    .on('GET', '/v1/me/remote-rooms', { body: { data: [] } })
    .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: [message()] } })
    .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
    .on('GET', `/v1/me/rooms/${room().id}/members`, {
      body: { data: [{ user_id: ana.user_id, display_name: ana.username }] },
    })
}

async function openPeople() {
  const person = userEvent.setup()
  await person.click(await screen.findByRole('button', { name: 'People' }))
  return person
}

describe('inviting somebody into a room here', () => {
  it('sends the handle and shows the link to send', async () => {
    const server = roomHere().on('POST', `/v1/me/rooms/${room().id}/invitations`, {
      status: 201,
      body: {
        id: invitationId,
        link: `https://convia.example/invitations/${invitationId}`,
        invitee: 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH',
        expires_at: '2026-09-15T14:04:56.000Z',
      },
    })
    open(server)
    const person = await openPeople()

    await person.type(screen.getByLabelText('Handle'), ' bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH ')
    await person.click(screen.getByRole('button', { name: 'Invite' }))

    expect(await screen.findByLabelText('Invitation link')).toHaveValue(
      `https://convia.example/invitations/${invitationId}`,
    )
    expect(server.asked('POST', `/v1/me/rooms/${room().id}/invitations`)?.body).toEqual({
      handle: 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH',
    })
    expect(screen.queryByText(/only works for somebody using Convia on this same machine/)).toBeNull()
  })

  /*
  A link names the address the page was opened at. Opened as localhost, nobody
  anywhere else can follow it, and saying so now is the difference between a
  person fixing it and a person waiting for somebody who can never arrive.
  */
  it('warns when the link names this computer', async () => {
    open(
      roomHere().on('POST', `/v1/me/rooms/${room().id}/invitations`, {
        status: 201,
        body: {
          id: invitationId,
          link: `http://localhost:5173/invitations/${invitationId}`,
          invitee: 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH',
          expires_at: '2026-09-15T14:04:56.000Z',
        },
      }),
    )
    const person = await openPeople()

    await person.type(screen.getByLabelText('Handle'), 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EH')
    await person.click(screen.getByRole('button', { name: 'Invite' }))

    expect(await screen.findByText(/only works for somebody using Convia on this same machine/)).toBeInTheDocument()
  })

  it('says a mistyped handle is wrong, in its own words', async () => {
    open(
      roomHere().on('POST', `/v1/me/rooms/${room().id}/invitations`, {
        status: 400,
        failure: { code: 'invalid_request', message: 'The identifier in the handle has a typo in it.' },
      }),
    )
    const person = await openPeople()

    await person.type(screen.getByLabelText('Handle'), 'bia#7QK4XMZP2VJH6TBWNDR3YAFC5EX')
    await person.click(screen.getByRole('button', { name: 'Invite' }))

    const complaint = await screen.findByRole('alert')
    expect(complaint).toHaveTextContent('That handle is not right')
    expect(screen.getByLabelText('Handle')).toHaveValue('bia#7QK4XMZP2VJH6TBWNDR3YAFC5EX')
  })
})

describe('a room on another Convia', () => {
  const link = `https://elsewhere.example/invitations/${invitationId}`
  const remoteRoom = {
    id: remoteId,
    home: 'https://elsewhere.example',
    room_id: room().id,
    user_id: anaThere,
    name: 'Their room',
  }

  function nothingHere() {
    return new FakeConvia()
      .on('GET', '/v1/me/rooms', { body: { data: [] } })
      .on('GET', '/v1/me/remote-rooms', { body: { data: [] } })
  }

  function readingElsewhere(server: FakeConvia) {
    return server
      .on('GET', `/v1/me/remote-rooms/${remoteId}/messages`, {
        body: { data: [message({ user_id: anaThere, body: 'Said from my own Convia.' })] },
      })
      .on('PUT', `/v1/me/remote-rooms/${remoteId}/read_state`, {
        body: { room_id: room().id, user_id: anaThere, sequence: 1, unread: 0 },
      })
      .on('GET', `/v1/me/remote-rooms/${remoteId}/members`, {
        body: { data: [{ user_id: anaThere, display_name: 'ana#7QK4' }] },
      })
  }

  /*
  Looking comes before joining. A link is an address anybody can make, and the
  only way to know what it leads to — and who sent it — is to ask.
  */
  it('shows what a link is for, joins it, and reads the room through this Convia', async () => {
    const server = readingElsewhere(
      nothingHere()
        .on('POST', '/v1/me/invitation-previews', {
          body: {
            home: 'https://elsewhere.example',
            room_name: 'Their room',
            inviter: 'bruno#7KQZP4XN2VJH6TBWMDR3YAFC5EC',
            invitee: ana.handle,
            expires_at: '2026-09-15T14:04:56.000Z',
          },
        })
        .on('POST', '/v1/me/remote-rooms', {
          status: 201,
          body: { room_id: room().id, room_name: 'Their room', remote_room: remoteRoom },
        }),
    )
    open(server)
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Join with a link' }))
    await person.type(screen.getByLabelText('Invitation link'), link)
    await person.click(screen.getByRole('button', { name: 'Look' }))

    expect(await screen.findByText(/Invited by bruno#7KQZP4XN2VJH6TBWMDR3YAFC5EC, on elsewhere.example/)).toBeInTheDocument()
    expect(server.asked('POST', '/v1/me/remote-rooms')).toBeUndefined()

    await person.click(screen.getByRole('button', { name: 'Join' }))

    expect(await screen.findByRole('heading', { name: 'Their room' })).toBeInTheDocument()
    expect(await screen.findByText('Said from my own Convia.')).toBeInTheDocument()
    expect(server.asked('POST', '/v1/me/remote-rooms')?.body).toEqual({ link })

    const elsewhere = screen.getByRole('region', { name: 'Rooms on other Convias' })
    expect(within(elsewhere).getByText('elsewhere.example')).toBeInTheDocument()

    /*
    The message is ana's own, by the user she is at the room's home. Recognizing
    it by her user here would name her as a stranger in her own conversation.
    */
    expect(screen.getByText('You')).toBeInTheDocument()
  })

  it('says a link that cannot be used cannot be used', async () => {
    open(
      nothingHere().on('POST', '/v1/me/invitation-previews', {
        status: 404,
        failure: { code: 'not_found', message: 'That invitation cannot be used.' },
      }),
    )
    const person = userEvent.setup()

    await person.click(await screen.findByRole('button', { name: 'Join with a link' }))
    await person.type(screen.getByLabelText('Invitation link'), link)
    await person.click(screen.getByRole('button', { name: 'Look' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('may have expired, been used, or been meant for somebody else')
  })

  /*
  A room elsewhere that stops letting somebody in is a room they cannot read,
  not a session that ended. Being sent back to the sign-in form would sign them
  out of the Convia that did nothing wrong.
  */
  it('does not sign somebody out when the other Convia turns them away', async () => {
    const signedOut = vi.fn()
    new FakeConvia()
      .on('GET', '/v1/me/rooms', { body: { data: [] } })
      .on('GET', '/v1/me/remote-rooms', { body: { data: [remoteRoom] } })
      .on('GET', `/v1/me/remote-rooms/${remoteId}/messages`, {
        status: 403,
        failure: { code: 'forbidden', message: 'The other Convia no longer lets you into that room.' },
      })
      .on('GET', `/v1/me/remote-rooms/${remoteId}/members`, { status: 403, failure: { code: 'forbidden', message: 'no' } })
      .install()

    render(<Workspace account={ana} onSignedOut={signedOut} />)

    expect(await screen.findByText('This conversation could not be read. Convia will try again.')).toBeInTheDocument()
    expect(signedOut).not.toHaveBeenCalled()
  })

  it('offers no way to add or invite people in a room that lives elsewhere', async () => {
    const server = readingElsewhere(
      new FakeConvia()
        .on('GET', '/v1/me/rooms', { body: { data: [] } })
        .on('GET', '/v1/me/remote-rooms', { body: { data: [remoteRoom] } }),
    )
    open(server)
    const person = userEvent.setup()
    await person.click(await screen.findByRole('button', { name: 'People' }))

    expect(screen.getByText(/This room lives on elsewhere.example/)).toBeInTheDocument()
    expect(screen.queryByLabelText('Handle')).toBeNull()
    expect(screen.queryByText('Add somebody')).toBeNull()
    expect(server.asked('GET', '/v1/me/people')).toBeUndefined()
  })
})
