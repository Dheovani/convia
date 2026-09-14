import { useEffect, useId, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Person, RoomInvitation, SidebarRoom } from '../api/types'
import { Button, input } from './controls'

interface RoomPeopleProps {
  id: string
  room: SidebarRoom
  // selfId is who this person is in the room: their user here, or at the room's home.
  selfId: string
  // home is set for a room on another installation.
  home?: string
  members: Person[]
  membersFailed: boolean
  onChanged: () => void
  onLeave: () => Promise<void>
  // onForget is set for a room on another installation, for when leaving it fails.
  onForget?: () => Promise<void>
  onExpired: () => void
}

// label is what a person reads for somebody, which is never blank.
export function label(person: Person): string {
  return person.display_name.trim() === '' ? 'Somebody unnamed' : person.display_name
}

function explain(error: unknown, fallback: string): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError && error.status !== 404) {
    return error.message
  }
  return fallback
}

// explainInvitation says why an invitation was not made, from the status alone.
function explainInvitation(error: unknown): string {
  if (error instanceof NetworkError) {
    return 'Convia could not be reached. Try again.'
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return 'That handle is not right. Check it with the person — every character counts.'
      case 404:
        return 'You are no longer in this room.'
      case 409:
        return 'That person is already in this room.'
    }
  }
  return 'The invitation could not be made. Try again.'
}

/*
namesThisComputer reports a link other machines cannot follow.

A link names the address this page was opened at. Opened as localhost, that is
this computer, and a person anywhere else following it would reach their own
machine instead.
*/
function namesThisComputer(link: string): boolean {
  try {
    const host = new URL(link).hostname
    return host === 'localhost' || host === '127.0.0.1' || host === '[::1]' || host === '::1'
  } catch {
    return false
  }
}

function hostOf(home: string): string {
  try {
    return new URL(home).host
  } catch {
    return home
  }
}

const headingText = 'm-0 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint uppercase'
const heading = `${headingText} mb-2`

/*
Invite makes an invitation for a handle and shows the link to send.

**This is the one field in the interface a person types somebody's identity
into**, and it is safe to be one because a handle is not a lookup: nothing here
says whether it names an account, anywhere. The link works only for the key the
handle's identifier is the fingerprint of, so a handle with a mistake in it
produces a link nobody can use rather than one somebody else can.
*/
function Invite({ roomId, onExpired }: { roomId: string; onExpired: () => void }) {
  const [handle, setHandle] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [invitation, setInvitation] = useState<RoomInvitation | null>(null)
  const [copied, setCopied] = useState(false)
  const field = useId()

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (handle.trim() === '' || busy) {
      return
    }
    setBusy(true)
    setFailure(null)
    setCopied(false)
    try {
      setInvitation(await api.invite(roomId, handle.trim()))
      setHandle('')
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onExpired()
        return
      }
      setFailure(explainInvitation(error))
    } finally {
      setBusy(false)
    }
  }

  function copy(link: string) {
    void navigator.clipboard
      ?.writeText(link)
      .then(() => setCopied(true))
      .catch(() => setCopied(false))
  }

  return (
    <section>
      <h3 className={heading}>Invite by handle</h3>
      <form className="flex gap-2" onSubmit={submit}>
        <label className="sr-only" htmlFor={field}>
          Handle
        </label>
        <input
          id={field}
          className={`${input} min-w-0 flex-1 py-1.5 text-[0.82rem]`}
          placeholder="name#IDENTIFIER"
          autoCapitalize="none"
          spellCheck={false}
          value={handle}
          onChange={(event) => setHandle(event.target.value)}
        />
        <Button size="small" type="submit" disabled={busy || handle.trim() === ''}>
          {busy ? 'Inviting…' : 'Invite'}
        </Button>
      </form>

      {failure !== null && (
        <p className="mt-2 mb-0 text-[0.8rem] text-danger" role="alert">
          {failure}
        </p>
      )}

      {invitation !== null && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">
            Send this link to {invitation.invitee}. It works once, for a day, and only for them.
          </p>
          <input
            className={`${input} py-1.5 font-mono text-[0.72rem]`}
            aria-label="Invitation link"
            readOnly
            value={invitation.link}
            onFocus={(event) => event.target.select()}
          />
          <Button size="small" className="self-start" onClick={() => copy(invitation.link)}>
            {copied ? 'Copied' : 'Copy link'}
          </Button>
          {namesThisComputer(invitation.link) && (
            <p className="m-0 text-[0.75rem] leading-relaxed text-danger">
              This link names this computer, so it only works for somebody using Convia on this same
              machine. Open Convia at an address others can reach to invite them.
            </p>
          )}
        </div>
      )}

      <p className="mt-2 mb-0 text-[0.72rem] leading-relaxed text-ink-faint">
        People on any Convia can join with their handle. They find it on their avatar.
      </p>
    </section>
  )
}

