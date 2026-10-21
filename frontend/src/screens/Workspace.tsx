import { useCallback, useEffect, useState } from "react";
import {
  Conversations,
  Notify,
  Requests,
  SpaceInvitations,
  Spaces,
  Viewing,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { CallBar } from "../calls/CallBar";
import { Mark } from "../components/Mark";
import { useWords } from "../i18n/language";
import { rememberedNotificationText } from "../state/preferences";
import { plain } from "../text/markdown";
import { Channel } from "./Channel";
import { Contacts } from "./Contacts";
import { MemberList } from "./MemberList";
import { Conversation, type Message } from "./Conversation";
import type { Identity } from "./Me";
import { NewSpace } from "./NewSpace";
import { Search } from "./Search";
import { Settings } from "./Settings";
import { SpaceSettings } from "./SpaceSettings";
import { SpaceSidebar, type SpaceInfo } from "./SpaceSidebar";

export interface ConversationSummary {
  publicKey: string;
  name: string;
  verified: boolean;
  safetyCode: string;
  last?: Message;
  unread: number;
}

type Pane =
  | { kind: "conversation"; publicKey: string; focus?: string }
  | { kind: "contacts" }
  | { kind: "settings" }
  | { kind: "space"; space: string; channel?: string; settings?: boolean }
  | { kind: "new-space" }
  | { kind: "none" };

// initials are what a space's button in the rail shows of its name.
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((word) => [...word][0])
    .join("")
    .toUpperCase();
}

interface Outdated {
  publicKey: string;
  theirs: boolean;
}

// attentive is whether the person is looking at the window.
function attentive(): boolean {
  return document.visibilityState === "visible" && document.hasFocus();
}

