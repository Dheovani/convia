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
  Convia refuses an unknown address and a wrong password identically, so that
  the form is not a way to find out who has an account. The interface must not
  undo that by wording the two differently.
  */
  it('says the same thing whichever half was wrong', async () => {
    convia()
      .on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
      .on('POST', '/v1/sessions', {
        status: 401,
        failure: { code: 'unauthenticated', message: 'The email or password is incorrect.' },
      })
      .install()

    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Email'), 'ana@example.com')
    await person.type(screen.getByLabelText('Password'), 'wrong')
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    const complaint = await screen.findByRole('alert')
    expect(complaint).toHaveTextContent('That email and password do not match an account')
    for (const leak of ['no account', 'not found', 'unknown', 'suspended']) {
      expect(complaint.textContent?.toLowerCase()).not.toContain(leak)
    }
  })

  it('does not blame the password when Convia is unreachable', async () => {
    convia().on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } }).install()
    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Email'), 'ana@example.com')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')

    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')))
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('could not be reached')
  })

  it('opens the workspace on success', async () => {
    convia()
      .on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
      .on('POST', '/v1/sessions', { status: 201, body: ana })
      .on('GET', '/v1/me/rooms', { body: { data: [room({ unread: 3 })] } })
      .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: [message()] } })
      .on('PUT', `/v1/me/rooms/${room().id}/read_state`, {
        body: { room_id: room().id, user_id: ana.user_id, sequence: 1, unread: 0 },
      })
      .install()

    render(<App />)
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Email'), 'ana@example.com')
    await person.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await person.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('heading', { name: 'Standup' })).toBeInTheDocument()
    expect(await screen.findByText('Standup in five minutes.')).toBeInTheDocument()
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
