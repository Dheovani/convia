import { useId, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Account } from '../api/types'
import { Button, Field } from '../components/controls'
import { Wordmark } from '../components/Mark'

/*
explain turns a refusal into something worth reading.

Convia answers a failed sign-in identically whether the address is unknown or
the password is wrong, and that is deliberate: the form must not be a way to
find out who has an account. So this says the same thing for both, rather than
inventing a distinction the server refused to make.
*/
function explain(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Check your connection and try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 401:
        return 'That email and password do not match an account.'
      case 403:
        return 'This page could not prove it came from Convia. Reload and try again.'
      case 429:
        return 'Too many attempts from here. Wait a moment and try again.'
      case 503:
        return 'Convia is busy right now. Try again in a moment.'
      default:
        return error.message
    }
  }
  return 'Something went wrong. Try again.'
}

export function SignIn({ onSignedIn }: { onSignedIn: (account: Account) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const failureId = useId()

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (busy) {
      return
    }

    setBusy(true)
    setFailure(null)
    try {
      onSignedIn(await api.signIn(email, password))
    } catch (error) {
      setFailure(explain(error))
      setBusy(false)
    }
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
          Sign in
        </h1>
        <p className="mt-0 mb-2 text-[0.9rem] text-ink-dim">Talk, meet, and stay in touch.</p>

        <Field
          label="Email"
          type="email"
          name="email"
          autoComplete="username"
          autoFocus
          required
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          {...(failure ? { 'aria-describedby': failureId } : {})}
        />

        <Field
          label="Password"
          type="password"
          name="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          {...(failure ? { 'aria-describedby': failureId } : {})}
        />

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
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>

        <p className="mt-2 mb-0 text-[0.78rem] leading-relaxed text-ink-faint">
          Accounts are created by an operator. If you do not have one, ask the person who
          runs this Convia.
        </p>
      </form>
    </main>
  )
}
