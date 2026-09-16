import { Component, createContext, useContext, useEffect, useId, useState } from 'react'
import { createPortal } from 'react-dom'

import { useWords } from '../i18n/language'
import { Button } from './controls'

/*
What the interface does when part of it breaks.

The product owner decided the shape: **each zone has its own boundary**, so a
conversation that throws leaves the list, the rail and a call in progress as they
were, and the page has one more for whatever is left. What broke offers **Try
again**, which draws that part afresh, **Reload the page**, and **details to
report**. Nothing is sent anywhere: the failure is written to the console.

Failures that break no component — a promise nobody waited for, an error thrown
outside React — are said in a toast at the bottom right, and the page stays as it
is.
*/

// page is the one way this module reloads the page, so a test can see it asked.
export const page = {
  reload: () => window.location.reload(),
}

/*
build names the bundle this page is running, by the file it was loaded from.

The file name carries the hash of what was built, so it says exactly which build
failed without a version anybody has to remember to raise.
*/
function build(): string {
  try {
    return new URL(import.meta.url).pathname.split('/').pop() ?? 'unknown'
  } catch {
    return 'unknown'
  }
}

/*
report is what a person can copy into a bug report: the build, the time, where it
broke and what was thrown. It carries nothing a person wrote, because what was
drawn is not part of it.
*/
function report(zone: string, error: Error): string {
  const stack = (error.stack ?? '').split('\n').slice(0, 8).join('\n')
  return [
    `Convia ${build()}`,
    new Date().toISOString(),
    `Zone: ${zone}`,
    `${error.name}: ${error.message}`,
    stack,
  ]
    .filter((line) => line !== '')
    .join('\n')
}

/*
The shelf is where toasts stack, at the bottom right of the page. A toast rendered
where no shelf was set up — a component tested on its own — stands there by
itself.
*/
const ShelfContext = createContext<HTMLElement | null>(null)

const shelfClass = 'pointer-events-none fixed right-4 bottom-4 z-30 flex flex-col items-end gap-1'

export function Shelf({ children }: { children: React.ReactNode }) {
  const [shelf, setShelf] = useState<HTMLElement | null>(null)

  return (
    <ShelfContext.Provider value={shelf}>
      {children}
      <div ref={setShelf} className={shelfClass} />
    </ShelfContext.Provider>
  )
}

export function OnShelf({ children }: { children: React.ReactNode }) {
  const shelf = useContext(ShelfContext)
  if (shelf === null) {
    return <div className={shelfClass}>{children}</div>
  }
  return createPortal(children, shelf)
}

export const toastClass =
  'pointer-events-auto m-0 flex items-center gap-2 rounded-md border border-line bg-surface-raised px-3 py-1.5 ' +
  'text-[0.8rem] shadow-raised'

function Details({ text }: { text: string }) {
  const said = useWords().recovery
  const [copied, setCopied] = useState(false)

  return (
    <details className="w-full max-w-xl text-left">
      <summary className="cursor-pointer text-[0.8rem] text-ink-dim">{said.details}</summary>
      {/* Technical, and written for whoever reads the report: it is not translated. */}
      <pre
        translate="no"
        className="mt-2 max-h-48 overflow-auto rounded-md border border-line bg-surface-sunken p-2 font-mono
          text-[0.7rem] whitespace-pre-wrap text-ink-dim"
      >
        {text}
      </pre>
      <Button
        size="small"
        className="mt-2"
        onClick={() =>
          void navigator.clipboard
            ?.writeText(text)
            .then(() => setCopied(true))
            .catch(() => setCopied(false))
        }
      >
        {copied ? said.copied : said.copy}
      </Button>
    </details>
  )
}

/*
A zone takes the place of what broke. A toast is for a part too small to hold the
explanation — the rail, or the call's sound, which draws nothing — and leaves an
empty placeholder where the part was, so the layout around it holds.
*/
type Variant = 'page' | 'zone' | 'toast' | 'sound'

