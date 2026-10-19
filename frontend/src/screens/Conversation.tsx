import { useMemo } from "react";
import {
  React as react,
  ChooseFile,
  Conversation as readConversation,
  DeleteMessage,
  EditMessage,
  ForgetMessage,
  SendFile,
  SendMessage,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { useWords } from "../i18n/language";
import { Thread, type Message, type Source } from "./Thread";

export {
  MaxCharacters,
  offered,
  type Message,
  type Quote,
  type Reaction,
} from "./Thread";

export interface Person {
  publicKey: string;
  name: string;
  verified: boolean;
}

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
  const { publicKey, name, verified } = contact;
  const source = useMemo<Source>(
    () => ({
      title: name,
      header: (
        <>
          <h2 className="font-display font-semibold">{name}</h2>
          {verified && (
            <span className="rounded-sm bg-accent-soft px-1.5 text-[0.72rem] text-accent-ink">
              {words.workspace.verified}
            </span>
          )}
        </>
      ),
      other: name,
      fieldLabel: words.conversation.field(name),
      load: () =>
        readConversation(publicKey).then((all) => all as unknown as Message[]),
      subscribe: (changed) =>
        EventsOn("conversation", (key: string) => {
          if (key === publicKey) changed();
        }),
      send: (text, replyTo) => SendMessage(publicKey, text, replyTo),
      react: (id, emoji, on) => react(publicKey, id, emoji, on),
      edit: (id, text) => EditMessage(publicKey, id, text),
      remove: (id) => DeleteMessage(publicKey, id),
      forget: (id) => ForgetMessage(publicKey, id),
      attach: async () => {
        const path = await ChooseFile();
        if (path) await SendFile(publicKey, path);
      },
      statuses: true,
    }),
    [publicKey, name, verified, words],
  );
  return <Thread source={source} meName={meName} focus={focus} />;
}
