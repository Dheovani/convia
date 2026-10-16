import { useId, useState } from "react";
import { input } from "../components/controls";
import { languageNames, useChoosing, useWords } from "../i18n/language";
import {
  applyTheme,
  languageChoices,
  rememberedNotificationText,
  rememberedTheme,
  rememberNotificationText,
  rememberTheme,
  themes,
  type LanguageChoice,
  type Theme,
} from "../state/preferences";
import { Me, type Identity } from "./Me";

// Settings is how this person's Convia looks, speaks and tells them things, and who they are.
export function Settings({
  me,
  onChanged,
}: {
  me: Identity;
  onChanged: () => void;
}) {
  const words = useWords();
  const choosing = useChoosing();
  const languageId = useId();
  const themeId = useId();
  const [theme, setTheme] = useState(rememberedTheme);
  const [notificationText, setNotificationText] = useState(
    rememberedNotificationText,
  );

  return (
    <section
      aria-label={words.settings.title}
      className="flex h-full flex-1 flex-col items-center gap-8 overflow-y-auto px-8 py-8"
    >
      <div className="flex w-full max-w-lg flex-col gap-4">
        <h2 className="font-display text-lg font-semibold">
          {words.settings.title}
        </h2>
        <div className="flex flex-col gap-1">
          <label
            htmlFor={languageId}
            className="text-[0.8rem] font-medium text-ink-dim"
          >
            {words.settings.language}
          </label>
          <select
            id={languageId}
            className={input}
            value={choosing.choice}
            onChange={(event) =>
              choosing.setChoice(event.target.value as LanguageChoice)
            }
          >
            {languageChoices.map((choice) => (
              <option key={choice} value={choice}>
                {choice === "system"
                  ? words.settings.languageSystem(
                      languageNames[choosing.system],
                    )
                  : languageNames[choice]}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label
            htmlFor={themeId}
            className="text-[0.8rem] font-medium text-ink-dim"
          >
            {words.settings.theme}
          </label>
          <select
            id={themeId}
            className={input}
            value={theme}
            onChange={(event) => {
              const next = event.target.value as Theme;
              setTheme(next);
              rememberTheme(next);
              applyTheme(next);
            }}
          >
            {themes.map((t) => (
              <option key={t} value={t}>
                {words.settings.themes[t]}
              </option>
            ))}
          </select>
        </div>
        <h3 className="mt-2 font-medium">
          {words.settings.notificationsTitle}
        </h3>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={notificationText}
            onChange={(event) => {
              setNotificationText(event.target.checked);
              rememberNotificationText(event.target.checked);
            }}
          />
          {words.settings.notificationText}
        </label>
        <p className="text-xs text-ink-faint">
          {words.settings.notificationTextHint}
        </p>
      </div>
      <Me key={me.publicKey + me.name} me={me} onChanged={onChanged} />
    </section>
  );
}
