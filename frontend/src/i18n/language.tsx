import { createContext, useContext, useEffect, useMemo, useState } from "react";

import {
  rememberedLanguage,
  rememberLanguage,
  type LanguageChoice,
} from "../state/preferences";
import { TrayWords } from "../../wailsjs/go/app/Bridge";
import { en, type Words } from "./en";
import { ptBR } from "./pt-BR";

/*
Language is what the interface speaks, and how it writes dates and times.

The two can differ: somebody whose system prefers Portugal's Portuguese reads
the Brazilian words, and their dates the way Portugal writes them.
*/
export interface Language {
  // tag is the language the words are in, for the document's `lang`.
  tag: string;
  words: Words;
  // formatting is the system's own tag that chose the words, for Intl.
  formatting: string;
}

const languages: { tag: "en" | "pt-BR"; primary: string; words: Words }[] = [
  { tag: "en", primary: "en", words: en },
  { tag: "pt-BR", primary: "pt", words: ptBR },
];

/*
languageNames are each language in its own words, which is how somebody looks
for theirs in a list, whatever the page is speaking.
*/
export const languageNames: Record<"en" | "pt-BR", string> = {
  en: "English",
  "pt-BR": "Português (Brasil)",
};

export const english: Language = { tag: "en", words: en, formatting: "en" };

const primaryOf = (tag: string) => tag.split("-", 1)[0]?.toLowerCase();

/*
choose picks the language the interface speaks.

A language the person chose wins. Otherwise it is the first one the system
prefers that the interface speaks, matched on the primary subtag, so a system
asking for pt-PT or en-GB is spoken to in the one Portuguese or English there
is. Dates follow the system's own tag for that language when it has one.
*/
export function choose(
  preferred: readonly string[],
  chosen: LanguageChoice = "system",
): Language {
  const named = languages.find((language) => language.tag === chosen);
  if (named !== undefined) {
    const formatting =
      preferred.find((tag) => primaryOf(tag) === named.primary) ?? named.tag;
    return { tag: named.tag, words: named.words, formatting };
  }

  for (const tag of preferred) {
    const spoken = languages.find(
      (language) => language.primary === primaryOf(tag),
    );
    if (spoken !== undefined) {
      return { tag: spoken.tag, words: spoken.words, formatting: tag };
    }
  }
  return english;
}

/*
The default is English, so a component rendered on its own — in a test — speaks
the language the rest of those tests are written in.
*/
export const LanguageContext = createContext<Language>(english);

export function useLanguage(): Language {
  return useContext(LanguageContext);
}

export function useWords(): Words {
  return useContext(LanguageContext).words;
}

// Choosing is what the settings offer: the choice, and changing it.
export interface Choosing {
  choice: LanguageChoice;
  // system is the language the system alone would pick.
  system: "en" | "pt-BR";
  setChoice: (choice: LanguageChoice) => void;
}

export const ChoosingContext = createContext<Choosing>({
  choice: "system",
  system: "en",
  setChoice: () => {},
});

export function useChoosing(): Choosing {
  return useContext(ChoosingContext);
}

/*
Speaking is the interface speaking the language chosen, and changing it the
moment another is chosen. The document's `lang` follows.
*/
export function Speaking({ children }: { children: React.ReactNode }) {
  const [choice, setChoice] = useState(rememberedLanguage);

  const language = useMemo(() => choose(navigator.languages, choice), [choice]);
  const choosing = useMemo<Choosing>(
    () => ({
      choice,
      system: choose(navigator.languages).tag === "pt-BR" ? "pt-BR" : "en",
      setChoice: (next) => {
        rememberLanguage(next);
        setChoice(next);
      },
    }),
    [choice],
  );

  useEffect(() => {
    document.documentElement.lang = language.tag;
    const { open, autostart, quit } = language.words.tray;
    void TrayWords(open, autostart, quit).catch(() => undefined);
  }, [language]);

  return (
    <ChoosingContext.Provider value={choosing}>
      <LanguageContext.Provider value={language}>
        {children}
      </LanguageContext.Provider>
    </ChoosingContext.Provider>
  );
}
