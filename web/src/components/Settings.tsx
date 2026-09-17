import { useEffect, useId, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Account } from '../api/types'
import type { Words } from '../i18n/en'
import { languageNames, refused, useChoosing, useWords } from '../i18n/language'
import { minimumPasswordLength } from '../screens/SignIn'
import { useCall } from '../state/call'
import {
  applyTheme,
  languageChoices,
  rememberedTheme,
  rememberTheme,
  themes,
  type LanguageChoice,
  type Theme,
} from '../state/preferences'
import { Devices, OwnLevel, OwnPicture, PreviewRefusals, usePreviewFor } from './Call'
import { Button, Field, input } from './controls'

export type Section = 'account' | 'appearance' | 'language' | 'calls'

export const sections: readonly Section[] = ['account', 'appearance', 'language', 'calls']

/*
checking is who holds the preview the calls section opens. A room's identifier
never looks like it, so the two previews cannot be mistaken for each other.
*/
const checking = 'settings'

const headingText = 'm-0 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint uppercase'

const rowClass =
  'flex w-full cursor-pointer items-center gap-2 rounded-md px-3 py-2 text-left text-ink-dim ' +
  'transition-colors hover:bg-surface-hover hover:text-ink ' +
  'aria-[current=true]:bg-accent-soft aria-[current=true]:text-ink'

