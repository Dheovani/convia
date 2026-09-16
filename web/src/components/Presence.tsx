import type { PresenceState } from '../api/types'
import { useWords } from '../i18n/language'

/*
PresenceDot shows whether somebody is available, and says it in words for
whoever cannot see the colour: available is filled, busy is the danger colour,
away is a ring, and offline is a faint ring.
*/
const looks: Record<PresenceState, string> = {
  online: 'bg-positive',
  busy: 'bg-danger',
  away: 'border-2 border-ink-dim bg-transparent',
  offline: 'border border-ink-faint bg-transparent',
}

export function PresenceDot({ state, className = '' }: { state: PresenceState; className?: string }) {
  const said = useWords().presence
  return (
    <span className={`inline-flex flex-none items-center ${className}`}>
      <span aria-hidden="true" title={said[state]} className={`inline-block size-2.5 rounded-full ${looks[state]}`} />
      <span className="sr-only">{said[state]}</span>
    </span>
  )
}
