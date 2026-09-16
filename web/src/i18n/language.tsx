import { createContext, useContext } from 'react'

import { ApiError, NetworkError } from '../api/client'
import { en, type Words } from './en'
import { ptBR } from './pt-BR'

/*
Language is what the interface speaks, and how it writes dates and times.

The two can differ: somebody whose browser prefers Portugal's Portuguese reads
the Brazilian words, and their dates the way Portugal writes them.
*/
export interface Language {
  // tag is the language the words are in, for the page's `lang`.
  tag: string
  words: Words
  // formatting is the browser's own tag that chose the words, for Intl.
  formatting: string
}

const languages: { tag: string; primary: string; words: Words }[] = [
  { tag: 'en', primary: 'en', words: en },
  { tag: 'pt-BR', primary: 'pt', words: ptBR },
]

export const english: Language = { tag: 'en', words: en, formatting: 'en' }

/*
choose picks the first language the browser prefers that the interface speaks.

It matches on the primary subtag, so a browser asking for pt-PT or en-GB is
spoken to in the one Portuguese or English there is. Nothing is remembered yet:
the choice is the browser's until there are settings to override it.
*/
export function choose(preferred: readonly string[]): Language {
  for (const tag of preferred) {
    const primary = tag.split('-', 1)[0]?.toLowerCase()
    const spoken = languages.find((language) => language.primary === primary)
    if (spoken !== undefined) {
      return { tag: spoken.tag, words: spoken.words, formatting: tag }
    }
  }
  return english
}

/*
The default is English, so a component rendered on its own — in a test — speaks
the language the rest of those tests are written in.
*/
export const LanguageContext = createContext<Language>(english)

export function useLanguage(): Language {
  return useContext(LanguageContext)
}

export function useWords(): Words {
  return useContext(LanguageContext).words
}

/*
refused says what a failure means when nothing more specific does.

It reads the code, which Convia keeps stable, and never the message, which is
English prose that may be reworded. A code with no words of its own falls back
to what the caller would say anyway.
*/
export function refused(words: Words, error: unknown, fallback: string): string {
  if (error instanceof NetworkError) {
    return words.common.unreachable
  }
  if (error instanceof ApiError) {
    return words.refusals[error.code] ?? fallback
  }
  return fallback
}
