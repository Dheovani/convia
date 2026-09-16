import type { Choice } from '../media/connection'

// key is where this browser keeps what a person chose for calls.
const key = 'convia.call'

/*
defaultChoice is what somebody who never chose gets: speaking and unseen, on the
system's devices.
*/
export const defaultChoice: Choice = {
  microphone: true,
  camera: false,
  audioInput: '',
  videoInput: '',
  audioOutput: '',
}

/*
remembered reads what this person chose in this browser.

It is kept in the browser rather than the account on purpose: a device identifier
means something only to the browser that saw the device, so carrying it to
another computer would choose nothing there. Anything unreadable — storage that is
blocked, a value from an older build — is the default rather than an error.
*/
export function remembered(): Choice {
  try {
    const stored: unknown = JSON.parse(window.localStorage.getItem(key) ?? 'null')
    if (typeof stored !== 'object' || stored === null) {
      return defaultChoice
    }

    const read = stored as Record<string, unknown>
    const text = (value: unknown) => (typeof value === 'string' ? value : '')

    return {
      microphone: read['microphone'] !== false,
      camera: read['camera'] === true,
      audioInput: text(read['audioInput']),
      videoInput: text(read['videoInput']),
      audioOutput: text(read['audioOutput']),
    }
  } catch {
    return defaultChoice
  }
}

// remember keeps a choice for the next call. Storage that refuses is not worth a failure.
export function remember(choice: Choice): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(choice))
  } catch {
    // The choice still holds for this page.
  }
}

/*
How the page looks and which language it speaks are kept in this browser too, for
the same reason as the devices and for one of their own: the choice is made
before signing in as much as after, and the sign-in page has no account to read
it from.
*/
export type Theme = 'system' | 'dark' | 'light'
export type LanguageChoice = 'browser' | 'en' | 'pt-BR'

const themeKey = 'convia.theme'
const languageKey = 'convia.language'

function read<T extends string>(storage: string, allowed: readonly T[], fallback: T): T {
  try {
    const stored = window.localStorage.getItem(storage)
    return allowed.find((value) => value === stored) ?? fallback
  } catch {
    return fallback
  }
}

function write(storage: string, value: string): void {
  try {
    window.localStorage.setItem(storage, value)
  } catch {
    // The choice still holds for this page.
  }
}

export const themes: readonly Theme[] = ['system', 'dark', 'light']
export const languageChoices: readonly LanguageChoice[] = ['browser', 'en', 'pt-BR']

export function rememberedTheme(): Theme {
  return read(themeKey, themes, 'system')
}

/*
applyTheme puts a theme on the page. The stylesheet follows the system unless
the page names one, so following the system is naming none.
*/
export function applyTheme(theme: Theme): void {
  if (theme === 'system') {
    delete document.documentElement.dataset['theme']
  } else {
    document.documentElement.dataset['theme'] = theme
  }
}

export function rememberTheme(theme: Theme): void {
  write(themeKey, theme)
}

export function rememberedLanguage(): LanguageChoice {
  return read(languageKey, languageChoices, 'browser')
}

export function rememberLanguage(choice: LanguageChoice): void {
  write(languageKey, choice)
}
