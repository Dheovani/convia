import { vi } from 'vitest'

import type { Account, Message, SidebarRoom } from '../api/types'

/*
A stand-in for Convia, scripted per route.

`fetch` is replaced rather than a client injected, because what these tests are
checking is partly the request itself: the method, the path, and the body the
interface sends are as much a part of the contract as the state it ends up in.
A test double one layer higher would agree with whatever the client did.
*/
export interface Route {
  status?: number
  body?: unknown
  failure?: { code: string; message: string }
}

export interface Call {
  method: string
  path: string
  body: unknown
}

export class FakeConvia {
  readonly calls: Call[] = []
  private readonly routes = new Map<string, Route>()

  on(method: string, path: string, route: Route): this {
    this.routes.set(`${method} ${path}`, route)
    return this
  }

  install(): void {
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://convia.test')
      const method = init?.method ?? 'GET'
      const body: unknown =
        typeof init?.body === 'string' ? JSON.parse(init.body) : undefined

      this.calls.push({ method, path: url.pathname + url.search, body })

      const route =
        this.routes.get(`${method} ${url.pathname}${url.search}`) ??
        this.routes.get(`${method} ${url.pathname}`)

      if (route === undefined) {
        return Promise.resolve(answer(404, { error: { code: 'not_found', message: 'No route.' } }))
      }
      if (route.failure !== undefined) {
        return Promise.resolve(answer(route.status ?? 400, { error: route.failure }))
      }
      return Promise.resolve(answer(route.status ?? 200, route.body))
    })
  }

  // asked reports whether one request was made, for the assertions that are
  // about what the interface sent rather than what it drew.
  asked(method: string, path: string): Call | undefined {
    return this.calls.find((call) => call.method === method && call.path.split('?')[0] === path)
  }
}

function answer(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export const ana: Account = {
  account_id: 'acc_7QK4XMZP2VJH6TBWNDR3YAFC5E',
  user_id: 'usr_7KQZP4XN2VJH6TBWMDR3YAFC5E',
  username: 'ana',
  handle: 'ana#7QK4XMZP2VJH6TBWNDR3YAFC5EH',
}

export function room(overrides: Partial<SidebarRoom> = {}): SidebarRoom {
  return {
    id: 'room_7KQZP4XN2VJH6TBWMDR3YAFC5E',
    name: 'Standup',
    status: 'open',
    unread: 0,
    owned: false,
    moderator: false,
    ...overrides,
  }
}

export function message(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg_6TBWNDR3YAFC5E7QK4XMZP2VJH',
    application_id: 'app_MXHJAY4MJNX2FO22XWJ3XNCKHT',
    room_id: room().id,
    sequence: 1,
    user_id: ana.user_id,
    body: 'Standup in five minutes.',
    deleted: false,
    created_at: '2026-09-05T14:04:56.154Z',
    ...overrides,
  }
}

/*
withdrawn is a message that was taken back.

Its body and its author are *absent* rather than empty: Convia omits both from
the JSON, and modelling them as present-and-undefined would let a component pass
a test by reading a field that never arrives.
*/
export function withdrawn(): Message {
  const said = message()
  return {
    id: said.id,
    application_id: said.application_id,
    room_id: said.room_id,
    sequence: said.sequence,
    deleted: true,
    created_at: said.created_at,
    deleted_at: '2026-09-05T14:20:00.000Z',
  }
}
