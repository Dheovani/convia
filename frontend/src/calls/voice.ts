import { useCallback, useEffect, useState } from "react";
import { Voice, VoiceSeat } from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";

export interface Participant {
  publicKey: string;
  name: string;
  video: boolean;
  // speaks is whether the others hear them: a member muted, or who may not write in the channel, only listens.
  speaks: boolean;
  me: boolean;
  connection?: string;
}

export interface VoiceChannel {
  channel: string;
  participants: Participant[];
}

export interface Seat {
  space: string;
  channel: string;
}

/*
useVoice is who is in each voice channel of space, and the voice channel this person is in, kept current: a
connection to somebody in the channel shows once it is made, so they are asked for again while this person is in one.
*/
export function useVoice(space: string | undefined) {
  const [channels, setChannels] = useState<VoiceChannel[]>([]);
  const [seat, setSeat] = useState<Seat | null>(null);

  const load = useCallback(
    () =>
      VoiceSeat()
        .then(async (found) => {
          const sat = (found as Seat | null) ?? null;
          const of = space ?? sat?.space;
          const all = of ? ((await Voice(of)) as VoiceChannel[]) : [];
          setSeat(sat);
          setChannels(all ?? []);
        })
        .catch(() => setChannels([])),
    [space],
  );

  useEffect(() => {
    void load();
    return EventsOn("voice", () => void load());
  }, [load]);

  const seated = seat !== null;
  useEffect(() => {
    if (!seated) return;
    const t = window.setInterval(() => void load(), 2000);
    return () => window.clearInterval(t);
  }, [seated, load]);

  return { channels, seat, reload: load };
}

// inChannel are the participants of channel.
export function inChannel(
  channels: VoiceChannel[],
  channel: string,
): Participant[] {
  return channels.find((c) => c.channel === channel)?.participants ?? [];
}
