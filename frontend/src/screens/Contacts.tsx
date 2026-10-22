import { useCallback, useEffect, useState } from "react";
import {
  AcceptRequest,
  AddContact,
  BlockContact,
  Blocked,
  Contacts as readContacts,
  DeclineRequest,
  DismissRequest,
  MyInvitation,
  RemoveContact,
  Requests,
  SetVerified,
  Unblock,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Button, input } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";

export interface Contact {
  publicKey: string;
  name: string;
  verified: boolean;
  safetyCode: string;
}

export interface Request {
  publicKey: string;
  name: string;
  safetyCode: string;
  at: number;
  expired: boolean;
}

interface State {
  invitation: string;
  contacts: Contact[];
  received: Request[];
  sent: Request[];
  blocked: string[];
}

async function load(): Promise<State> {
  const [invitation, contacts, requests, blocked] = await Promise.all([
    MyInvitation(),
    readContacts(),
    Requests(),
    Blocked(),
  ]);

  return {
    invitation,
    contacts,
    received: requests.received,
    sent: requests.sent,
    blocked,
  };
}

// SafetyCode is shown only when asked for, so that nobody near the screen reads it by chance.
function SafetyCode({ code }: { code: string }) {
  const words = useWords();
  const [shown, setShown] = useState(false);
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[0.75rem] text-ink-faint">
        {words.contacts.safetyCode}
      </span>
      <div className="flex items-center gap-2 rounded-sm bg-surface-sunken px-3 py-2">
        <code
          className={`flex-1 font-mono text-sm tracking-wide break-words ${shown ? "" : "text-ink-faint select-none"}`}
        >
          {shown ? code : code.replace(/\d/g, "•")}
        </code>
        <Button
          size="small"
          aria-pressed={shown}
          onClick={() => setShown(!shown)}
        >
          {shown ? words.contacts.hideCode : words.contacts.showCode}
        </Button>
      </div>
    </div>
  );
}

type Parting = { contact: Contact; block: boolean };

const card = "flex flex-col gap-3 rounded-md bg-surface-raised p-4";

