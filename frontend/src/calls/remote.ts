import { useEffect, useState } from "react";
import type { Track } from "../transport/connections";
import { transport } from "../transport/wails";

// What the other side of each connection sends, by stream: it may arrive before this side is ready for it.
export const remote = new Map<string, Map<string, MediaStream>>();

const listeners = new Set<() => void>();

const heard: Track = (id, track, streams) => {
  const known = remote.get(id) ?? new Map<string, MediaStream>();
  const stream = streams[0] ?? known.get("") ?? new MediaStream();
  if (!stream.getTracks().includes(track)) stream.addTrack(track);
  known.set(streams[0] ? stream.id : "", stream);
  remote.set(id, known);
  for (const listener of listeners) listener();
};

// useRemote keeps what the other sides send, and is a number that changes each time a track arrives.
export function useRemote(): number {
  const [count, setCount] = useState(0);
  useEffect(() => {
    const conns = transport();
    if (conns) conns.track = heard;
    const listener = () => setCount((n) => n + 1);
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);
  return count;
}

// voiceOf is the stream that carries the voice and camera of the other side of connection, apart from a screen.
export function voiceOf(
  connection: string,
  screen?: string,
): MediaStream | undefined {
  const streams = [...(remote.get(connection)?.values() ?? [])].filter(
    (s) => !screen || s.id !== screen,
  );
  return streams.find((s) => s.getAudioTracks().length > 0) ?? streams[0];
}

// cameraOf is the stream that carries a camera from the other side of connection.
export function cameraOf(connection: string): MediaStream | undefined {
  return [...(remote.get(connection)?.values() ?? [])].find(
    (s) => s.getVideoTracks().length > 0,
  );
}
