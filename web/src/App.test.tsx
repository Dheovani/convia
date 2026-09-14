import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from './App'
import { FakeConvia, ana, message, room } from './test/server'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function convia(): FakeConvia {
  return new FakeConvia()
}

// signedOut is a Convia with nobody signed in yet.
function signedOut(): FakeConvia {
  return convia().on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
}

// workspace answers what the workspace asks for once somebody is in.
function workspace(server: FakeConvia): FakeConvia {
  return server
    .on('GET', '/v1/me/rooms', { body: { data: [room({ unread: 3 })] } })
    .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: [message()] } })
    .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
      body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
    })
}

describe('what the page does before it knows who it is serving', () => {
  /*
  The session lives in a cookie the script cannot read, so the page cannot
  answer this itself. It asks, and what it must not do is guess: a flash of the
  sign-in form on every reload is what guessing looks like.
  */
  it('asks Convia rather than showing a form it may not need', async () => {
    const server = convia().on('GET', '/v1/me', { body: ana })
    server.install()

    render(<App />)

    expect(screen.queryByRole('button', { name: 'Sign in' })).toBeNull()
    await screen.findByRole('heading', { name: 'Standup' }).catch(() => undefined)
    await waitFor(() => expect(server.asked('GET', '/v1/me')).toBeDefined())
  })

  it('shows the sign-in form when nobody is signed in', async () => {
    convia().on('GET', '/v1/me', {
      status: 401,
      failure: { code: 'unauthenticated', message: 'The request did not carry a usable session.' },
    }).install()

    render(<App />)

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })
})

describe('signing in', () => {
  /*
  Convia refuses an unknown username and a wrong password identically. The
  interface must not undo that by wording the two differently, and it must not
  repeat whatever prose the server sent.
  */
  it('says the same thing whichever half was wrong', async () => {
    signedOut()
      .on('POST', '/v1/sessions', {
        status: 401,
        failure: { code: 'unauthenticated', message: 'The username or password is incorrect.' },
      })
      .install()

    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'wrong')
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    const complaint = await screen.findByRole('alert')
    expect(complaint).toHaveTextContent('That username and password do not match an account')
    for (const leak of ['no account', 'not found', 'unknown', 'suspended', 'incorrect']) {
      expect(complaint.textContent?.toLowerCase()).not.toContain(leak)
    }
  })

  it('does not blame the password when Convia is unreachable', async () => {
    signedOut().install()
    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')

    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')))
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('could not be reached')
  })

  it('opens the workspace on success', async () => {
    const server = workspace(signedOut().on('POST', '/v1/sessions', { status: 201, body: ana }))
    server.install()

    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(await screen.findByText('Standup in five minutes.')).toBeInTheDocument()
    expect(server.asked('POST', '/v1/sessions')?.body).toEqual({
      username: 'ana',
      password: 'correct horse battery staple',
    })
  })
})

describe('creating an account', () => {
  async function openRegistration() {
    render(<App />)
    const person = userEvent.setup()
    await person.click(await screen.findByRole('button', { name: 'Create an account' }))
    return person
  }

  it('creates the account, signs its owner in, and opens the workspace', async () => {
    const server = workspace(signedOut().on('POST', '/v1/accounts', { status: 201, body: ana }))
    server.install()

    const person = await openRegistration()
    await person.type(screen.getByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.type(screen.getByLabelText('Confirm password'), 'correct horse battery staple')
    await person.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(server.asked('POST', '/v1/accounts')?.body).toEqual({
      username: 'ana',
      password: 'correct horse battery staple',
    })
    expect(server.asked('POST', '/v1/sessions')).toBeUndefined()
  })

  /*
  The password seals the account's key, so a forgotten one cannot be reset.
  That has to be said before the account exists, not discovered afterwards.
  */
  it('says the password cannot be reset before anything is sent', async () => {
    signedOut().install()
    await openRegistration()

    expect(screen.getByText(/nobody can reset it/)).toBeInTheDocument()
  })

  it('catches a mistyped confirmation without asking Convia', async () => {
    const server = signedOut()
    server.install()

    const person = await openRegistration()
    await person.type(screen.getByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.type(screen.getByLabelText('Confirm password'), 'correct horse battery stapel')
    await person.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The two passwords do not match')
    expect(server.asked('POST', '/v1/accounts')).toBeUndefined()
  })

  it('catches a password below the floor and an unusable username without asking Convia', async () => {
    const server = signedOut()
    server.install()

    const person = await openRegistration()
    await person.type(screen.getByLabelText('Username'), 'ana ribeiro')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.type(screen.getByLabelText('Confirm password'), 'correct horse battery staple')
    await person.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not one Convia accepts')

    await person.clear(screen.getByLabelText('Username'))
    await person.type(screen.getByLabelText('Username'), 'ana')
    await person.clear(screen.getByLabelText('Password'))
    await person.type(screen.getByLabelText('Password'), 'too short')
    await person.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('at least 12 characters')

    expect(server.asked('POST', '/v1/accounts')).toBeUndefined()
  })

  it('says a taken username is taken', async () => {
    signedOut()
      .on('POST', '/v1/accounts', {
        status: 409,
        failure: { code: 'conflict', message: 'That username is already taken.' },
      })
      .install()

    const person = await openRegistration()
    await person.type(screen.getByLabelText('Username'), 'ana')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.type(screen.getByLabelText('Confirm password'), 'correct horse battery staple')
    await person.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That username is taken')
    expect(screen.getByLabelText('Username')).toHaveValue('ana')
  })

  it('goes back to signing in', async () => {
    signedOut().install()

    const person = await openRegistration()
    await person.click(screen.getByRole('button', { name: 'I already have an account' }))

    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Confirm password')).toBeNull()
  })
})

/*
A session that expires while somebody is reading must take the screen with it.
Leaving the last-drawn conversation up is the failure that matters here: on a
shared machine it is the previous person's.
*/
describe('a session that ends', () => {
  it('returns to the sign-in form when Convia stops recognising it', async () => {
    convia()
      .on('GET', '/v1/me', { body: ana })
      .on('GET', '/v1/me/rooms', {
        status: 401,
        failure: { code: 'unauthenticated', message: 'The request did not carry a usable session.' },
      })
      .install()

    render(<App />)

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })
})
