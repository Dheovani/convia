import { useId, useState } from "react";
import { CreateChannel, InviteToSpace } from "../../wailsjs/go/app/Bridge";
import { Button, input } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import type { ChannelInfo } from "./Channel";

export interface SpaceInfo {
  id: string;
  name: string;
  rank: "owner" | "administrator" | "member";
  channels: ChannelInfo[];
}

interface Someone {
  publicKey: string;
  name: string;
}

// SpaceSidebar is a space's channels, and, for those who administer it, where to add a channel or a person.
export function SpaceSidebar({
  space,
  open,
  contacts,
  onOpen,
  onChanged,
}: {
  space: SpaceInfo;
  open: string;
  contacts: Someone[];
  onOpen: (channel: string) => void;
  onChanged: () => void;
}) {
  const words = useWords();
  const nameId = useId();
  const inviteId = useId();
  const [adding, setAdding] = useState<"channel" | "person" | null>(null);
  const [name, setName] = useState("");
  const [person, setPerson] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const administers = space.rank !== "member";

  async function attempt(work: () => Promise<unknown>) {
    setNotice(null);
    try {
      await work();
      setAdding(null);
      setName("");
      setPerson("");
      onChanged();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  return (
    <>
      <header className="flex h-12 items-center border-b border-line px-4 font-display font-semibold shadow-raised">
        <span className="truncate">{space.name}</span>
      </header>
      <div className="flex flex-col gap-1 overflow-y-auto p-2">
        <h3 className="mt-1 px-2 text-[0.72rem] font-semibold tracking-wide text-ink-faint uppercase">
          {words.spaces.channels}
        </h3>
        {space.channels.length === 0 && (
          <p className="px-2 text-sm text-ink-dim">{words.spaces.noChannels}</p>
        )}
        <ul className="flex flex-col gap-0.5">
          {space.channels.map((c) => (
            <li key={c.id}>
              <button
                type="button"
                className={`flex w-full cursor-pointer items-center gap-1.5 rounded-sm px-2 py-1.5 text-left text-sm ${
                  open === c.id
                    ? "bg-surface-hover text-ink"
                    : "text-ink-dim hover:bg-surface-hover hover:text-ink"
                }`}
                aria-current={open === c.id ? "page" : undefined}
                onClick={() => onOpen(c.id)}
              >
                <span aria-hidden="true" className="text-ink-faint">
                  {c.restricted ? "🔒" : "#"}
                </span>
                <span className="truncate">{c.name}</span>
              </button>
            </li>
          ))}
        </ul>
        {administers && (
          <div className="mt-3 flex flex-col gap-2 px-2">
            {adding === "channel" ? (
              <form
                className="flex flex-col gap-1"
                onSubmit={(event) => {
                  event.preventDefault();
                  void attempt(() => CreateChannel(space.id, name));
                }}
              >
                <label htmlFor={nameId} className="text-[0.75rem] text-ink-dim">
                  {words.spaces.channelName}
                </label>
                <input
                  id={nameId}
                  autoFocus
                  className={`${input} text-sm`}
                  value={name}
                  maxLength={100}
                  onChange={(event) => setName(event.target.value)}
                />
                <span className="flex gap-2">
                  <Button size="small" onClick={() => setAdding(null)}>
                    {words.spaces.cancel}
                  </Button>
                  <Button
                    size="small"
                    tone="primary"
                    type="submit"
                    disabled={name.trim() === ""}
                  >
                    {words.spaces.addChannel}
                  </Button>
                </span>
              </form>
            ) : adding === "person" ? (
              <form
                className="flex flex-col gap-1"
                onSubmit={(event) => {
                  event.preventDefault();
                  void attempt(() => InviteToSpace(space.id, person));
                }}
              >
                <label
                  htmlFor={inviteId}
                  className="text-[0.75rem] text-ink-dim"
                >
                  {words.spaces.whom}
                </label>
                <select
                  id={inviteId}
                  className={`${input} text-sm`}
                  value={person}
                  onChange={(event) => setPerson(event.target.value)}
                >
                  <option value="">{words.spaces.choose}</option>
                  {contacts.map((c) => (
                    <option key={c.publicKey} value={c.publicKey}>
                      {c.name}
                    </option>
                  ))}
                </select>
                <span className="flex gap-2">
                  <Button size="small" onClick={() => setAdding(null)}>
                    {words.spaces.cancel}
                  </Button>
                  <Button
                    size="small"
                    tone="primary"
                    type="submit"
                    disabled={person === ""}
                  >
                    {words.spaces.invite}
                  </Button>
                </span>
              </form>
            ) : (
              <>
                <Button size="small" onClick={() => setAdding("channel")}>
                  {words.spaces.newChannel}
                </Button>
                <Button size="small" onClick={() => setAdding("person")}>
                  {words.spaces.invitePerson}
                </Button>
              </>
            )}
          </div>
        )}
        {notice && (
          <p role="status" className="px-2 text-sm">
            {notice}
          </p>
        )}
      </div>
    </>
  );
}
