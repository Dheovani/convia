import { useEffect, useState } from 'react'

// narrowQuery is Tailwind's `md` breakpoint turned around: below it, one zone at a time.
const narrowQuery = '(max-width: 47.99rem)'

function matches(): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia(narrowQuery).matches
}

/*
useNarrow reports whether the screen is too narrow for the zones side by side.

It is decided in the script rather than only in the stylesheet, because what
changes on a narrow screen is not how a zone looks but whether it is there: the
zone that is not shown is not rendered, so it can be neither reached by the
keyboard nor mistaken for present by anything reading the page. A browser without
matchMedia is wide.
*/
export function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(matches)

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      return
    }
    const query = window.matchMedia(narrowQuery)
    const changed = () => setNarrow(query.matches)
    changed()
    query.addEventListener('change', changed)
    return () => query.removeEventListener('change', changed)
  }, [])

  return narrow
}
