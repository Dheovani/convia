import { useState } from 'react'

import { api, ApiError, NetworkError } from '../api/client'
import type { Account, Person, SidebarRoom } from '../api/types'
import { Button } from './controls'

interface RoomPeopleProps {
  id: string
  room: SidebarRoom
  account: Account
  members: Person[]
  membersFailed: boolean
  onChanged: () => void
  onLeave: () => Promise<void>
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

const heading = 'm-0 mb-2 font-display text-[0.72rem] font-semibold tracking-[0.08em] text-ink-faint uppercase'

/*
RoomPeople is who is in a room, who could be, and the way out.

**Who could be added comes from Convia and from nowhere else.** There is no field
to type an address or an identifier into, because on this surface a person names
only somebody they already share a room with. The list is the whole of discovery,
and a text box would be an invitation to guess at who exists.

**There is no way to remove somebody.** Convia offers none to a person, because
membership carries no role for that power to rest on, and a button that could
only ever fail would be worse than no button.

Leaving asks first. It is the one act here a person cannot undo by themselves:
getting back in needs somebody still inside.
*/
export function RoomPeople({
  id,
  room,
  account,
  members,
  membersFailed,
  onChanged,
  onLeave,
  onExpired,
}: RoomPeopleProps) {
  const [candidates, setCandidates] = useState<Person[] | null>(null)
  const [looking, setLooking] = useState(false)
  const [adding, setAdding] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [leaving, setLeaving] = useState(false)

  const inRoom = new Set(members.map((person) => person.user_id))
  const addable = (candidates ?? []).filter((person) => !inRoom.has(person.user_id))

  function fail(error: unknown, fallback: string) {
    if (error instanceof ApiError && error.unauthenticated) {
      onExpired()
      return
    }
    setFailure(explain(error, fallback))
  }

  async function look() {
    setLooking(true)
    setFailure(null)
    try {
      setCandidates((await api.people()).data)
    } catch (error) {
      fail(error, 'The people you could add could not be read.')
    } finally {
      setLooking(false)
    }
  }

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
      fail(error, 'You are no longer in this room.')
      setLeaving(false)
      setConfirming(false)
    }
  }

  return (
    <aside
      id={id}
      aria-label={`People in ${room.name}`}
      className="flex max-h-[40vh] min-h-0 flex-col gap-5 overflow-y-auto border-t border-line
        bg-surface-deep p-4 md:max-h-none md:w-64 md:flex-none md:border-t-0 md:border-l"
    >
      <section>
        <h3 className={heading}>In this room</h3>
        {membersFailed ? (
          <p className="m-0 text-[0.8rem] text-ink-faint">Who is here could not be read.</p>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {members.map((person) => (
              <li key={person.user_id} className="truncate text-[0.88rem]">
                {label(person)}
                {person.user_id === account.user_id && (
                  <span className="text-ink-faint"> (you)</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h3 className={heading}>Add somebody</h3>
        {candidates === null ? (
          <Button size="small" disabled={looking} onClick={() => void look()}>
            {looking ? 'Looking…' : 'Show people you can add'}
          </Button>
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
      </section>

      {failure !== null && (
        <p className="m-0 text-[0.8rem] text-danger" role="alert">
          {failure}
        </p>
      )}

      <section className="mt-auto">
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
