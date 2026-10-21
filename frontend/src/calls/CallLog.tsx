import { useCallback, useEffect, useState } from "react";
import { CallLog as readCallLog, StartCall } from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import {
  IncomingIcon,
  OutgoingIcon,
  PhoneIcon,
  VideoIcon,
} from "../components/Icons";
import { useLanguage, useWords } from "../i18n/language";
import { refused } from "../i18n/refused";

interface Called {
  publicKey: string;
  name: string;
  incoming: boolean;
  video: boolean;
  outcome: string;
  rang: number;
  seconds: number;
}

function lasted(seconds: number): string {
  const m = Math.floor(seconds / 60);
  return `${m}:${String(seconds % 60).padStart(2, "0")}`;
}

// CallLog is every call this person made or was called by, kept on this machine alone (ADR 0045).
export function CallLog({ onOpen }: { onOpen: (publicKey: string) => void }) {
  const words = useWords();
  const { formatting } = useLanguage();
  const [log, setLog] = useState<Called[]>([]);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(
    () =>
      readCallLog().then(
        (all) => setLog((all ?? []) as Called[]),
        (error: unknown) => setNotice(refused(words, error)),
      ),
    [words],
  );

  useEffect(() => {
    void load();
    return EventsOn("call-log", () => void load());
  }, [load]);

  const when = new Intl.DateTimeFormat(formatting, {
    dateStyle: "short",
    timeStyle: "short",
  });
  const button =
    "flex h-8 w-8 cursor-pointer items-center justify-center rounded-sm text-ink-dim hover:bg-surface-hover hover:text-ink";

  return (
    <section
      aria-label={words.calls.log}
      className="flex w-full max-w-2xl flex-col gap-4"
    >
      <h2 className="font-display text-xl font-semibold">{words.calls.log}</h2>
      {log.length === 0 && (
        <p className="text-sm text-ink-dim">{words.calls.noCalls}</p>
      )}
      <ul className="flex flex-col gap-1">
        {log.map((c) => {
          const missed = c.incoming && c.outcome === "missed";
          const name = c.name || words.home.someone;
          return (
            <li
              key={`${c.publicKey}-${c.rang}`}
              className="flex items-center gap-3 rounded-md px-3 py-2 hover:bg-surface-hover"
            >
              <span
                className={missed ? "text-danger" : "text-ink-faint"}
                aria-label={
                  c.incoming ? words.calls.received : words.calls.made
                }
                role="img"
              >
                {c.incoming ? <IncomingIcon /> : <OutgoingIcon />}
              </span>
              <button
                type="button"
                className="flex min-w-0 flex-1 cursor-pointer flex-col text-left"
                onClick={() => onOpen(c.publicKey)}
              >
                <span
                  className={`truncate text-sm font-medium ${missed ? "text-danger" : ""}`}
                >
                  {name}
                </span>
                <span className="text-[0.75rem] text-ink-faint">
                  {when.format(c.rang)}
                  {" · "}
                  {c.seconds > 0
                    ? `${c.video ? words.calls.video : words.calls.voice} · ${lasted(c.seconds)}`
                    : (words.calls.logged[
                        c.outcome as keyof typeof words.calls.logged
                      ] ?? words.calls.logged.ended)}
                </span>
              </button>
              <button
                type="button"
                className={button}
                aria-label={words.calls.call(name)}
                title={words.calls.call(name)}
                onClick={() =>
                  void StartCall(c.publicKey, false).catch((error: unknown) =>
                    setNotice(refused(words, error)),
                  )
                }
              >
                <PhoneIcon />
              </button>
              <button
                type="button"
                className={button}
                aria-label={words.calls.videoCall(name)}
                title={words.calls.videoCall(name)}
                onClick={() =>
                  void StartCall(c.publicKey, true).catch((error: unknown) =>
                    setNotice(refused(words, error)),
                  )
                }
              >
                <VideoIcon />
              </button>
            </li>
          );
        })}
      </ul>
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
    </section>
  );
}
