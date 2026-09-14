import { useId, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Account } from '../api/types'
import { Button, Field } from '../components/controls'
import { Wordmark } from '../components/Mark'

type Mode = 'sign-in' | 'register'

/*
The rules Convia applies to a new account, repeated here so that a person finds
out before sending rather than after.

They are a courtesy and not the check: Convia refuses anything that breaks them
whatever this page does, and a page that fell out of step would only be sending
requests that come back refused.
*/
const minimumPasswordLength = 12
const usernamePattern = /^[a-z0-9][a-z0-9._-]{2,31}$/

/*
explain turns a refusal into something worth reading.

Convia answers a failed sign-in identically whether the username is unknown or
the password is wrong, so this says the same thing for both, rather than
inventing a distinction the server refused to make. Nothing here repeats the
server's own prose: the status and the mode decide the words.
*/
function explain(error: unknown, mode: Mode): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Check your connection and try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return 'Convia did not accept that username or password. Check the rules under each field.'
      case 401:
        return 'That username and password do not match an account.'
      case 403:
        return 'This page could not prove it came from Convia. Reload and try again.'
      case 409:
        return 'That username is taken. Choose another.'
      case 429:
        return mode === 'register'
          ? 'Too many accounts were attempted from here. Try again later.'
          : 'Too many attempts from here. Wait a moment and try again.'
      case 503:
        return 'Convia is busy right now. Try again in a moment.'
    }
  }
  return mode === 'register'
    ? 'Convia could not create the account. Try again.'
    : 'Convia could not sign you in. Try again.'
}

// problem reports what is wrong with a new account before it is sent, or null.
function problem(username: string, password: string, confirmation: string): string | null {
  if (!usernamePattern.test(username.trim().toLowerCase())) {
    return 'That username is not one Convia accepts. Check the rule under it.'
  }
  if ([...password].length < minimumPasswordLength) {
    return `A password must be at least ${minimumPasswordLength} characters.`
  }
  if (password !== confirmation) {
    return 'The two passwords do not match.'
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
      const wrong = problem(username, password, confirmation)
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
      setFailure(explain(error, mode))
      setBusy(false)
    }
  }

  const described = failure ? { 'aria-describedby': failureId } : {}

  let action = registering ? 'Create account' : 'Sign in'
  if (busy) {
    action = registering ? 'Creating account…' : 'Signing in…'
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
          {registering ? 'Create your account' : 'Sign in'}
        </h1>
        <p className="mt-0 mb-2 text-[0.9rem] text-ink-dim">
          {registering
            ? 'Your account lives on this Convia, and nowhere else.'
            : 'Talk, meet, and stay in touch.'}
        </p>

        <Field
          label="Username"
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
        {registering && (
          <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">
            3 to 32 characters: letters, digits, dots, dashes and underscores, starting with a
            letter or a digit.
          </p>
        )}

        <Field
          label="Password"
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
            <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">
              At least {minimumPasswordLength} characters. That is the only rule.
            </p>
            <Field
              label="Confirm password"
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
              <strong className="font-semibold text-ink">Keep this password safe.</strong> It locks
              your account's key, so nobody can reset it, not even whoever runs this Convia. If you
              forget it, the account is lost.
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
          {registering ? 'I already have an account' : 'Create an account'}
        </Button>
      </form>
    </main>
  )
}
