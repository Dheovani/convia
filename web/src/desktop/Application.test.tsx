import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import { ApiError, NetworkError } from '../api/errors'
import { desktop, NotConviaError } from './bridge'
import { FakeApplication, refused } from '../test/application'
import { FakeConvia, ana, message, room } from '../test/server'
import { FakeSocket } from '../test/socket'

afterEach(() => vi.unstubAllGlobals())

/*
These drive the interface as Convia's application shows it.

The difference from every other test here is the first screen and the first
question. A page in a browser knows which Convia it talks to, because it came
from one. An application knows nothing until somebody says so, and what they
say is trusted with a password on the very next screen.
*/

// carried is what the application carries to the installation: the ordinary
// routes still go out as requests, so the fake Convia answers them.
function carried(): FakeConvia {
  return new FakeConvia()
    .on('GET', '/v1/me', { body: ana })
    .on('GET', '/v1/me/rooms', { body: { data: [room({ unread: 3 })] } })
    .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: [message()] } })
    .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
}

describe('the first screen of an installed application', () => {
  /*
  A browser has an address bar and a history; an application has neither. The
  question has to be asked, and it has to be asked before a password is.
  */
  it('asks which installation to connect to when this machine knows none', async () => {
    new FakeApplication().install()

    render(<App />)

    await screen.findByRole('heading', { name: 'Where is your Convia?' })
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  /*
  Asking every morning for an answer given yesterday is the chore this exists
  to avoid. The one used last is reconnected to without a word.
  */
  it('reconnects to the one used last, and signs somebody back in with the session kept for it', async () => {
    carried().install()
    const application = new FakeApplication()
      .knows('https://convia.example', 'http://localhost:8080')
      .holds(ana)
    application.install()

    render(<App />)

    await screen.findByRole('heading', { name: 'Standup' })
    expect(application.asked('Connect')).toEqual([['https://convia.example']])
    expect(screen.queryByRole('heading', { name: 'Where is your Convia?' })).toBeNull()
  })

  /*
  The ones used before are offered, so that somebody with a Convia at work and
  one at home does not type either of them again.

  Getting here takes a click, because the one used last was reconnected to on
  its own — which is the behaviour above, and the reason this screen is not the
  one most starts show.
  */
  it('offers the installations this machine has used, most recent first', async () => {
    carried().install()
    new FakeApplication().knows('https://work.example', 'https://home.example').install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Sign in' })
    await userEvent.click(screen.getByRole('button', { name: 'Connect to another Convia' }))

    await screen.findByRole('heading', { name: 'Where is your Convia?' })
    const offered = screen.getAllByRole('button', { name: /^https:\/\/\w+\.example$/ })
    expect(offered.map((button) => button.textContent)).toEqual([
      'https://work.example',
      'https://home.example',
    ])
  })

  it('stops offering one somebody is done with', async () => {
    carried().install()
    const application = new FakeApplication().knows('https://work.example', 'https://home.example')
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Sign in' })
    await userEvent.click(screen.getByRole('button', { name: 'Connect to another Convia' }))
    await screen.findByRole('heading', { name: 'Where is your Convia?' })

    await userEvent.click(screen.getByRole('button', { name: 'Forget https://home.example' }))

    await waitFor(() => expect(application.asked('Forget')).toEqual([['https://home.example']]))
    expect(screen.queryByRole('button', { name: 'https://home.example' })).toBeNull()
  })
})

describe('an address that is not going to work', () => {
  /*
  A typo that becomes a password sent to whatever answered is the failure this
  screen exists to prevent, so each way of being wrong gets its own words.
  */
  it('says so when something else answered at that address', async () => {
    const application = new FakeApplication()
    application.refuses('Connect', refused('not_convia', 'it did not answer as a Convia'))
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Where is your Convia?' })

    await userEvent.type(screen.getByLabelText('Address'), 'convia.example')
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }))

    await screen.findByText('Something answered there, and it was not a Convia you can sign in to.')
  })

  it('says something different when nothing answered at all', async () => {
    const application = new FakeApplication()
    application.refuses('Connect', refused('unreachable', 'convia could not be reached'))
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Where is your Convia?' })

    await userEvent.type(screen.getByLabelText('Address'), 'convia.example')
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }))

    await screen.findByText(
      'Nothing answered at that address. Check it and your connection, and try again.',
    )
  })

  /*
  A session travelling in a header over plain HTTP is a session anybody on the
  network holds. It is refused before anything is asked of the address, so the
  request is never made at all.
  */
  it('refuses plain HTTP to anywhere but this machine, without asking', async () => {
    const application = new FakeApplication()
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Where is your Convia?' })

    await userEvent.type(screen.getByLabelText('Address'), 'http://convia.example')
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }))

    await screen.findByText('An installation anywhere but this machine has to be reached over HTTPS.')
    expect(application.asked('Connect')).toEqual([])
  })
})

