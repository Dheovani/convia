import { describe, expect, it } from 'vitest'

import theme from './theme.css?raw'

/*
The palette is held to WCAG 2.2 AA, which the product owner set for this
interface, and this is what holds it there: every colour text is drawn in has to
read at 4.5:1 on every surface it can sit on, in both palettes, and the accent has
to stand out at 3:1 where it marks something without words.

It reads the stylesheet itself, so a token changed there is checked the moment it
is changed, rather than on the day somebody squints at it.
*/

const light = '@media (prefers-color-scheme: light)'

function tokens(css: string): Map<string, string> {
  const found = new Map<string, string>()
  for (const match of css.matchAll(/--color-([a-z-]+):\s*#([0-9a-f]{6})\b/gi)) {
    const [, name, value] = match
    if (name !== undefined && value !== undefined) {
      found.set(name, value)
    }
  }
  return found
}

const [darkCss = '', lightCss = ''] = theme.split(light)
const palettes = {
  dark: tokens(darkCss),
  light: new Map([...tokens(darkCss), ...tokens(lightCss)]),
}

function luminance(hex: string): number {
  const [r = 0, g = 0, b = 0] = (hex.match(/../g) ?? []).map((pair) => {
    const channel = parseInt(pair, 16) / 255
    return channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(one: string, other: string): number {
  const [lighter, darker] = [luminance(one), luminance(other)].sort((a, b) => b - a)
  return ((lighter ?? 0) + 0.05) / ((darker ?? 0) + 0.05)
}

const surfaces = ['surface-sunken', 'surface-deep', 'surface', 'surface-raised', 'surface-hover']
const inks = ['ink', 'ink-dim', 'ink-faint', 'accent-ink', 'danger', 'positive']

describe.each(Object.entries(palettes))('the %s palette', (_, palette) => {
  function colour(name: string): string {
    const value = palette.get(name)
    expect(value, `--color-${name} is defined`).toBeDefined()
    return value ?? ''
  }

  it.each(inks)('draws %s text readably on every surface', (ink) => {
    for (const surface of surfaces) {
      expect(contrast(colour(ink), colour(surface)), `${ink} on ${surface}`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('draws text readably on the accent, at rest and when pointed at', () => {
    for (const fill of ['accent', 'accent-strong']) {
      expect(contrast(colour('on-accent'), colour(fill)), `on-accent on ${fill}`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('makes the accent stand out where it marks something without words', () => {
    for (const surface of surfaces) {
      expect(contrast(colour('accent'), colour(surface)), `accent on ${surface}`).toBeGreaterThanOrEqual(3)
    }
  })
})