// Workspace is the window of a person with an identity: the rail, the sidebar and what is open (M12).
export function Workspace({
  me,
  onChanged,
}: {
  me: Identity;
  onChanged: () => void;
}) {
  const words = useWords();
  const [pane, setPane] = useState<Pane>({ kind: "none" });
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [waiting, setWaiting] = useState(0);
  const [spaces, setSpaces] = useState<SpaceInfo[]>([]);
  const [invited, setInvited] = useState(0);
  const [focused, setFocused] = useState(attentive);
  const [outdated, setOutdated] = useState<string | null>(null);

  const load = useCallback(
    () =>
      Promise.all([Conversations(), Requests()]).then(
        ([all, requests]) => {
          setConversations((all ?? []) as ConversationSummary[]);
          setWaiting(requests?.received.length ?? 0);
        },
        // The list stays as it was; the next change loads it again.
        () => undefined,
      ),
    [],
  );

  const loadSpaces = useCallback(
    () =>
      Promise.all([Spaces(), SpaceInvitations()]).then(
        ([all, invitations]) => {
          setSpaces((all ?? []) as SpaceInfo[]);
          setInvited(invitations?.length ?? 0);
        },
        () => undefined,
      ),
    [],
  );

  useEffect(() => {
    void loadSpaces();
    return EventsOn("spaces", () => void loadSpaces());
  }, [loadSpaces]);

  useEffect(() => {
    void load();
    const off = [
      EventsOn("conversation", () => void load()),
      EventsOn("contacts", () => void load()),
      EventsOn("open-conversation", (publicKey: string) =>
        setPane({ kind: "conversation", publicKey }),
      ),
    ];
    return () => off.forEach((stop) => stop());
  }, [load]);

  // A message that arrived away from its conversation is told of by name alone, unless chosen otherwise (ADR 0033).
  useEffect(
    () =>
      EventsOn("received", (r: { publicKey: string; text: string }) => {
        const name =
          conversations.find((c) => c.publicKey === r.publicKey)?.name ??
          words.home.someone;
        void Notify(
          r.publicKey,
          words.notification.title(name),
          rememberedNotificationText() ? r.text : "",
        );
      }),
    [conversations, words],
  );

  useEffect(
    () =>
      EventsOn("outdated", (o: Outdated) => {
        const name =
          conversations.find((c) => c.publicKey === o.publicKey)?.name ??
          words.home.someone;
        setOutdated(
          o.theirs
            ? words.home.outdatedTheirs(name)
            : words.home.outdatedMine(name),
        );
      }),
    [conversations, words],
  );

  useEffect(() => {
    const update = () => setFocused(attentive());
    window.addEventListener("focus", update);
    window.addEventListener("blur", update);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", update);
      document.removeEventListener("visibilitychange", update);
    };
  }, []);

  const open = pane.kind === "conversation" ? pane.publicKey : "";
  useEffect(() => {
    void Viewing(open, focused).then(() => load());
  }, [open, focused, load]);

  // Alt+Up and Alt+Down move between conversations, as in Discord.
  useEffect(() => {
    const move = (event: KeyboardEvent) => {
      if (!event.altKey || conversations.length === 0) return;
      const step =
        event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
      if (step === 0) return;
      event.preventDefault();
      const at = conversations.findIndex((c) => c.publicKey === open);
      const next =
        conversations[
          (at + step + conversations.length) % conversations.length
        ];
      setPane({ kind: "conversation", publicKey: next.publicKey });
    };
    window.addEventListener("keydown", move);
    return () => window.removeEventListener("keydown", move);
  }, [conversations, open]);

  const current = conversations.find((c) => c.publicKey === open);
  const space =
    pane.kind === "space" ? spaces.find((x) => x.id === pane.space) : undefined;
  const channel =
    pane.kind === "space"
      ? (space?.channels.find((c) => c.id === pane.channel) ??
        space?.channels[0])
      : undefined;
  const home =
    pane.kind !== "settings" &&
    pane.kind !== "space" &&
    pane.kind !== "new-space";

  const railButton = (selected: boolean) =>
    `flex h-12 w-12 cursor-pointer items-center justify-center rounded-lg transition-colors ${
      selected
        ? "bg-accent text-on-accent"
        : "bg-surface-raised text-ink-dim hover:bg-surface-hover hover:text-ink"
    }`;

  return (
    <div className="flex h-full bg-surface-sunken text-ink">
      <nav
        aria-label="Convia"
        className="flex w-(--rail-width) shrink-0 flex-col items-center gap-2 py-3"
      >
        <button
          type="button"
          className={railButton(home)}
          aria-label={words.workspace.home}
          aria-current={home ? "page" : undefined}
          onClick={() => !home && setPane({ kind: "none" })}
        >
          <Mark size={28} />
        </button>
        <span aria-hidden="true" className="h-px w-8 bg-line" />
        <ul aria-label={words.spaces.title} className="flex flex-col gap-2">
          {spaces.map((x) => (
            <li key={x.id}>
              <button
                type="button"
                className={`${railButton(pane.kind === "space" && pane.space === x.id)} font-display text-sm font-semibold`}
                aria-label={x.name}
                title={x.name}
                aria-current={
                  pane.kind === "space" && pane.space === x.id
                    ? "page"
                    : undefined
                }
                onClick={() => setPane({ kind: "space", space: x.id })}
              >
                {initials(x.name)}
              </button>
            </li>
          ))}
        </ul>
        <button
          type="button"
          className={`relative ${railButton(pane.kind === "new-space")}`}
          aria-label={words.spaces.add}
          title={words.spaces.add}
          aria-current={pane.kind === "new-space" ? "page" : undefined}
          onClick={() => setPane({ kind: "new-space" })}
        >
          <span aria-hidden="true" className="text-xl">
            +
          </span>
          {invited > 0 && (
            <span
              className="absolute -top-1 -right-1 rounded-full bg-accent px-1.5 text-[0.72rem] text-on-accent"
              aria-label={words.spaces.invitationsWaiting(invited)}
            >
              {invited}
            </span>
          )}
        </button>
        <div className="flex-1" />
        <button
          type="button"
          className={railButton(pane.kind === "settings")}
          aria-label={words.workspace.settings}
          aria-current={pane.kind === "settings" ? "page" : undefined}
          onClick={() => setPane({ kind: "settings" })}
        >
          <span aria-hidden="true" className="text-xl">
            ⚙
          </span>
        </button>
      </nav>

      <aside
        aria-label={space ? space.name : words.workspace.directMessages}
        className="flex w-(--sidebar-width) shrink-0 flex-col rounded-tl-lg bg-surface-deep"
      >
        {space ? (
          <SpaceSidebar
            space={space}
            open={channel?.id ?? ""}
            contacts={conversations}
            onOpen={(id) =>
              setPane({ kind: "space", space: space.id, channel: id })
            }
            onChanged={() => void loadSpaces()}
            onSettings={() =>
              setPane({ kind: "space", space: space.id, settings: true })
            }
          />
        ) : (
          <>
            <header className="flex h-12 items-center border-b border-line px-4 font-display font-semibold shadow-raised">
              {words.workspace.directMessages}
            </header>
            <div className="flex flex-col gap-1 overflow-y-auto p-2">
              <button
                type="button"
                className={`flex cursor-pointer items-center justify-between rounded-sm px-2 py-2 text-left text-sm ${
                  pane.kind === "contacts"
                    ? "bg-surface-hover text-ink"
                    : "text-ink-dim hover:bg-surface-hover hover:text-ink"
                }`}
                aria-current={pane.kind === "contacts" ? "page" : undefined}
                onClick={() => setPane({ kind: "contacts" })}
              >
                {words.workspace.contacts}
                {waiting > 0 && (
                  <span
                    className="rounded-full bg-accent px-1.5 text-[0.72rem] text-on-accent"
                    aria-label={words.workspace.requests(waiting)}
                  >
                    {waiting}
                  </span>
                )}
              </button>
              <Search
                onOpen={(publicKey, focus) =>
                  setPane({ kind: "conversation", publicKey, focus })
                }
              >
                <h3 className="mt-3 px-2 text-[0.72rem] font-semibold tracking-wide text-ink-faint uppercase">
                  {words.workspace.conversations}
                </h3>
                {conversations.length === 0 && (
                  <p className="px-2 text-sm text-ink-dim">
                    {words.workspace.noConversations}
                  </p>
                )}
                <ul className="flex flex-col gap-0.5">
                  {conversations.map((c) => (
                    <li key={c.publicKey}>
                      <button
                        type="button"
                        className={`flex w-full cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-left ${
                          open === c.publicKey
                            ? "bg-surface-hover text-ink"
                            : "text-ink-dim hover:bg-surface-hover hover:text-ink"
                        }`}
                        aria-current={open === c.publicKey ? "page" : undefined}
                        onClick={() =>
                          setPane({
                            kind: "conversation",
                            publicKey: c.publicKey,
                          })
                        }
                      >
                        <span className="flex min-w-0 flex-1 flex-col">
                          <span
                            className={`truncate text-sm ${c.unread > 0 ? "font-semibold text-ink" : ""}`}
                          >
                            {c.name}
                          </span>
                          {c.last && (
                            <span className="truncate text-[0.75rem] text-ink-faint">
                              {c.last.file
                                ? `📎 ${c.last.text}`
                                : plain(c.last.text)}
                            </span>
                          )}
                        </span>
                        {c.unread > 0 && (
                          <span
                            className="rounded-full bg-accent px-1.5 text-[0.72rem] text-on-accent"
                            aria-label={words.workspace.unread(c.unread)}
                          >
                            {c.unread}
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </Search>
            </div>
          </>
        )}
      </aside>

      <main className="flex min-w-0 flex-1 flex-col bg-surface">
        <CallBar people={conversations} />
        {outdated && (
          <p role="alert" className="bg-surface-raised p-3 text-sm">
            {outdated}
          </p>
        )}
        {space && pane.kind === "space" && pane.settings ? (
          <div className="flex flex-1 justify-center overflow-y-auto px-8 py-8">
            <SpaceSettings
              key={space.id}
              space={space}
              onLeft={() => {
                setPane({ kind: "none" });
                void loadSpaces();
              }}
            />
          </div>
        ) : space && channel ? (
          <Channel
            key={`${space.id}-${channel.id}`}
            space={space.id}
            channel={channel}
            meName={me.name}
            muted={space.muted}
          />
        ) : space ? (
          <p className="m-auto text-sm text-ink-dim">
            {words.spaces.noChannels}
          </p>
        ) : pane.kind === "new-space" ? (
          <div className="flex flex-1 justify-center overflow-y-auto px-8 py-8">
            <NewSpace
              onOpen={(id) => {
                void loadSpaces();
                setPane({ kind: "space", space: id });
              }}
            />
          </div>
        ) : pane.kind === "conversation" && current ? (
          <Conversation
            key={current.publicKey}
            contact={current}
            meName={me.name}
            focus={pane.focus}
          />
        ) : pane.kind === "contacts" ? (
          <div className="flex flex-1 justify-center overflow-y-auto px-8 py-8">
            <Contacts
              onOpen={(c) =>
                setPane({ kind: "conversation", publicKey: c.publicKey })
              }
            />
          </div>
        ) : pane.kind === "settings" ? (
          <Settings me={me} onChanged={onChanged} />
        ) : (
          <p className="m-auto text-sm text-ink-dim">
            {words.workspace.choose}
          </p>
        )}
      </main>
      {space && <MemberList key={space.id} space={space.id} />}
    </div>
  );
}