describe('signing in through the application', () => {
  /*
  The six routes that hand a session over or take one away are the
  application's, because their answer contains the session and the answer to a
  carried request is read by this webview.
  */
  it('asks the application rather than sending the password itself', async () => {
    const server = carried()
    server.install()
    const application = new FakeApplication().knows('https://convia.example')
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Sign in' })

    await userEvent.type(screen.getByLabelText('Username'), 'ana')
    await userEvent.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    await screen.findByRole('heading', { name: 'Standup' })
    expect(application.asked('SignIn')).toEqual([['ana', 'correct horse battery staple']])
    expect(server.calls.some((call) => call.path === '/v1/sessions')).toBe(false)
  })

  /*
  A refusal has to keep its code across the boundary. A wrong password, a name
  already taken and too many attempts are three different screens, told apart
  by that and nothing else.
  */
  it('keeps the code a refusal carried, so the words are still the right ones', async () => {
    carried().install()
    const application = new FakeApplication().knows('https://convia.example')
    application.refuses(
      'SignIn',
      refused('refused', 'The username and password do not match an account.', {
        status: 401,
        code: 'unauthenticated',
      }),
    )
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Sign in' })

    await userEvent.type(screen.getByLabelText('Username'), 'ana')
    await userEvent.type(screen.getByLabelText('Password'), 'a wrong password')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    await screen.findByText('That username and password do not match an account.')
  })

  // Somebody who signed out of the wrong installation would otherwise be
  // stuck: the sign-in form is the only screen, and it asks nothing about
  // where it is.
  it('offers a way back to the installation screen', async () => {
    carried().install()
    new FakeApplication().knows('https://convia.example').install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Sign in' })

    await userEvent.click(screen.getByRole('button', { name: 'Connect to another Convia' }))

    await screen.findByRole('heading', { name: 'Where is your Convia?' })
  })
})


describe('what the application refuses with', () => {
  /*
  A refusal has to arrive here as a refusal, with the code intact.

  It crosses as the text of a rejected promise, because Wails builds that
  promise's Error from what the Go side returns and an object arrives as the
  words "[object Object]". The code is the part anything branches on — a wrong
  password, a name already taken and too many attempts are three different
  screens — so losing it would be losing all three.
  */
  it('keeps the code, the status and the request identifier of a refusal', async () => {
    const application = new FakeApplication()
    application.refuses(
      'SignIn',
      new Error(
        JSON.stringify({
          kind: 'refused',
          status: 403,
          code: 'wrong_password',
          message: 'The password is not right.',
          request_id: 'req_1',
        }),
      ),
    )
    application.install()

    const refusal = await desktop.signIn('ana', 'a wrong password').catch((error: unknown) => error)

    expect(refusal).toBeInstanceOf(ApiError)
    expect(refusal).toMatchObject({ code: 'wrong_password', status: 403, requestId: 'req_1' })
  })

  /*
  The three kinds stay three kinds, because they need different words: a
  refusal has an explanation worth showing, an unreachable installation has
  none, and an address that is not a Convia is a typo.
  */
  it('tells a refusal, an unreachable installation and a wrong address apart', async () => {
    const application = new FakeApplication()
    application.refuses('SignIn', refused('unreachable', 'convia could not be reached'))
    application.refuses('Connect', refused('not_convia', 'it did not answer as a Convia'))
    application.install()

    await expect(desktop.signIn('ana', 'x')).rejects.toBeInstanceOf(NetworkError)
    await expect(desktop.connect('convia.example')).rejects.toBeInstanceOf(NotConviaError)
  })

  /*
  Anything it cannot read is left alone. An error nobody wrote in this shape
  came from somewhere else, and dressing it up as a refusal would put a code on
  a screen that no installation ever said.
  */
  it('does not dress up an error it cannot read', async () => {
    const application = new FakeApplication()
    const strange = new Error('the webview went away')
    application.refuses('SignOut', strange)
    application.install()

    await expect(desktop.signOut()).rejects.toBe(strange)
  })
})

describe('the person’s events inside the application', () => {
  /*
  A page cannot set a header on a WebSocket handshake, so the application holds
  the stream and the events arrive already read. Nothing downstream changes:
  the same events, and the same `live` deciding whether a screen waits or asks.
  */
  it('takes them from the application and opens no socket of its own', async () => {
    carried().install()
    const application = new FakeApplication().knows('https://convia.example').holds(ana)
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Standup' })

    expect(FakeSocket.opened).toHaveLength(0)

    act(() => {
      application.stream({ Live: true })
      application.event({
        id: 'evt_1',
        type: 'room.updated',
        subject: { type: 'room', id: room().id },
        data: { room_id: room().id },
        occurred_at: new Date().toISOString(),
      })
    })

    // What the screen does with it is the same as in a browser, and is covered
    // there. What matters here is that nothing went looking for a socket.
    await waitFor(() => expect(FakeSocket.opened).toHaveLength(0))
  })
})
