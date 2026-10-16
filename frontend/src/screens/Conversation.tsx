import { useCallback, useEffect, useId, useState } from "react";
import {
  Conversation as readConversation,
  SendMessage,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button, input } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import type { Contact } from "./Contacts";

export interface Message {
  id: string;
  mine: boolean;
  text: string;
  status: "waiting" | "sent" | "delivered" | "received";
  at: number;
}

// MaxCharacters is the most a message holds (ADR 0029); the core refuses more too.
export const MaxCharacters = 4000;

// The counter appears only as the limit nears.
const showCounterFrom = MaxCharacters - 400;

// Conversation is everything said with one contact, until the workspace of M12 replaces it.
export function Conversation({
  contact,
  onBack,
}: {
  contact: Contact;
  onBack: () => void;
}) {
  const words = useWords();
  const fieldId = useId();
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState<string | null>(null);

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

  return (
    <section className="flex w-full max-w-lg flex-col gap-4">
      <header className="flex items-center gap-3">
        <Button size="small" onClick={onBack}>
          {words.conversation.back}
        </Button>
        <h2 className="font-display text-lg font-semibold">{contact.name}</h2>
      </header>

      <ol className="flex flex-col gap-2">
        {messages.length === 0 && (
          <li className="text-sm text-ink-dim">{words.conversation.none}</li>
        )}
        {messages.map((m) => (
          <li
            key={m.id}
            className={`flex max-w-[85%] flex-col gap-1 rounded-md px-3 py-2 ${
              m.mine
                ? "self-end bg-accent-soft"
                : "self-start bg-surface-raised"
            }`}
          >
            <p className="text-sm break-words whitespace-pre-wrap">{m.text}</p>
            {m.mine && (
              <span
                className="self-end text-[0.72rem] text-ink-faint"
                title={
                  m.status === "waiting"
                    ? words.conversation.waitingHint(contact.name)
                    : undefined
                }
              >
                {status(m)}
              </span>
            )}
          </li>
        ))}
      </ol>

      <div className="flex flex-col gap-1">
        <label htmlFor={fieldId} className="sr-only">
          {words.conversation.field(contact.name)}
        </label>
        <textarea
          id={fieldId}
          className={`${input} text-sm`}
          rows={3}
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
          <span className="text-xs text-ink-faint">
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

      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
    </section>
  );
}
