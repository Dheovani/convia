import { describe, expect, it } from 'vitest'

import { rememberedLanguage, rememberedTheme } from '../state/preferences'
import { en } from './en'
import { choose } from './language'
import { plural } from './plural'
import { ptBR } from './pt-BR'

describe('the language the page speaks', () => {
  it('is the first one the browser prefers that the interface knows', () => {
    expect(choose(['fr-FR', 'pt-BR', 'en-US'])).toMatchObject({ tag: 'pt-BR', formatting: 'pt-BR' })
    expect(choose(['en-GB', 'pt-BR'])).toMatchObject({ tag: 'en', formatting: 'en-GB' })
  })

  // Portugal's Portuguese reads the Brazilian words, and keeps its own dates.
  it('matches by language rather than by country', () => {
    const language = choose(['pt-PT'])
    expect(language.tag).toBe('pt-BR')
    expect(language.words).toBe(ptBR)
    expect(language.formatting).toBe('pt-PT')
  })

  it('is the one the person chose, with dates the browser writes that language in', () => {
    expect(choose(['pt-PT', 'en-GB'], 'en')).toMatchObject({ tag: 'en', formatting: 'en-GB' })
    expect(choose(['en-US'], 'pt-BR')).toMatchObject({ tag: 'pt-BR', words: ptBR, formatting: 'pt-BR' })
    expect(choose(['pt-PT'], 'browser')).toMatchObject({ tag: 'pt-BR', formatting: 'pt-PT' })
  })

  it('forgets a choice this browser kept that it cannot read', () => {
    window.localStorage.setItem('convia.language', 'klingon')
    expect(rememberedLanguage()).toBe('browser')
    window.localStorage.setItem('convia.theme', 'sepia')
    expect(rememberedTheme()).toBe('system')
    window.localStorage.clear()
  })

  it('is English when the browser prefers nothing the interface knows', () => {
    expect(choose(['ja-JP'])).toMatchObject({ tag: 'en', words: en })
    expect(choose([])).toMatchObject({ tag: 'en', words: en })
  })
})

describe('counting', () => {
  it('follows each language’s own rules', () => {
    const forms = { one: 'one', other: 'other' }
    expect(plural('en', 0, forms)).toBe('other')
    expect(plural('en', 1, forms)).toBe('one')
    // Portuguese counts zero as one.
    expect(plural('pt-BR', 0, forms)).toBe('one')
    expect(plural('pt-BR', 2, forms)).toBe('other')
  })

  it('reads well in both languages', () => {
    expect(en.sidebar.unread(1)).toBe('1 unread message')
    expect(en.sidebar.unread(3)).toBe('3 unread messages')
    expect(ptBR.sidebar.unread(1)).toBe('1 mensagem não lida')
    expect(ptBR.sidebar.unread(3)).toBe('3 mensagens não lidas')
  })
})

// sameWords lists every phrase that is identical in two languages, by where it is.
function sameWords(one: unknown, other: unknown, at = ''): string[] {
  if (typeof one === 'string') {
    return one === other ? [at] : []
  }
  if (typeof one === 'object' && one !== null && typeof other === 'object' && other !== null) {
    return Object.entries(one).flatMap(([key, inner]) =>
      sameWords(inner, (other as Record<string, unknown>)[key], at === '' ? key : `${at}.${key}`),
    )
  }
  return []
}

describe('the Portuguese catalogue', () => {
  /*
  The compiler holds that every phrase is there. This holds that each was
  translated rather than copied: a phrase left in English is the same in both.
  */
  it('translates every phrase that is not a name or a number', () => {
    expect(sameWords(en, ptBR)).toEqual(['brand', 'sidebar.manyUnread'])
  })

  it('agrees with the device it names', () => {
    expect(ptBR.call.refused('busy', 'audioinput', false)).toBe('Seu microfone está sendo usado por outro aplicativo.')
    expect(ptBR.call.refused('busy', 'videoinput', false)).toBe('Sua câmera está sendo usada por outro aplicativo.')
    expect(ptBR.call.replaced('videoinput')).toBe(
      'Sua câmera foi desconectada, então a chamada está usando o padrão do sistema.',
    )
  })
})
