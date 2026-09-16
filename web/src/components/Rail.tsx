import { useWords } from '../i18n/language'
import { Mark } from './Mark'

export type Mode = 'chat' | 'calls' | 'settings'

interface RailProps {
  mode: Mode
  onMode: (mode: Mode) => void
  displayName: string
  // handle is how this person is named to somebody else, shown on the avatar.
  handle: string
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

export function Rail({ mode, onMode, displayName, handle, onSignOut, narrow = false }: RailProps) {
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
        <span
          className="grid size-9 place-items-center rounded-full bg-accent-soft text-xs
            font-semibold text-accent-ink"
          title={handle}
          aria-hidden="true"
        >
          {initials(displayName)}
        </span>
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