/*
RoomPeople is who is in a room, who could be, and the way out.

**Who could be added directly comes from Convia and from nowhere else.** A
person adds only somebody they already share a room with; anybody else is
invited by handle, which makes a link rather than a membership.

**There is no way to remove somebody.** Convia offers none to a person, because
membership carries no role for that power to rest on, and a button that could
only ever fail would be worse than no button.

A room on another installation shows who is in it and the way out, and nothing
else: adding and inviting are acts of the room's home, done by the people who
signed in there.

Leaving asks first. It is the one act here a person cannot undo by themselves:
getting back in needs somebody still inside.
*/
export function RoomPeople({
  id,
  room,
  selfId,
  home,
  members,
  membersFailed,
  onChanged,
  onLeave,
  onForget,
  onExpired,
}: RoomPeopleProps) {
  const [candidates, setCandidates] = useState<Person[] | null>(null)
  const [candidatesFailed, setCandidatesFailed] = useState(false)
  const [adding, setAdding] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [leaving, setLeaving] = useState(false)
  // stranded is a room elsewhere whose home did not confirm this person left.
  const [stranded, setStranded] = useState(false)

  const remote = home !== undefined
  const inRoom = new Set(members.map((person) => person.user_id))
  const addable = (candidates ?? []).filter((person) => !inRoom.has(person.user_id))

  const expired = useRef(onExpired)
  expired.current = onExpired

  function fail(error: unknown, fallback: string) {
    if (error instanceof ApiError && error.unauthenticated) {
      onExpired()
      return
    }
    setFailure(explain(error, fallback))
  }

  /*
  Who could be added is read as soon as the panel opens, because showing them is
  the only thing that section is for. A room elsewhere reads nobody: adding
  belongs to its home.
  */
  useEffect(() => {
    if (remote) {
      return
    }
    const controller = new AbortController()
    api.people(controller.signal).then(
      (page) => {
        if (!controller.signal.aborted) {
          setCandidates(page.data)
        }
      },
      (error: unknown) => {
        if (controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setCandidatesFailed(true)
      },
    )
    return () => controller.abort()
  }, [remote])

  async function add(person: Person) {
    setAdding(person.user_id)
    setFailure(null)
    try {
      await api.addMember(room.id, person.user_id)
      onChanged()
    } catch (error) {
      /*
      Convia gives one answer for every reason somebody cannot be added, so this
      gives one sentence. Guessing which reason it was would be inventing a
      distinction the server refused to make.
      */
      fail(error, `${label(person)} cannot be added to this room.`)
    } finally {
      setAdding(null)
    }
  }

  async function leave() {
    setLeaving(true)
    setFailure(null)
    try {
      await onLeave()
    } catch (error) {
      if (remote && error instanceof ApiError && error.status === 503) {
        setFailure('The Convia this room lives on did not confirm that you left, so you are still in it. Try again later.')
        setStranded(true)
      } else {
        fail(error, 'You are no longer in this room.')
      }
      setLeaving(false)
      setConfirming(false)
    }
  }

  /*
  forget drops a room elsewhere from this Convia without its home.

  It is offered only once leaving has failed, and only after saying what it
  leaves behind: the person is still a member there, and nothing here can take
  them out of it later.
  */
  async function forget() {
    if (onForget === undefined) {
      return
    }
    setLeaving(true)
    setFailure(null)
    try {
      await onForget()
    } catch (error) {
      fail(error, 'This room could not be forgotten. Try again.')
      setLeaving(false)
    }
  }

  return (
    <aside
      id={id}
      aria-label={`People in ${room.name}`}
      className="flex max-h-[40vh] min-h-0 flex-col gap-5 overflow-y-auto border-t border-line
        bg-surface-deep p-4 md:max-h-none md:w-64 md:flex-none md:border-t-0 md:border-l"
    >
      {remote && (
        <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">
          This room lives on {hostOf(home)}. You take part through your own Convia.
        </p>
      )}

      <section>
        <h3 className={heading}>In this room</h3>
        {membersFailed ? (
          <p className="m-0 text-[0.8rem] text-ink-faint">Who is here could not be read.</p>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {members.map((person) => (
              <li key={person.user_id} className="truncate text-[0.88rem]">
                {label(person)}
                {person.user_id === selfId && <span className="text-ink-faint"> (you)</span>}
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* The section folds away, so a long list does not push the rest of the panel out of reach. */}
      {!remote && (
        <details open className="group">
          <summary
            className="mb-2 flex cursor-pointer list-none items-center gap-1.5 rounded-sm
              [&::-webkit-details-marker]:hidden"
          >
            <svg
              aria-hidden="true"
              viewBox="0 0 12 12"
              className="size-3 flex-none text-ink-faint transition-transform group-open:rotate-90"
            >
              <path
                d="M4.5 2.5 8 6l-3.5 3.5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <h3 className={headingText}>Add somebody</h3>
            {addable.length > 0 && <span className="text-[0.72rem] text-ink-faint">{addable.length}</span>}
          </summary>
          {candidatesFailed ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">The people you could add could not be read.</p>
          ) : candidates === null ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">Loading…</p>
          ) : addable.length === 0 ? (
            <p className="m-0 text-[0.8rem] leading-relaxed text-ink-faint">
              {candidates.length === 0
                ? 'You do not share a room with anybody else yet.'
                : 'Everybody you share a room with is already here.'}
            </p>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
              {addable.map((person) => (
                <li key={person.user_id} className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-[0.88rem]">{label(person)}</span>
                  <Button
                    size="small"
                    aria-label={`Add ${label(person)}`}
                    disabled={adding !== null}
                    onClick={() => void add(person)}
                  >
                    {adding === person.user_id ? 'Adding…' : 'Add'}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-2 mb-0 text-[0.72rem] leading-relaxed text-ink-faint">
            You can add people you already share a room with.
          </p>
        </details>
      )}

      {!remote && <Invite roomId={room.id} onExpired={onExpired} />}

      {failure !== null && (
        <p className="m-0 text-[0.8rem] text-danger" role="alert">
          {failure}
        </p>
      )}

      <section className="mt-auto">
        {stranded && onForget !== undefined && (
          <div className="mb-3 flex flex-col gap-2">
            <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">
              You can forget it here instead. It leaves your list, but {hostOf(home ?? '')} still counts you as a
              member, and nothing here can take you out of it later.
            </p>
            <Button size="small" className="self-start" disabled={leaving} onClick={() => void forget()}>
              {leaving ? 'Forgetting…' : 'Forget it here'}
            </Button>
          </div>
        )}
        {confirming ? (
          <div className="flex flex-col gap-2">
            <p className="m-0 text-[0.85rem]">Leave {room.name}? What you said stays.</p>
            <div className="flex gap-2">
              <Button tone="primary" size="small" disabled={leaving} onClick={() => void leave()}>
                {leaving ? 'Leaving…' : 'Leave'}
              </Button>
              <Button size="small" disabled={leaving} onClick={() => setConfirming(false)}>
                Stay
              </Button>
            </div>
          </div>
        ) : (
          <Button size="small" onClick={() => setConfirming(true)}>
            Leave this room
          </Button>
        )}
      </section>
    </aside>
  )
}
