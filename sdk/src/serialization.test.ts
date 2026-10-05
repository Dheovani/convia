import { describe, expect, it } from 'vitest'

import { ConviaError, Unreadable, isConviaError, isUnreadable } from './errors.js'
import { invitationFrom } from './media.js'
import { Convia } from './transport.js'

/*
What happens at the edges of turning values into a request and an answer back
into values.

The middle of that is covered where each piece lives. These are the shapes that
arrive from somewhere nobody controls -- a proxy, an older installation, a
caller passing what their own code produced -- and the question each asks is
whether the answer is wrong *quietly*.
*/

function answering(reply: (request: Request) => Response) {
  const asked: Request[] = []
  const client = new Convia({
    fetch: (input, init) => {
      const request = new Request(new URL(String(input), 'http://convia.test'), init)
      asked.push(request)
      return Promise.resolve(reply(request))
    },
  })
  return { client, asked }
}

const json = (status: number, body: string, type = 'application/json') =>
  new Response(body, { status, headers: { 'Content-Type': type } })

describe('reading an answer', () => {
  /*
  **This was returning `undefined` typed as the thing the caller asked for.**

  A proxy that replaces a body, or a response truncated in flight, produced a
  value the caller then read a field from -- a TypeError somewhere else entirely,
  with nothing in it about the answer being unreadable.
  */
  it('says so when a success cannot be read, rather than answering undefined', async () => {
    const { client } = answering(() => json(200, '<h1>502 from a proxy that lied about the status</h1>'))

    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect(isUnreadable(thrown)).toBe(true)
    expect((thrown as Unreadable).status).toBe(200)
    expect((thrown as Unreadable).body).toContain('proxy')
  })

  it('keeps only enough of the body to recognise what replaced it', async () => {
    const { client } = answering(() => json(200, 'x'.repeat(5_000)))
    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect((thrown as Unreadable).body.length).toBeLessThanOrEqual(200)
  })

  /*
  A refusal that cannot be read is still a refusal, and the status is the part
  that still means something. This must stay a ConviaError rather than becoming
  the new one: a caller branching on `unauthenticated` would stop seeing 401s.
  */
  it('still reports an unreadable refusal as a refusal', async () => {
    const { client } = answering(() => json(401, '<h1>Unauthorized</h1>'))

    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect(isConviaError(thrown)).toBe(true)
    expect(isUnreadable(thrown)).toBe(false)
    expect((thrown as ConviaError).status).toBe(401)
    expect((thrown as ConviaError).unauthenticated).toBe(true)
  })

  it('reads an empty body as nothing rather than as unreadable', async () => {
    const { client } = answering(() => new Response(null, { status: 204 }))
    await expect(client.delete('/me/sessions/current')).resolves.toBeUndefined()
  })

  /*
  A 200 with no body at all is read as nothing too, which is the lenient half of
  the rule and deliberately so: **an empty body is an absence, not a lie about
  its content.** A proxy that replaced the answer sends a page; a route that
  answers 200 where it might have answered 204 sends nothing, and refusing that
  would refuse something harmless.

  The 204 above cannot test this -- it returns before the body is read at all --
  so this is the case that holds the guard in place.
  */
  it('reads an empty body on a 200 as nothing, not as a body that failed to parse', async () => {
    const { client } = answering(() => new Response('', { status: 200 }))
    await expect(client.post('/me/rooms/room_1/call/leave')).resolves.toBeUndefined()
  })

  it('reads JSON that is not an object, because the contract has those too', async () => {
    const { client } = answering(() => json(200, '[]'))
    await expect(client.get('/me/rooms')).resolves.toEqual([])
  })

  /*
  `null` is valid JSON and parses to null, which is a value rather than a
  failure. Treating it as unreadable would refuse something Convia may legally
  send.
  */
  it('reads a literal null as null rather than as unreadable', async () => {
    const { client } = answering(() => json(200, 'null'))
    await expect(client.get('/me')).resolves.toBeNull()
  })
})

describe('writing a request', () => {
  const ok = () => json(200, '{}')

  it('sends what it was given, unchanged', async () => {
    const { client, asked } = answering(ok)
    await client.post('/me/rooms', { body: { name: 'Standup', capacity: 8, open: true } })
    expect(await asked[0]!.text()).toBe('{"name":"Standup","capacity":8,"open":true}')
  })

  /*
  JSON drops an undefined property, so a caller building a body from optional
  values sends the field absent rather than null -- which is what Convia's
  "absent means unchanged" routes expect.
  */
  it('leaves an undefined field out rather than sending it as null', async () => {
    const { client, asked } = answering(ok)
    await client.patch('/me/rooms/room_1', { body: { name: 'Standup', topic: undefined } })
    expect(await asked[0]!.text()).toBe('{"name":"Standup"}')
  })

  it('sends an explicit null as null, which is not the same as absent', async () => {
    const { client, asked } = answering(ok)
    await client.patch('/me/rooms/room_1', { body: { topic: null } })
    expect(await asked[0]!.text()).toBe('{"topic":null}')
  })

  /*
  A body that cannot be serialised is the caller's bug, and it reaches them as
  the error JSON raised rather than as an unreachable Convia -- nothing was sent,
  so saying the network failed would be a lie.
  */
  it('refuses a body that cannot be serialised, without sending anything', async () => {
    const { client, asked } = answering(ok)
    const circular: Record<string, unknown> = {}
    circular['self'] = circular

    await expect(client.post('/me/rooms', { body: circular })).rejects.toThrow(TypeError)
    expect(asked).toHaveLength(0)
  })

  it('writes every kind of query value the way Convia reads it', async () => {
    const { client, asked } = answering(ok)
    await client.get('/me/rooms', { query: { limit: 50, open: true, closed: false, name: 'a b' } })

    const url = new URL(asked[0]!.url)
    expect(url.searchParams.get('limit')).toBe('50')
    expect(url.searchParams.get('open')).toBe('true')
    expect(url.searchParams.get('closed')).toBe('false')
    expect(url.searchParams.get('name')).toBe('a b')
  })

  it('escapes a query value rather than letting it add parameters', async () => {
    const { client, asked } = answering(ok)
    await client.get('/me/rooms', { query: { name: 'a&limit=9999' } })

    const url = new URL(asked[0]!.url)
    expect(url.searchParams.get('name')).toBe('a&limit=9999')
    expect(url.searchParams.get('limit')).toBeNull()
  })
})

describe('reading a timestamp', () => {
  const answered = {
    participant_id: 'part_1',
    call_id: 'call_1',
    media_url: 'wss://media.convia.example',
    media_token: 'token',
    expires_at: '2026-09-09T19:45:00.000Z',
  }

  it('reads one the contract promises', () => {
    expect(invitationFrom(answered).expiresAt.toISOString()).toBe(answered.expires_at)
  })

  /*
  **This was producing an Invalid Date and carrying on.**

  `new Date` answers one instead of failing, arithmetic on it is NaN rather than
  an error, and a caller scheduling a refresh from NaN gets a `setTimeout` that
  fires at once and keeps firing. The bound on the wait could not prevent it,
  because `Math.max(0, NaN)` is NaN.
  */
  it('refuses one that is not a timestamp, rather than carrying NaN forward', () => {
    for (const bad of ['', 'not a date', 'yesterday', '2026-13-45T99:99:99Z']) {
      expect(() => invitationFrom({ ...answered, expires_at: bad }), bad).toThrow(TypeError)
    }
  })
})
