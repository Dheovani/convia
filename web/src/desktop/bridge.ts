import { ApiError, NetworkError } from '../api/client'
import type { Account } from '../api/types'

/*
The boundary between this interface and the application that shows it.

Convia's own client is a desktop application: a Go process with this interface
compiled into it, rendered in a webview. Most of what the interface does does
not change — it asks for `/v1/...` and the application carries that to the
installation with the session attached, which is the same position the page is
in with a cookie.

What is here is the part that cannot work that way:

  - **the routes that hand a session over or take one away.** Their answer
    contains the session itself, and the application refuses to carry them
    because the answer to a carried request is read by this webview;
  - **the person's event stream.** A page cannot set a header on a WebSocket
    handshake, so the application holds the stream and the events arrive here
    instead;
  - **which installation to talk to at all**, which is a question a page in a
    browser never had to ask.

Everything here is absent in a browser, where this same interface runs against
a Convia serving it. `inApplication` is what tells them apart, and nothing
below is called when it is false.
*/

/*
Signed is who is signed in, as the application reports them.

It is the same shape as Account deliberately: the application returns the
person without the session, which is what Convia's own `/v1/me` answers for a
browser. Nothing else crosses this boundary about a session, ever.
*/
export type Signed = Account

// Connection is what the application says after an installation is chosen.
// `signed` is null when nobody is signed in to it on this machine.
export interface Connection {
  address: string
  signed: Signed | null
}

// StreamState is what the application says about the person's stream.
export interface StreamState {
  Live: boolean
  Gap: boolean
  Ended: boolean
  Reason: string
}

/*
Bound is what the Go process exposes to this interface.

It mirrors internal/desktop/app. The names are Go's because Wails binds
exported methods by their own names, and writing them any other way here would
be a second name for one thing.
*/
interface Bound {
  Installations: () => Promise<string[] | null>
  Connect: (address: string) => Promise<Connection>
  ConnectHere: () => Promise<Connection>
  Forget: (address: string) => Promise<void>
  PendingLink: () => Promise<string>
  SignIn: (username: string, password: string) => Promise<Signed>
  Register: (username: string, password: string) => Promise<Signed>
  SignOut: () => Promise<void>
  SignOutEverywhere: () => Promise<void>
  ChangePassword: (current: string, next: string) => Promise<void>
  DeleteAccount: (password: string) => Promise<void>
}

interface Runtime {
  EventsOn: (name: string, callback: (...data: unknown[]) => void) => () => void
}

interface Host {
  go?: { app?: { App?: Bound } }
  runtime?: Runtime
}

function host(): Host {
  return globalThis as unknown as Host
}

/*
inApplication reports whether this interface is being shown by Convia's
application rather than by a browser.

It asks whether the application's methods are there rather than sniffing a user
agent, because that is the thing it actually needs: a build that looks like the
application but bound nothing would fail at the first call, and here it takes
the browser's path instead.
*/
export function inApplication(): boolean {
  return host().go?.app?.App !== undefined
}

function bound(): Bound {
  const application = host().go?.app?.App
  if (application === undefined) {
    throw new Error('This interface is not being shown by Convia’s application.')
  }
  return application
}

/*
NotConviaError is an address that answered and was not a Convia anybody can
sign in to.

It is separate from NetworkError because the two are different mistakes. An
installation that does not answer may be starting, or behind a network that is
down, and the answer is to try again. Something else answering means the
address is wrong, and trying again will not change that.
*/
export class NotConviaError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'NotConviaError'
  }
}

/*
Failure is what internal/desktop/app renders a refusal as.

It arrives as the text of a rejected promise rather than as an object, because
Wails builds that promise's Error from what it is given and an object arrives
as the words "[object Object]". The Go side renders JSON for that reason, and
this is the other half of it.
*/
interface Failure {
  kind: 'refused' | 'unreachable' | 'not_convia' | 'failed'
  status?: number
  code?: string
  message: string
  request_id?: string
}

/*
asFailure reads what the application refused with, and gives up rather than
guessing.

Anything it cannot read is left alone: an error nobody wrote in this shape is
an error from somewhere else, and dressing it up as a refusal would put a code
on a screen that no installation ever said.
*/
function asFailure(thrown: unknown): Failure | undefined {
  if (!(thrown instanceof Error)) {
    return undefined
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(thrown.message)
  } catch {
    return undefined
  }

  const failure = parsed as Partial<Failure> | null
  if (failure === null || typeof failure !== 'object' || typeof failure.kind !== 'string') {
    return undefined
  }
  return failure as Failure
}

