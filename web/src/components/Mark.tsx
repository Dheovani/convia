import { useWords } from '../i18n/language'

/*
Convia's mark: an open ring and a point at the opening.

The ring is a conversation that is not closed, and the point is somebody about
to join it. It is drawn rather than imported so that it inherits the current
colour and needs no network request — the policy this page is served under
forbids third-party origins, and a mark is the last thing that should depend on
one.
*/
export function Mark({ size = 32 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M 23.07 23.07 A 10 10 0 1 1 23.07 8.93"
        stroke="currentColor"
        strokeWidth="3.5"
        strokeLinecap="round"
      />
      <circle cx="26" cy="16" r="3" fill="var(--color-accent)" />
    </svg>
  )
}

// Wordmark pairs the mark with the name, for the places that are the product
// speaking rather than a control.
export function Wordmark() {
  const words = useWords()

  return (
    <span className="inline-flex items-center gap-2 text-ink">
      <Mark size={28} />
      <span className="font-display text-[1.2rem] font-semibold tracking-[-0.01em]">
        {words.brand}
      </span>
    </span>
  )
}
