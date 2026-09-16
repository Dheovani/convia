import { en, type Words } from '../i18n/en'
import type { Language } from '../i18n/language'

/*
A language in which every word the catalogue gives is marked.

A test that renders the interface in it can tell a string that came through the
catalogue from one written straight into a component: the first is marked, and
the second is English that a translation would never reach.
*/
const opens = '⟦'
const closes = '⟧'

function mark(value: unknown): unknown {
  if (typeof value === 'string') {
    return `${opens}${value}${closes}`
  }
  if (typeof value === 'function') {
    return (...args: unknown[]) => `${opens}${(value as (...args: unknown[]) => string)(...args)}${closes}`
  }
  if (typeof value === 'object' && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, inner]) => [key, mark(inner)]))
  }
  return value
}

export const marked = mark(en) as Words

export const markedLanguage: Language = { tag: 'en', words: marked, formatting: 'en' }

const letter = /\p{L}/u

/*
unmarked lists the words on screen, and in the names and hints a screen reader
reads, that did not come through the catalogue.

What people wrote — names, messages, device labels — is not the catalogue's, so
the test names it in `data`. Anything without a letter, such as a count or a
time, says nothing a translation would change.
*/
export function unmarked(root: HTMLElement, data: readonly (string | RegExp)[]): string[] {
  const known = (text: string) =>
    !letter.test(text) ||
    (text.startsWith(opens) && text.endsWith(closes)) ||
    data.some((allowed) => (typeof allowed === 'string' ? allowed === text : allowed.test(text)))

  const found = new Set<string>()
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
    const text = node.textContent?.trim() ?? ''
    if (!known(text)) {
      found.add(text)
    }
  }
  for (const element of root.querySelectorAll('*')) {
    for (const name of ['aria-label', 'title', 'placeholder', 'alt']) {
      const text = element.getAttribute(name)?.trim()
      if (text !== undefined && !known(text)) {
        found.add(`${name}="${text}"`)
      }
    }
  }
  return [...found]
}
