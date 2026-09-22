import { useEffect, useState } from 'react'

import { desktop, inApplication, whenLinked } from './bridge'

/*
Arrived is an invitation somebody clicked, and when it reached the interface.

The moment is carried because the same link clicked twice is two arrivals: the
first opens the panel, somebody closes it without joining, and the second has
to open it again. Two identical strings would be one value and would not.
*/
export interface Arrived {
  link: string
  at: number
}

/*
useInvitation is the invitation that opened this window, or arrived at it.

Clicking a link on a machine where Convia is closed starts the application with
it, which means it arrives before there is anywhere to put it. It waits in the
application until this asks, which is why both cases — one that started the
window and one that reached it while it was open — are the same call.

In a browser there is nothing to ask, and this answers nothing: a page is
opened by a link rather than handed one.
*/
export function useInvitation(): Arrived | undefined {
  const [arrived, setArrived] = useState<Arrived | undefined>(undefined)

  useEffect(() => {
    if (!inApplication()) {
      return
    }

    let showing = true

    function take() {
      desktop
        .pendingLink()
        .then((link) => {
          if (showing && link !== '') {
            setArrived({ link, at: Date.now() })
          }
        })
        .catch(() => {
          // An invitation that cannot be read is one nobody is shown. The
          // link can still be pasted, which is how every one was opened
          // before a click could open this window.
        })
    }

    take()
    const stop = whenLinked(take)

    return () => {
      showing = false
      stop()
    }
  }, [])

  return arrived
}