function Broken({
  variant,
  placement,
  details,
  onRetry,
}: {
  variant: Variant
  placement: string | undefined
  details: string
  onRetry: () => void
}) {
  const said = useWords().recovery
  const titleId = useId()

  const actions = (
    <div className="flex flex-wrap justify-center gap-2">
      <Button tone="primary" size="small" onClick={onRetry}>
        {said.tryAgain}
      </Button>
      <Button size="small" onClick={() => page.reload()}>
        {said.reload}
      </Button>
    </div>
  )

  if (variant === 'toast' || variant === 'sound') {
    return (
      <>
        {placement !== undefined && <div className={placement} aria-hidden="true" />}
        <OnShelf>
          <div role="alert" className={toastClass}>
            <span>{variant === 'sound' ? said.soundBroken : said.zoneBroken}</span>
            <Button size="small" onClick={onRetry}>
              {said.tryAgain}
            </Button>
          </div>
        </OnShelf>
      </>
    )
  }

  return (
    <div
      role="alert"
      aria-labelledby={titleId}
      className={`flex flex-col items-center justify-center gap-3 p-6 text-center ${
        variant === 'page' ? 'min-h-full bg-surface-deep' : (placement ?? 'min-h-0 flex-1')
      }`}
    >
      <p id={titleId} className="m-0 font-display text-base font-semibold">
        {variant === 'page' ? said.pageBroken : said.zoneBroken}
      </p>
      <p className="m-0 max-w-md text-[0.85rem] text-ink-dim">{said.hint}</p>
      {actions}
      <Details text={details} />
    </div>
  )
}

interface RecoverableProps {
  // zone names the part that broke, in the details and in the console.
  zone: string
  variant?: Variant
  // resetKey clears a failure when what the zone shows changes, such as another room.
  resetKey?: string
  // placement is the class the broken zone takes in the layout around it.
  placement?: string | undefined
  children: React.ReactNode
}

interface RecoverableState {
  failure: { error: Error; details: string } | null
}

/*
Recoverable is one boundary. What is inside it is drawn afresh on Try again: it
was unmounted when the failure took its place, so nothing of the state that broke
it is left.
*/
export class Recoverable extends Component<RecoverableProps, RecoverableState> {
  override state: RecoverableState = { failure: null }

  static getDerivedStateFromError(thrown: unknown): Partial<RecoverableState> {
    const error = thrown instanceof Error ? thrown : new Error(String(thrown))
    return { failure: { error, details: '' } }
  }

  override componentDidCatch(thrown: unknown): void {
    const error = thrown instanceof Error ? thrown : new Error(String(thrown))
    console.error(`convia: the ${this.props.zone} stopped working`, error)
    this.setState({ failure: { error, details: report(this.props.zone, error) } })
  }

  override componentDidUpdate(previous: RecoverableProps): void {
    if (previous.resetKey !== this.props.resetKey && this.state.failure !== null) {
      this.retry()
    }
  }

  retry = () => {
    this.setState({ failure: null })
  }

  override render() {
    const { failure } = this.state
    const { children, variant = 'zone', placement } = this.props

    if (failure !== null) {
      return <Broken variant={variant} placement={placement} details={failure.details} onRetry={this.retry} />
    }
    return children
  }
}

// aborted is a request this page gave up on itself, which is not a failure.
function aborted(reason: unknown): boolean {
  return reason instanceof DOMException && reason.name === 'AbortError'
}

/*
Unexpected says, once, that something failed outside any component. It stays
until dismissed; a second failure while it is shown says nothing more.
*/
export function Unexpected() {
  const said = useWords().recovery
  const [shown, setShown] = useState(false)

  useEffect(() => {
    const failed = (event: ErrorEvent) => {
      // An event with no error is a resource that did not load, or a script from elsewhere.
      if (event.error !== undefined && event.error !== null) {
        setShown(true)
      }
    }
    const rejected = (event: PromiseRejectionEvent) => {
      if (!aborted(event.reason)) {
        setShown(true)
      }
    }
    window.addEventListener('error', failed)
    window.addEventListener('unhandledrejection', rejected)
    return () => {
      window.removeEventListener('error', failed)
      window.removeEventListener('unhandledrejection', rejected)
    }
  }, [])

  if (!shown) {
    return null
  }

  return (
    <OnShelf>
      <div role="status" className={toastClass}>
        <span>{said.unexpected}</span>
        <Button size="small" onClick={() => setShown(false)}>
          {said.dismiss}
        </Button>
      </div>
    </OnShelf>
  )
}
