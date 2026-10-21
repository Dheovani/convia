import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Attachment, type FileInfo } from "../components/Attachment";
import {
  AddReactionIcon,
  DeleteIcon,
  EditIcon,
  PhoneIcon,
  ReplyIcon,
} from "../components/Icons";
import { Button, input } from "../components/controls";
import { ConfirmDialog } from "../components/ConfirmDialog";
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
  // author is who wrote the message answered, where more than two speak.
  author?: string;
  text: string;
  missing: boolean;
  deleted?: boolean;
}

export interface Message {
  id: string;
  mine: boolean;
  // author is who wrote it, where more than two speak.
  author?: string;
  text: string;
  status: "waiting" | "sent" | "delivered" | "received";
  at: number;
  reactions?: Reaction[];
  replyTo?: Quote;
  edited?: boolean;
  deleted?: boolean;
  file?: FileInfo;
  // deletable is a channel's message this person may delete, being its author or a moderator of its author.
  deletable?: boolean;
  moderated?: boolean;
  // missed is a call this person missed, shown here on this machine alone (ADR 0044).
  missed?: boolean;
}

// The reactions offered at a click; any single emoji is one (ADR 0035).
export const offered = ["👍", "❤️", "😂", "😮", "😢", "🙏", "🎉", "👀"];

// The first of them are a hover away; the rest one click more.
const quick = 3;

/*
Source is what a thread of messages is read from and written to: a conversation with a contact, or a channel of a
space (ADR 0042). What only a conversation has — files, statuses, forgetting — it leaves out where it has none.
*/
export interface Source {
  // title names the thread: the contact, or the channel.
  title: string;
  header: ReactNode;
  // other is who the person writes to, named where a message says nothing of its author.
  other: string;
  fieldLabel: string;
  load: () => Promise<Message[]>;
  // subscribe calls changed whenever the thread may have changed, until what it returns is called.
  subscribe: (changed: () => void) => () => void;
  send: (text: string, replyTo: string) => Promise<unknown>;
  react: (id: string, emoji: string, on: boolean) => Promise<unknown>;
  edit: (id: string, text: string) => Promise<unknown>;
  remove: (id: string) => Promise<unknown>;
  forget?: (id: string) => Promise<unknown>;
  attach?: () => Promise<unknown>;
  // fetch asks for a file pulled only when asked for.
  fetch?: (id: string) => Promise<unknown>;
  statuses: boolean;
  deleting?: string;
  // closed says why nothing can be written here, where nothing can.
  closed?: string;
}

// MaxCharacters is the most a message holds (ADR 0029); the core refuses more too.
export const MaxCharacters = 4000;

// The counter appears only as the limit nears.
const showCounterFrom = MaxCharacters - 400;

// Messages from one author within this long of each other are shown as one group, as Discord does.
const groupWithin = 5 * 60 * 1000;

