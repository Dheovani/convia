import { useEffect, useId, useState } from "react";
import { SearchMessages } from "../../wailsjs/go/app/Bridge";
import { input } from "../components/controls";
import { useLanguage, useWords } from "../i18n/language";
import { plain } from "../text/markdown";
import type { Message } from "./Conversation";

interface Found {
  publicKey: string;
  name: string;
  message: Message;
}

// How long typing pauses before a search runs: each one decrypts the history (ADR 0037).
const pause = 250;

/*
Search finds messages in every conversation. While there is something to find, its results take the
place of what it is given to show otherwise.
*/
export function Search({
  onOpen,
  children,
}: {
  onOpen: (publicKey: string, messageId: string) => void;
  children: React.ReactNode;
}) {
  const words = useWords();
  const { formatting } = useLanguage();
  const id = useId();
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Found[] | null>(null);

  useEffect(() => {
    if (query.trim() === "")
      return;

    let current = true;
    const timer = setTimeout(() => {
      SearchMessages(query).then(
        (results) => current && setFound((results ?? []) as Found[]),
        () => current && setFound([]),
      );
    }, pause);

    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [query]);

  const searching = query.trim() !== "";
  const date = new Intl.DateTimeFormat(formatting, { dateStyle: "short" });

  return (
    <>
      <label htmlFor={id} className="sr-only">
        {words.workspace.search}
      </label>
      <input
        id={id}
        type="search"
        className={`${input} mb-1 py-1.5 text-sm`}
        placeholder={words.workspace.search}
        value={query}
        onChange={(event) => {
          setQuery(event.target.value);
          if (event.target.value.trim() === "") setFound(null);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setQuery("");
            setFound(null);
          }
        }}
      />
      {!searching ? (
        children
      ) : found === null ? null : found.length === 0 ? (
        <p className="px-2 text-sm text-ink-dim" role="status">
          {words.workspace.searchNone}
        </p>
      ) : (
        <>
          <p className="sr-only" role="status">
            {words.workspace.searchFound(found.length)}
          </p>
          <ul className="flex flex-col gap-0.5">
            {found.map((f) => (
              <li key={f.message.id}>
                <button
                  type="button"
                  className="flex w-full cursor-pointer flex-col rounded-sm px-2 py-1.5 text-left text-ink-dim hover:bg-surface-hover hover:text-ink"
                  onClick={() => onOpen(f.publicKey, f.message.id)}
                >
                  <span className="flex items-baseline justify-between gap-2 text-sm">
                    <span className="truncate">{f.name}</span>
                    <time className="shrink-0 text-[0.72rem] text-ink-faint">
                      {date.format(f.message.at)}
                    </time>
                  </span>
                  <span className="line-clamp-2 text-[0.75rem] text-ink-faint">
                    {plain(f.message.text)}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}
