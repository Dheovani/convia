import { useCallback, useEffect, useId, useState } from "react";
import {
  CreateRole,
  DeleteChannel,
  DeleteRole,
  DeleteSpace,
  LeaveSpace,
  MuteMember,
  RemoveMember,
  RenameChannel,
  RenameRole,
  RenameSpace,
  SetAdministrator,
  SetChannelRoles,
  SetChannelWriters,
  SetMemberRole,
  SpaceMembers,
  SpaceRoles,
  TransferOwnership,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Button, input } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import type { Member } from "./MemberList";
import type { SpaceInfo } from "./SpaceSidebar";

interface Role {
  id: string;
  name: string;
}

interface Asking {
  title: string;
  explanation: string;
  confirm: string;
  work: () => Promise<unknown>;
}

// Renamer is a name shown with a field to change it; keyed by the name, it starts again when the name changes.
function Renamer({
  label,
  name,
  onRename,
}: {
  label: string;
  name: string;
  onRename: (name: string) => void;
}) {
  const words = useWords();
  const id = useId();
  const [draft, setDraft] = useState(name);
  return (
    <form
      className="flex min-w-0 flex-1 items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (draft.trim() !== "" && draft !== name) onRename(draft);
      }}
    >
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <input
        id={id}
        className={`${input} min-w-0 flex-1 py-1.5 text-sm`}
        value={draft}
        maxLength={100}
        onChange={(event) => setDraft(event.target.value)}
      />
      {draft !== name && (
        <Button size="small" type="submit" disabled={draft.trim() === ""}>
          {words.spaces.save}
        </Button>
      )}
    </form>
  );
}

