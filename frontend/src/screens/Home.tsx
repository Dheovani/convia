import { useEffect, useState } from "react";
import { Contacts as readContacts } from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Wordmark } from "../components/Mark";
import { useWords } from "../i18n/language";
import { Contacts, type Contact } from "./Contacts";
import { Conversation } from "./Conversation";
import { Me, type Identity } from "./Me";

interface Outdated {
  publicKey: string;
  theirs: boolean;
}

// Home is everything a person with an identity reaches, until the workspace of M12 replaces it.
export function Home({
  me,
  onChanged,
}: {
  me: Identity;
  onChanged: () => void;
}) {
  const words = useWords();
  const [tab, setTab] = useState<"contacts" | "you">("contacts");
  const [outdated, setOutdated] = useState<string | null>(null);
  const [talkingTo, setTalkingTo] = useState<Contact | null>(null);

  useEffect(
    () =>
      EventsOn("outdated", (o: Outdated) => {
        void readContacts()
          .catch(() => [])
          .then((contacts) => {
            const name =
              contacts.find((c) => c.publicKey === o.publicKey)?.name ??
              words.home.someone;
            setOutdated(
              o.theirs
                ? words.home.outdatedTheirs(name)
                : words.home.outdatedMine(name),
            );
          });
      }),
    [words],
  );

  const tabClass = (selected: boolean) =>
    `cursor-pointer rounded-md px-3 py-1.5 text-sm font-medium ${
      selected ? "bg-surface-raised" : "text-ink-dim hover:text-ink"
    }`;

  return (
    <main className="flex h-full flex-col items-center gap-6 overflow-y-auto bg-surface px-8 py-10 text-ink">
      <Wordmark />
      <nav className="flex gap-2">
        <button
          type="button"
          className={tabClass(tab === "contacts")}
          aria-pressed={tab === "contacts"}
          onClick={() => setTab("contacts")}
        >
          {words.home.contacts}
        </button>
        <button
          type="button"
          className={tabClass(tab === "you")}
          aria-pressed={tab === "you"}
          onClick={() => setTab("you")}
        >
          {words.home.you}
        </button>
      </nav>
      {outdated && (
        <p
          role="alert"
          className="max-w-lg rounded-md bg-surface-raised p-3 text-sm"
        >
          {outdated}
        </p>
      )}
      {tab === "contacts" && talkingTo ? (
        <Conversation contact={talkingTo} onBack={() => setTalkingTo(null)} />
      ) : tab === "contacts" ? (
        <Contacts onOpen={setTalkingTo} />
      ) : (
        <Me key={me.publicKey + me.name} me={me} onChanged={onChanged} />
      )}
    </main>
  );
}
