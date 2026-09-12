import { useEffect, useState } from 'react'

import { api, ApiError } from './api/client'
import type { Account } from './api/types'
import { SignIn } from './screens/SignIn'
import { Workspace } from './screens/Workspace'

/*
Who is signed in, as far as this page knows.

`undefined` is not the same as `null` and the difference is the whole first
paint: undefined means Convia has not answered yet, and null means it answered
that nobody is. Collapsing them would flash the sign-in form at somebody who is
already signed in, on every reload.
*/
type Session = Account | null | undefined

export function App() {
  const [session, setSession] = useState<Session>(undefined)

  /*
  The page asks who it is serving before it draws anything.

  There is nothing in the page to ask: the session lives in a cookie the script
  cannot read, which is what makes it safe from an XSS in this very bundle. The
  server is the only thing that knows, so this is a request rather than a read.
  */
  useEffect(() => {
    const controller = new AbortController()

    api
      .me(controller.signal)
      .then((account) => setSession(account))
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          setSession(null)
          return
        }
        /*
        Anything else — the server is down, the network is gone — is also
        answered with the sign-in form. It is the one screen that works without
        knowing anything, and it explains the failure when somebody tries.
        */
        setSession(null)
      })

    return () => controller.abort()
  }, [])

  if (session === undefined) {
    return (
      <div className="grid h-full place-items-center bg-surface-deep" role="status" aria-live="polite">
        <span className="sr-only">Loading Convia</span>
      </div>
    )
  }

  if (session === null) {
    return <SignIn onSignedIn={setSession} />
  }

  return <Workspace account={session} onSignedOut={() => setSession(null)} />
}