// Contacts is who this person talks to, who asks to, and how to be asked (ADR 0026, ADR 0028).
export function Contacts({ onOpen }: { onOpen: (contact: Contact) => void }) {
  const words = useWords();
  const [state, setState] = useState<State | null>(null);
  const [code, setCode] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [parting, setParting] = useState<Parting | null>(null);
  const [forget, setForget] = useState(false);

  const refresh = useCallback(
    () =>
      load().then(setState, (error: unknown) =>
        setNotice(refused(words, error)),
      ),
    [words],
  );

  useEffect(() => {
    load().then(setState, (error: unknown) => setNotice(refused(words, error)));
    return EventsOn("contacts", () => void refresh());
  }, [refresh, words]);

  async function attempt(work: () => Promise<unknown>, done?: string) {
    setNotice(null);
    try {
      await work();
      if (done) setNotice(done);
      await refresh();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function ask() {
    setNotice(null);
    try {
      const asked = await AddContact(code);
      setCode("");
      setNotice(words.contacts.asked(asked.name));
      await refresh();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  if (state === null) {
    return notice ? <p role="status">{notice}</p> : null;
  }

  return (
    <div className="flex w-full max-w-lg flex-col gap-8">
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}

      {state.received.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">
            {words.contacts.received}
          </h2>
          {state.received.map((r) => (
            <article key={r.publicKey} className={card}>
              <h3 className="font-medium">{r.name}</h3>
              <SafetyCode code={r.safetyCode} />
              <div className="flex gap-2">
                <Button
                  tone="primary"
                  size="small"
                  onClick={() => attempt(() => AcceptRequest(r.publicKey))}
                >
                  {words.contacts.accept}
                </Button>
                <Button
                  size="small"
                  onClick={() => attempt(() => DeclineRequest(r.publicKey))}
                >
                  {words.contacts.decline}
                </Button>
                <Button
                  size="small"
                  onClick={() => attempt(() => BlockContact(r.publicKey, true))}
                >
                  {words.contacts.block}
                </Button>
              </div>
            </article>
          ))}
        </section>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">
          {words.contacts.list}
        </h2>
        {state.contacts.length === 0 && (
          <p className="text-sm text-ink-dim">{words.contacts.none}</p>
        )}
        {state.contacts.map((c) => (
          <article key={c.publicKey} className={card}>
            <h3 className="flex items-center gap-2 font-medium">
              {c.name}
              {c.verified && (
                <span className="rounded-sm bg-accent-soft px-1.5 text-[0.72rem]">
                  {words.contacts.verified}
                </span>
              )}
            </h3>
            <Button tone="primary" size="small" onClick={() => onOpen(c)}>
              {words.contacts.open}
            </Button>
            <SafetyCode code={c.safetyCode} />
            {!c.verified && (
              <div className="flex flex-col items-start gap-2">
                <p className="text-xs text-ink-faint">
                  {words.contacts.safetyHint}
                </p>
                <Button
                  size="small"
                  onClick={() => attempt(() => SetVerified(c.publicKey, true))}
                >
                  {words.contacts.verify(c.name)}
                </Button>
              </div>
            )}
            <div className="flex items-center gap-2">
              <Button
                size="small"
                onClick={() => {
                  setForget(false);
                  setParting({ contact: c, block: false });
                }}
              >
                {words.contacts.remove}
              </Button>
              <Button
                size="small"
                onClick={() => {
                  setForget(false);
                  setParting({ contact: c, block: true });
                }}
              >
                {words.contacts.block}
              </Button>
            </div>
          </article>
        ))}
      </section>

      {parting && (
        <ConfirmDialog
          title={
            parting.block
              ? words.contacts.blockTitle(parting.contact.name)
              : words.contacts.removeTitle(parting.contact.name)
          }
          explanation={words.contacts.removeHint}
          confirm={parting.block ? words.contacts.block : words.contacts.remove}
          onClose={() => setParting(null)}
          onConfirm={() => {
            const { contact, block } = parting;
            setParting(null);
            void attempt(() =>
              block
                ? BlockContact(contact.publicKey, forget)
                : RemoveContact(contact.publicKey, forget),
            );
          }}
        >
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={forget}
              onChange={(event) => setForget(event.target.checked)}
            />
            {words.contacts.forget}
          </label>
        </ConfirmDialog>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">
          {words.contacts.addTitle}
        </h2>
        <p className="text-sm text-ink-dim">{words.contacts.addExplanation}</p>
        <label className="flex flex-col gap-1">
          <span className="text-[0.8rem] font-medium text-ink-dim">
            {words.contacts.code}
          </span>
          <textarea
            className={`${input} font-mono text-sm`}
            rows={3}
            value={code}
            onChange={(event) => setCode(event.target.value)}
          />
        </label>
        <Button tone="primary" disabled={code.trim() === ""} onClick={ask}>
          {words.contacts.ask}
        </Button>
      </section>

      {state.sent.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">
            {words.contacts.sent}
          </h2>
          {state.sent.map((r) => (
            <article
              key={r.publicKey}
              className={`${card} flex-row items-center justify-between`}
            >
              <div>
                <h3 className="font-medium">{r.name}</h3>
                <p className="text-sm text-ink-dim">
                  {r.expired
                    ? words.contacts.notAccepted
                    : words.contacts.waiting}
                </p>
              </div>
              <Button
                size="small"
                onClick={() => attempt(() => DismissRequest(r.publicKey))}
              >
                {words.contacts.dismiss}
              </Button>
            </article>
          ))}
        </section>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">
          {words.contacts.invitationTitle}
        </h2>
        <p className="text-sm text-ink-dim">
          {words.contacts.invitationExplanation}
        </p>
        <code className="rounded-md bg-surface-raised p-3 font-mono text-xs break-all">
          {state.invitation}
        </code>
        <Button
          onClick={() =>
            attempt(
              () => navigator.clipboard.writeText(state.invitation),
              words.contacts.copied,
            )
          }
        >
          {words.contacts.copy}
        </Button>
      </section>

      {state.blocked.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">
            {words.contacts.blocked}
          </h2>
          {state.blocked.map((key) => (
            <div key={key} className="flex items-center justify-between gap-2">
              <code className="font-mono text-xs">{key.slice(0, 16)}…</code>
              <Button size="small" onClick={() => attempt(() => Unblock(key))}>
                {words.contacts.unblock}
              </Button>
            </div>
          ))}
        </section>
      )}
    </div>
  );
}
