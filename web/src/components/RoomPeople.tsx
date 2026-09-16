import { useEffect, useId, useRef, useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Person, RoomInvitation, SidebarRoom } from '../api/types'
import type { Words } from '../i18n/en'
import { refused, useLanguage, useWords } from '../i18n/language'
import { usePresenceOf } from '../state/presence'
import { Button, input } from './controls'
import { PresenceDot } from './Presence'

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
  // onRoomChanged asks for the room to be read again, after handing it over.
  onRoomChanged: () => void
  onLeave: () => Promise<void>
  // onForget is set for a room on another installation, for when leaving it fails.
  onForget?: () => Promise<void>
  onExpired: () => void
}

// label is what a person reads for somebody, which is never blank.
export function label(words: Words, person: Person): string {
  return person.display_name.trim() === '' ? words.common.unnamed : person.display_name
}

// explainInvitation says why an invitation was not made, from the status alone.
function explainInvitation(words: Words, error: unknown): string {
  const said = words.people
  if (error instanceof NetworkError) {
    return words.common.unreachable
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return said.badHandle
      case 404:
        return said.noLongerIn
      case 409:
        return said.alreadyIn
    }
  }
  return said.inviteFailed
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

const moderation =
  'inline-flex min-h-6 cursor-pointer items-center rounded-sm px-1.5 py-0.5 text-[0.72rem] text-ink-faint ' +
  'hover:bg-surface-hover hover:text-ink ' +
  'disabled:cursor-default disabled:opacity-60'

