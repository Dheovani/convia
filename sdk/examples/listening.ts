/*
Being told what happens, and knowing when to go and ask instead.

**Being told is an improvement on asking, never a replacement for being able to
ask.** Everything the stream delivers can also be read over REST, and a consumer
that cannot work without the stream has built something more fragile than the
stream it depends on. What `watch` reports is enough to know when asking is
necessary again.
*/

import { EventStream, type Condition, type ConviaEvent } from '../src/index.js'

export interface Watching {
  /* Stops listening and closes the connection. */
  stop: () => void
}

/*
followARoom keeps one connection and reports when a gap appeared.

`reread` is called when the consumer has to go and ask: Convia dropped events
this stream could not keep up with, or the cursor it resumed from was older than
Convia keeps. Neither can be filled by waiting.
*/
export function followARoom(
  roomId: string,
  onMessage: (event: ConviaEvent) => void,
  reread: (why: Condition['kind']) => void,
): Watching {
  const stream = new EventStream()

  const stopListening = stream.listen((event) => {
    const about = event.data as { room_id?: string } | undefined
    if (about?.room_id !== roomId) {
      return
    }
    if (event.type === 'message.posted' || event.type === 'message.edited') {
      onMessage(event)
    }
  })

  const stopWatching = stream.watch((condition) => {
    switch (condition.kind) {
      case 'behind':
      case 'restarted':
        reread(condition.kind)
        break

      case 'ended':
        /*
        Nothing reconnects after this, and nothing should: the upgrade would be
        refused and the refusal reaches a script without its status, so every
        attempt would teach the client nothing and it would keep making them.
        */
        reread(condition.kind)
        break

      default:
        break
    }
  })

  return {
    stop: () => {
      stopListening()
      stopWatching()
      stream.close()
    },
  }
}

/*
resuming picks up from where a previous process stopped.

Reconnecting within one stream resumes by itself. This is the other case: a
cursor kept somewhere that survives a page reload, so what happened while
nothing was connected at all still arrives.
*/
export function resuming(lastCursor: string | undefined, remember: (cursor: string) => void) {
  const stream = new EventStream(lastCursor === undefined ? {} : { after: lastCursor })

  stream.listen((event) => {
    if (event.cursor !== undefined) {
      remember(event.cursor)
    }
  })

  return stream
}
