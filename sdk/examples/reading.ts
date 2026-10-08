/*
Asking Convia something, and telling the ways it can fail apart.

These examples are **compiled**, not quoted: `npm run typecheck` reads this
directory, so an example that stopped being true stops the build rather than
sitting in a document being wrong. They are not run -- what they do against a
real installation is `e2e/`.
*/

import { Convia, Timeout, Unreachable, Unreadable, isConviaError } from '../src/index.js'

interface Me {
  account_id: string
  user_id: string
  username: string
  handle: string
}

/* whoAmI reads the signed-in person. */
export async function whoAmI(): Promise<Me> {
  const convia = new Convia()
  return convia.get<Me>('/me')
}

/*
openARoom makes one, and tells the three kinds of failure apart.

**A client that confuses them says the wrong thing to somebody.** Reporting a
cancelled request as a network failure shows an error for a page they navigated
away from; reporting an unreachable installation as a refusal tells them they
did something wrong.
*/
export async function openARoom(name: string): Promise<string | undefined> {
  const convia = new Convia()

  try {
    const room = await convia.post<{ id: string }>('/me/rooms', { body: { name } })
    return room.id
  } catch (error) {
    if (isConviaError(error)) {
      if (error.unauthenticated) {
        // The session is gone. Everything else is a request to fix.
        return undefined
      }
      // Branch on the code, never on the message: the code is the documented
      // identifier and the message is prose that may be reworded.
      if (error.code === 'conflict') {
        return undefined
      }
      throw error
    }

    if (error instanceof Timeout || error instanceof Unreachable) {
      // Convia may be fine and may not. Nothing here knows, so nothing here
      // guesses -- the caller decides whether to ask again.
      return undefined
    }

    if (error instanceof Unreadable) {
      // The request arrived and the answer is not what it claimed to be: a
      // truncated body, or a proxy that replaced it.
      return undefined
    }

    throw error
  }
}

/*
listRooms shows a caller's own cancellation composing with the client's deadline.

Either may fire. The caller does not have to choose between its own abort and a
deadline, which is the whole reason the two are composed rather than one winning.
*/
export async function listRooms(stop: AbortSignal): Promise<{ id: string; name: string }[]> {
  const convia = new Convia()
  const answered = await convia.get<{ data: { id: string; name: string }[] }>('/me/rooms', {
    signal: stop,
    timeout: 5_000,
  })
  return answered.data
}
