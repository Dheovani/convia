import { useCallback, useEffect, useId, useRef, useState } from "react";
import {
  React as react,
  Conversation as readConversation,
  DeleteMessage,
  EditMessage,
  ForgetMessage,
  SendMessage,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button, input } from "../components/controls";
import { DeleteDialog } from "../components/DeleteDialog";
import { LinkDialog } from "../components/LinkDialog";
import { useLanguage, useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import { Formatted } from "../text/Formatted";
import { plain } from "../text/markdown";

export interface Reaction {
  emoji: string;
  count: number;
  mine: boolean;
}

export interface Quote {
  id: string;
  mine: boolean;
  text: string;
  missing: boolean;
  deleted?: boolean;
}

export interface Message {
  id: string;
  mine: boolean;
  text: string;
  status: "waiting" | "sent" | "delivered" | "received";
  at: number;
  reactions?: Reaction[];
  replyTo?: Quote;
  edited?: boolean;
  deleted?: boolean;
}

// The reactions offered at a click; any single emoji is one (ADR 0035).
export const offered = ["👍", "❤️", "😂", "😮", "😢", "🙏", "🎉", "👀"];

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
  focus,
}: {
  contact: Person;
  meName: string;
  // focus is a message to bring into view and mark, as when a search found it.
  focus?: string;
}) {
  const words = useWords();
  const { formatting } = useLanguage();
  const fieldId = useId();
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [link, setLink] = useState<string | null>(null);
  const [answering, setAnswering] = useState<Message | null>(null);
  const [choosing, setChoosing] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Message | null>(null);
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(
    null,
  );
  const field = useRef<HTMLTextAreaElement>(null);
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
    const found = focus && document.getElementById(`message-${focus}`);
    if (found) {
      found.scrollIntoView?.({ block: "center" });
    } else {
      end.current?.scrollIntoView?.({ block: "end" });
    }
  }, [messages.length, focus]);

  const length = [...draft].length;
  const tooLong = length > MaxCharacters;
  const empty = draft.trim() === "";

  async function send() {
    if (tooLong || empty) return;
    setNotice(null);
    try {
      await SendMessage(contact.publicKey, draft, answering?.id ?? "");
      setDraft("");
      setAnswering(null);
      await load();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function toggle(m: Message, emoji: string, on: boolean) {
    setChoosing(null);
    try {
      await react(contact.publicKey, m.id, emoji, on);
      await load();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function attempt(work: () => Promise<unknown>) {
    try {
      await work();
      await load();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function saveEdit() {
    if (!editing)
      return;

    const { id, text } = editing;
    if (text.trim() === "" || [...text].length > MaxCharacters)
      return;

    setEditing(null);
    await attempt(() => EditMessage(contact.publicKey, id, text));
  }

  const author = (mine: boolean) => (mine ? meName : contact.name);

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

      <div className="flex flex-1 flex-col overflow-y-auto px-4 py-3">
        <ol className="flex flex-col">
          {messages.length === 0 && (
            <li className="py-8 text-center text-sm text-ink-dim">
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
                id={`message-${m.id}`}
                aria-current={m.id === focus ? "true" : undefined}
                className={`group relative flex flex-col rounded-sm px-2 hover:bg-surface-hover focus-within:bg-surface-hover ${starts ? "mt-3" : ""} ${m.id === focus ? "bg-accent-soft" : ""}`}
              >
                {!m.deleted && (
                  <div className="absolute -top-3 right-2 flex gap-1 rounded-sm border border-line bg-surface-raised opacity-0 shadow-raised group-hover:opacity-100 group-focus-within:opacity-100">
                    <button
                      type="button"
                      className="cursor-pointer px-2 py-0.5 text-[0.72rem] hover:bg-surface-hover"
                      onClick={() => {
                        setAnswering(m);
                        field.current?.focus();
                      }}
                    >
                      {words.conversation.reply}
                    </button>
                    <button
                      type="button"
                      className="cursor-pointer px-2 py-0.5 text-[0.72rem] hover:bg-surface-hover"
                      aria-expanded={choosing === m.id}
                      onClick={() =>
                        setChoosing(choosing === m.id ? null : m.id)
                      }
                    >
                      {words.conversation.react}
                    </button>
                    {m.mine && (
                      <button
                        type="button"
                        className="cursor-pointer px-2 py-0.5 text-[0.72rem] hover:bg-surface-hover"
                        onClick={() => setEditing({ id: m.id, text: m.text })}
                      >
                        {words.conversation.edit}
                      </button>
                    )}
                    <button
                      type="button"
                      className="cursor-pointer px-2 py-0.5 text-[0.72rem] text-danger hover:bg-surface-hover"
                      onClick={() => setDeleting(m)}
                    >
                      {words.conversation.delete}
                    </button>
                  </div>
                )}
                {choosing === m.id && (
                  <div
                    role="group"
                    aria-label={words.conversation.chooseReaction}
                    className="flex flex-wrap gap-1 py-1"
                    onKeyDown={(event) => {
                      if (event.key === "Escape") setChoosing(null);
                    }}
                  >
                    {offered.map((emoji) => (
                      <button
                        key={emoji}
                        type="button"
                        className="cursor-pointer rounded-sm px-1.5 py-0.5 text-lg hover:bg-surface-raised"
                        aria-label={words.conversation.reactWith(emoji)}
                        onClick={() => void toggle(m, emoji, true)}
                      >
                        {emoji}
                      </button>
                    ))}
                  </div>
                )}
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
                {m.replyTo && (
                  <button
                    type="button"
                    className="mt-1 flex cursor-pointer gap-1 truncate border-l-2 border-line-strong pl-2 text-left text-[0.75rem] text-ink-faint hover:text-ink-dim"
                    onClick={() =>
                      document
                        .getElementById(`message-${m.replyTo?.id}`)
                        ?.scrollIntoView?.({ block: "center" })
                    }
                  >
                    {m.replyTo.missing ? (
                      words.conversation.quoteMissing
                    ) : m.replyTo.deleted ? (
                      words.conversation.quoteDeleted
                    ) : (
                      <>
                        <span className="font-medium">
                          {author(m.replyTo.mine)}
                        </span>
                        <span className="truncate">
                          {plain(m.replyTo.text)}
                        </span>
                      </>
                    )}
                  </button>
                )}
                <div className="flex items-end gap-2">
                  <div className="min-w-0 flex-1 text-sm break-words whitespace-pre-wrap">
                    {m.deleted ? (
                      <p className="text-ink-faint italic">
                        {words.conversation.deleted}
                      </p>
                    ) : editing?.id === m.id ? (
                      <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-edit`} className="sr-only">
                          {words.conversation.editing}
                        </label>
                        <textarea
                          id={`${fieldId}-edit`}
                          autoFocus
                          className={`${input} resize-none text-sm`}
                          rows={2}
                          value={editing.text}
                          onChange={(event) =>
                            setEditing({ id: m.id, text: event.target.value })
                          }
                          onKeyDown={(event) => {
                            if (event.key === "Escape") setEditing(null);
                            if (event.key === "Enter" && !event.shiftKey) {
                              event.preventDefault();
                              void saveEdit();
                            }
                          }}
                        />
                        <div className="flex gap-2">
                          <Button size="small" onClick={() => setEditing(null)}>
                            {words.conversation.cancelEdit}
                          </Button>
                          <Button
                            tone="primary"
                            size="small"
                            disabled={
                              editing.text.trim() === "" ||
                              [...editing.text].length > MaxCharacters
                            }
                            onClick={() => void saveEdit()}
                          >
                            {words.conversation.save}
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <>
                        <Formatted text={m.text} onLink={setLink} />
                        {m.edited && (
                          <span className="ml-1 text-[0.7rem] text-ink-faint">
                            {words.conversation.edited}
                          </span>
                        )}
                      </>
                    )}
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
                {m.reactions && m.reactions.length > 0 && (
                  <div className="flex flex-wrap gap-1 py-1">
                    {m.reactions.map((r) => (
                      <button
                        key={r.emoji}
                        type="button"
                        aria-pressed={r.mine}
                        aria-label={words.conversation.reaction(
                          r.emoji,
                          r.count,
                          r.mine,
                        )}
                        className={`cursor-pointer rounded-full border px-2 text-sm ${
                          r.mine
                            ? "border-accent bg-accent-soft"
                            : "border-line bg-surface-raised hover:border-line-strong"
                        }`}
                        onClick={() => void toggle(m, r.emoji, !r.mine)}
                      >
                        {r.emoji} {r.count}
                      </button>
                    ))}
                  </div>
                )}
              </li>
            );
          })}
        </ol>
        <div ref={end} />
      </div>

      <div className="flex shrink-0 flex-col gap-1 px-4 pb-4">
        {notice && (
          <p role="status" className="text-sm">
            {notice}
          </p>
        )}
        {answering && (
          <div className="flex items-center justify-between gap-2 rounded-sm bg-surface-raised px-3 py-1 text-[0.8rem]">
            <span className="truncate text-ink-dim">
              {words.conversation.replyingTo(author(answering.mine))}{" "}
              <span className="text-ink-faint">{plain(answering.text)}</span>
            </span>
            <Button size="small" onClick={() => setAnswering(null)}>
              {words.conversation.cancelReply}
            </Button>
          </div>
        )}
        <label htmlFor={fieldId} className="sr-only">
          {words.conversation.field(contact.name)}
        </label>
        <textarea
          ref={field}
          id={fieldId}
          className={`${input} resize-none text-sm`}
          rows={2}
          placeholder={words.conversation.field(contact.name)}
          value={draft}
          aria-invalid={tooLong}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && answering) {
              setAnswering(null);
            }
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
      {deleting && (
        <DeleteDialog
          mine={deleting.mine}
          delivered={deleting.status === "delivered"}
          onBoth={() => {
            const id = deleting.id;
            setDeleting(null);
            void attempt(() => DeleteMessage(contact.publicKey, id));
          }}
          onHere={() => {
            const id = deleting.id;
            setDeleting(null);
            void attempt(() => ForgetMessage(contact.publicKey, id));
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </section>
  );
}
