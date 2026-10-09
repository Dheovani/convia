import { expect, test } from '@playwright/test'

import { roomOf, signedIn, theSamePerson } from './support.js'

/*
What only a real Convia and a real browser can show.

The unit tests decide what the SDK sends and what it makes of an answer, with
the network faked. **None of them can show that any of it works**: a fake fetch
records `credentials: 'same-origin'` without a cookie existing, a fake WebSocket
never upgrades, and a fake answer never came from a route. These do.
*/

test('a session cookie the page cannot read is what authenticates every request', async ({
  browser,
}) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)

  const me = await page.evaluate(async () => {
    const convia = new window.sdk.Convia()
    return convia.get<{ user_id: string }>('/me')
  })

  expect(me.user_id).toBe(ana.userId)

  /*
  The page genuinely cannot read it. If it could, the session would be reachable
  by anything that gets a script onto the page, and this whole model would be
  the wrong one.
  */
  const visible = await page.evaluate(() => document.cookie)
  expect(visible).not.toContain('convia_session')
})

test('a refusal arrives as a refusal, with the code to branch on', async ({ browser }) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)

  const refusal = await page.evaluate(async () => {
    const convia = new window.sdk.Convia()
    try {
      await convia.get('/me/rooms/room_4XZQP7KN2VJH6TBWMDR3YAFC5E/messages')
      return { thrown: false }
    } catch (error) {
      const refused = window.sdk.isConviaError(error)
      return {
        thrown: true,
        refused,
        status: refused ? (error as InstanceType<typeof window.sdk.ConviaError>).status : 0,
        code: refused ? (error as InstanceType<typeof window.sdk.ConviaError>).code : '',
      }
    }
  })

  expect(refusal.thrown).toBe(true)
  expect(refusal.refused).toBe(true)
  expect(refusal.status).toBe(404)
  expect(refusal.code).toBe('not_found')
})

test('an unauthenticated request says the session is gone rather than that the network failed', async ({
  browser,
}) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)

  // Ending the session leaves the page holding a cookie Convia no longer knows.
  await page.evaluate(async () => {
    const convia = new window.sdk.Convia()
    await convia.delete('/sessions/current')
  })

  const after = await page.evaluate(async () => {
    const convia = new window.sdk.Convia()
    try {
      await convia.get('/me')
      return { thrown: false, unauthenticated: false, unreachable: false }
    } catch (error) {
      return {
        thrown: true,
        unauthenticated:
          window.sdk.isConviaError(error) &&
          (error as InstanceType<typeof window.sdk.ConviaError>).unauthenticated,
        unreachable: window.sdk.isUnreachable(error),
      }
    }
  })

  expect(after.thrown).toBe(true)
  expect(after.unauthenticated).toBe(true)
  expect(after.unreachable).toBe(false)
})

/*
The key doing its job against a real Convia, which it could not until `M19-015`.

Until then no route under `/v1/me` read the header, so a test asserting that it
worked would have been asserting something untrue -- and the one that stood here
said so instead.
*/
test('the same key across attempts makes one room, not two', async ({ browser }) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)
  const name = `Repeated ${Date.now().toString(36)}`

  const made = await page.evaluate(async (called) => {
    const convia = new window.sdk.Convia()
    const key = window.sdk.newIdempotencyKey()

    const first = await convia.post<{ id: string }>('/me/rooms', {
      body: { name: called },
      idempotencyKey: key,
    })

    /*
    The same request again, as a client that lost the first answer would send
    it. Convia performs it at most once and replays the original response, so
    what comes back is the room that already exists.
    */
    const again = await convia.post<{ id: string }>('/me/rooms', {
      body: { name: called },
      idempotencyKey: key,
    })

    const rooms = await convia.get<{ data: { id: string; name: string }[] }>('/me/rooms')
    return {
      first: first.id,
      again: again.id,
      named: rooms.data.filter((room) => room.name === called).length,
    }
  }, name)

  expect(made.again).toBe(made.first)
  expect(made.named).toBe(1)
})

