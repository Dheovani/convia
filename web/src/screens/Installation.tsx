import { useEffect, useId, useState } from 'react'

import { NetworkError } from '../api/errors'
import { Button, Field } from '../components/controls'
import { Wordmark } from '../components/Mark'
import { desktop, NotConviaError, type Connection } from '../desktop/bridge'
import type { Words } from '../i18n/en'
import { useWords } from '../i18n/language'

/*
Where Convia is, asked only when the application could not work it out.

An address is something whoever set Convia up knows, and an ordinary person
does not — so this screen is the fallback and not the front door. Before it is
ever drawn, the application has tried the installation used last and then the
one on this computer. What is left over is the person whose Convia is
somewhere else, and they were given an address by whoever runs it.

Even then, the Convia on this computer is the first thing offered: somebody who
just installed it and started it a moment ago is one button away, and the
address field is below for the person who actually needs it.
*/

// local names the machine this application is running on, which is the one
// place a plain HTTP address is not a session travelling in the clear.
const local = new Set(['localhost', '127.0.0.1', '[::1]', '::1'])

/*
problem reports what is wrong with an address before it is sent, or null.

It is a courtesy and not the check: the application refuses the same things
whatever this screen does. What it buys is the right words — refused here, the
reason is this interface's to word; refused there, it arrives as prose written
for a log.
*/
export function problem(words: Words, typed: string): string | null {
  const address = typed.trim()
  if (address === '') {
    return words.installation.needed
  }

  if (!address.startsWith('http://')) {
    return null
  }

  const host = address.slice('http://'.length).split('/')[0]?.split(':')[0] ?? ''
  return local.has(host) ? null : words.installation.insecure
}

/*
explain turns a refusal into something worth reading.

The three cases are three different mistakes. Nothing answered: the address may
be right and the network wrong, so trying again is worth suggesting. Something
answered and was not a Convia: the address is wrong, and trying again will not
change that. Anything else has no useful distinction to draw.
*/
function explain(words: Words, error: unknown): string {
  if (error instanceof NetworkError) {
    return words.installation.unreachable
  }
  if (error instanceof NotConviaError) {
    return words.installation.notConvia
  }
  return words.installation.failed
}

export function Installation({ onConnected }: { onConnected: (connection: Connection) => void }) {
  const [address, setAddress] = useState('')
  const [remembered, setRemembered] = useState<string[]>([])
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState<'here' | 'address' | null>(null)

  const words = useWords()
  const said = words.installation
  const failureId = useId()

  useEffect(() => {
    let showing = true
    desktop
      .installations()
      .then((known) => {
        if (showing) {
          setRemembered(known)
        }
      })
      .catch(() => {
        /*
        A list that cannot be read is an empty list on this screen. What this
        screen needs is the button and the field, and refusing to draw them
        would leave somebody unable to connect at all.
        */
      })
    return () => {
      showing = false
    }
  }, [])

  // reach is every way of arriving somewhere: the button, a remembered one, or
  // a typed address. They differ in what they try and in what they say about it.
  async function reach(doing: 'here' | 'address', attempt: () => Promise<Connection>, failed: string) {
    setBusy(doing)
    setFailure(null)
    try {
      onConnected(await attempt())
    } catch (error) {
      setFailure(doing === 'here' ? failed : explain(words, error))
      setBusy(null)
    }
  }

  async function byAddress(typed: string) {
    const wrong = problem(words, typed)
    if (wrong !== null) {
      setFailure(wrong)
      return
    }
    await reach('address', () => desktop.connect(typed.trim()), said.failed)
  }

  async function forget(known: string) {
    setRemembered((offered) => offered.filter((one) => one !== known))
    try {
      await desktop.forget(known)
    } catch {
      // What was asked for is the state afterwards, and the screen already
      // shows it. Putting the row back would be a worse answer than a list
      // that is right again at the next start.
    }
  }

  const described = failure ? { 'aria-describedby': failureId } : {}

  return (
    <main
      className="grid min-h-full place-items-center p-6
        [background:radial-gradient(120%_90%_at_50%_-10%,var(--color-accent-soft),transparent_62%),var(--color-surface-deep)]"
    >
      <form
        className="flex w-full max-w-[380px] flex-col gap-3 rounded-lg border border-line
          bg-surface p-8 shadow-raised"
        onSubmit={(event) => {
          event.preventDefault()
          void byAddress(address)
        }}
        noValidate
      >
        <div className="mb-2">
          <Wordmark />
        </div>
        <h1 className="m-0 font-display text-[1.6rem] font-semibold tracking-[-0.02em]">{said.title}</h1>
        <p className="mt-0 mb-2 text-[0.9rem] text-ink-dim">{said.lead}</p>

        {/*
        The one thing most people need, and the only thing on this screen that
        asks nothing of them.
        */}
        <Button
          tone="primary"
          disabled={busy !== null}
          onClick={() => void reach('here', () => desktop.connectHere(), said.noneHere)}
        >
          {busy === 'here' ? said.lookingHere : said.here}
        </Button>

        {remembered.length > 0 && (
          <section className="flex flex-col gap-1" aria-label={said.remembered}>
            <h2 className="m-0 text-[0.75rem] font-medium tracking-wide text-ink-faint uppercase">
              {said.remembered}
            </h2>
            {remembered.map((known) => (
              <div key={known} className="flex items-center gap-1">
                <Button
                  size="small"
                  className="min-w-0 flex-1 justify-start truncate"
                  disabled={busy !== null}
                  onClick={() => void byAddress(known)}
                >
                  {known}
                </Button>
                <Button
                  size="small"
                  aria-label={said.forget(known)}
                  disabled={busy !== null}
                  onClick={() => void forget(known)}
                >
                  ×
                </Button>
              </div>
            ))}
          </section>
        )}

        <section className="mt-2 flex flex-col gap-3 border-t border-line pt-4" aria-label={said.given}>
          <h2 className="m-0 text-[0.75rem] font-medium tracking-wide text-ink-faint uppercase">
            {said.given}
          </h2>

          <Field
            label={said.address}
            name="address"
            inputMode="url"
            autoComplete="url"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={address}
            onChange={(event) => setAddress(event.target.value)}
            {...described}
          />
          <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">{said.addressRule}</p>

          <Button size="small" type="submit" disabled={busy !== null}>
            {busy === 'address' ? said.connecting : said.connect}
          </Button>
        </section>

        {/*
        The failure keeps its line whether or not it says anything: an error
        that appears by pushing the button away is an error that gets clicked
        through.
        */}
        <p className="m-0 min-h-5 text-[0.85rem] text-danger" id={failureId} role="alert">
          {failure}
        </p>
      </form>
    </main>
  )
}
