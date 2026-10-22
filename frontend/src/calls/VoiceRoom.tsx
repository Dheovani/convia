import { useState } from "react";
import { JoinVoice } from "../../wailsjs/go/app/Bridge";
import { Button } from "../components/controls";
import { SpeakerIcon } from "../components/Icons";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import { cameraOf, useRemote } from "./remote";
import { inChannel, useVoice } from "./voice";

// VoiceRoom is a voice channel: who is in it, their cameras, and where to join it (ADR 0047).
export function VoiceRoom({
  space,
  channel,
}: {
  space: string;
  channel: { id: string; name: string };
}) {
  const words = useWords();
  const { channels, seat, reload } = useVoice(space);
  useRemote();
  const [notice, setNotice] = useState<string | null>(null);
  const people = inChannel(channels, channel.id);
  const here = seat?.space === space && seat.channel === channel.id;

  async function join() {
    setNotice(null);
    try {
      await JoinVoice(space, channel.id, false);
      await reload();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  return (
    <section
      aria-label={words.voice.title(channel.name)}
      className="flex flex-1 flex-col gap-4 overflow-y-auto p-6"
    >
      <header className="flex items-center gap-2">
        <span className="text-ink-faint">
          <SpeakerIcon />
        </span>
        <h2 className="flex-1 font-display font-semibold">{channel.name}</h2>
        {!here && (
          <Button tone="primary" onClick={() => void join()}>
            {words.voice.join}
          </Button>
        )}
      </header>
      <p className="text-sm text-ink-dim">{words.voice.limits}</p>
      {notice && (
        <p role="alert" className="text-sm">
          {notice}
        </p>
      )}
      {people.length === 0 ? (
        <p className="text-sm text-ink-dim">{words.voice.nobody}</p>
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-3">
          {people.map((p) => {
            const stream =
              p.video && p.connection ? cameraOf(p.connection) : undefined;
            return (
              <li
                key={p.publicKey}
                className="flex flex-col gap-2 rounded-md bg-surface-raised p-3"
              >
                {stream && (
                  <video
                    autoPlay
                    playsInline
                    muted
                    aria-label={words.calls.theirVideo(p.name)}
                    className="aspect-video w-full rounded-sm bg-black"
                    ref={(element) => {
                      if (element && element.srcObject !== stream)
                        element.srcObject = stream;
                    }}
                  />
                )}
                <span className="truncate text-sm font-medium">
                  {p.name || words.home.someone}
                  {p.me && ` (${words.voice.you})`}
                </span>
                <span className="text-[0.75rem] text-ink-dim">
                  {[
                    p.speaks ? "" : words.voice.listening,
                    p.video ? words.voice.camera : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
