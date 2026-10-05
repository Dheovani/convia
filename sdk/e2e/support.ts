import { fileURLToPath } from 'node:url'

import { expect, type Browser, type Page } from '@playwright/test'

export const base = process.env['CONVIA_E2E_URL'] ?? 'http://localhost:8080'

/*
The bundle `npm run build:e2e` writes, injected into every page.
*
Through `fileURLToPath` rather than `.pathname`: on Windows that yields
`/C:/...`, which is not a path anything can open.
*/
export const bundle = fileURLToPath(new URL('./.built/sdk.js', import.meta.url))

const password = 'correct horse battery staple'

export interface Person {
  username: string
  userId: string
}

/*
register makes somebody new, over HTTP rather than through the SDK.

The SDK does not reach the registration surface and should not: these tests are
about what it does *as* a signed-in person, so arriving as one is setup rather
than subject.
*/
/*
theSamePerson is one account for the whole file, made on first use.

**Registering is rationed to twenty an hour per address**, successes included,
so a test that registers somebody of its own costs one of them -- and a suite of
eight could then be run twice before it started failing for a reason that has
nothing to do with the SDK.

One account is enough because what these tests keep apart is rooms, which each
makes its own of, and sessions, which `signedIn` opens one of per page. Ending a
session does not end the account, so even the test that signs out leaves the
others alone.
*/
let theOne: Promise<Person> | undefined

export function theSamePerson(): Promise<Person> {
  theOne ??= register('ana')
  return theOne
}

export async function register(prefix: string): Promise<Person> {
  const username = `${prefix}${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const response = await fetch(`${base}/v1/accounts`, {
    method: 'POST',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  expect(response.status, `registering ${username}`).toBe(201)

  const account = (await response.json()) as { user_id: string }
  return { username, userId: account.user_id }
}

/*
signedIn opens a page on Convia's own origin with a session, and the SDK on it.

**The page has to be on that origin.** The cookie is host-only, so a page
anywhere else would carry nothing, and `credentials: 'same-origin'` would send
nothing -- which would make every test here pass or fail for the wrong reason.
*/
export async function signedIn(browser: Browser, person: Person): Promise<Page> {
  const context = await browser.newContext()
  const signed = await context.request.post(`${base}/v1/sessions`, {
    headers: { Origin: base },
    data: { username: person.username, password },
  })
  expect(signed.ok(), `signing ${person.username} in`).toBe(true)

  const page = await context.newPage()

  /*
  Added before the page loads, rather than injected as a script tag.
  *
  Convia serves `script-src 'self'`, so a tag with inline source is
  refused -- correctly, and that refusal is the policy doing its job. An
  init script runs through the browser's own protocol instead, which puts
  the SDK on the page without asking the page to relax anything.
  */
  await page.addInitScript({ path: bundle })
  await page.goto('/')
  await page.waitForFunction(() => window.sdk !== undefined)
  return page
}

/* roomOf opens a room, through the SDK, as the person on this page. */
export async function roomOf(page: Page, name: string): Promise<string> {
  return page.evaluate(async (called) => {
    const convia = new window.sdk.Convia()
    const room = await convia.post<{ id: string }>('/me/rooms', { body: { name: called } })
    return room.id
  }, name)
}
