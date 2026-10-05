import { inspect } from 'node:util'
import { describe, expect, it } from 'vitest'

import { Calls } from './calls.js'
import { MediaToken, invitationFrom, longestWait, usable } from './media.js'
import { Convia } from './transport.js'

const secret = 'eyJhbGciOiJIUzI1NiJ9.a-real-looking-credential.signature'

const answered = {
  participant_id: 'part_7KQZP4XN2VJH6TBWMDR3YAFC5E',
  call_id: 'call_7KQZP4XN2VJH6TBWMDR3YAFC5E',
  media_url: 'wss://media.convia.example',
  media_token: secret,
  expires_at: '2026-09-09T19:45:00.000Z',
}

/*
Every way a value ends up somewhere it was not meant to go.

The contract says the credential is never logged. Saying so is not enough:
`console.log` is one keystroke, a structured logger serialises whatever it is
handed, and a crash reporter walks the object. Each of these is a path that has
leaked a secret in somebody's production logs, so each is a test.
*/
describe('a credential that does not render itself', () => {
  const token = new MediaToken(secret)

  it('does not appear in a template literal', () => {
    expect(`${token}`).toBe('[redacted]')
  })

  it('does not appear in String()', () => {
    expect(String(token)).toBe('[redacted]')
  })

  /*
  Called directly, which is a different path from the one above.

  `Symbol.toPrimitive` takes precedence over `toString` for coercion, so every
  test that coerces passes whether `toString` redacts or not. A logger that
  calls `.toString()` itself reaches the method nothing else does -- and that is
  the one a refactor can drop without any other test noticing.
  */
  it('does not appear when toString is called outright', () => {
    expect(token.toString()).toBe('[redacted]')
  })

  it('does not appear in JSON, which is what a structured logger writes', () => {
    expect(JSON.stringify(token)).toBe('"[redacted]"')
    expect(JSON.stringify({ token })).toBe('{"token":"[redacted]"}')
  })

  it('does not appear in console.log, which consults neither of those', () => {
    expect(inspect(token)).toBe('[redacted]')
    expect(inspect({ token })).toContain('[redacted]')
    expect(inspect({ token })).not.toContain(secret)
  })

  it('does not appear through concatenation or coercion', () => {
    expect('token=' + token).toBe('token=[redacted]')
    expect(`${token}`.includes(secret)).toBe(false)
  })

  /*
  The whole invitation is the thing somebody logs, not the token alone, so the
  object has to be safe rather than just the field.
  */
  it('does not appear when the whole invitation is logged', () => {
    const invitation = invitationFrom(answered)

    expect(JSON.stringify(invitation)).not.toContain(secret)
    expect(inspect(invitation)).not.toContain(secret)
    expect(inspect(invitation, { depth: 10 })).not.toContain(secret)
  })

  it('comes out whole when it is being presented', () => {
    expect(token.reveal()).toBe(secret)
  })
})

describe('reading what Convia answered', () => {
  it('carries across every field the client connects with', () => {
    const invitation = invitationFrom(answered)

    expect(invitation.participantId).toBe(answered.participant_id)
    expect(invitation.callId).toBe(answered.call_id)
    expect(invitation.url).toBe(answered.media_url)
    expect(invitation.token.reveal()).toBe(secret)
    expect(invitation.expiresAt.toISOString()).toBe(answered.expires_at)
  })

  /*
  The address is used exactly as given. A deployment commonly reaches its media
  infrastructure over a private network, so one derived from Convia's own
  address works in development and fails in production.
  */
  it('uses the address Convia gave rather than one derived from it', () => {
    const elsewhere = invitationFrom({ ...answered, media_url: 'wss://10.0.0.4:7880' })
    expect(elsewhere.url).toBe('wss://10.0.0.4:7880')
  })
})

describe('knowing when the credential has stopped working', () => {
  const invitation = invitationFrom(answered)
  const expiry = new Date(answered.expires_at)

  it('is usable right up to the moment it is not', () => {
    expect(usable(invitation, new Date(expiry.getTime() - 1))).toBe(true)
    expect(usable(invitation, expiry)).toBe(false)
    expect(usable(invitation, new Date(expiry.getTime() + 1))).toBe(false)
  })

  it('says how long is left', () => {
    expect(longestWait(invitation, new Date(expiry.getTime() - 5_000))).toBe(5_000)
  })

  /*
  Never negative. A caller scheduling a refresh from this would otherwise
  schedule it in the past, and setTimeout runs a negative delay at once -- which
  turns an expired credential into a loop rather than a refresh.
  */
  it('says nothing is left rather than a negative amount', () => {
    expect(longestWait(invitation, new Date(expiry.getTime() + 60_000))).toBe(0)
  })
})

describe('joining and leaving', () => {
  function answering(reply: (request: Request) => Response) {
    const asked: Request[] = []
    const convia = new Convia({
      fetch: (input, init) => {
        const request = new Request(new URL(String(input), 'http://convia.test'), init)
        asked.push(request)
        return Promise.resolve(reply(request))
      },
    })
    return { calls: new Calls(convia), asked }
  }

  const json = (status: number, body: unknown) =>
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })

  it('asks the right route and reads the answer into an invitation', async () => {
    const { calls, asked } = answering(() => json(200, answered))
    const invitation = await calls.join('room_1')

    expect(new URL(asked[0]!.url).pathname).toBe('/v1/me/rooms/room_1/call/join')
    expect(asked[0]!.method).toBe('POST')
    expect(invitation.token.reveal()).toBe(secret)
  })

  /*
  A room identifier arrives from somewhere, and one with a slash or a question
  mark in it would otherwise change which route is called.
  */
  it('encodes the room identifier rather than pasting it into a path', async () => {
    const { calls, asked } = answering(() => json(200, answered))
    await calls.join('room/../../operator')

    expect(new URL(asked[0]!.url).pathname).toBe('/v1/me/rooms/room%2F..%2F..%2Foperator/call/join')
  })

  it('leaves without expecting anything back', async () => {
    const { calls, asked } = answering(() => new Response(null, { status: 204 }))
    await expect(calls.leave('room_1')).resolves.toBeUndefined()
    expect(new URL(asked[0]!.url).pathname).toBe('/v1/me/rooms/room_1/call/leave')
  })

  it('unwraps the roster rather than handing back the envelope', async () => {
    const { calls } = answering(() => json(200, { data: [{ id: 'part_1' }, { id: 'part_2' }] }))
    const roster = await calls.participants('room_1')
    expect(roster).toHaveLength(2)
    expect(roster[0]?.id).toBe('part_1')
  })
})
