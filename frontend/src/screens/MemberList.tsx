import { useCallback, useEffect, useState } from "react";
import { SpaceMembers } from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { useWords } from "../i18n/language";

export interface Member {
  publicKey: string;
  name: string;
  rank: "owner" | "administrator" | "member";
  roles: string[];
  me: boolean;
}

// MemberList is who belongs to a space, by rank, with the roles each holds (ADR 0038); it says nothing of who is online.
export function MemberList({ space }: { space: string }) {
  const words = useWords();
  const [members, setMembers] = useState<Member[]>([]);

  const load = useCallback(
    () =>
      SpaceMembers(space).then(
        (all) => setMembers((all ?? []) as Member[]),
        () => undefined,
      ),
    [space],
  );

  useEffect(() => {
    void load();
    return EventsOn("spaces", (changed: string) => {
      if (changed === space) void load();
    });
  }, [space, load]);

  const groups = (["owner", "administrator", "member"] as const)
    .map((rank) => ({
      rank,
      people: members.filter((m) => m.rank === rank),
    }))
    .filter((g) => g.people.length > 0);

  return (
    <aside
      aria-label={words.spaces.members}
      className="hidden w-60 shrink-0 flex-col gap-4 overflow-y-auto bg-surface-deep p-3 lg:flex"
    >
      {groups.map((g) => (
        <section key={g.rank} className="flex flex-col gap-1">
          <h3 className="px-2 text-[0.72rem] font-semibold tracking-wide text-ink-faint uppercase">
            {words.spaces.rank[g.rank]} — {g.people.length}
          </h3>
          <ul className="flex flex-col gap-0.5">
            {g.people.map((m) => (
              <li
                key={m.publicKey}
                className="flex flex-col rounded-sm px-2 py-1 hover:bg-surface-hover"
              >
                <span className="truncate text-sm">
                  {m.name}
                  {m.me && (
                    <span className="ml-1 text-[0.72rem] text-ink-faint">
                      ({words.workspace.you})
                    </span>
                  )}
                </span>
                {m.roles.length > 0 && (
                  <span className="flex flex-wrap gap-1 pt-0.5">
                    {m.roles.map((r) => (
                      <span
                        key={r}
                        className="rounded-sm bg-surface-raised px-1.5 text-[0.7rem] text-ink-dim"
                      >
                        {r}
                      </span>
                    ))}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </aside>
  );
}
