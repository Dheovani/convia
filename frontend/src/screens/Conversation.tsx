import { useCallback, useEffect, useId, useRef, useState } from "react";
import {
  Conversation as readConversation,
  SendMessage,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button, input } from "../components/controls";
import { LinkDialog } from "../components/LinkDialog";
import { useLanguage, useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import { Formatted } from "../text/Formatted";

export interface Message {
  id: string;
  mine: boolean;
  text: string;
  status: "waiting" | "sent" | "delivered" | "received";
  at: number;
}

export interface Person {
  publicKey: string;
  name: string;
  verified: boolean;
}

// MaxCharacters is the most a message holds (ADR 0029); the core refuses more too.
export const MaxCharacters = 4000;

// The counter appears only as the limit nears.
const showCounterFrom = MaxCharacters - 400;

// Messages from one author within this long of each other are shown as one group, as Discord does.
const groupWithin = 5 * 60 * 1000;

// Conversation is everything said with one contact, and where to say more.
export function Conversation({
  contact,
  meName,
}: {
  contact: Person;
  meName: string;
}) {
  const words = useWords();
  const { formatting } = useLanguage();
  const fieldId = useId();
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [link, setLink] = useState<string | null>(null);
  const end = useRef<HTMLDivElement>(null);

  const load = useCallback(
    () =>
      readConversation(contact.publicKey).then(
        (all) => setMessages(all as Message[]),
        (error: unknown) => setNotice(refused(words, error)),
      ),
    [contact.publicKey, words],
  );

  useEffect(() => {
    void load();
    return EventsOn("conversation", (publicKey: string) => {
      if (publicKey === contact.publicKey) void load();
    });
  }, [contact.publicKey, load]);

  useEffect(() => {
    end.current?.scrollIntoView?.({ block: "end" });
  }, [messages.length]);

  const length = [...draft].length;
  const tooLong = length > MaxCharacters;
  const empty = draft.trim() === "";

  async function send() {
    if (tooLong || empty) return;
    setNotice(null);
    try {
      await SendMessage(contact.publicKey, draft);
      setDraft("");
      await load();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  const status = (m: Message) =>
    m.status === "waiting"
      ? words.conversation.waiting
      : m.status === "sent"
        ? words.conversation.sent
        : words.conversation.delivered;

  const time = new Intl.DateTimeFormat(formatting, {
    dateStyle: "short",
    timeStyle: "short",
  });

  return (
    <section
      aria-label={contact.name}
      className="flex h-full min-w-0 flex-1 flex-col"
    >
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4 shadow-raised">
        <h2 className="font-display font-semibold">{contact.name}</h2>
        {contact.verified && (
          <span className="rounded-sm bg-accent-soft px-1.5 text-[0.72rem] text-accent-ink">
            {words.workspace.verified}
          </span>
        )}
      </header>

      <ol className="flex flex-1 flex-col overflow-y-auto px-4 py-3">
        {messages.length === 0 && (
          <li className="m-auto text-sm text-ink-dim">
            {words.conversation.none}
          </li>
        )}
        {messages.map((m, i) => {
          const previous = messages[i - 1];
          const starts =
            !previous ||
            previous.mine !== m.mine ||
            m.at - previous.at > groupWithin;
          return (
            <li
              key={m.id}
              className={`flex flex-col rounded-sm px-2 hover:bg-surface-hover ${starts ? "mt-3" : ""}`}
            >
              {starts && (
                <div className="flex items-baseline gap-2">
                  <span
                    className={`font-medium ${m.mine ? "text-accent-ink" : ""}`}
                  >
                    {m.mine ? meName : contact.name}
                  </span>
                  <time
                    className="text-[0.72rem] text-ink-faint"
                    dateTime={new Date(m.at).toISOString()}
                  >
                    {time.format(m.at)}
                  </time>
                </div>
              )}
              <div className="flex items-end gap-2">
                <div className="min-w-0 flex-1 text-sm break-words whitespace-pre-wrap">
                  <Formatted text={m.text} onLink={setLink} />
                </div>
                {m.mine && (
                  <span
                    className="shrink-0 text-[0.72rem] text-ink-faint"
                    title={
                      m.status === "waiting"
                        ? words.conversation.waitingHint(contact.name)
                        : undefined
                    }
                  >
                    {status(m)}
                  </span>
                )}
              </div>
            </li>
          );
        })}
        <div ref={end} />
      </ol>

      <div className="flex shrink-0 flex-col gap-1 px-4 pb-4">
        {notice && (
          <p role="status" className="text-sm">
            {notice}
          </p>
        )}
        <label htmlFor={fieldId} className="sr-only">
          {words.conversation.field(contact.name)}
        </label>
        <textarea
          id={fieldId}
          className={`${input} resize-none text-sm`}
          rows={2}
          placeholder={words.conversation.field(contact.name)}
          value={draft}
          aria-invalid={tooLong}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              void send();
            }
          }}
        />
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs text-ink-faint" aria-live="polite">
            {length >= showCounterFrom
              ? tooLong
                ? words.conversation.over(length - MaxCharacters)
                : words.conversation.left(MaxCharacters - length)
              : words.conversation.sendHint}
          </span>
          <Button
            tone="primary"
            size="small"
            disabled={tooLong || empty}
            onClick={() => void send()}
          >
            {words.conversation.send}
          </Button>
        </div>
      </div>

      {link && <LinkDialog url={link} onClose={() => setLink(null)} />}
    </section>
  );
}
