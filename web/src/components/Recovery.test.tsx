import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import type { JoinSession } from '../api/types'
import { LanguageContext } from '../i18n/language'
import { Workspace } from '../screens/Workspace'
import { marked, markedLanguage, unmarked } from '../test/language'
import { Room } from '../test/livekit'
import { FakeConvia, ana, room } from '../test/server'
import { page, Recoverable, Shelf } from './Recovery'

vi.mock('livekit-client', () => import('../test/livekit'))

/*
breaking says which part throws the next time it is drawn. The parts are the real
ones otherwise, so what is checked is the boundary around them and not a stand-in.
*/
const breaking = { composer: false, stage: false, sound: false }

vi.mock('./Composer', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./Composer')>()
  return {
    Composer: (props: Parameters<typeof actual.Composer>[0]) => {
      if (breaking.composer) {
        throw new Error('the composer broke')
      }
      return actual.Composer(props)
    },
  }
})

vi.mock('./Call', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./Call')>()
  return {
    ...actual,
    CallStage: (props: Parameters<typeof actual.CallStage>[0]) => {
      if (breaking.stage) {
        throw new Error('the stage broke')
      }
      return actual.CallStage(props)
    },
    CallAudio: () => {
      if (breaking.sound) {
        throw new Error('the sound broke')
      }
      return actual.CallAudio()
    },
  }
})

let logged: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  Room.reset()
  window.localStorage.clear()
  breaking.composer = false
  breaking.stage = false
  breaking.sound = false
  // React and the boundaries both write what broke to the console, which is expected here.
  logged = vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const standupPath = `/v1/me/rooms/${room().id}`
const design = room({ id: 'room_DESIGNREVIEW7QK4XMZP2VJH6T', name: 'Design review' })

const session: JoinSession = {
  participant_id: 'part_ANAINTHECALL7QK4XMZP2VJH6',
  call_id: 'call_STANDUPCALL7QK4XMZP2VJH6TB',
  media_url: 'wss://media.convia.test',
  media_token: 'a-credential-for-one-call',
  expires_at: '2026-09-14T18:35:00.000Z',
}

function convia() {
  const server = new FakeConvia()
    .on('GET', '/v1/me', { body: ana })
    .on('GET', '/v1/me/rooms', { body: { data: [room(), design] } })
    .on('GET', '/v1/me/calls', { body: { data: [] } })
    .on('POST', `${standupPath}/call/join`, { status: 201, body: session })
    .on('POST', `${standupPath}/call/leave`, { status: 204 })
    .on('GET', `${standupPath}/call/participants`, { body: { data: [] } })
  for (const path of [standupPath, `/v1/me/rooms/${design.id}`]) {
    server.on('GET', `${path}/messages`, { body: { data: [] } }).on('GET', `${path}/members`, { body: { data: [] } })
  }
  return server
}

function open() {
  convia().install()
  return render(
    <Shelf>
      <Workspace account={ana} onSignedOut={() => {}} />
    </Shelf>,
  )
}

type Person = ReturnType<typeof userEvent.setup>

async function inACall(person: Person): Promise<Room> {
  await person.click(await screen.findByRole('button', { name: 'Start call' }))
  const ready = await screen.findByRole('region', { name: 'Prepare to join' })
  await person.click(within(ready).getByRole('button', { name: 'Start call' }))
  await screen.findByRole('button', { name: 'Leave call' })
  return Room.latest() as Room
}