// SettingsNav is the list of sections, in the zone that lists conversations elsewhere.
export function SettingsNav({ section, onSection }: { section: Section; onSection: (section: Section) => void }) {
  const said = useWords().settings

  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
      <h2 className={`${headingText} mx-2 mb-3`}>{said.title}</h2>
      <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
        {sections.map((each) => (
          <li key={each}>
            <button
              type="button"
              className={rowClass}
              aria-current={each === section ? 'true' : undefined}
              onClick={() => onSection(each)}
            >
              {said.sections[each]}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

/*
Part is one thing a section holds. A section that holds only one needs no second
heading for it: the section's own names it.
*/
function Part({ title, children }: { title?: string; children: React.ReactNode }) {
  const id = useId()
  if (title === undefined) {
    return <div className="flex max-w-xl flex-col gap-3">{children}</div>
  }
  return (
    <section aria-labelledby={id} className="flex max-w-xl flex-col gap-3">
      <h3 id={id} className="m-0 text-[0.95rem] font-semibold">
        {title}
      </h3>
      {children}
    </section>
  )
}

const hint = 'm-0 text-[0.8rem] leading-relaxed text-ink-dim'

// Handle shows how other people name this person, to copy and send.
function Handle({ handle }: { handle: string }) {
  const said = useWords().settings
  const [copied, setCopied] = useState(false)

  return (
    <Part title={said.handleHeading}>
      <p className={hint}>{said.handleHint}</p>
      <div className="flex gap-2">
        <input
          className={`${input} min-w-0 flex-1 py-1.5 font-mono text-[0.8rem]`}
          aria-label={said.handleHeading}
          readOnly
          value={handle}
          onFocus={(event) => event.target.select()}
        />
        <Button
          size="small"
          onClick={() =>
            void navigator.clipboard
              ?.writeText(handle)
              .then(() => setCopied(true))
              .catch(() => setCopied(false))
          }
        >
          {copied ? said.copied : said.copyHandle}
        </Button>
      </div>
    </Part>
  )
}

/*
explainChange says why a password was not changed.

A wrong current password is its own answer, and it is not a session that ended:
the person stays signed in and may try again, until the address runs out of
attempts.
*/
function explainChange(words: Words, error: unknown): string {
  const said = words.settings
  if (error instanceof NetworkError) {
    return words.common.unreachable
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return said.passwordNotAccepted
      case 403:
        return error.code === 'wrong_password' ? said.wrongPassword : words.signIn.foreignPage
      case 429:
        return said.tooManyAttempts
      case 503:
        return words.signIn.busy
    }
  }
  return refused(words, error, said.passwordFailed)
}

/*
PasswordChange replaces the password. The current one is asked for, because a
session left open somewhere must not be enough to take the account; and what the
registration form says is said again, because nothing can reset a password that
is lost.
*/
function PasswordChange({ onExpired }: { onExpired: () => void }) {
  const words = useWords()
  const said = words.settings
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [changed, setChanged] = useState(false)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (busy) {
      return
    }
    setChanged(false)
    if ([...next].length < minimumPasswordLength) {
      setFailure(words.signIn.shortPassword(minimumPasswordLength))
      return
    }
    if (next !== confirmation) {
      setFailure(words.signIn.passwordsDiffer)
      return
    }

    setBusy(true)
    setFailure(null)
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      setConfirmation('')
      setChanged(true)
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onExpired()
        return
      }
      setFailure(explainChange(words, error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Part title={said.passwordHeading}>
      <form className="flex flex-col gap-3" onSubmit={submit} noValidate>
        <Field
          label={said.currentPassword}
          type="password"
          autoComplete="current-password"
          required
          value={current}
          onChange={(event) => setCurrent(event.target.value)}
        />
        <Field
          label={said.newPassword}
          type="password"
          autoComplete="new-password"
          required
          value={next}
          onChange={(event) => setNext(event.target.value)}
        />
        <p className="-mt-2 mb-0 text-[0.75rem] text-ink-faint">{words.signIn.passwordRule(minimumPasswordLength)}</p>
        <Field
          label={said.confirmPassword}
          type="password"
          autoComplete="new-password"
          required
          value={confirmation}
          onChange={(event) => setConfirmation(event.target.value)}
        />
        <p
          className="m-0 rounded-md border border-line bg-surface-raised px-3 py-2 text-[0.78rem] leading-relaxed
            text-ink-dim"
        >
          <strong className="font-semibold text-ink">{words.signIn.keepSafe}</strong> {words.signIn.keepSafeWhy}
        </p>
        {failure !== null && (
          <p className="m-0 text-[0.82rem] text-danger" role="alert">
            {failure}
          </p>
        )}
        <p className="m-0 text-[0.82rem] text-positive" role="status">
          {changed ? said.passwordChanged : null}
        </p>
        <Button
          tone="primary"
          size="small"
          type="submit"
          className="self-start"
          disabled={busy || current === '' || next === ''}
        >
          {busy ? said.changingPassword : said.changePassword}
        </Button>
      </form>
    </Part>
  )
}

// Everywhere ends every session of the account, and this page with them.
function Everywhere({ onSignedOut }: { onSignedOut: () => void }) {
  const words = useWords()
  const said = words.settings
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  async function end() {
    setBusy(true)
    setFailure(null)
    try {
      await api.signOutEverywhere()
      onSignedOut()
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onSignedOut()
        return
      }
      setFailure(refused(words, error, words.roomSettings.failed))
      setBusy(false)
    }
  }

  return (
    <Part title={said.everywhereHeading}>
      <p className={hint}>{said.everywhereHint}</p>
      {confirming ? (
        <div className="flex flex-col gap-2">
          <p className="m-0 text-[0.85rem]">{said.confirmEverywhere}</p>
          <div className="flex gap-2">
            <Button tone="primary" size="small" disabled={busy} onClick={() => void end()}>
              {busy ? said.signingOut : said.everywhere}
            </Button>
            <Button size="small" disabled={busy} onClick={() => setConfirming(false)}>
              {said.stay}
            </Button>
          </div>
        </div>
      ) : (
        <Button size="small" className="self-start" onClick={() => setConfirming(true)}>
          {said.everywhere}
        </Button>
      )}
      {failure !== null && (
        <p className="m-0 text-[0.82rem] text-danger" role="alert">
          {failure}
        </p>
      )}
    </Part>
  )
}

// explainDeletion says why an account was not deleted.
function explainDeletion(words: Words, error: unknown): string {
  const said = words.settings
  if (error instanceof NetworkError) {
    return words.common.unreachable
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return said.deletePasswordMissing
      case 403:
        return error.code === 'wrong_password' ? said.deleteWrongPassword : words.signIn.foreignPage
      case 429:
        return said.tooManyAttempts
      case 503:
        return words.signIn.busy
    }
  }
  return refused(words, error, said.deleteFailed)
}

/*
DeleteAccount deletes the account for good.

Everything it does is said before the password is asked for, because nothing
can bring any of it back. Rooms elsewhere are named in particular: a home that
does not answer keeps the person as a member, and nothing here can change that
afterwards.
*/
function DeleteAccount({ onSignedOut }: { onSignedOut: () => void }) {
  const words = useWords()
  const said = words.settings
  const [confirming, setConfirming] = useState(false)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (busy) {
      return
    }
    setBusy(true)
    setFailure(null)
    try {
      await api.deleteAccount(password)
      onSignedOut()
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onSignedOut()
        return
      }
      setFailure(explainDeletion(words, error))
      setBusy(false)
    }
  }

  function keep() {
    setConfirming(false)
    setPassword('')
    setFailure(null)
  }

  return (
    <Part title={said.deleteHeading}>
      <p className={hint}>{said.deleteHint}</p>
      {confirming ? (
        <form className="flex flex-col gap-3" onSubmit={submit} noValidate>
          <ul className="m-0 flex list-disc flex-col gap-1 pl-5 text-[0.82rem] leading-relaxed text-ink-dim">
            {said.deleteConsequences.map((consequence) => (
              <li key={consequence}>{consequence}</li>
            ))}
          </ul>
          <Field
            label={said.deletePassword}
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          {failure !== null && (
            <p className="m-0 text-[0.82rem] text-danger" role="alert">
              {failure}
            </p>
          )}
          <div className="flex gap-2">
            <Button tone="primary" size="small" type="submit" disabled={busy || password === ''}>
              {busy ? said.deleting : said.deleteForGood}
            </Button>
            <Button size="small" disabled={busy} onClick={keep}>
              {said.keepAccount}
            </Button>
          </div>
        </form>
      ) : (
        <Button size="small" className="self-start" onClick={() => setConfirming(true)}>
          {said.deleteAccount}
        </Button>
      )}
    </Part>
  )
}

