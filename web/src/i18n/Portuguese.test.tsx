import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from '../App'
import { FakeConvia, ana, message, room } from '../test/server'
import { choose, LanguageContext } from './language'

afterEach(() => vi.unstubAllGlobals())

function inPortuguese() {
  return render(
    <LanguageContext.Provider value={choose(['pt-BR'])}>
      <App />
    </LanguageContext.Provider>,
  )
}

const roomPath = `/v1/me/rooms/${room().id}`

describe('the interface in Portuguese', () => {
  it('asks somebody to sign in in Portuguese', async () => {
    new FakeConvia()
      .on('GET', '/v1/me', { status: 401, failure: { code: 'unauthenticated', message: 'no' } })
      .on('POST', '/v1/sessions', {
        status: 401,
        failure: { code: 'unauthenticated', message: 'The username or password is incorrect.' },
      })
      .install()
    inPortuguese()
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Nome de usuário'), 'ana')
    await person.type(screen.getByLabelText('Senha'), 'errada')
    await person.click(screen.getByRole('button', { name: 'Entrar' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Esse nome de usuário e essa senha não correspondem a uma conta.',
    )
  })

  it('counts unread messages by Portuguese rules', async () => {
    new FakeConvia()
      .on('GET', '/v1/me', { body: ana })
      .on('GET', '/v1/me/rooms', { body: { data: [room({ unread: 1 }), room({ id: 'room_OTHER7KQZP4XN2VJH6TBWMDR3', name: 'Outra', unread: 4 })] } })
      .on('GET', `${roomPath}/messages`, { body: { data: [message()] } })
      .install()
    inPortuguese()

    expect(await screen.findByText('1 mensagem não lida')).toBeInTheDocument()
    expect(screen.getByText('4 mensagens não lidas')).toBeInTheDocument()
  })

  /*
  Convia's error bodies are English, and only their code is promised. What is
  shown is worded from the code, so a refusal never reaches the screen in the
  server's language.
  */
  it("says a refusal in its own words, never in Convia's", async () => {
    new FakeConvia()
      .on('GET', '/v1/me', { body: ana })
      .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
      .on('GET', `${roomPath}/messages`, { body: { data: [] } })
      .on('POST', `${roomPath}/messages`, {
        status: 413,
        failure: { code: 'payload_too_large', message: 'The request body is too large.' },
      })
      .install()
    inPortuguese()
    const person = userEvent.setup()

    await person.type(await screen.findByLabelText('Mensagem para Standup'), 'Olá')
    await person.click(screen.getByRole('button', { name: 'Enviar' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Isso é longo demais.')
    expect(screen.queryByText(/too large/)).not.toBeInTheDocument()
  })

  /*
  Both languages are checked, because the machine running this has a language of
  its own: whichever it is, one of the two differs from it.
  */
  it('writes the time the way the language does, not the way the machine does', async () => {
    new FakeConvia()
      .on('GET', '/v1/me', { body: ana })
      .on('GET', '/v1/me/rooms', { body: { data: [room()] } })
      .on('GET', `${roomPath}/messages`, { body: { data: [message()] } })
      .install()
    const at = new Date(message().created_at)

    const portuguese = inPortuguese()
    expect(await screen.findByText(/^\d{2}:\d{2}$/)).toHaveTextContent(
      at.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' }),
    )
    portuguese.unmount()

    render(<App />)
    expect(await screen.findByText(/^\d{2}:\d{2} [AP]M$/)).toHaveTextContent(
      at.toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' }),
    )
  })
})
