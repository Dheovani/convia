import { useEffect, useState } from 'react'

import { api, ApiError } from './api/client'
import type { Account } from './api/types'
import { Shelf, Unexpected } from './components/Recovery'
import { Button } from './components/controls'
import { desktop, inApplication, type Connection } from './desktop/bridge'
import { useWords } from './i18n/language'
import { Installation } from './screens/Installation'
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

/*
The same interface, reached two ways.

In a browser it is a page Convia serves, and where it came from is which Convia
it talks to. In Convia's own application it is compiled into a program that had
to be told which installation to connect to before anything else could happen.
The screens below are the same; what differs is only what has to be known
before the first of them is drawn.
*/
export function App() {
  return (
    <Shelf>
      <Unexpected />
      {inApplication() ? <Installed /> : <Served />}
    </Shelf>
  )
}

// Served is the interface as a page: the origin is the installation, and the
// only question is who is signed in.
function Served() {
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

  return <Signed session={session} onSession={setSession} />
}

/*
Installed is the interface as an application.

Two things have to be known rather than one, and in this order: which
installation, and then who.

**Neither of them is a question if it can be avoided.** The installation used
last is reconnected to without asking, because asking every morning for an
answer given yesterday is a chore. A machine that has connected nowhere is
tried for a Convia of its own, because somebody who installed Convia on their
computer should not have to learn what an address is to open it. Only when both
come to nothing does anybody get asked anything — and the session kept for the
installation signs them straight back in either way.
*/
function Installed() {
  const [connection, setConnection] = useState<Connection | null | undefined>(undefined)

  useEffect(() => {
    let showing = true

    async function reconnect(): Promise<Connection> {
      const remembered = await desktop.installations()
      const last = remembered[0]
      if (last !== undefined) {
        return await desktop.connect(last)
      }
      return await desktop.connectHere()
    }

    reconnect()
      .then((reached) => {
        if (showing) {
          setConnection(reached)
        }
      })
      .catch(() => {
        /*
        Nothing was found: no Convia on this computer, or the one used last is
        gone or unreachable. That is the screen that asks — where the Convia on
        this computer is still the first thing offered, because it may have
        been started in the meantime.
        */
        if (showing) {
          setConnection(null)
        }
      })

    return () => {
      showing = false
    }
  }, [])

  if (connection === undefined) {
    return <Loading />
  }

  if (connection === null) {
    return <Installation onConnected={setConnection} />
  }

  return (
    <Signed
      session={connection.signed}
      onSession={(session) => setConnection({ ...connection, signed: session ?? null })}
      elsewhere={<Elsewhere onChoose={() => setConnection(null)} />}
    />
  )
}

// Elsewhere is the way back to the screen that asks which installation to
// connect to. Without it, somebody who signed out of the wrong one is stuck.
function Elsewhere({ onChoose }: { onChoose: () => void }) {
  const words = useWords()

  return (
    <Button size="small" className="self-center" onClick={onChoose}>
      {words.installation.elsewhere}
    </Button>
  )
}

function Loading() {
  const words = useWords()

  return (
    <div className="grid h-full place-items-center bg-surface-deep" role="status" aria-live="polite">
      <span className="sr-only">{words.app.loading}</span>
    </div>
  )
}

// Signed is the screen for who is signed in: nobody yet known, nobody, or somebody.
function Signed({
  session,
  onSession,
  elsewhere,
}: {
  session: Session
  onSession: (session: Session) => void
  elsewhere?: React.ReactNode
}) {
  if (session === undefined) {
    return <Loading />
  }

  if (session === null) {
    return <SignIn onSignedIn={onSession} elsewhere={elsewhere} />
  }

  return <Workspace account={session} onSignedOut={() => onSession(null)} />
}
