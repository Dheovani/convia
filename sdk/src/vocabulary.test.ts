import { describe, expect, it } from 'vitest'

import { errorCodes, eventTypes, isErrorCode, isEventType } from './vocabulary.js'

/*
The lists being complete is checked by the compiler, not here: see the
assertions in vocabulary.ts. What is left for a test is the behaviour a caller
depends on at run time, where the string came off a socket and nothing has
checked it.
*/

describe('knowing a name from a name it is not', () => {
  it('accepts every name this package ships', () => {
    for (const code of errorCodes) {
      expect(isErrorCode(code)).toBe(true)
    }
    for (const type of eventTypes) {
      expect(isEventType(type)).toBe(true)
    }
  })

  /*
  An installation newer than this package sends names it does not have, and the
  honest answer is no. A client that treats an unknown event as a known one acts
  on something it did not read -- so this is the case that matters, not the
  nonsense one.
  */
  it('refuses a name from a newer installation rather than guessing', () => {
    expect(isEventType('message.pinned')).toBe(false)
    expect(isErrorCode('quota_exhausted')).toBe(false)
  })

  it('refuses a name that merely starts like one', () => {
    expect(isEventType('message.post')).toBe(false)
    expect(isEventType('message.postedd')).toBe(false)
    expect(isErrorCode('not_foun')).toBe(false)
    expect(isErrorCode('')).toBe(false)
  })

  /*
  The two vocabularies are separate, and nothing should accept across them. They
  are both lowercase dotted-or-underscored strings, which is exactly the kind of
  similarity that makes a copied check pass for the wrong reason.
  */
  it('never accepts one vocabulary as the other', () => {
    for (const type of eventTypes) {
      expect(isErrorCode(type)).toBe(false)
    }
    for (const code of errorCodes) {
      expect(isEventType(code)).toBe(false)
    }
  })

  it('has no duplicates, which would make a list longer without covering more', () => {
    expect(new Set(errorCodes).size).toBe(errorCodes.length)
    expect(new Set(eventTypes).size).toBe(eventTypes.length)
  })
})