/*
And the refusal that will never clear, which is the other half of the key.

A different body under a key already used is a client bug -- two intents sharing
one key -- and no later attempt changes it. It is `conflict`, not `in_progress`,
and the SDK's retry tells them apart by exactly that.
*/
test('a different body under the same key is refused for good', async ({ browser }) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)

  const refused = await page.evaluate(async () => {
    const convia = new window.sdk.Convia()
    const key = window.sdk.newIdempotencyKey()

    await convia.post('/me/rooms', { body: { name: 'First' }, idempotencyKey: key })
    try {
      await convia.post('/me/rooms', { body: { name: 'Different' }, idempotencyKey: key })
      return { thrown: false, status: 0, code: '' }
    } catch (error) {
      const refusal = window.sdk.isConviaError(error)
      return {
        thrown: true,
        status: refusal ? (error as InstanceType<typeof window.sdk.ConviaError>).status : 0,
        code: refusal ? String((error as InstanceType<typeof window.sdk.ConviaError>).code) : '',
      }
    }
  })

  expect(refused.thrown).toBe(true)
  expect(refused.status).toBe(409)
  expect(refused.code).toBe('conflict')
})

test('the stream opens, carries what happens, and says it is live', async ({ browser }) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)
  const roomId = await roomOf(page, 'Stream check')

  const heard = await page.evaluate(async (room) => {
    const convia = new window.sdk.Convia()
    const stream = new window.sdk.EventStream()

    const seen: string[] = []
    const conditions: string[] = []
    stream.listen((event) => seen.push(event.type))
    stream.watch((condition) => conditions.push(condition.kind))

    await new Promise<void>((ready, give) => {
      const waiting = setTimeout(() => give(new Error('the stream never went live')), 15_000)
      stream.watch((condition) => {
        if (condition.kind === 'live') {
          clearTimeout(waiting)
          ready()
        }
      })
    })

    // Something that happens, which the stream should carry.
    await convia.post(`/me/rooms/${room}/messages`, { body: { body: 'Said over the stream.' } })
    await new Promise((wake) => setTimeout(wake, 3_000))

    stream.close()
    return { seen, conditions }
  }, roomId)

  expect(heard.conditions).toContain('live')
  expect(heard.seen).toContain('message.posted')
})

test('a cursor kept from an earlier stream still gets what was missed', async ({ browser }) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)
  const roomId = await roomOf(page, 'Resume check')

  const heard = await page.evaluate(async (room) => {
    const convia = new window.sdk.Convia()

    const live = (stream: InstanceType<typeof window.sdk.EventStream>) =>
      new Promise<void>((ready, give) => {
        const waiting = setTimeout(() => give(new Error('the stream never went live')), 15_000)
        const stop = stream.watch((condition) => {
          if (condition.kind === 'live') {
            clearTimeout(waiting)
            stop()
            ready()
          }
        })
      })

    /*
    The first stream takes a cursor and then goes away entirely, as a page
    being reloaded does -- nothing is listening while the next thing happens.
    */
    const first = new window.sdk.EventStream()
    let cursor: string | undefined
    first.listen((event) => {
      cursor = event.cursor ?? cursor
    })

    await live(first)
    await convia.post(`/me/rooms/${room}/messages`, { body: { body: 'Before the gap.' } })
    await new Promise((wake) => setTimeout(wake, 2_000))
    first.close()

    // Nothing is connected for this one.
    const missed = await convia.post<{ id: string }>(`/me/rooms/${room}/messages`, {
      body: { body: 'During the gap.' },
    })

    const second = new window.sdk.EventStream(cursor === undefined ? {} : { after: cursor })
    const seen: string[] = []
    second.listen((event) => {
      const subject = event.subject as { id?: string } | undefined
      if (subject?.id !== undefined) {
        seen.push(subject.id)
      }
    })

    await live(second)
    await new Promise((wake) => setTimeout(wake, 2_000))
    second.close()

    return { cursor, missed: missed.id, seen }
  }, roomId)

  expect(heard.cursor).toBeDefined()

  // The message written while nothing was connected arrived on the next stream.
  expect(heard.seen).toContain(heard.missed)
})

test('joining a call answers with an invitation whose credential does not render itself', async ({
  browser,
}) => {
  const ana = await theSamePerson()
  const page = await signedIn(browser, ana)
  const roomId = await roomOf(page, 'Call check')

  const joined = await page.evaluate(async (room) => {
    const convia = new window.sdk.Convia()
    const calls = new window.sdk.Calls(convia)

    const invitation = await calls.join(room)

    return {
      url: invitation.url,
      participantId: invitation.participantId,
      revealed: invitation.token.reveal().length,
      logged: JSON.stringify(invitation),
      usable: window.sdk.usable(invitation),
    }
  }, roomId)

  expect(joined.url).toMatch(/^wss?:\/\//)
  expect(joined.participantId).toMatch(/^part_/)
  expect(joined.revealed).toBeGreaterThan(20)
  expect(joined.usable).toBe(true)

  // The credential is in the invitation and not in what a logger would write.
  expect(joined.logged).toContain('[redacted]')
})