describe('a zone that breaks', () => {
  it('takes only its own place, and is drawn afresh on Try again', async () => {
    breaking.composer = true
    open()
    const person = userEvent.setup()

    const main = await screen.findByRole('main')
    const broken = await within(main).findByRole('alert')
    expect(broken).toHaveTextContent('This part of Convia stopped working.')
    expect(screen.getByRole('button', { name: 'Design review' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Convia' })).toBeInTheDocument()

    breaking.composer = false
    await person.click(within(broken).getByRole('button', { name: 'Try again' }))

    expect(await screen.findByLabelText('Message Standup')).toBeInTheDocument()
    expect(screen.queryByText('This part of Convia stopped working.')).not.toBeInTheDocument()
  })

  it('is left behind when another room is opened', async () => {
    breaking.composer = true
    open()
    const person = userEvent.setup()
    await within(await screen.findByRole('main')).findByRole('alert')

    breaking.composer = false
    await person.click(screen.getByRole('button', { name: 'Design review' }))

    expect(await screen.findByLabelText('Message Design review')).toBeInTheDocument()
  })

  it('offers details to report, and writes them to the console', async () => {
    breaking.composer = true
    open()
    const person = userEvent.setup()
    const broken = await within(await screen.findByRole('main')).findByRole('alert')

    await person.click(within(broken).getByText('Details to report'))
    const details = broken.querySelector('pre')
    expect(details).toHaveAttribute('translate', 'no')
    expect(details).toHaveTextContent('Zone: conversation')
    expect(details).toHaveTextContent('Error: the composer broke')

    await person.click(within(broken).getByRole('button', { name: 'Copy details' }))
    expect(await navigator.clipboard.readText()).toContain('the composer broke')
    expect(logged).toHaveBeenCalledWith('convia: the conversation stopped working', expect.any(Error))
  })

  it('reloads the page when asked', async () => {
    const reload = vi.spyOn(page, 'reload').mockImplementation(() => {})
    breaking.composer = true
    open()
    const person = userEvent.setup()
    const broken = await within(await screen.findByRole('main')).findByRole('alert')

    await person.click(within(broken).getByRole('button', { name: 'Reload the page' }))

    expect(reload).toHaveBeenCalled()
  })

  it('speaks through the catalogue', async () => {
    breaking.composer = true
    convia().install()
    render(
      <LanguageContext.Provider value={markedLanguage}>
        <Workspace account={ana} onSignedOut={() => {}} />
      </LanguageContext.Provider>,
    )
    const person = userEvent.setup()
    const broken = await within(await screen.findByRole('main')).findByRole('alert')
    await person.click(within(broken).getByText(marked.recovery.details))

    expect(unmarked(document.body, ['Convia', 'Standup', 'Design review', ana.handle, 'A'])).toEqual([])
  })
})

describe('a call when part of the page breaks', () => {
  it('goes on when its stage breaks, and the conversation stays', async () => {
    open()
    const person = userEvent.setup()
    const media = await inACall(person)

    breaking.stage = true
    act(() => media.arrive('part_BRUNOINTHECALL7QK4XMZP2VJ'))

    const main = screen.getByRole('main')
    expect(await within(main).findByRole('alert')).toHaveTextContent('This part of Convia stopped working.')
    expect(screen.getByLabelText('Message Standup')).toBeInTheDocument()
    expect(media.disconnect).not.toHaveBeenCalled()
    expect(document.querySelectorAll('audio')).toHaveLength(1)
  })

  it('says when its sound breaks, and keeps the call', async () => {
    open()
    const person = userEvent.setup()
    const media = await inACall(person)

    breaking.sound = true
    act(() => media.arrive('part_BRUNOINTHECALL7QK4XMZP2VJ'))

    const toast = await screen.findByText("The call's sound stopped working.")
    expect(screen.getByRole('button', { name: 'Leave call' })).toBeInTheDocument()
    expect(media.disconnect).not.toHaveBeenCalled()

    breaking.sound = false
    await person.click(within(toast.parentElement as HTMLElement).getByRole('button', { name: 'Try again' }))
    await waitFor(() => expect(document.querySelectorAll('audio')).toHaveLength(1))
  })
})

function Thrower(): React.ReactNode {
  throw new Error('everything broke')
}

describe('the page', () => {
  it('says it stopped working when nothing smaller caught the failure', () => {
    render(
      <Recoverable zone="page" variant="page">
        <Thrower />
      </Recoverable>,
    )

    expect(screen.getByRole('alert')).toHaveTextContent('Convia stopped working.')
    expect(screen.getByRole('button', { name: 'Reload the page' })).toBeInTheDocument()
  })
})

describe('a failure outside any component', () => {
  function signedOut() {
    new FakeConvia()
      .on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
      .install()
    render(<App />)
  }

  function rejection(reason: unknown): Event {
    return Object.assign(new Event('unhandledrejection'), { reason })
  }

  it('is said once, at the bottom right, until dismissed', async () => {
    signedOut()
    const person = userEvent.setup()
    await screen.findByRole('button', { name: 'Sign in' })

    act(() => {
      window.dispatchEvent(rejection(new Error('nobody waited for this')))
      window.dispatchEvent(rejection(new Error('nor for this')))
    })

    const said = await screen.findAllByText('Something went wrong. If the page stops responding, reload it.')
    expect(said).toHaveLength(1)

    await person.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByText(/Something went wrong/)).not.toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new ErrorEvent('error', { error: new Error('thrown outside React') }))
    })
    expect(await screen.findByText(/Something went wrong/)).toBeInTheDocument()
  })

  it('is not a request the page gave up on, nor a file that did not load', async () => {
    signedOut()
    await screen.findByRole('button', { name: 'Sign in' })

    act(() => {
      window.dispatchEvent(rejection(new DOMException('aborted', 'AbortError')))
      window.dispatchEvent(new ErrorEvent('error'))
    })

    expect(screen.queryByText(/Something went wrong/)).not.toBeInTheDocument()
  })
})
