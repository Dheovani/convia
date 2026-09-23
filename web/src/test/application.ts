import { vi } from 'vitest'

import type { Connection, Signed, StreamState } from '../desktop/bridge'

/*
A stand-in for Convia's application, which is what shows this interface when it
is not a page in a browser.

Wails puts the Go process's methods on `window.go` and its events on
`window.runtime`, so that is what this puts there. The shape is the contract
between the two halves: a test that drives this is checking what the interface
asks of the application, which is as much of the arrangement as what it does
with the answers.
*/

/*
Refused is how the application refuses.

It is the text of a rejected promise rather than an object, because Wails
builds that promise's Error from what the Go side returns and an object arrives
as the words "[object Object]". internal/desktop/app renders JSON for that
reason, and this renders the same JSON.
*/
export function refused(
  kind: 'refused' | 'unreachable' | 'not_convia' | 'failed',
  message: string,
  extra: { status?: number; code?: string } = {},
): Error {
  return new Error(JSON.stringify({ kind, message, ...extra }))
}

type Listener = (...data: unknown[]) => void

export class FakeApplication {
  readonly calls: { method: string; args: unknown[] }[] = []

  private remembered: string[] = []
  // running is whether there is a Convia on this computer to be found.
  private running = false
  // pending is an invitation somebody clicked, waiting to be taken.
  private pending = ''
  private connection: Connection | null = null
  private signed: Signed | null = null
  private readonly refusals = new Map<string, Error>()
  private readonly listeners = new Map<string, Set<Listener>>()

  // knows makes the application remember installations, as a machine that has
  // connected before would.
  knows(...addresses: string[]): this {
    this.remembered = addresses
    return this
  }

  // runsHere makes this computer one with a Convia on it, which is what an
  // ordinary person has when they installed the whole thing.
  runsHere(): this {
    this.running = true
    return this
  }

  // clicked is an invitation somebody opened, which the application holds
  // until the interface comes for it.
  clicked(link: string): this {
    this.pending = link
    this.deliver('convia:link', link)
    return this
  }

  // holds makes the installation it connects to one somebody is already signed
  // in to, which is what a kept session does.
  holds(signed: Signed): this {
    this.signed = signed
    return this
  }

  // refuses makes one method fail, the way the application reports a failure.
  refuses(method: string, error: Error): this {
    this.refusals.set(method, error)
    return this
  }

  // connected is the installation the application is pointed at, or null.
  get at(): Connection | null {
    return this.connection
  }

  // offered is what the application would say it remembers.
  get offered(): string[] {
    return [...this.remembered]
  }

  // asked reports whether a method was called, and with what.
  asked(method: string): unknown[][] {
    return this.calls.filter((call) => call.method === method).map((call) => call.args)
  }

  // deliver sends one event to the interface, as the application does when the
  // person's stream carries something.
  deliver(topic: string, what: unknown): void {
    for (const listener of this.listeners.get(topic) ?? []) {
      listener(what)
    }
  }

  event(what: unknown): void {
    this.deliver('convia:event', what)
  }

  stream(state: Partial<StreamState>): void {
    this.deliver('convia:stream', { Live: false, Gap: false, Ended: false, Reason: '', ...state })
  }

  private record<T>(method: string, args: unknown[], answer: () => T): Promise<T> {
    this.calls.push({ method, args })
    const refusal = this.refusals.get(method)
    if (refusal !== undefined) {
      return Promise.reject(refusal)
    }
    return Promise.resolve(answer())
  }

  install(): void {
    const bound = {
      Installations: () => this.record('Installations', [], () => [...this.remembered]),
      Connect: (address: string) =>
        this.record('Connect', [address], () => {
          if (!this.remembered.includes(address)) {
            this.remembered = [address, ...this.remembered]
          }
          this.connection = { address, signed: this.signed }
          return this.connection
        }),
      ConnectHere: () =>
        this.record('ConnectHere', [], () => {
          if (!this.running) {
            throw refused('unreachable', 'convia could not be reached')
          }
          return bound.Connect('http://localhost:8080')
        }),
      PendingLink: () =>
        this.record('PendingLink', [], () => {
          const link = this.pending
          this.pending = ''
          return link
        }),
      Notify: (title: string, body: string) =>
        this.record('Notify', [title, body], () => undefined),
      Named: (open: string, quit: string) => this.record('Named', [open, quit], () => undefined),
      Forget: (address: string) =>
        this.record('Forget', [address], () => {
          this.remembered = this.remembered.filter((one) => one !== address)
        }),
      SignIn: (username: string, password: string) =>
        this.record('SignIn', [username, password], () => {
          this.signed = {
            account_id: 'acc_ANA7QK4XMZP2VJH6TBWRSY5CN',
            user_id: 'usr_ANA7QK4XMZP2VJH6TBWRSY5CN',
            username,
            handle: `${username}#7QK4`,
          }
          if (this.connection !== null) {
            this.connection = { ...this.connection, signed: this.signed }
          }
          return this.signed
        }),
      Register: (username: string, password: string) => bound.SignIn(username, password),
      SignOut: () =>
        this.record('SignOut', [], () => {
          this.signed = null
        }),
      SignOutEverywhere: () => this.record('SignOutEverywhere', [], () => undefined),
      ChangePassword: (current: string, next: string) =>
        this.record('ChangePassword', [current, next], () => undefined),
      DeleteAccount: (password: string) => this.record('DeleteAccount', [password], () => undefined),
    }

    const runtime = {
      EventsOn: (name: string, listener: Listener) => {
        const listening = this.listeners.get(name) ?? new Set<Listener>()
        listening.add(listener)
        this.listeners.set(name, listening)
        return () => listening.delete(listener)
      },
    }

    vi.stubGlobal('go', { app: { App: bound } })
    vi.stubGlobal('runtime', runtime)
  }
}
