import { useCallback, useEffect, useRef, useState } from "react";
import { LeaveVoice, VoiceVideo } from "../../wailsjs/go/app/Bridge";
import { Button } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import { transport } from "../transport/wails";
import { camera, microphone, speak, trouble } from "./media";
import { useRemote, voiceOf } from "./remote";
import { inChannel, useVoice } from "./voice";

interface Place {
  id: string;
  name: string;
  channels: { id: string; name: string }[];
}

/*
VoiceBar is the voice channel this person is in, wherever they are in the window: it sends their microphone, and
their camera if on, to each other member there, plays those who speak, and says what is in use (ADR 0047).
*/
export function VoiceBar({ spaces }: { spaces: Place[] }) {
  const words = useWords();
  const { channels, seat, reload } = useVoice(undefined);
  const heard = useRemote();
  const [muted, setMuted] = useState(false);
  const [filming, setFilming] = useState(false);
  const [opened, setOpened] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  // What this side sends, and the connections it is sent on.
  const sending = useRef<MediaStream | null>(null);
  const sentTo = useRef(new Map<string, MediaStreamTrack[]>());

  const people = seat ? inChannel(channels, seat.channel) : [];
  const me = people.find((p) => p.me);
  const speaks = me?.speaks ?? false;
  const seatKey = seat ? `${seat.space}/${seat.channel}` : "";
  const wanted = people
    .filter((p) => !p.me && p.connection)
    .map((p) => p.connection as string)
    .join(" ");

  const stopAll = useCallback(() => {
    const conns = transport();
    for (const [connection, tracks] of sentTo.current) {
      for (const track of tracks) conns?.removeTrack(connection, track);
    }
    sentTo.current.clear();
    for (const track of sending.current?.getTracks() ?? []) track.stop();
    sending.current = null;
    setFilming(false);
  }, []);

  // A seat opens the microphone of one who may speak, and lets everything go once left.
  useEffect(() => {
    if (!seatKey) return;
    let gone = false;
    if (speaks) {
      Promise.resolve()
        .then(microphone)
        .then(
          (stream) => {
            if (gone) {
              for (const track of stream.getTracks()) track.stop();
              return;
            }
            sending.current = stream;
            setOpened((n) => n + 1);
          },
          (error: unknown) => {
            if (!gone)
              setNotice(
                words.calls.trouble[trouble(error)](
                  words.calls.devices.microphone,
                ),
              );
          },
        );
    }
    return () => {
      gone = true;
      stopAll();
      setMuted(false);
    };
  }, [seatKey, speaks, stopAll, words]);

  // What is sent goes on each connection to the others in the channel, and off those no longer there.
  useEffect(() => {
    const conns = transport();
    const stream = sending.current;
    const now = new Set(wanted ? wanted.split(" ") : []);
    for (const [connection, tracks] of sentTo.current) {
      if (now.has(connection)) continue;
      for (const track of tracks) conns?.removeTrack(connection, track);
      sentTo.current.delete(connection);
    }
    if (!stream) return;
    for (const connection of now) {
      if (sentTo.current.has(connection)) continue;
      const tracks = stream.getTracks();
      for (const track of tracks) conns?.addTrack(connection, track, stream);
      sentTo.current.set(connection, [...tracks]);
    }
  }, [wanted, opened]);

  if (!seat) return null;

  const place = spaces.find((s) => s.id === seat.space);
  const channelName =
    place?.channels.find((c) => c.id === seat.channel)?.name ?? "";
  const speaking = people.filter((p) => !p.me && p.speaks && p.connection);

  async function attempt(work: () => Promise<unknown>) {
    setNotice(null);
    try {
      await work();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  function toggleMute() {
    const next = !muted;
    for (const track of sending.current?.getAudioTracks() ?? [])
      track.enabled = !next;
    setMuted(next);
  }

  async function toggleCamera() {
    const conns = transport();
    const stream = sending.current;
    if (!stream) return;
    if (filming) {
      for (const track of stream.getVideoTracks()) {
        for (const [connection, tracks] of sentTo.current) {
          conns?.removeTrack(connection, track);
          sentTo.current.set(
            connection,
            tracks.filter((t) => t !== track),
          );
        }
        track.stop();
        stream.removeTrack(track);
      }
      setFilming(false);
      await attempt(() => VoiceVideo(false));
      return;
    }

    try {
      await VoiceVideo(true);
    } catch (error) {
      setNotice(refused(words, error));
      return;
    }
    try {
      const video = await camera();
      for (const track of video.getVideoTracks()) {
        stream.addTrack(track);
        for (const [connection, tracks] of sentTo.current) {
          conns?.addTrack(connection, track, stream);
          sentTo.current.set(connection, [...tracks, track]);
        }
      }
      setFilming(true);
    } catch (error) {
      setNotice(
        words.calls.trouble[trouble(error)](words.calls.devices.camera),
      );
      void VoiceVideo(false).catch(() => undefined);
    }
  }

  return (
    <section
      aria-label={words.voice.bar}
      className="flex flex-wrap items-center gap-3 border-b border-line bg-surface-deep px-4 py-2"
    >
      <span className="min-w-0 flex-1 truncate text-sm font-medium">
        {words.voice.inChannel(channelName, place?.name ?? "")}
      </span>
      {speaks ? (
        <>
          {/* What is in use is said for as long as it is: no prompt guards it (docs/platform/media-permissions.md). */}
          <span className="text-[0.75rem] text-danger" role="status">
            {words.calls.inUse(!muted, filming, "")}
          </span>
          <Button size="small" aria-pressed={muted} onClick={toggleMute}>
            {muted ? words.calls.unmute : words.calls.mute}
          </Button>
          <Button
            size="small"
            aria-pressed={filming}
            onClick={() => void toggleCamera()}
          >
            {filming ? words.calls.cameraOff : words.calls.cameraOn}
          </Button>
        </>
      ) : (
        <span className="text-[0.75rem] text-ink-dim">
          {words.voice.listeningOnly}
        </span>
      )}
      <Button
        size="small"
        onClick={() =>
          void attempt(async () => {
            await LeaveVoice();
            await reload();
          })
        }
      >
        {words.voice.leave}
      </Button>
      {notice && (
        <p role="alert" className="w-full text-sm">
          {notice}
        </p>
      )}
      {/* Only those who may speak are heard (ADR 0047). */}
      {speaking.map((p) => (
        <audio
          key={p.publicKey}
          data-heard={heard}
          autoPlay
          ref={(element) => {
            const stream = voiceOf(p.connection as string);
            if (element && stream && element.srcObject !== stream) {
              element.srcObject = stream;
              void speak(element);
            }
          }}
        />
      ))}
    </section>
  );
}
