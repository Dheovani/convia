import { describe, expect, it } from 'vitest'

import { ConviaError, Unreachable, isConviaError, isUnreachable } from './errors.js'

const refusal = (status: number, code: string) =>
  new ConviaError(status, { code: code as never, message: 'No.', request_id: 'req_1' })

describe('telling a refusal from silence', () => {
  /*
  The two are different types so that a `catch` handling one does not silently
  handle the other. Telling somebody their password was wrong when the network
  was down would be a lie, and one class with a flag is how that lie gets told.
  */
  it('never reports one as the other', () => {
    const refused = refusal(403, 'wrong_password')
    const silent = new Unreachable(new TypeError('failed to fetch'))

    expect(isConviaError(refused)).toBe(true)
    expect(isUnreachable(refused)).toBe(false)
    expect(isConviaError(silent)).toBe(false)
    expect(isUnreachable(silent)).toBe(true)
  })

  it('is not fooled by something that merely looks like one', () => {
    expect(isConviaError(new Error('No.'))).toBe(false)
    expect(isConviaError({ status: 403, code: 'wrong_password' })).toBe(false)
    expect(isUnreachable(undefined)).toBe(false)
    expect(isUnreachable(null)).toBe(false)
  })

  it('keeps the request identifier, which is the only way to find the other side', () => {
    expect(refusal(500, 'internal_error').requestId).toBe('req_1')
  })

  it('keeps the cause, so a proxy failure is still debuggable', () => {
    const cause = new TypeError('failed to fetch')
    expect(new Unreachable(cause).cause).toBe(cause)
  })
})

describe('what a caller is allowed to conclude', () => {
  /*
  401 is the one refusal that ends the signed-in state. 403 is not: a wrong
  current password when changing it is a 403, and signing somebody out for
  mistyping it would lose whatever they were doing.
  */
  it('reports only 401 as a session that is gone', () => {
    expect(refusal(401, 'unauthenticated').unauthenticated).toBe(true)
    expect(refusal(403, 'wrong_password').unauthenticated).toBe(false)
    expect(refusal(403, 'forbidden').unauthenticated).toBe(false)
    expect(refusal(404, 'not_found').unauthenticated).toBe(false)
  })

  /*
  Retrying is how one bad request becomes a thousand. Only a refusal that may
  succeed later *unchanged* is worth repeating: everything else is the caller's
  to fix, and a client that retries a 400 will retry it forever.
  */
  it('reports as retryable only what a later attempt could change', () => {
    expect(refusal(429, 'rate_limited').retryable).toBe(true)
    expect(refusal(503, 'unavailable').retryable).toBe(true)

    for (const [status, code] of [
      [400, 'invalid_request'],
      [401, 'unauthenticated'],
      [403, 'forbidden'],
      [404, 'not_found'],
      [409, 'conflict'],
      [413, 'payload_too_large'],
      [500, 'internal_error'],
    ] as const) {
      expect(refusal(status, code).retryable, `${status} ${code}`).toBe(false)
    }
  })

  /*
  An absent Retry-After is not zero. Reading it as "now" is how a rate limit
  becomes an outage, so it stays undefined until somebody sets it.
  */
  it('leaves an unstated wait undefined rather than zero', () => {
    expect(refusal(429, 'rate_limited').retryAfter).toBeUndefined()
  })

  it('carries a code it has never heard of rather than failing to parse', () => {
    const newer = refusal(422, 'quota_exhausted')
    expect(newer.code).toBe('quota_exhausted')
    expect(newer.retryable).toBe(false)
  })
})