/*
SpaceSettings is where a space is administered (ADR 0038): its name, its channels and who sees them, its roles,
and its members — their roles, their rank, their removal, and who owns it. Each member is offered only what their
rank allows; the core refuses the rest all the same.
*/
export function SpaceSettings({
  space,
  onLeft,
}: {
  space: SpaceInfo;
  onLeft: () => void;
}) {
  const words = useWords();
  const [roles, setRoles] = useState<Role[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [newRole, setNewRole] = useState("");
  const [asking, setAsking] = useState<Asking | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const newRoleId = useId();
  const owner = space.rank === "owner";
  const administers = space.rank !== "member";

  const load = useCallback(
    () =>
      Promise.all([SpaceRoles(space.id), SpaceMembers(space.id)]).then(
        ([r, m]) => {
          setRoles((r ?? []) as Role[]);
          setMembers((m ?? []) as Member[]);
        },
        (error: unknown) => setNotice(refused(words, error)),
      ),
    [space.id, words],
  );

  useEffect(() => {
    void load();
    return EventsOn("spaces", (changed: string) => {
      if (changed === space.id) void load();
    });
  }, [space.id, load]);

  async function attempt(work: () => Promise<unknown>) {
    setNotice(null);
    try {
      await work();
      await load();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  // moderates is whether this person's rank is above that of the member, as removing them needs (ADR 0038).
  const moderates = (m: Member) =>
    !m.me && (owner || (administers && m.rank === "member"));

  const section = "flex flex-col gap-2";
  const heading = "font-display text-lg font-semibold";
  const row =
    "flex flex-wrap items-center gap-2 rounded-md bg-surface-raised p-2";

  return (
    <div className="flex w-full max-w-2xl flex-col gap-8">
      <h2 className="font-display text-xl font-semibold">
        {words.spaces.settings(space.name)}
      </h2>

      {administers && (
        <section className={section}>
          <h3 className={heading}>{words.spaces.overview}</h3>
          <div className={row}>
            <Renamer
              key={space.name}
              label={words.spaces.name}
              name={space.name}
              onRename={(name) =>
                void attempt(() => RenameSpace(space.id, name))
              }
            />
          </div>
        </section>
      )}

      {administers && (
        <section className={section}>
          <h3 className={heading}>{words.spaces.channels}</h3>
          <ul className="flex flex-col gap-2">
            {space.channels.map((c) => (
              <li key={c.id} className={`${row} flex-col items-stretch`}>
                <div className="flex items-center gap-2">
                  <Renamer
                    key={c.name}
                    label={words.spaces.channelName}
                    name={c.name}
                    onRename={(name) =>
                      void attempt(() => RenameChannel(space.id, c.id, name))
                    }
                  />
                  <Button
                    size="small"
                    onClick={() =>
                      setAsking({
                        title: words.spaces.deleteChannel(c.name),
                        explanation: words.spaces.deleteChannelExplained,
                        confirm: words.spaces.delete,
                        work: () => DeleteChannel(space.id, c.id),
                      })
                    }
                  >
                    {words.spaces.delete}
                  </Button>
                </div>
                <fieldset className="flex flex-wrap items-center gap-3 px-1 text-sm">
                  <legend className="sr-only">
                    {words.spaces.whoSees(c.name)}
                  </legend>
                  <span className="text-ink-dim">{words.spaces.sees}</span>
                  <label className="flex items-center gap-1">
                    <input
                      type="checkbox"
                      checked={!c.restricted}
                      disabled={!c.restricted}
                      onChange={() =>
                        void attempt(() => SetChannelRoles(space.id, c.id, []))
                      }
                    />
                    {words.spaces.everyone}
                  </label>
                  {roles.map((r) => {
                    const holds = c.restricted && c.roles.includes(r.id);
                    return (
                      <label key={r.id} className="flex items-center gap-1">
                        <input
                          type="checkbox"
                          checked={holds}
                          onChange={() => {
                            const next = holds
                              ? c.roles.filter((x) => x !== r.id)
                              : [...(c.restricted ? c.roles : []), r.id];
                            void attempt(() =>
                              SetChannelRoles(space.id, c.id, next),
                            );
                          }}
                        />
                        {r.name}
                      </label>
                    );
                  })}
                </fieldset>
                <fieldset className="flex flex-wrap items-center gap-3 px-1 text-sm">
                  <legend className="sr-only">
                    {words.spaces.whoWrites(c.name)}
                  </legend>
                  <span className="text-ink-dim">{words.spaces.writes}</span>
                  <label className="flex items-center gap-1">
                    <input
                      type="checkbox"
                      checked={!c.guarded}
                      onChange={() =>
                        void attempt(() =>
                          SetChannelWriters(space.id, c.id, c.guarded, []),
                        )
                      }
                    />
                    {words.spaces.everyReader}
                  </label>
                  {roles.map((r) => {
                    const writes = c.guarded && c.writers.includes(r.id);
                    return (
                      <label key={r.id} className="flex items-center gap-1">
                        <input
                          type="checkbox"
                          checked={writes}
                          onChange={() => {
                            const next = writes
                              ? c.writers.filter((x) => x !== r.id)
                              : [...(c.guarded ? c.writers : []), r.id];
                            void attempt(() =>
                              SetChannelWriters(space.id, c.id, false, next),
                            );
                          }}
                        />
                        {r.name}
                      </label>
                    );
                  })}
                </fieldset>
              </li>
            ))}
          </ul>
        </section>
      )}

      {administers && (
        <section className={section}>
          <h3 className={heading}>{words.spaces.roles}</h3>
          <p className="text-sm text-ink-dim">{words.spaces.rolesExplained}</p>
          <ul className="flex flex-col gap-2">
            {roles.map((r) => (
              <li key={r.id} className={row}>
                <Renamer
                  key={r.name}
                  label={words.spaces.roleName}
                  name={r.name}
                  onRename={(name) =>
                    void attempt(() => RenameRole(space.id, r.id, name))
                  }
                />
                <Button
                  size="small"
                  onClick={() =>
                    setAsking({
                      title: words.spaces.deleteRole(r.name),
                      explanation: words.spaces.deleteRoleExplained,
                      confirm: words.spaces.delete,
                      work: () => DeleteRole(space.id, r.id),
                    })
                  }
                >
                  {words.spaces.delete}
                </Button>
              </li>
            ))}
          </ul>
          <form
            className="flex items-end gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              void attempt(async () => {
                await CreateRole(space.id, newRole);
                setNewRole("");
              });
            }}
          >
            <span className="flex flex-1 flex-col gap-1">
              <label htmlFor={newRoleId} className="text-[0.8rem] text-ink-dim">
                {words.spaces.newRole}
              </label>
              <input
                id={newRoleId}
                className={`${input} py-1.5 text-sm`}
                value={newRole}
                maxLength={100}
                onChange={(event) => setNewRole(event.target.value)}
              />
            </span>
            <Button size="small" type="submit" disabled={newRole.trim() === ""}>
              {words.spaces.addChannel}
            </Button>
          </form>
        </section>
      )}

      {administers && (
        <section className={section}>
          <h3 className={heading}>{words.spaces.members}</h3>
          <ul className="flex flex-col gap-2">
            {members.map((m) => (
              <li key={m.publicKey} className={`${row} flex-col items-stretch`}>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">
                    {m.name}{" "}
                    <span className="text-[0.75rem] font-normal text-ink-faint">
                      {words.spaces.one[m.rank]}
                    </span>
                  </span>
                  {owner && !m.me && m.rank !== "owner" && (
                    <Button
                      size="small"
                      onClick={() =>
                        void attempt(() =>
                          SetAdministrator(
                            space.id,
                            m.publicKey,
                            m.rank === "member",
                          ),
                        )
                      }
                    >
                      {m.rank === "member"
                        ? words.spaces.makeAdmin
                        : words.spaces.unmakeAdmin}
                    </Button>
                  )}
                  {owner && !m.me && (
                    <Button
                      size="small"
                      onClick={() =>
                        setAsking({
                          title: words.spaces.transfer(m.name),
                          explanation: words.spaces.transferExplained,
                          confirm: words.spaces.transferButton,
                          work: () => TransferOwnership(space.id, m.publicKey),
                        })
                      }
                    >
                      {words.spaces.transferButton}
                    </Button>
                  )}
                  {moderates(m) && (
                    <Button
                      size="small"
                      onClick={() =>
                        void attempt(() =>
                          MuteMember(space.id, m.publicKey, !m.muted),
                        )
                      }
                    >
                      {m.muted ? words.spaces.unmute : words.spaces.mute}
                    </Button>
                  )}
                  {moderates(m) && (
                    <Button
                      size="small"
                      onClick={() =>
                        setAsking({
                          title: words.spaces.remove(m.name),
                          explanation: words.spaces.removeExplained,
                          confirm: words.spaces.removeButton,
                          work: () => RemoveMember(space.id, m.publicKey),
                        })
                      }
                    >
                      {words.spaces.removeButton}
                    </Button>
                  )}
                </div>
                {roles.length > 0 && (
                  <fieldset className="flex flex-wrap items-center gap-3 px-1 text-sm">
                    <legend className="sr-only">
                      {words.spaces.rolesOf(m.name)}
                    </legend>
                    {roles.map((r) => {
                      const holds = m.roles.includes(r.name);
                      return (
                        <label key={r.id} className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={holds}
                            onChange={() =>
                              void attempt(() =>
                                SetMemberRole(
                                  space.id,
                                  m.publicKey,
                                  r.id,
                                  !holds,
                                ),
                              )
                            }
                          />
                          {r.name}
                        </label>
                      );
                    })}
                  </fieldset>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className={section}>
        <h3 className={heading}>{words.spaces.leaving}</h3>
        {owner ? (
          <div className="flex flex-col items-start gap-2">
            <p className="text-sm text-ink-dim">{words.spaces.ownerStays}</p>
            <Button
              onClick={() =>
                setAsking({
                  title: words.spaces.deleteSpace(space.name),
                  explanation: words.spaces.deleteSpaceExplained,
                  confirm: words.spaces.delete,
                  work: async () => {
                    await DeleteSpace(space.id);
                    onLeft();
                  },
                })
              }
            >
              {words.spaces.deleteSpaceButton}
            </Button>
          </div>
        ) : (
          <Button
            className="self-start"
            onClick={() =>
              setAsking({
                title: words.spaces.leave(space.name),
                explanation: words.spaces.leaveExplained,
                confirm: words.spaces.leaveButton,
                work: async () => {
                  await LeaveSpace(space.id);
                  onLeft();
                },
              })
            }
          >
            {words.spaces.leaveButton}
          </Button>
        )}
      </section>

      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      {asking && (
        <ConfirmDialog
          title={asking.title}
          explanation={asking.explanation}
          confirm={asking.confirm}
          onConfirm={() => {
            const work = asking.work;
            setAsking(null);
            void attempt(work);
          }}
          onClose={() => setAsking(null)}
        />
      )}
    </div>
  );
}