// Choices is a group of mutually exclusive options, named by its legend.
function Choices<Value extends string>({
  legend,
  options,
  value,
  onChoose,
}: {
  legend: string
  options: readonly { value: Value; label: string; lang?: string }[]
  value: Value
  onChoose: (value: Value) => void
}) {
  const name = useId()
  return (
    <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
      <legend className="mb-1 p-0 text-[0.8rem] font-medium text-ink-dim">{legend}</legend>
      {options.map((option) => (
        <label key={option.value} className="flex min-h-6 cursor-pointer items-center gap-2 text-[0.88rem]">
          <input
            type="radio"
            name={name}
            className="accent-[var(--color-accent)]"
            checked={option.value === value}
            onChange={() => onChoose(option.value)}
          />
          <span lang={option.lang}>{option.label}</span>
        </label>
      ))}
    </fieldset>
  )
}

function Appearance() {
  const said = useWords().settings
  const [theme, setTheme] = useState(rememberedTheme)

  function choose(next: Theme) {
    applyTheme(next)
    rememberTheme(next)
    setTheme(next)
  }

  return (
    <Part>
      <Choices
        legend={said.theme}
        options={themes.map((each) => ({ value: each, label: said[each] }))}
        value={theme}
        onChoose={choose}
      />
      <p className={hint}>{said.keptHere}</p>
    </Part>
  )
}

/*
Language lists each language in its own words, marked as that language, so a
screen reader says each name the way its speakers do.
*/
function Language() {
  const said = useWords().settings
  const { choice, browser, setChoice } = useChoosing()

  const label = (each: LanguageChoice) =>
    each === 'browser' ? { label: said.browserLanguage(languageNames[browser]) } : { label: languageNames[each], lang: each }

  return (
    <Part>
      <Choices
        legend={said.languageLegend}
        options={languageChoices.map((each) => ({ value: each, ...label(each) }))}
        value={choice}
        onChoose={setChoice}
      />
      <p className={hint}>{said.keptHere}</p>
    </Part>
  )
}

