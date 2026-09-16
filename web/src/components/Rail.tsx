import { useId, useRef, useState } from 'react'

import type { PresenceState } from '../api/types'
import { useWords } from '../i18n/language'
import { statuses, type Status } from '../state/preferences'
import { Mark } from './Mark'
import { PresenceDot } from './Presence'

export type Mode = 'chat' | 'calls' | 'settings'

interface RailProps {
  mode: Mode
  onMode: (mode: Mode) => void
  displayName: string
  // status is what the person chose; said is what their page says, which is away when it went idle.
  status: Status
  said: PresenceState
  onStatus: (status: Status) => void
  onSignOut: () => void
  // narrow lays the rail out as a bar along the bottom of the screen.
  narrow?: boolean
}

/*
The rail is the outermost of the three zones: which part of Convia you are in.

The destinations are icons, named for a screen reader and in a tooltip. Words do
not fit: the rail is as narrow as an icon, and a destination's name in another
language can be twice as long as in English.

On a narrow screen it is a bar along the bottom, where a thumb reaches it, and
the mark gives up its place.
*/
const destinations: Mode[] = ['chat', 'calls', 'settings']

const iconProps = {
  'aria-hidden': true,
  viewBox: '0 0 24 24',
  className: 'size-5',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.75,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const

const icons: Record<Mode, React.ReactNode> = {
  chat: (
    <svg {...iconProps}>
      <path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9a1.5 1.5 0 0 1-1.5 1.5H10l-4.5 4v-4h0A1.5 1.5 0 0 1 4 14.5z" />
    </svg>
  ),
  calls: (
    <svg {...iconProps}>
      <path d="M6.6 3.5h2.1l1.5 4-2 1.3a11 11 0 0 0 7 7l1.3-2 4 1.5v2.1a2 2 0 0 1-2.2 2A16.5 16.5 0 0 1 4.6 5.7a2 2 0 0 1 2-2.2z" />
    </svg>
  ),
  settings: (
    <svg {...iconProps}>
      <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="17" r="2" />
    </svg>
  ),
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) {
    return '?'
  }
  const first = parts[0]?.[0] ?? ''
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : ''
  return (first + last).toUpperCase()
}

/*
StatusMenu is the person's avatar, which says their status and changes it. The
choices open above it on a narrow screen, where the rail is at the bottom, and
beside it otherwise; Escape closes them and gives the keyboard back.
*/
function StatusMenu({
  displayName,
  status,
  said,
  onStatus,
  narrow,
}: {
  displayName: string
  status: Status
  said: PresenceState
  onStatus: (status: Status) => void
  narrow: boolean
}) {
  const words = useWords()
  const [open, setOpen] = useState(false)
  const menu = useId()
  const avatar = useRef<HTMLButtonElement>(null)

  function close() {
    setOpen(false)
    avatar.current?.focus()
  }

  return (
    <div
      className="relative"
      onKeyDown={(event) => {
        if (event.key === 'Escape' && open) {
          close()
        }
      }}
    >
      <button
        ref={avatar}
        type="button"
        className="relative grid size-9 cursor-pointer place-items-center rounded-full bg-accent-soft text-xs
          font-semibold text-accent-ink"
        aria-label={words.presence.current(words.presence[said])}
        title={words.presence.current(words.presence[said])}
        aria-expanded={open}
        aria-controls={menu}
        onClick={() => (open ? close() : setOpen(true))}
      >
        <span aria-hidden="true">{initials(displayName)}</span>
        <span className="absolute -right-0.5 -bottom-0.5 grid rounded-full bg-surface-sunken p-0.5" aria-hidden="true">
          <PresenceDot state={said} />
        </span>
      </button>
      {open && (
        <div
          id={menu}
          role="group"
          aria-label={words.presence.yours}
          className={`absolute z-20 flex w-48 flex-col gap-1 rounded-md border border-line bg-surface-raised p-2
            shadow-raised ${narrow ? 'right-0 bottom-11' : 'bottom-0 left-11'}`}
        >
          {statuses.map((each) => (
            <button
              key={each}
              type="button"
              aria-pressed={each === status}
              className="flex min-h-8 cursor-pointer items-center gap-2 rounded-sm px-2 text-left text-[0.82rem]
                hover:bg-surface-hover aria-pressed:bg-accent-soft"
              onClick={() => {
                onStatus(each)
                close()
              }}
            >
              <PresenceDot state={each} />
              <span aria-hidden="true">{words.presence[each]}</span>
            </button>
          ))}
          <p className="m-0 px-2 pt-1 text-[0.7rem] leading-snug text-ink-faint">{words.presence.idle}</p>
        </div>
      )}
    </div>
  )
}

export function Rail({
  mode,
  onMode,
  displayName,
  status,
  said,
  onStatus,
  onSignOut,
  narrow = false,
}: RailProps) {
  const words = useWords()

  return (
    <nav
      className={
        narrow
          ? 'row-start-2 flex items-center gap-2 border-t border-line bg-surface-sunken px-2 py-1'
          : 'flex flex-col items-center gap-3 border-r border-line bg-surface-sunken px-2 py-3'
      }
      aria-label={words.brand}
    >
      {!narrow && (
        <div className="grid size-11 place-items-center text-ink" title={words.brand}>
          <Mark size={30} />
        </div>
      )}

      <ul className={`m-0 flex list-none gap-1 p-0 ${narrow ? 'flex-1 flex-row' : 'w-full flex-col'}`}>
        {destinations.map((destination) => (
          <li key={destination} className={narrow ? 'flex-1' : undefined}>
            <button
              type="button"
              className="grid min-h-11 w-full cursor-pointer place-items-center rounded-md px-1 py-2
                text-ink-dim transition-colors hover:bg-surface-hover hover:text-ink
                aria-[current=page]:bg-accent-soft aria-[current=page]:text-accent-ink"
              aria-current={mode === destination ? 'page' : undefined}
              aria-label={words.rail[destination]}
              title={words.rail[destination]}
              onClick={() => onMode(destination)}
            >
              {icons[destination]}
            </button>
          </li>
        ))}
      </ul>

      <div className={narrow ? 'flex items-center gap-1' : 'mt-auto flex flex-col items-center gap-2'}>
        <StatusMenu displayName={displayName} status={status} said={said} onStatus={onStatus} narrow={narrow} />
        <button
          type="button"
          className="min-h-6 cursor-pointer rounded-sm px-1.5 py-1 text-[0.68rem] text-ink-faint hover:text-ink"
          onClick={onSignOut}
        >
          {words.rail.signOut}
        </button>
      </div>
    </nav>
  )
}
