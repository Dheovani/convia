import { expect, type Browser, type BrowserContext, type Page } from '@playwright/test'

export const base = process.env['CONVIA_E2E_URL'] ?? 'http://localhost:8080'

/*
shareBase is this same Convia at an address another installation could reach.

People share a room through an invitation link, and the link is followed like any
other: a link to loopback is refused as Convia itself, so the journeys invite
through an address on the machine's network, which the Convia under test is
configured to allow with CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES. The browser stays
on `base`, because the session cookie is `__Host-` and only localhost is a secure
context over plain HTTP.
*/
const shareBase = process.env['CONVIA_E2E_SHARE_URL']

const password = 'correct horse battery staple'

export interface Person {
  username: string
  user_id: string
  handle: string
  cookie: string
}

export interface Answer {
  status: number
  // Whatever Convia answered with; the journeys read the fields they check.
  body: any
}

async function send(at: string, person: Person | null, method: string, path: string, body?: unknown): Promise<Answer> {
  const response = await fetch(at + path, {
    method,
    headers: {
      Origin: at,
      ...(person === null ? {} : { Cookie: person.cookie }),
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  const text = await response.text()
  return { status: response.status, body: text === '' ? undefined : JSON.parse(text) }
}

// api asks Convia something as a person, the way the page would.
export function api(person: Person, method: string, path: string, body?: unknown): Promise<Answer> {
  return send(base, person, method, path, body)
}

// register makes somebody new, with a name nobody has used before.
export async function register(prefix: string): Promise<Person> {
  const username = `${prefix}${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const response = await fetch(`${base}/v1/accounts`, {
    method: 'POST',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  expect(response.status, `registering ${username}`).toBe(201)

  const cookie = (response.headers.get('set-cookie') ?? '').split(';')[0] ?? ''
  const account = (await response.json()) as { user_id: string; handle: string }
  return { username, user_id: account.user_id, handle: account.handle, cookie }
}

// shareRoom opens a room as its owner and brings somebody else into it by invitation.
export async function shareRoom(owner: Person, guest: Person): Promise<{ id: string; name: string }> {
  if (shareBase === undefined) {
    throw new Error('Set CONVIA_E2E_SHARE_URL to this Convia at an address on the network, such as http://192.168.0.2:8080.')
  }

  const name = `Call check ${Date.now().toString(36)}`
  const room = await api(owner, 'POST', '/v1/me/rooms', { name })
  expect(room.status, 'opening a room').toBe(201)

  const invitation = await send(shareBase, owner, 'POST', `/v1/me/rooms/${room.body.id}/invitations`, {
    handle: guest.handle,
  })
  expect(invitation.status, 'inviting somebody').toBe(201)

  const joined = await send(shareBase, guest, 'POST', '/v1/me/remote-rooms', { link: invitation.body.link })
  expect(joined.status, 'accepting the invitation').toBeLessThan(300)
  expect(joined.body.room_id, 'the room joined').toBe(room.body.id)

  return { id: room.body.id, name }
}

// openRoom opens a room of one's own that nobody else is in.
export async function openRoom(owner: Person): Promise<{ id: string; name: string }> {
  const name = `Quiet ${Date.now().toString(36)}`
  const room = await api(owner, 'POST', '/v1/me/rooms', { name })
  expect(room.status, 'opening a room').toBe(201)
  return { id: room.body.id, name }
}

export interface Signed {
  context: BrowserContext
  page: Page
}

// signedIn opens the interface as somebody, signed in by Convia itself.
export async function signedIn(browser: Browser, person: Person, options: { narrow?: boolean } = {}): Promise<Signed> {
  const context = await browser.newContext(options.narrow ? { viewport: { width: 390, height: 844 } } : {})
  const signed = await context.request.post(`${base}/v1/sessions`, {
    headers: { Origin: base },
    data: { username: person.username, password },
  })
  expect(signed.ok(), `signing ${person.username} in`).toBe(true)

  const page = await context.newPage()
  await page.goto('/')
  return { context, page }
}

// callStage is the call as shown in the open room.
export function callStage(page: Page) {
  return page.getByRole('region', { name: 'Call' })
}

// join presses the call button, gets ready, and joins, then waits to be in the call.
export async function join(page: Page, label: 'Start call' | 'Join call') {
  await page.getByRole('button', { name: label }).first().click()
  const ready = page.getByRole('region', { name: 'Prepare to join' })
  await expect(ready.getByRole('meter', { name: 'Microphone level' })).toBeVisible()
  await ready.getByRole('button', { name: label }).click()
  await expect(callStage(page).getByRole('button', { name: 'Leave call' })).toBeVisible()
}

// inCall is how many people Convia says are in a room's call.
export async function inCall(person: Person, roomId: string): Promise<number> {
  const roster = await api(person, 'GET', `/v1/me/rooms/${roomId}/call/participants`)
  return roster.status === 200 ? roster.body.data.length : 0
}

// callsOf is how many calls are running in somebody's rooms.
export async function callsOf(person: Person): Promise<number> {
  return (await api(person, 'GET', '/v1/me/calls')).body.data.length
}
