import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import { ApiError, NetworkError } from '../api/errors'
import { desktop, NotConviaError } from './bridge'
import { en } from '../i18n/en'
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
  An address is something whoever set Convia up knows, and an ordinary person
  does not. Somebody who installed the whole thing on their own computer must
  not have to learn what an address is to open it — so before anybody is asked
  anything, the application looks for a Convia here.
  */
  it('opens straight into the Convia on this computer, asking nothing', async () => {
    carried().install()
    const application = new FakeApplication().runsHere().holds(ana)
    application.install()

    render(<App />)

    await screen.findByRole('heading', { name: 'Standup' })
    expect(application.asked('ConnectHere')).toHaveLength(1)
    expect(screen.queryByRole('heading', { name: 'Connect to Convia' })).toBeNull()
  })

  /*
  Only when there is nothing here and nothing remembered is anybody asked —
  and the Convia on this computer is still the first thing offered, because it
  may have been started in the meantime.
  */
  it('asks only when it found nothing, and offers this computer first', async () => {
    new FakeApplication().install()

    render(<App />)

    await screen.findByRole('heading', { name: 'Connect to Convia' })
    expect(screen.getByRole('button', { name: 'Use the Convia on this computer' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  it('connects when that button finds one, and says so when it does not', async () => {
    carried().install()
    const application = new FakeApplication()
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Connect to Convia' })

    await userEvent.click(screen.getByRole('button', { name: 'Use the Convia on this computer' }))
    await screen.findByText(/There is no Convia running on this computer/)

    application.runsHere()
    await userEvent.click(screen.getByRole('button', { name: 'Use the Convia on this computer' }))
    await screen.findByRole('heading', { name: 'Sign in' })
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
    expect(screen.queryByRole('heading', { name: 'Connect to Convia' })).toBeNull()
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

    await screen.findByRole('heading', { name: 'Connect to Convia' })
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
    await screen.findByRole('heading', { name: 'Connect to Convia' })

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
    await screen.findByRole('heading', { name: 'Connect to Convia' })

    await userEvent.type(screen.getByLabelText('Address'), 'convia.example')
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }))

    await screen.findByText('Something answered there, and it was not a Convia you can sign in to.')
  })

  it('says something different when nothing answered at all', async () => {
    const application = new FakeApplication()
    application.refuses('Connect', refused('unreachable', 'convia could not be reached'))
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Connect to Convia' })

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
    await screen.findByRole('heading', { name: 'Connect to Convia' })

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

    await screen.findByRole('heading', { name: 'Connect to Convia' })
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



describe('an invitation somebody clicked', () => {
  const link = 'https://elsewhere.example/invitations/inv_7QK4XMZP2VJH6TBWNDR3YAFC5'

  // preview is what the home of the link says the invitation is for, which is
  // shown before anybody joins anything.
  function previewing(): FakeConvia {
    return carried().on('POST', '/v1/me/invitation-previews', {
      body: {
        home: 'https://elsewhere.example',
        room_name: 'Their room',
        inviter: 'bruno#7KQZP4XN2VJH6TBWMDR3YAFC5EC',
        invitee: ana.handle,
        expires_at: '2026-12-15T14:04:56.000Z',
      },
    })
  }

  /*
  Clicking an invitation on a machine where Convia is closed starts the
  application with it: the link arrives before the window exists, before an
  installation is chosen, and before anybody has signed in. It waits in the
  application, and the interface comes for it once there is somewhere to put
  it.
  */
  it('shows what the invitation is for, without being asked to look', async () => {
    const server = previewing()
    server.install()
    const application = new FakeApplication().runsHere().holds(ana)
    application.clicked(link)
    application.install()

    render(<App />)

    /*
    Clicking an invitation is the asking. What somebody sees is what it leads
    to, not a form with their own link in it and a button that says do the
    thing you just did.
    */
    expect(await screen.findByText('Their room')).toBeInTheDocument()
    expect(application.asked('PendingLink')).not.toHaveLength(0)
    expect(server.asked('POST', '/v1/me/invitation-previews')?.body).toEqual({ link })

    /*
    Joining still waits for a second, deliberate press.

    Clicking a link is somebody saying show me; it is not somebody saying put
    me in a room. Anybody can make a link and send it to anybody, so the room
    and who invited them are shown first and joined after — the same as for a
    link that was pasted.
    */
    expect(screen.getByRole('button', { name: 'Join' })).toBeInTheDocument()
    expect(server.asked('POST', '/v1/me/remote-rooms')).toBeUndefined()
  })

  /*
  The same invitation clicked twice is two arrivals. The first opens the panel,
  somebody closes it without joining, and the second has to open it again —
  which it would not if the link alone decided, because the two are one string.
  */
  it('opens again when the same invitation is clicked a second time', async () => {
    previewing().install()
    const application = new FakeApplication().runsHere().holds(ana)
    application.install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Standup' })

    await act(async () => {
      application.clicked(link)
      await Promise.resolve()
    })
    await screen.findByText('Their room')

    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByText('Their room')).toBeNull()

    await act(async () => {
      application.clicked(link)
      await Promise.resolve()
    })
    await screen.findByText('Their room')
  })

  // A page in a browser is opened by a link rather than handed one, so nothing
  // here is ever asked of it.
  it('asks nothing of a browser', async () => {
    previewing().install()

    render(<App />)
    await screen.findByRole('heading', { name: 'Standup' })

    expect(screen.queryByLabelText('Invitation link')).toBeNull()
    expect(screen.queryByText('Their room')).toBeNull()
  })
})

describe('a device the application is not allowed to use', () => {
  /*
  The same refusal, and a different remedy.

  A page in a browser is told to look beside the address bar, because that is
  where a browser keeps it. An installed application has no address bar, and
  whether it may use a camera at all is Windows' to decide — so it names
  Windows' own setting instead of a place that is not there.
  */
  it('names Windows rather than a site setting that does not exist', () => {
    const said = en.call

    expect(said.refused('denied', 'audioinput', true)).toContain('Windows')
    expect(said.refused('denied', 'audioinput', true)).toContain('Microphone')
    expect(said.refused('denied', 'audioinput', true)).not.toContain('address bar')

    expect(said.refused('denied', 'videoinput', false)).toContain('address bar')
  })

  // The other three are the same wherever the interface is running: a device
  // nobody has, one another program is holding, and one that would not start.
  it('says the same thing about the failures Windows has nothing to do with', () => {
    for (const refusal of ['missing', 'busy', 'failed'] as const) {
      expect(en.call.refused(refusal, 'videoinput', true)).toBe(en.call.refused(refusal, 'videoinput', false))
    }
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
