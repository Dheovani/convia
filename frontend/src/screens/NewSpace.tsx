import { useCallback, useEffect, useState } from "react";
import {
  AcceptSpace,
  CreateSpace,
  DeclineSpace,
  SpaceInvitations,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button, Field } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";

interface Invitation {
  id: string;
  name: string;
  inviter: string;
}

// NewSpace is where a person makes a space, and answers the invitations to others (ADR 0039).
export function NewSpace({ onOpen }: { onOpen: (space: string) => void }) {
  const words = useWords();
  const [name, setName] = useState("");
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(
    () =>
      SpaceInvitations().then(
        (all) => setInvitations((all ?? []) as Invitation[]),
        (error: unknown) => setNotice(refused(words, error)),
      ),
    [words],
  );

  useEffect(() => {
    void load();
    return EventsOn("spaces", () => void load());
  }, [load]);

  async function create() {
    setNotice(null);
    try {
      const space = await CreateSpace(name, words.spaces.firstChannel);
      setName("");
      if (space) onOpen(space.id);
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function answer(id: string, accept: boolean) {
    setNotice(null);
    try {
      await (accept ? AcceptSpace(id) : DeclineSpace(id));
      await load();
      if (accept) onOpen(id);
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  return (
    <div className="flex w-full max-w-md flex-col gap-6">
      <section className="flex flex-col gap-3">
        <h2 className="font-display text-xl font-semibold">
          {words.spaces.create}
        </h2>
        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            void create();
          }}
        >
          <Field
            label={words.spaces.name}
            value={name}
            maxLength={100}
            onChange={(event) => setName(event.target.value)}
          />
          <Button tone="primary" type="submit" disabled={name.trim() === ""}>
            {words.spaces.createButton}
          </Button>
        </form>
      </section>
      <section className="flex flex-col gap-2">
        <h2 className="font-display text-xl font-semibold">
          {words.spaces.invitations}
        </h2>
        {invitations.length === 0 && (
          <p className="text-sm text-ink-dim">{words.spaces.noInvitations}</p>
        )}
        <ul className="flex flex-col gap-2">
          {invitations.map((i) => (
            <li
              key={i.id}
              className="flex items-center justify-between gap-2 rounded-md bg-surface-raised p-3"
            >
              <span className="min-w-0 truncate text-sm">
                {words.spaces.invitedBy(i.name, i.inviter)}
              </span>
              <span className="flex shrink-0 gap-2">
                <Button size="small" onClick={() => void answer(i.id, false)}>
                  {words.spaces.decline}
                </Button>
                <Button
                  size="small"
                  tone="primary"
                  onClick={() => void answer(i.id, true)}
                >
                  {words.spaces.accept}
                </Button>
              </span>
            </li>
          ))}
        </ul>
      </section>
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
    </div>
  );
}
