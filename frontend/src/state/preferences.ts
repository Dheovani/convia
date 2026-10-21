/*
How the window looks and which language it speaks, as this person chose them.

They are kept in the window's own storage, because they are preferences of this
interface and nothing else: no other peer ever needs them. Anything unreadable
there -- storage that is blocked, a value from an older build -- is the default
rather than an error.
*/
export type Theme = "system" | "dark" | "light";
export type LanguageChoice = "system" | "en" | "pt-BR";

const themeKey = "convia.theme";
const languageKey = "convia.language";

function read<T extends string>(
  storage: string,
  allowed: readonly T[],
  fallback: T,
): T {
  try {
    const stored = window.localStorage.getItem(storage);
    return allowed.find((value) => value === stored) ?? fallback;
  } catch {
    return fallback;
  }
}

function write(storage: string, value: string): void {
  try {
    window.localStorage.setItem(storage, value);
  } catch {
    // The choice still holds while the window is open.
  }
}

export const themes: readonly Theme[] = ["system", "dark", "light"];
export const languageChoices: readonly LanguageChoice[] = [
  "system",
  "en",
  "pt-BR",
];

export function rememberedTheme(): Theme {
  return read(themeKey, themes, "system");
}

/*
applyTheme puts a theme on the window. The stylesheet follows the system unless
the document names one, so following the system is naming none.
*/
export function applyTheme(theme: Theme): void {
  if (theme === "system") {
    delete document.documentElement.dataset["theme"];
  } else {
    document.documentElement.dataset["theme"] = theme;
  }
}

export function rememberTheme(theme: Theme): void {
  write(themeKey, theme);
}

export function rememberedLanguage(): LanguageChoice {
  return read(languageKey, languageChoices, "system");
}

export function rememberLanguage(choice: LanguageChoice): void {
  write(languageKey, choice);
}

const notificationTextKey = "convia.notificationText";

// rememberedNotificationText is whether a notification shows what the message says; off unless chosen (ADR 0033).
export function rememberedNotificationText(): boolean {
  return read(notificationTextKey, ["on", "off"] as const, "off") === "on";
}

export function rememberNotificationText(on: boolean): void {
  write(notificationTextKey, on ? "on" : "off");
}

// Device is a kind of device a call uses.
export type Device = "microphone" | "camera" | "speaker";

/*
rememberedDevice is the device of kind this person chose for calls, or "" for the system's own. Which devices a
machine has is the machine's: the choice is kept here, and never travels.
*/
export function rememberedDevice(kind: Device): string {
  try {
    return window.localStorage.getItem(`convia.device.${kind}`) ?? "";
  } catch {
    return "";
  }
}

export function rememberDevice(kind: Device, id: string): void {
  write(`convia.device.${kind}`, id);
}
