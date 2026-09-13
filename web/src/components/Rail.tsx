import { Mark } from './Mark'

export type Mode = 'chat' | 'calls' | 'settings'

interface RailProps {
  mode: Mode
  onMode: (mode: Mode) => void
  displayName: string
  // handle is how this person is named to somebody else, shown on the avatar.
  handle: string
  onSignOut: () => void
}

/*
The rail is the outermost of the three zones: which part of Convia you are in.

Calls and settings are present and disabled rather than absent. An interface
that grows new top-level destinations as they are built teaches people that its
shape is unreliable; one that shows where they will be, greyed, teaches them
where to look later. Each says what it is waiting for.
*/
const destinations: { id: Mode; label: string; ready: boolean; waiting?: string }[] = [
  { id: 'chat', label: 'Chat', ready: true },
  { id: 'calls', label: 'Calls', ready: false, waiting: 'Calls arrive with M18-004.' },
  { id: 'settings', label: 'Settings', ready: false, waiting: 'Settings arrive later in M18.' },
]

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) {
    return '?'
  }
  const first = parts[0]?.[0] ?? ''
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : ''
  return (first + last).toUpperCase()
}

export function Rail({ mode, onMode, displayName, handle, onSignOut }: RailProps) {
  return (
    <nav
      className="row-span-2 flex flex-col items-center gap-3 border-r border-line
        bg-surface-sunken px-2 py-3 md:row-span-1"
      aria-label="Convia"
    >
      <div className="grid size-11 place-items-center text-ink" title="Convia">
        <Mark size={30} />
      </div>

      <ul className="m-0 flex w-full list-none flex-col gap-1 p-0">
        {destinations.map((destination) => (
          <li key={destination.id}>
            <button
              type="button"
              className="w-full cursor-pointer rounded-md px-1 py-2 text-[0.72rem] font-medium
                text-ink-dim transition-colors enabled:hover:bg-surface-hover
                enabled:hover:text-ink aria-[current=page]:bg-accent-soft
                aria-[current=page]:text-accent disabled:cursor-default disabled:opacity-40"
              aria-current={mode === destination.id ? 'page' : undefined}
              disabled={!destination.ready}
              title={destination.ready ? destination.label : destination.waiting}
              onClick={() => onMode(destination.id)}
            >
              {destination.label}
            </button>
          </li>
        ))}
      </ul>

      <div className="mt-auto flex flex-col items-center gap-2">
        <span
          className="grid size-9 place-items-center rounded-full bg-accent-soft text-xs
            font-semibold text-accent"
          title={handle}
          aria-hidden="true"
        >
          {initials(displayName)}
        </span>
        <button
          type="button"
          className="cursor-pointer rounded-sm p-1 text-[0.68rem] text-ink-faint hover:text-ink"
          onClick={onSignOut}
        >
          Sign out
        </button>
      </div>
    </nav>
  )
}