// Thread is everything said in a conversation or a channel, and where to say more.
export function Thread({
  source,
  meName,
  focus,
}: {
  source: Source;
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

  const { load: read, subscribe } = source;
  const load = useCallback(
    () =>
      read().then(
        (all) => setMessages(all),
        (error: unknown) => setNotice(refused(words, error)),
      ),
    [read, words],
  );

  useEffect(() => {
    void load();
    return subscribe(() => void load());
  }, [subscribe, load]);

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
      await source.send(draft, answering?.id ?? "");
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
      await source.react(m.id, emoji, on);
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

  async function attach() {
    setNotice(null);
    await attempt(() => source.attach?.() ?? Promise.resolve());
  }

  async function saveEdit() {
    if (!editing) return;

    const { id, text } = editing;
    if (text.trim() === "" || [...text].length > MaxCharacters) return;

    setEditing(null);
    await attempt(() => source.edit(id, text));
  }

  const author = (said: { mine: boolean; author?: string }) =>
    said.author ?? (said.mine ? meName : source.other);

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
      aria-label={source.title}
      className="flex h-full min-w-0 flex-1 flex-col"
    >
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4 shadow-raised">
        {source.header}
      </header>

      <div className="flex flex-1 flex-col overflow-y-auto px-4 py-3">
        <ol className="flex flex-col">
          {messages.length === 0 && (
            <li className="py-8 text-center text-sm text-ink-dim">
              {words.conversation.none}
            </li>
          )}
          {messages.map((m, i) => {
            if (m.missed) {
              return (
                <li
                  key={m.id}
                  className="mt-3 flex items-baseline gap-2 px-2 text-sm text-danger"
                >
                  <PhoneIcon />
                  <span>{words.calls.missed}</span>
                  <time
                    className="text-[0.72rem] text-ink-faint"
                    dateTime={new Date(m.at).toISOString()}
                  >
                    {time.format(m.at)}
                  </time>
                </li>
              );
            }
            const previous = messages[i - 1];
            const starts =
              !previous ||
              previous.mine !== m.mine ||
              author(previous) !== author(m) ||
              m.at - previous.at > groupWithin;
            return (
              <li
                key={m.id}
                id={`message-${m.id}`}
                aria-current={m.id === focus ? "true" : undefined}
                className={`group relative flex flex-col rounded-sm px-2 hover:bg-surface-hover focus-within:bg-surface-hover ${starts ? "mt-3" : ""} ${m.id === focus ? "bg-accent-soft" : ""}`}
              >
                {!m.deleted && (
                  <div
                    className={`absolute -top-4 right-2 z-10 flex items-center gap-0.5 rounded-md border border-line bg-surface-raised p-0.5 shadow-raised group-hover:opacity-100 group-focus-within:opacity-100 ${choosing === m.id ? "opacity-100" : "opacity-0"}`}
                  >
                    {offered.slice(0, quick).map((emoji) => (
                      <button
                        key={emoji}
                        type="button"
                        className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-lg hover:bg-surface-hover"
                        aria-label={words.conversation.reactWith(emoji)}
                        title={words.conversation.reactWith(emoji)}
                        onClick={() => void toggle(m, emoji, true)}
                      >
                        {emoji}
                      </button>
                    ))}
                    <span
                      aria-hidden="true"
                      className="mx-0.5 h-5 w-px bg-line"
                    />
                    <button
                      type="button"
                      className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-ink-dim hover:bg-surface-hover hover:text-ink"
                      aria-label={words.conversation.react}
                      title={words.conversation.react}
                      aria-expanded={choosing === m.id}
                      onClick={() =>
                        setChoosing(choosing === m.id ? null : m.id)
                      }
                    >
                      <AddReactionIcon />
                    </button>
                    <button
                      type="button"
                      className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-ink-dim hover:bg-surface-hover hover:text-ink"
                      aria-label={words.conversation.reply}
                      title={words.conversation.reply}
                      onClick={() => {
                        setAnswering(m);
                        field.current?.focus();
                      }}
                    >
                      <ReplyIcon />
                    </button>
                    {m.mine && !m.file && (
                      <button
                        type="button"
                        className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-ink-dim hover:bg-surface-hover hover:text-ink"
                        aria-label={words.conversation.edit}
                        title={words.conversation.edit}
                        onClick={() => setEditing({ id: m.id, text: m.text })}
                      >
                        <EditIcon />
                      </button>
                    )}
                    {(m.mine || source.forget || m.deletable) && (
                      <button
                        type="button"
                        className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-ink-dim hover:bg-surface-hover hover:text-danger"
                        aria-label={words.conversation.delete}
                        title={words.conversation.delete}
                        onClick={() => setDeleting(m)}
                      >
                        <DeleteIcon />
                      </button>
                    )}
                  </div>
                )}
                {choosing === m.id && (
                  <div
                    role="group"
                    aria-label={words.conversation.chooseReaction}
                    className="absolute top-5 right-2 z-20 flex gap-0.5 rounded-md border border-line bg-surface-raised p-1 shadow-raised"
                    onKeyDown={(event) => {
                      if (event.key === "Escape") setChoosing(null);
                    }}
                  >
                    {offered.slice(quick).map((emoji) => (
                      <button
                        key={emoji}
                        type="button"
                        className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-lg hover:bg-surface-hover"
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
                      {author(m)}
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
                        <span className="font-medium">{author(m.replyTo)}</span>
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
                        {m.moderated
                          ? words.spaces.deletedByModerator
                          : words.conversation.deleted}
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
                    ) : m.file ? (
                      <Attachment
                        id={m.id}
                        name={m.text}
                        file={m.file}
                        mine={m.mine}
                        from={source.other}
                        onError={(error) => setNotice(refused(words, error))}
                        onFetch={
                          source.fetch
                            ? () =>
                                void attempt(
                                  () =>
                                    source.fetch?.(m.id) ?? Promise.resolve(),
                                )
                            : undefined
                        }
                      />
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
                  {m.mine && source.statuses && (
                    <span
                      className="shrink-0 text-[0.72rem] text-ink-faint"
                      title={
                        m.status === "waiting"
                          ? words.conversation.waitingHint(source.other)
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
        {source.closed ? (
          <p className="rounded-md bg-surface-raised px-3 py-2 text-sm text-ink-dim">
            {source.closed}
          </p>
        ) : (
          <>
            {answering && (
              <div className="flex items-center justify-between gap-2 rounded-sm bg-surface-raised px-3 py-1 text-[0.8rem]">
                <span className="truncate text-ink-dim">
                  {words.conversation.replyingTo(author(answering))}{" "}
                  <span className="text-ink-faint">
                    {plain(answering.text)}
                  </span>
                </span>
                <Button size="small" onClick={() => setAnswering(null)}>
                  {words.conversation.cancelReply}
                </Button>
              </div>
            )}
            <label htmlFor={fieldId} className="sr-only">
              {source.fieldLabel}
            </label>
            <textarea
              ref={field}
              id={fieldId}
              className={`${input} resize-none text-sm`}
              rows={2}
              placeholder={source.fieldLabel}
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
              {source.attach && (
                <Button
                  size="small"
                  aria-label={words.files.attach}
                  title={words.files.attach}
                  onClick={() => void attach()}
                >
                  📎
                </Button>
              )}
              <span
                className="flex-1 text-xs text-ink-faint"
                aria-live="polite"
              >
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
          </>
        )}
      </div>

      {link && <LinkDialog url={link} onClose={() => setLink(null)} />}
      {deleting && !source.forget && (
        <ConfirmDialog
          title={words.deleting.title}
          explanation={source.deleting ?? ""}
          confirm={words.conversation.delete}
          onConfirm={() => {
            const id = deleting.id;
            setDeleting(null);
            void attempt(() => source.remove(id));
          }}
          onClose={() => setDeleting(null)}
        />
      )}
      {deleting && source.forget && (
        <DeleteDialog
          mine={deleting.mine}
          delivered={
            source.forget !== undefined && deleting.status === "delivered"
          }
          explanation={source.deleting}
          onBoth={() => {
            const id = deleting.id;
            setDeleting(null);
            void attempt(() => source.remove(id));
          }}
          onHere={() => {
            const id = deleting.id;
            setDeleting(null);
            void attempt(() => source.forget?.(id) ?? Promise.resolve());
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </section>
  );
}
