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

On a narrow screen it is a bar along the bottom, where a thumb reaches it, and
the mark gives up its place.
*/
const destinations: Mode[] = ['chat', 'calls', 'settings']

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
              className="min-h-11 w-full cursor-pointer rounded-md px-1 py-2 text-[0.72rem] font-medium
                text-ink-dim transition-colors enabled:hover:bg-surface-hover
                enabled:hover:text-ink aria-[current=page]:bg-accent-soft
                aria-[current=page]:text-accent-ink"
              aria-current={mode === destination ? 'page' : undefined}
              onClick={() => onMode(destination)}
            >
              {words.rail[destination]}
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