/*
asked is one call to the application, with its refusals translated into the
errors the rest of this interface already knows how to show.

A screen that handles a wrong password does not need to learn a second way of
being told about one just because the request went through the application
instead of through fetch.
*/
async function asked<T>(call: () => Promise<T>): Promise<T> {
  try {
    return await call()
  } catch (thrown) {
    const failure = asFailure(thrown)
    if (failure === undefined) {
      throw thrown
    }

    switch (failure.kind) {
      case 'refused':
        throw new ApiError(failure.status ?? 0, {
          code: failure.code ?? 'unknown',
          message: failure.message,
          ...(failure.request_id === undefined ? {} : { request_id: failure.request_id }),
        })
      case 'unreachable':
        throw new NetworkError(thrown)
      case 'not_convia':
        throw new NotConviaError(failure.message)
      default:
        throw new Error(failure.message)
    }
  }
}

/*
desktop is everything this interface asks of the application it runs in.

Calling any of it in a browser throws, which is the truthful answer: none of it
exists there, and a screen that reaches for it is a screen that did not check.
*/
export const desktop = {
  // installations are the Convias this machine has connected to, most recently
  // used first. An empty list is somebody's first start rather than a failure.
  installations(): Promise<string[]> {
    return asked(async () => (await bound().Installations()) ?? [])
  },

  /*
  connect points the application at an installation and remembers it.

  The address is checked before anything else happens, because what follows
  this screen is somebody typing a password: a typo must not become a
  credential that somebody else holds.
  */
  connect(address: string): Promise<Connection> {
    return asked(() => bound().Connect(address))
  },

  /*
  connectHere connects to the Convia on this computer.

  It is what the application tries before asking anybody anything. An address
  is something whoever set Convia up knows and an ordinary person does not, so
  the screen that asks for one is the fallback rather than the front door.
  */
  connectHere(): Promise<Connection> {
    return asked(() => bound().ConnectHere())
  },

  /*
  pendingLink takes the invitation somebody clicked, and forgets it.

  **It is the only place a link is read.** One that arrived is kept by the
  application until it is taken, because clicking an invitation on a machine
  where Convia is closed starts the application with it — before the window
  exists, before an installation is chosen, and before anybody has signed in.
  The event below is a nudge to come and take it, not the link itself.
  */
  pendingLink(): Promise<string> {
    return asked(() => bound().PendingLink())
  },

  // forget stops offering an installation, and drops the session kept for it.
  forget(address: string): Promise<void> {
    return asked(() => bound().Forget(address))
  },

  signIn(username: string, password: string): Promise<Signed> {
    return asked(() => bound().SignIn(username, password))
  },

  register(username: string, password: string): Promise<Signed> {
    return asked(() => bound().Register(username, password))
  },

  signOut(): Promise<void> {
    return asked(() => bound().SignOut())
  },

  signOutEverywhere(): Promise<void> {
    return asked(() => bound().SignOutEverywhere())
  },

  changePassword(current: string, next: string): Promise<void> {
    return asked(() => bound().ChangePassword(current, next))
  },

  deleteAccount(password: string): Promise<void> {
    return asked(() => bound().DeleteAccount(password))
  },
}

// The topics the application emits on. They are internal/desktop/app's
// constants, and the two have to say the same thing.
const eventTopic = 'convia:event'
const streamTopic = 'convia:stream'
const linkTopic = 'convia:link'

/*
listen subscribes to what the application sends without being asked: the
person's events, and whether their stream is open.

It answers with the function that stops listening, so that a component which
mounts twice does not end up with two subscriptions writing the same events
onto the screen.
*/
/*
whenLinked says when an invitation was clicked while this window was open.

What it hands over is the fact rather than the link: the link is taken with
`pendingLink`, so that one arriving before the window existed and one arriving
after are the same thing to whoever is listening.
*/
export function whenLinked(onLink: () => void): () => void {
  const runtime = host().runtime
  if (runtime === undefined) {
    return () => {}
  }
  return runtime.EventsOn(linkTopic, () => onLink())
}

export function listen(
  onEvent: (event: unknown) => void,
  onState: (state: StreamState) => void,
): () => void {
  const runtime = host().runtime
  if (runtime === undefined) {
    return () => {}
  }

  const events = runtime.EventsOn(eventTopic, (...data: unknown[]) => onEvent(data[0]))
  const states = runtime.EventsOn(streamTopic, (...data: unknown[]) => onState(data[0] as StreamState))

  return () => {
    events()
    states()
  }
}