/*
Calls is how calls start and which devices they use, chosen away from any call.

The devices are listed at once, and opened only when the person asks to check
them: opening them is what makes the browser ask for permission. A person in a
call is not offered the check, because the call holds the devices; what they
choose here switches the call's.
*/
function Calls() {
  const said = useWords().settings
  const call = useCall()
  const inCall = call.roomId !== null
  const open = call.preparing === checking

  usePreviewFor(checking)

  // Read once, when the section opens; a device plugged in later is noticed by the call session.
  const refresh = useRef(call.refreshDevices)
  useEffect(() => {
    void refresh.current()
  }, [])

  return (
    <Part>
      <p className={hint}>
        {said.callsHint} {said.keptHere}
      </p>
      <label className="flex min-h-6 cursor-pointer items-center gap-2 text-[0.88rem]">
        <input
          type="checkbox"
          className="accent-[var(--color-accent)]"
          checked={call.choice.microphone}
          onChange={(event) => void call.choose({ microphone: event.target.checked })}
        />
        {said.startMicrophone}
      </label>
      <label className="flex min-h-6 cursor-pointer items-center gap-2 text-[0.88rem]">
        <input
          type="checkbox"
          className="accent-[var(--color-accent)]"
          checked={call.choice.camera}
          onChange={(event) => void call.choose({ camera: event.target.checked })}
        />
        {said.startCamera}
      </label>

      <Devices onChoose={(kind, id) => void call.switchDevice(kind, id)} />

      {inCall ? (
        <p className={hint}>{said.inCall}</p>
      ) : (
        <Button
          size="small"
          className="self-start"
          aria-expanded={open}
          onClick={() => void (open ? call.cancelPreparing(checking) : call.prepare(checking))}
        >
          {open ? said.stopChecking : said.checkDevices}
        </Button>
      )}

      {open && (
        <section aria-label={said.checking} className="flex flex-col gap-2">
          <OwnPicture holder={checking} />
          <OwnLevel />
          <PreviewRefusals />
        </section>
      )}
    </Part>
  )
}

/*
SettingsPage is one section of the settings, in the zone a conversation takes
elsewhere.

What is kept in the account — the password, the sessions — is changed through
Convia. What is kept in this browser — how the page looks, the language, the
devices — is changed here at once and needs nothing saved.
*/
export function SettingsPage({
  section,
  account,
  onSignedOut,
  onBack,
}: {
  section: Section
  account: Account
  onSignedOut: () => void
  // onBack is set on a narrow screen, where the section is shown instead of the list.
  onBack?: () => void
}) {
  const said = useWords().settings

  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={said.sections[section]}>
      <header className="flex items-center gap-3 border-b border-line px-3 py-3 md:px-5">
        {onBack !== undefined && (
          <button
            type="button"
            className="grid size-8 flex-none cursor-pointer place-items-center rounded-md text-ink-dim
              hover:bg-surface-hover hover:text-ink"
            aria-label={said.back}
            onClick={onBack}
          >
            <svg aria-hidden="true" viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor"
              strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round">
              <path d="M10 3.5 5.5 8l4.5 4.5" />
            </svg>
          </button>
        )}
        <h2 className="m-0 min-w-0 truncate font-display text-base font-semibold">{said.sections[section]}</h2>
      </header>

      <div className="flex min-h-0 flex-1 flex-col gap-8 overflow-y-auto px-3 py-5 md:px-5">
        {section === 'account' && (
          <>
            <Handle handle={account.handle} />
            <PasswordChange onExpired={onSignedOut} />
            <Everywhere onSignedOut={onSignedOut} />
            <DeleteAccount onSignedOut={onSignedOut} />
          </>
        )}
        {section === 'appearance' && <Appearance />}
        {section === 'language' && <Language />}
        {section === 'calls' && <Calls />}
      </div>
    </section>
  )
}
