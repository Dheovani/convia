import { useId, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Account } from '../api/types'
import { Button, Field } from '../components/controls'
import { Wordmark } from '../components/Mark'
import type { Words } from '../i18n/en'
import { useWords } from '../i18n/language'

type Mode = 'sign-in' | 'register'

/*
The rules Convia applies to a new account, repeated here so that a person finds
out before sending rather than after.

They are a courtesy and not the check: Convia refuses anything that breaks them
whatever this page does, and a page that fell out of step would only be sending
requests that come back refused.
*/
export const minimumPasswordLength = 12
const usernamePattern = /^[a-z0-9][a-z0-9._-]{2,31}$/

/*
explain turns a refusal into something worth reading.

Convia answers a failed sign-in identically whether the username is unknown or
the password is wrong, so this says the same thing for both, rather than
inventing a distinction the server refused to make. Nothing here repeats the
server's own prose: the status and the mode decide the words.
*/
function explain(words: Words, error: unknown, mode: Mode): string {
  const said = words.signIn
  if (error instanceof NetworkError) {
    return said.unreachable
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return said.notAccepted
      case 401:
        return said.mismatch
      case 403:
        return said.foreignPage
      case 409:
        return said.taken
      case 429:
        return mode === 'register' ? said.tooManyAccounts : said.tooManyAttempts
      case 503:
        return said.busy
    }
  }
  return mode === 'register' ? said.registerFailed : said.signInFailed
}

// problem reports what is wrong with a new account before it is sent, or null.
function problem(words: Words, username: string, password: string, confirmation: string): string | null {
  if (!usernamePattern.test(username.trim().toLowerCase())) {
    return words.signIn.badUsername
  }
  if ([...password].length < minimumPasswordLength) {
    return words.signIn.shortPassword(minimumPasswordLength)
  }
  if (password !== confirmation) {
    return words.signIn.passwordsDiffer
  }
  return null
}

export function SignIn({ onSignedIn }: { onSignedIn: (account: Account) => void }) {
  const [mode, setMode] = useState<Mode>('sign-in')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const words = useWords()
  const said = words.signIn
  const failureId = useId()
  const registering = mode === 'register'

  function switchTo(next: Mode) {
    setMode(next)
    setFailure(null)
    setConfirmation('')
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (busy) {
      return
    }

    if (registering) {
      const wrong = problem(words, username, password, confirmation)
      if (wrong !== null) {
        setFailure(wrong)
        return
      }
    }

    setBusy(true)
    setFailure(null)
    try {
      onSignedIn(
        registering ? await api.register(username, password) : await api.signIn(username, password),
      )
    } catch (error) {
      setFailure(explain(words, error, mode))
      setBusy(false)
    }
  }

  const described = failure ? { 'aria-describedby': failureId } : {}

  let action = registering ? said.createAccount : said.signIn
  if (busy) {
    action = registering ? said.creatingAccount : said.signingIn
  }

  return (
    <main
      className="grid min-h-full place-items-center p-6
        [background:radial-gradient(120%_90%_at_50%_-10%,var(--color-accent-soft),transparent_62%),var(--color-surface-deep)]"
    >
      <form
        className="flex w-full max-w-[380px] flex-col gap-3 rounded-lg border border-line
          bg-surface p-8 shadow-raised"
        onSubmit={submit}
        noValidate
      >
        <div className="mb-2">
          <Wordmark />
        </div>
        <h1 className="m-0 font-display text-[1.6rem] font-semibold tracking-[-0.02em]">
          {registering ? said.registerTitle : said.signInTitle}
        </h1>
        <p className="mt-0 mb-2 text-[0.9rem] text-ink-dim">
          {registering ? said.registerLead : said.signInLead}
        </p>

        <Field
          label={said.username}
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          autoFocus
          required
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          {...described}
        />
        {registering && <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">{said.usernameRule}</p>}

        <Field
          label={said.password}
          type="password"
          name="password"
          autoComplete={registering ? 'new-password' : 'current-password'}
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          {...described}
        />

        {registering && (
          <>
            <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">{said.passwordRule(minimumPasswordLength)}</p>
            <Field
              label={said.confirmation}
              type="password"
              name="confirmation"
              autoComplete="new-password"
              required
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              {...described}
            />
            {/*
            Said before the account exists rather than discovered after. The
            password seals the account's key, so there is nothing a reset could
            use to give the account back.
            */}
            <p
              className="m-0 rounded-md border border-line bg-surface-raised px-3 py-2
                text-[0.78rem] leading-relaxed text-ink-dim"
            >
              <strong className="font-semibold text-ink">{said.keepSafe}</strong> {said.keepSafeWhy}
            </p>
          </>
        )}

        {/*
        The failure is a live region so that somebody using a screen reader
        hears it, and it keeps its line whether or not it says anything: an
        error that appears by pushing the button away is an error that gets
        clicked through.
        */}
        <p className="m-0 min-h-5 text-[0.85rem] text-danger" id={failureId} role="alert">
          {failure}
        </p>

        <Button tone="primary" type="submit" disabled={busy}>
          {action}
        </Button>

        <Button
          size="small"
          className="self-center"
          disabled={busy}
          onClick={() => switchTo(registering ? 'sign-in' : 'register')}
        >
          {registering ? said.haveAccount : said.wantAccount}
        </Button>
      </form>
    </main>
  )
}
