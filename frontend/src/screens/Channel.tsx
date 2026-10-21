import { useMemo } from "react";
import {
  ChannelMessages,
  DeleteInChannel,
  EditInChannel,
  ReactInChannel,
  SayInChannel,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { useWords } from "../i18n/language";
import { Thread, type Message, type Source } from "./Thread";

export interface ChannelInfo {
  id: string;
  name: string;
  restricted: boolean;
  // roles are the roles a restricted channel is open to.
  roles: string[];
  // guarded is a channel only its writers write in, and the administrators.
  guarded: boolean;
  writers: string[];
  writes: boolean;
}

// Channel is everything said in a channel of a space, and where to say more (ADR 0042).
export function Channel({
  space,
  channel,
  meName,
  muted,
}: {
  space: string;
  channel: ChannelInfo;
  meName: string;
  muted: boolean;
}) {
  const words = useWords();
  const { id, name } = channel;
  const source = useMemo<Source>(
    () => ({
      title: `#${name}`,
      header: (
        <h2 className="font-display font-semibold">
          <span aria-hidden="true" className="mr-1 text-ink-faint">
            #
          </span>
          {name}
        </h2>
      ),
      other: words.home.someone,
      fieldLabel: words.spaces.field(name),
      load: () =>
        ChannelMessages(space, id).then((all) => all as unknown as Message[]),
      subscribe: (changed) =>
        EventsOn("spaces", (changedSpace: string) => {
          if (changedSpace === space) changed();
        }),
      send: (text, replyTo) => SayInChannel(space, id, text, replyTo),
      react: (target, emoji, on) =>
        ReactInChannel(space, id, target, emoji, on),
      edit: (target, text) => EditInChannel(space, id, target, text),
      remove: (target) => DeleteInChannel(space, id, target),
      statuses: false,
      deleting: words.spaces.deleting,
      closed: muted
        ? words.spaces.youAreMuted
        : channel.writes
          ? undefined
          : words.spaces.readOnly,
    }),
    [space, id, name, muted, channel.writes, words],
  );
  return <Thread source={source} meName={meName} />;
}
