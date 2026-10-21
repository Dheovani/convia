import { rememberedDevice } from "../state/preferences";

// Trouble is why a device could not be opened, in words the interface has a sentence for.
export type Trouble = "blocked" | "missing" | "busy" | "unknown";

// trouble names what went wrong opening a device (docs/platform/media-permissions.md).
export function trouble(error: unknown): Trouble {
  const name = error instanceof DOMException ? error.name : "";
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "blocked";
    case "NotFoundError":
    case "OverconstrainedError":
      return "missing";
    case "NotReadableError":
    case "AbortError":
      return "busy";
  }
  return "unknown";
}

const chosen = (id: string): MediaTrackConstraints | boolean =>
  id ? { deviceId: { exact: id } } : true;

// microphone opens the microphone chosen for calls, or the system's own.
export function microphone(): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({
    audio: chosen(rememberedDevice("microphone")),
  });
}

// camera opens the camera chosen for calls, or the system's own.
export function camera(): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({
    video: chosen(rememberedDevice("camera")),
  });
}

// devices are the microphones, cameras and speakers this machine has, named once a call let them be.
export async function devices(): Promise<MediaDeviceInfo[]> {
  if (!navigator.mediaDevices?.enumerateDevices) return [];
  return navigator.mediaDevices.enumerateDevices();
}

// speak plays element through the speaker chosen for calls, where the browser lets it.
export async function speak(element: HTMLMediaElement): Promise<void> {
  const id = rememberedDevice("speaker");
  const sink = element as HTMLMediaElement & {
    setSinkId?: (id: string) => Promise<void>;
  };
  if (id && sink.setSinkId) await sink.setSinkId(id).catch(() => undefined);
}

/*
ring plays the sound of a call ringing until what it returns is called: a tone made here, so that Convia ships no
sound of its own to load.
*/
export function ring(): () => void {
  const Context = window.AudioContext as typeof AudioContext | undefined;
  if (!Context) return () => undefined;
  const context = new Context();
  const gain = context.createGain();
  gain.gain.value = 0;
  gain.connect(context.destination);
  const tone = context.createOscillator();
  tone.frequency.value = 440;
  tone.connect(gain);
  tone.start();
  // Two short tones, then a pause, as a telephone rings.
  const beat = window.setInterval(() => {
    const now = context.currentTime;
    gain.gain.setValueAtTime(0.15, now);
    gain.gain.setValueAtTime(0, now + 0.4);
    gain.gain.setValueAtTime(0.15, now + 0.6);
    gain.gain.setValueAtTime(0, now + 1);
  }, 3000);
  return () => {
    window.clearInterval(beat);
    tone.stop();
    void context.close();
  };
}
