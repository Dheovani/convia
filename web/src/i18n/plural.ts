/*
plural picks the form of a phrase a count needs, by the language's own rules.

Rules differ in ways a ternary cannot hold: English has one and other, Portuguese
counts zero as one, and other languages have few and many. The forms a language
does not use are left out, and `other` is the one every language has.
*/
export type Forms = Partial<Record<Intl.LDMLPluralRule, string>> & { other: string }

const rules = new Map<string, Intl.PluralRules>()

export function plural(language: string, count: number, forms: Forms): string {
  let rule = rules.get(language)
  if (rule === undefined) {
    rule = new Intl.PluralRules(language)
    rules.set(language, rule)
  }
  return forms[rule.select(count)] ?? forms.other
}
