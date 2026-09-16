import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import { Speaking } from '../i18n/language'
import { Workspace } from '../screens/Workspace'
import { Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'

vi.mock('livekit-client', () => import('../test/livekit'))

beforeEach(() => {
  Room.reset()
  window.localStorage.clear()
  delete document.documentElement.dataset['theme']
})
afterEach(() => vi.unstubAllGlobals())

const password = 'correct horse battery staple'
const replacement = 'a replacement password'

function convia() {
  return new FakeConvia()
    .on('GET', '/v1/me', { body: ana })
    .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
    .on('GET', `/v1/me/rooms/${room().id}/messages`, { body: { data: [] } })
}

function open(server: FakeConvia, onSignedOut = () => {}) {
  server.install()
  return render(<Workspace account={ana} onSignedOut={onSignedOut} />)
}

type Person = ReturnType<typeof userEvent.setup>

async function settings(person: Person, section?: string) {
  const rail = screen.getByRole('navigation', { name: 'Convia' })
  await person.click(within(rail).getByRole('button', { name: /^(Settings|Configurações)$/ }))
  if (section !== undefined) {
    const sections = screen.getByRole('complementary', { name: 'Settings' })
    await person.click(within(sections).getByRole('button', { name: section }))
  }
  return screen.getByRole('main')
}

// changePassword fills the form afresh and sends it.
async function changePassword(person: Person, current: string, next: string, confirmation = next) {
  for (const [label, value] of [
    ['Current password', current],
    ['New password', next],
    ['Confirm new password', confirmation],
  ] as const) {
    const field = screen.getByLabelText(label)
    await person.clear(field)
    await person.type(field, value)
  }
  await person.click(screen.getByRole('button', { name: 'Change password' }))
}

describe('the account', () => {
  it('shows the handle other people invite this person with, to copy', async () => {
    open(convia())
    const person = userEvent.setup()

    const page = await settings(person)

    expect(within(page).getByRole('heading', { name: 'Account' })).toBeInTheDocument()
    expect(within(page).getByRole('textbox', { name: 'Your handle' })).toHaveValue(ana.handle)
    await person.click(within(page).getByRole('button', { name: 'Copy handle' }))
    expect(await navigator.clipboard.readText()).toBe(ana.handle)
    expect(within(page).getByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })

  it('changes the password, and says what that did', async () => {
    const onSignedOut = vi.fn()
    const server = convia().on('PATCH', '/v1/me/password', { status: 204 })
    open(server, onSignedOut)
    const person = userEvent.setup()
    await settings(person)

    expect(screen.getByText(/nobody can reset it/)).toBeInTheDocument()
    await changePassword(person, password, replacement)

    expect(await screen.findByText(/Your password was changed/)).toBeInTheDocument()
    expect(server.asked('PATCH', '/v1/me/password')?.body).toEqual({
      current_password: password,
      new_password: replacement,
    })
    expect(screen.getByLabelText('Current password')).toHaveValue('')
    expect(onSignedOut).not.toHaveBeenCalled()
  })

  // A typo in the current password is not a session that ended.
  it('says a wrong current password, and keeps the person signed in', async () => {
    const onSignedOut = vi.fn()
    open(
      convia().on('PATCH', '/v1/me/password', {
        status: 403,
        failure: { code: 'wrong_password', message: 'The current password is not right.' },
      }),
      onSignedOut,
    )
    const person = userEvent.setup()
    await settings(person)

    await changePassword(person, 'not my password', replacement)

    expect(await screen.findByRole('alert')).toHaveTextContent('That is not your current password.')
    expect(onSignedOut).not.toHaveBeenCalled()
  })

  it('says when the address has run out of attempts', async () => {
    open(
      convia().on('PATCH', '/v1/me/password', {
        status: 429,
        failure: { code: 'rate_limited', message: 'Too many failed attempts.' },
      }),
    )
    const person = userEvent.setup()
    await settings(person)

    await changePassword(person, 'another guess', replacement)

    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts from here.')
  })

  it('checks the new password before sending it', async () => {
    const server = convia()
    open(server)
    const person = userEvent.setup()
    await settings(person)

    await changePassword(person, password, 'too short')
    expect(await screen.findByRole('alert')).toHaveTextContent('at least 12 characters')

    await changePassword(person, password, replacement, 'a different password')
    expect(await screen.findByRole('alert')).toHaveTextContent('The two passwords do not match.')

    expect(server.asked('PATCH', '/v1/me/password')).toBeUndefined()
  })

  it('treats a session that ended as signed out', async () => {
    const onSignedOut = vi.fn()
    open(
      convia().on('PATCH', '/v1/me/password', {
        status: 401,
        failure: { code: 'unauthenticated', message: 'The request did not carry a usable session.' },
      }),
      onSignedOut,
    )
    const person = userEvent.setup()
    await settings(person)

    await changePassword(person, password, replacement)

    await waitFor(() => expect(onSignedOut).toHaveBeenCalled())
  })

  it('signs out everywhere, this page included, once asked twice', async () => {
    const onSignedOut = vi.fn()
    const server = convia().on('DELETE', '/v1/sessions', { status: 204 })
    open(server, onSignedOut)
    const person = userEvent.setup()
    const page = await settings(person)

    await person.click(within(page).getByRole('button', { name: 'Sign out everywhere' }))
    expect(server.asked('DELETE', '/v1/sessions')).toBeUndefined()
    expect(within(page).getByText('Sign out on every device, including this one?')).toBeInTheDocument()

    await person.click(within(page).getByRole('button', { name: 'Sign out everywhere' }))

    await waitFor(() => expect(onSignedOut).toHaveBeenCalled())
    expect(server.asked('DELETE', '/v1/sessions')).toBeDefined()
  })
})

describe('what this browser keeps', () => {
  it('puts the chosen theme on the page, and remembers it', async () => {
    open(convia())
    const person = userEvent.setup()
    await settings(person, 'Appearance')

    expect(screen.getByRole('radio', { name: 'Match the system' })).toBeChecked()

    await person.click(screen.getByRole('radio', { name: 'Light' }))
    expect(document.documentElement.dataset['theme']).toBe('light')
    expect(window.localStorage.getItem('convia.theme')).toBe('light')

    await person.click(screen.getByRole('radio', { name: 'Match the system' }))
    expect(document.documentElement.dataset['theme']).toBeUndefined()
  })

  it('speaks the language chosen, at once and on the next visit', async () => {
    const server = convia()
    server.install()
    const first = render(
      <Speaking>
        <App />
      </Speaking>,
    )
    const person = userEvent.setup()
    await screen.findByRole('navigation', { name: 'Convia' })
    await settings(person, 'Language')

    expect(screen.getByRole('radio', { name: "The browser's language (English)" })).toBeChecked()
    const portuguese = screen.getByRole('radio', { name: 'Português (Brasil)' })
    expect(portuguese.nextElementSibling).toHaveAttribute('lang', 'pt-BR')

    await person.click(portuguese)

    expect(await screen.findByRole('heading', { level: 2, name: 'Idioma' })).toBeInTheDocument()
    expect(document.documentElement.lang).toBe('pt-BR')

    first.unmount()
    render(
      <Speaking>
        <App />
      </Speaking>,
    )
    expect(await screen.findByRole('button', { name: 'Configurações' })).toBeInTheDocument()
  })

  it('chooses how calls start and checks the devices away from any call', async () => {
    Room.devices = [{ deviceId: 'mic-headset', kind: 'audioinput', label: 'Headset' }]
    open(convia())
    const person = userEvent.setup()
    await settings(person, 'Calls')

    await person.click(screen.getByRole('checkbox', { name: 'Start calls with the camera on' }))
    expect(JSON.parse(window.localStorage.getItem('convia.call') ?? '{}')).toMatchObject({ camera: true })

    const microphone = screen.getByRole('combobox', { name: 'Microphone' })
    await waitFor(() => expect(within(microphone).getByRole('option', { name: 'Headset' })).toBeInTheDocument())
    await person.selectOptions(microphone, 'mic-headset')
    expect(JSON.parse(window.localStorage.getItem('convia.call') ?? '{}')).toMatchObject({ audioInput: 'mic-headset' })

    expect(Room.opened).toHaveLength(0)
    await person.click(screen.getByRole('button', { name: 'Check my devices' }))
    const check = await screen.findByRole('region', { name: 'Checking your devices' })
    expect(await within(check).findByRole('meter', { name: 'Microphone level' })).toBeInTheDocument()

    await person.click(screen.getByRole('button', { name: 'Stop checking' }))
    expect(screen.queryByRole('region', { name: 'Checking your devices' })).not.toBeInTheDocument()
    for (const track of Room.opened) {
      expect(track.stop).toHaveBeenCalled()
    }
  })

  // The call holds the devices, so they are not opened a second time beside it.
  it('does not check devices during a call', async () => {
    const standup = `/v1/me/rooms/${room().id}`
    open(
      convia()
        .on('GET', '/v1/me/calls', { body: { data: [] } })
        .on('GET', `${standup}/members`, { body: { data: [] } })
        .on('GET', `${standup}/call/participants`, { body: { data: [] } })
        .on('POST', `${standup}/call/join`, {
          status: 201,
          body: {
            participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
            call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
            media_url: 'wss://media.convia.test',
            media_token: 'a-credential-for-one-call',
            expires_at: '2026-09-14T18:35:00.000Z',
          },
        }),
    )
    const person = userEvent.setup()
    await person.click(await screen.findByRole('button', { name: 'Start call' }))
    const ready = await screen.findByRole('region', { name: 'Prepare to join' })
    await person.click(within(ready).getByRole('button', { name: 'Start call' }))
    await screen.findByRole('button', { name: 'Leave call' })

    await settings(person, 'Calls')

    expect(screen.getByText(/You are in a call/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Check my devices' })).not.toBeInTheDocument()
  })

  // Leaving the settings lets go of a camera the check had opened.
  it('lets go of the devices when the settings are left', async () => {
    open(convia())
    const person = userEvent.setup()
    await settings(person, 'Calls')

    await person.click(screen.getByRole('button', { name: 'Check my devices' }))
    await waitFor(() => expect(Room.opened.length).toBeGreaterThan(0))

    await person.click(within(screen.getByRole('navigation', { name: 'Convia' })).getByRole('button', { name: 'Chat' }))

    for (const track of Room.opened) {
      expect(track.stop).toHaveBeenCalled()
    }
  })
})