// Chevron marks a section that folds away, turned while it is open.
function Chevron() {
  return (
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
  )
}

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
  const [copied, setCopied] = useState<string | null>(null)
  const [pending, setPending] = useState<RoomInvitation[] | null>(null)
  const [pendingFailed, setPendingFailed] = useState(false)
  const [pendingRead, setPendingRead] = useState(0)
  const [withdrawing, setWithdrawing] = useState<string | null>(null)
  const field = useId()
  const { words, formatting } = useLanguage()
  const said = words.people

  const expired = useRef(onExpired)
  expired.current = onExpired

  // The invitations this person made are read when the panel opens, and after each change.
  useEffect(() => {
    const controller = new AbortController()
    api.pendingInvitations(roomId, controller.signal).then(
      (list) => {
        setPending(list.data)
        setPendingFailed(false)
      },
      (error: unknown) => {
        if (controller.signal.aborted) {
          return
        }
        if (error instanceof ApiError && error.unauthenticated) {
          expired.current()
          return
        }
        setPendingFailed(true)
      },
    )
    return () => controller.abort()
  }, [roomId, pendingRead])

  async function withdraw(target: RoomInvitation) {
    setWithdrawing(target.id)
    setFailure(null)
    try {
      await api.withdrawInvitation(target.id)
      if (invitation?.id === target.id) {
        setInvitation(null)
      }
      setPendingRead((count) => count + 1)
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onExpired()
        return
      }
      setFailure(refused(words, error, said.withdrawFailed))
    } finally {
      setWithdrawing(null)
    }
  }

  const until = (timestamp: string) =>
    new Date(timestamp).toLocaleTimeString(formatting, { hour: '2-digit', minute: '2-digit' })
  const others = (pending ?? []).filter((each) => each.id !== invitation?.id)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (handle.trim() === '' || busy) {
      return
    }
    setBusy(true)
    setFailure(null)
    setCopied(null)
    try {
      setInvitation(await api.invite(roomId, handle.trim()))
      setHandle('')
      setPendingRead((count) => count + 1)
    } catch (error) {
      if (error instanceof ApiError && error.unauthenticated) {
        onExpired()
        return
      }
      setFailure(explainInvitation(words, error))
    } finally {
      setBusy(false)
    }
  }

  function copy(target: RoomInvitation) {
    void navigator.clipboard
      ?.writeText(target.link)
      .then(() => setCopied(target.id))
      .catch(() => setCopied(null))
  }

  return (
    <section>
      <h3 className={heading}>{said.inviteHeading}</h3>
      <form className="flex gap-2" onSubmit={submit}>
        <label className="sr-only" htmlFor={field}>
          {said.handle}
        </label>
        <input
          id={field}
          className={`${input} min-w-0 flex-1 py-1.5 text-[0.82rem]`}
          placeholder={said.handlePlaceholder}
          autoCapitalize="none"
          spellCheck={false}
          value={handle}
          onChange={(event) => setHandle(event.target.value)}
        />
        <Button size="small" type="submit" disabled={busy || handle.trim() === ''}>
          {busy ? said.inviting : said.invite}
        </Button>
      </form>

      {failure !== null && (
        <p className="mt-2 mb-0 text-[0.8rem] text-danger" role="alert">
          {failure}
        </p>
      )}

      {invitation !== null && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">{said.sendLink(invitation.invitee)}</p>
          <input
            className={`${input} py-1.5 font-mono text-[0.72rem]`}
            aria-label={said.linkLabel}
            readOnly
            value={invitation.link}
            onFocus={(event) => event.target.select()}
          />
          <span className="flex gap-2">
            <Button size="small" onClick={() => copy(invitation)}>
              {copied === invitation.id ? said.copied : said.copy}
            </Button>
            <Button
              size="small"
              aria-label={said.withdrawFor(invitation.invitee)}
              disabled={withdrawing !== null}
              onClick={() => void withdraw(invitation)}
            >
              {said.withdraw}
            </Button>
          </span>
          {namesThisComputer(invitation.link) && (
            <p className="m-0 text-[0.75rem] leading-relaxed text-danger">{said.thisComputer}</p>
          )}
        </div>
      )}

      {pendingFailed ? (
        <p className="mt-3 mb-0 text-[0.8rem] text-ink-faint">{said.pendingUnreadable}</p>
      ) : (
        others.length > 0 && (
          <div className="mt-3">
            <h4 className="m-0 mb-1.5 text-[0.75rem] font-medium text-ink-dim">{said.pendingHeading}</h4>
            <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
              {others.map((each) => (
                <li key={each.id} className="flex flex-col gap-1">
                  <span className="flex items-baseline gap-2 text-[0.78rem]">
                    <span className="min-w-0 flex-1 truncate font-mono">{each.invitee}</span>
                    <span className="flex-none text-[0.7rem] text-ink-faint">{said.until(until(each.expires_at))}</span>
                  </span>
                  <span className="flex gap-1">
                    <button
                      type="button"
                      className={moderation}
                      aria-label={said.copyFor(each.invitee)}
                      onClick={() => copy(each)}
                    >
                      {copied === each.id ? said.copied : said.copy}
                    </button>
                    <button
                      type="button"
                      className={moderation}
                      aria-label={said.withdrawFor(each.invitee)}
                      disabled={withdrawing !== null}
                      onClick={() => void withdraw(each)}
                    >
                      {said.withdraw}
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )
      )}

      <p className="mt-2 mb-0 text-[0.72rem] leading-relaxed text-ink-faint">{said.anyConvia}</p>
    </section>
  )
}

/*
RoomPeople is who is in a room, who could be, and the way out.

**Who could be added directly comes from Convia and from nowhere else.** A
person adds only somebody they already share a room with; anybody else is
invited by handle, which makes a link rather than a membership.

**Moderating is offered only where Convia allows it.** The owner removes and
bans anybody, names moderators and hands the room over; a moderator removes and
bans members only. A button that could only ever fail would be worse than no
button.

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
  onRoomChanged,
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
  const [bans, setBans] = useState<Person[] | null>(null)
  const [bansFailed, setBansFailed] = useState(false)
  const [bansRead, setBansRead] = useState(0)
  const [acting, setActing] = useState<string | null>(null)
  // handing is who this person asked to make the owner, until they confirm.
  const [handing, setHanding] = useState<Person | null>(null)
  const words = useWords()
  const said = words.people
  const nameOf = (person: Person) => label(words, person)

  const remote = home !== undefined
  // Only the owner and the moderators of a room here moderate it from this page.
  const owning = room.owned && !remote
  const moderating = (room.owned || room.moderator) && !remote
  // A moderator acts on members only; the owner on anybody but themselves.
  const actsOn = (person: Person) =>
    person.user_id !== selfId && (owning || (moderating && person.role === 'member'))
  const inRoom = new Set(members.map((person) => person.user_id))
  const addable = (candidates ?? []).filter((person) => !inRoom.has(person.user_id))

  // Presence is only known for people here; a room elsewhere shows none.
  const presence = usePresenceOf(remote ? [] : [...inRoom, ...addable.map((person) => person.user_id)])
  const dot = (person: Person) => {
    const state = presence.get(person.user_id)
    return state === undefined ? null : <PresenceDot state={state} />
  }

  const expired = useRef(onExpired)
  expired.current = onExpired

  function fail(error: unknown, fallback: string) {
    if (error instanceof ApiError && error.unauthenticated) {
      onExpired()
      return
    }
    setFailure(refused(words, error, fallback))
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
      fail(error, said.cannotAdd(nameOf(person)))
    } finally {
      setAdding(null)
    }
  }

  /*
  Who is banned is read only for the owner and the moderators, the people Convia
  tells, and read again after every ban and every lifted one.
  */
  useEffect(() => {
    if (!moderating) {
      return
    }
    const controller = new AbortController()
    api.bans(room.id, controller.signal).then(
      (page) => {
        if (!controller.signal.aborted) {
          setBans(page.data)
          setBansFailed(false)
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
        setBansFailed(true)
      },
    )
    return () => controller.abort()
  }, [moderating, room.id, bansRead])

  // moderate runs one act of moderation on somebody, then reads what changed.
  async function moderate(person: Person, act: () => Promise<void>, fallback: string) {
    setActing(person.user_id)
    setFailure(null)
    try {
      await act()
      onChanged()
      setBansRead((count) => count + 1)
    } catch (error) {
      fail(error, fallback)
    } finally {
      setActing(null)
    }
  }

  // handOver makes somebody else the owner, then reads the room and its people again.
  async function handOver(person: Person) {
    setActing(person.user_id)
    setFailure(null)
    try {
      await api.handOver(room.id, person.user_id)
      setHanding(null)
      onChanged()
      onRoomChanged()
    } catch (error) {
      fail(error, said.handOverFailed(nameOf(person)))
    } finally {
      setActing(null)
    }
  }

  async function leave() {
    setLeaving(true)
    setFailure(null)
    try {
      await onLeave()
    } catch (error) {
      if (remote && error instanceof ApiError && error.status === 503) {
        setFailure(said.notConfirmed)
        setStranded(true)
      } else {
        fail(error, said.noLongerIn)
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
      fail(error, said.forgetFailed)
      setLeaving(false)
    }
  }

  return (
    <aside
      id={id}
      aria-label={said.label(room.name)}
      className="flex max-h-[40vh] min-h-0 flex-col gap-5 overflow-y-auto border-t border-line
        bg-surface-deep p-4 md:max-h-none md:w-64 md:flex-none md:border-t-0 md:border-l"
    >
      {remote && (
        <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">{said.livesOn(hostOf(home))}</p>
      )}

      <section>
        <h3 className={heading}>{said.inRoom}</h3>
        {membersFailed ? (
          <p className="m-0 text-[0.8rem] text-ink-faint">{said.membersUnreadable}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {members.map((person) => (
              <li key={person.user_id} className="flex flex-wrap items-center gap-x-2 text-[0.88rem]">
                {dot(person)}
                <span className="min-w-0 flex-1 truncate">
                  {nameOf(person)}
                  {person.role === 'owner' && <span className="text-ink-faint"> · {said.owner}</span>}
                  {person.role === 'moderator' && <span className="text-ink-faint"> · {said.moderator}</span>}
                  {person.user_id === selfId && <span className="text-ink-faint"> {said.youTag}</span>}
                </span>
                {actsOn(person) && (
                  <span className="flex flex-none flex-wrap gap-0.5">
                    <button
                      type="button"
                      className={moderation}
                      aria-label={said.remove(nameOf(person))}
                      disabled={acting !== null}
                      onClick={() =>
                        void moderate(
                          person,
                          () => api.removeMember(room.id, person.user_id),
                          said.removeFailed(nameOf(person)),
                        )
                      }
                    >
                      {words.common.remove}
                    </button>
                    <button
                      type="button"
                      className={moderation}
                      aria-label={said.banNamed(nameOf(person))}
                      disabled={acting !== null}
                      onClick={() =>
                        void moderate(
                          person,
                          () => api.ban(room.id, person.user_id),
                          said.banFailed(nameOf(person)),
                        )
                      }
                    >
                      {said.ban}
                    </button>
                    {owning && (
                      <>
                        <button
                          type="button"
                          className={moderation}
                          aria-label={
                            person.role === 'moderator'
                              ? said.unnameModeratorNamed(nameOf(person))
                              : said.nameModeratorNamed(nameOf(person))
                          }
                          disabled={acting !== null}
                          onClick={() =>
                            void moderate(
                              person,
                              () =>
                                person.role === 'moderator'
                                  ? api.unnameModerator(room.id, person.user_id)
                                  : api.nameModerator(room.id, person.user_id),
                              said.roleFailed(nameOf(person)),
                            )
                          }
                        >
                          {person.role === 'moderator' ? said.unnameModerator : said.nameModerator}
                        </button>
                        <button
                          type="button"
                          className={moderation}
                          aria-label={said.handOverNamed(nameOf(person))}
                          disabled={acting !== null}
                          onClick={() => setHanding(person)}
                        >
                          {said.handOver}
                        </button>
                      </>
                    )}
                  </span>
                )}
                {handing?.user_id === person.user_id && (
                  <div className="mt-1 flex basis-full flex-col gap-2">
                    <p className="m-0 text-[0.8rem] leading-relaxed">{said.confirmHandOver(nameOf(person), room.name)}</p>
                    <div className="flex gap-2">
                      <Button tone="primary" size="small" disabled={acting !== null} onClick={() => void handOver(person)}>
                        {acting === person.user_id ? said.handingOver : said.handOverConfirm}
                      </Button>
                      <Button size="small" disabled={acting !== null} onClick={() => setHanding(null)}>
                        {said.keepOwning}
                      </Button>
                    </div>
                  </div>
                )}
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
            <Chevron />
            <h3 className={headingText}>{said.addHeading}</h3>
            {addable.length > 0 && <span className="text-[0.72rem] text-ink-faint">{addable.length}</span>}
          </summary>
          {candidatesFailed ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">{said.candidatesUnreadable}</p>
          ) : candidates === null ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">{words.common.loading}</p>
          ) : addable.length === 0 ? (
            <p className="m-0 text-[0.8rem] leading-relaxed text-ink-faint">
              {candidates.length === 0 ? said.nobodyShared : said.everybodyHere}
            </p>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
              {addable.map((person) => (
                <li key={person.user_id} className="flex items-center gap-2">
                  {dot(person)}
                  <span className="min-w-0 flex-1 truncate text-[0.88rem]">{nameOf(person)}</span>
                  <Button
                    size="small"
                    aria-label={said.addNamed(nameOf(person))}
                    disabled={adding !== null}
                    onClick={() => void add(person)}
                  >
                    {adding === person.user_id ? said.adding : said.add}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-2 mb-0 text-[0.72rem] leading-relaxed text-ink-faint">{said.addHint}</p>
        </details>
      )}

      {moderating && (
        <details className="group">
          <summary
            className="mb-2 flex cursor-pointer list-none items-center gap-1.5 rounded-sm
              [&::-webkit-details-marker]:hidden"
          >
            <Chevron />
            <h3 className={headingText}>{said.banned}</h3>
            {bans !== null && bans.length > 0 && (
              <span className="text-[0.72rem] text-ink-faint">{bans.length}</span>
            )}
          </summary>
          {bansFailed ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">{said.bansUnreadable}</p>
          ) : bans === null ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">{words.common.loading}</p>
          ) : bans.length === 0 ? (
            <p className="m-0 text-[0.8rem] text-ink-faint">{said.nobodyBanned}</p>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
              {bans.map((person) => (
                <li key={person.user_id} className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-[0.88rem]">{nameOf(person)}</span>
                  <Button
                    size="small"
                    aria-label={said.unbanNamed(nameOf(person))}
                    disabled={acting !== null}
                    onClick={() =>
                      void moderate(
                        person,
                        () => api.unban(room.id, person.user_id),
                        said.unbanFailed(nameOf(person)),
                      )
                    }
                  >
                    {said.unban}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-2 mb-0 text-[0.72rem] leading-relaxed text-ink-faint">{said.bansHint}</p>
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
            <p className="m-0 text-[0.78rem] leading-relaxed text-ink-dim">{said.forgetInstead(hostOf(home ?? ''))}</p>
            <Button size="small" className="self-start" disabled={leaving} onClick={() => void forget()}>
              {leaving ? said.forgetting : said.forget}
            </Button>
          </div>
        )}
        {confirming ? (
          <div className="flex flex-col gap-2">
            <p className="m-0 text-[0.85rem]">{said.confirmLeave(room.name)}</p>
            <div className="flex gap-2">
              <Button tone="primary" size="small" disabled={leaving} onClick={() => void leave()}>
                {leaving ? said.leaving : said.leave}
              </Button>
              <Button size="small" disabled={leaving} onClick={() => setConfirming(false)}>
                {said.stay}
              </Button>
            </div>
          </div>
        ) : (
          <Button size="small" onClick={() => setConfirming(true)}>
            {said.leaveRoom}
          </Button>
        )}
      </section>
    </aside>
  )
}
