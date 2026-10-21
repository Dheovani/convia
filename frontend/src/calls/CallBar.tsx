import { useCallback, useEffect, useRef, useState } from "react";
import {
  AcceptCall,
  CallConnection,
  CurrentCall,
  DeclineCall,
  HangUp,
  Notify,
} from "../../wailsjs/go/app/Bridge";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button } from "../components/controls";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";
import { transport } from "../transport/wails";
import {
  camera,
  microphone,
  ring,
  speak,
  trouble,
  type Trouble,
} from "./media";

export interface Call {
  id: string;
  publicKey: string;
  incoming: boolean;
  video: boolean;
  state: "ringing" | "active" | "ended";
  outcome?: string;
  since: number;
}

interface Someone {
  publicKey: string;
  name: string;
}

// What the other side sends, by connection: it may arrive before this side's call is in progress.
const remote = new Map<string, MediaStream>();

function attentive(): boolean {
  return document.visibilityState === "visible" && document.hasFocus();
}

function elapsed(since: number, now: number): string {
  const seconds = Math.max(0, Math.floor((now - since) / 1000));
  const m = Math.floor(seconds / 60);
  return `${m}:${String(seconds % 60).padStart(2, "0")}`;
}

/*
CallBar is the call ringing or in progress, wherever the person is in the window: who it is with, and while it
lasts, that the microphone and the camera are in use (docs/platform/media-permissions.md).
*/
export function CallBar({ people }: { people: Someone[] }) {
  const words = useWords();
  const [call, setCall] = useState<Call | null>(null);
  const [ended, setEnded] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [muted, setMuted] = useState(false);
  const [filming, setFilming] = useState(false);
  const [heard, setHeard] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [conn, setConn] = useState("");
  // What this side sends, made once a call needs it.
  const sending = useRef<MediaStream | null>(null);
  const local = () => (sending.current ??= new MediaStream());
  const connection = useRef("");
  const theirVideo = useRef<HTMLVideoElement>(null);
  const theirVoice = useRef<HTMLAudioElement>(null);
  const myVideo = useRef<HTMLVideoElement>(null);
  const name = (key: string) =>
    people.find((p) => p.publicKey === key)?.name ?? words.home.someone;

  useEffect(() => {
    const conns = transport();
    if (!conns) return;
    conns.track = (id, track, streams) => {
      const stream = streams[0] ?? remote.get(id) ?? new MediaStream();
      if (!stream.getTracks().includes(track)) stream.addTrack(track);
      remote.set(id, stream);
      setHeard((n) => n + 1);
    };
    return () => {
      conns.track = undefined;
    };
  }, []);

  useEffect(() => {
    // What a step of a call says is newer than what was asked for before it.
    let told = false;
    void CurrentCall().then(
      (c) => {
        if (!told) setCall((c as Call | null) ?? null);
      },
      () => undefined,
    );
    return EventsOn("call", (c: Call) => {
      told = true;
      if (c.state === "ended") {
        setCall(null);
        setEnded(c.outcome ?? "ended");
      } else {
        setCall((was) => {
          if (c.state === "active" && was?.state !== "active") {
            setNotice(null);
            setMuted(false);
          }
          return c;
        });
        setEnded(null);
      }
    });
  }, []);

  // A call ringing for this person rings, and is told of by name alone when the window is hidden (ADR 0044).
  const ringing = call?.state === "ringing" && call.incoming;
  const caller = call?.publicKey ?? "";

  useEffect(() => {
    if (!ringing) return;
    if (!attentive()) {
      void Notify(caller, words.calls.notification(name(caller)), "");
    }
    return ring();
    // name is read once, as the call starts ringing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ringing, caller]);

  const stopAll = useCallback(() => {
    const conns = transport();
    for (const track of sending.current?.getTracks() ?? []) {
      if (connection.current) conns?.removeTrack(connection.current, track);
      track.stop();
      local().removeTrack(track);
    }
  }, []);

  const send = useCallback(async (stream: MediaStream) => {
    const conns = transport();
    for (const track of stream.getTracks()) {
      local().addTrack(track);
      if (connection.current)
        conns?.addTrack(connection.current, track, local());
    }
    if (myVideo.current) myVideo.current.srcObject = local();
  }, []);

  const failed = useCallback(
    (error: unknown, what: "microphone" | "camera") => {
      const why: Trouble = trouble(error);
      setNotice(words.calls.trouble[why](words.calls.devices[what]));
    },
    [words],
  );

  // A call in progress sends the microphone, and the camera if the caller turned theirs on.
  const active = call?.state === "active";
  const id = call?.id ?? "";
  const video = call?.video ?? false;

  useEffect(() => {
    if (!active) return;
    let gone = false;
    void (async () => {
      connection.current = await CallConnection();
      setConn(connection.current);
      if (gone) return;
      try {
        await send(await microphone());
      } catch (error) {
        failed(error, "microphone");
      }
      if (video && !gone) {
        try {
          await send(await camera());
          setFilming(true);
        } catch (error) {
          failed(error, "camera");
        }
      }
    })();

    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      gone = true;
      window.clearInterval(tick);
      stopAll();
      setFilming(false);
      connection.current = "";
      setConn("");
    };
  }, [active, id, video, send, stopAll, failed]);

  useEffect(() => {
    const stream = remote.get(conn);
    if (!active || !stream) return;
    if (theirVideo.current) theirVideo.current.srcObject = stream;
    if (theirVoice.current) {
      theirVoice.current.srcObject = stream;
      void speak(theirVoice.current);
    }
  }, [active, heard, conn]);

  // An ended call says so a moment, then gives way.
  useEffect(() => {
    if (!ended) return;
    const t = window.setTimeout(() => setEnded(null), 4000);
    return () => window.clearTimeout(t);
  }, [ended]);

  async function attempt(work: () => Promise<unknown>) {
    try {
      await work();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  function toggleMute() {
    const next = !muted;
    for (const track of local().getAudioTracks()) track.enabled = !next;
    setMuted(next);
  }

  async function toggleCamera() {
    const conns = transport();
    if (filming) {
      for (const track of local().getVideoTracks()) {
        if (connection.current) conns?.removeTrack(connection.current, track);
        track.stop();
        local().removeTrack(track);
      }
      setFilming(false);
      return;
    }

    try {
      await send(await camera());
      setFilming(true);
    } catch (error) {
      failed(error, "camera");
    }
  }

  if (!call) {
    return ended && ended !== "ended" ? (
      <p role="status" className="bg-surface-raised px-4 py-2 text-sm">
        {words.calls.outcomes[ended as keyof typeof words.calls.outcomes] ??
          words.calls.outcomes.ended}
      </p>
    ) : null;
  }

  const who = name(call.publicKey);
  const theirs = remote.get(conn);
  const theyFilm = (theirs?.getVideoTracks().length ?? 0) > 0;

  return (
    <section
      aria-label={words.calls.title(who)}
      className="flex flex-col gap-3 border-b border-line bg-surface-deep px-4 py-3"
    >
      <div className="flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-1 truncate font-medium">
          {call.state === "ringing"
            ? call.incoming
              ? words.calls.incoming(who)
              : words.calls.outgoing(who)
            : who}
        </span>
        {call.state === "active" && (
          <>
            <span className="text-sm text-ink-dim tabular-nums" aria-live="off">
              {elapsed(call.since, now)}
            </span>
            {/* What is in use is said for as long as it is: no prompt guards it (docs/platform/media-permissions.md). */}
            <span className="text-[0.75rem] text-danger" role="status">
              {words.calls.inUse(!muted, filming)}
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
        )}
        {ringing ? (
          <>
            <Button size="small" onClick={() => void attempt(DeclineCall)}>
              {words.calls.decline}
            </Button>
            <Button
              size="small"
              tone="primary"
              onClick={() => void attempt(AcceptCall)}
            >
              {words.calls.accept}
            </Button>
          </>
        ) : (
          <Button size="small" onClick={() => void attempt(HangUp)}>
            {call.state === "ringing" ? words.calls.cancel : words.calls.hangUp}
          </Button>
        )}
      </div>
      {call.state === "active" && (
        <div className="flex gap-3">
          <video
            ref={theirVideo}
            autoPlay
            playsInline
            muted
            aria-label={words.calls.theirVideo(who)}
            className={`max-h-72 rounded-md bg-black ${theyFilm ? "" : "hidden"}`}
          />
          <video
            ref={myVideo}
            autoPlay
            playsInline
            muted
            aria-label={words.calls.myVideo}
            className={`max-h-32 rounded-md bg-black ${filming ? "" : "hidden"}`}
          />
          <audio ref={theirVoice} autoPlay />
        </div>
      )}
      {notice && (
        <p role="alert" className="text-sm">
          {notice}
        </p>
      )}
    </section>
  );
}
