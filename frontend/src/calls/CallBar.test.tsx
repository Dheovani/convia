import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { emit, resetEvents } from "../test/runtime";
import { CallBar } from "./CallBar";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);
vi.mock(
  "../../wailsjs/runtime/runtime",
  async () => (await import("../test/runtime")).runtime,
);

class FakeTrack {
  enabled = true;
  stopped = false;
  ended?: () => void;
  constructor(readonly kind: string) {}
  stop() {
    this.stopped = true;
  }
  addEventListener(_: "ended", listener: () => void) {
    this.ended = listener;
  }
}

let streams = 0;

class FakeStream {
  tracks: FakeTrack[];
  id = `stream${++streams}`;
  constructor(tracks: FakeTrack[] = []) {
    this.tracks = [...tracks];
  }
  getTracks() {
    return this.tracks;
  }
  getAudioTracks() {
    return this.tracks.filter((t) => t.kind === "audio");
  }
  getVideoTracks() {
    return this.tracks.filter((t) => t.kind === "video");
  }
  addTrack(t: FakeTrack) {
    this.tracks.push(t);
  }
  removeTrack(t: FakeTrack) {
    this.tracks = this.tracks.filter((x) => x !== t);
  }
}

const sent: string[] = [];
const fake = {
  track: undefined as
    ((id: string, track: FakeTrack, streams: FakeStream[]) => void) | undefined,
  addTrack: (id: string, t: FakeTrack) => sent.push(`add ${id} ${t.kind}`),
  removeTrack: (id: string, t: FakeTrack) =>
    sent.push(`remove ${id} ${t.kind}`),
};
vi.mock("../transport/wails", () => ({ transport: () => fake }));

let opened: FakeTrack[];
let refuse: string | null;
let picked: DisplayMediaStreamOptions[];

beforeEach(() => {
  reset();
  resetEvents();
  sent.length = 0;
  opened = [];
  refuse = null;
  picked = [];
  vi.stubGlobal("MediaStream", FakeStream);
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: {
      getUserMedia: (c: MediaStreamConstraints) => {
        if (refuse) return Promise.reject(new DOMException("no", refuse));
        const track = new FakeTrack(c.audio ? "audio" : "video");
        opened.push(track);
        return Promise.resolve(new FakeStream([track]));
      },
      getDisplayMedia: (o: DisplayMediaStreamOptions) => {
        picked.push(o);
        if (refuse) return Promise.reject(new DOMException("no", refuse));
        const tracks = [new FakeTrack("video")];
        if (o.audio) tracks.push(new FakeTrack("audio"));
        opened.push(...tracks);
        return Promise.resolve(new FakeStream(tracks));
      },
    },
  });
  answers.CallConnection = () => Promise.resolve("c1");
  answers.CurrentCall = () => Promise.resolve(null);
});

afterEach(() => vi.unstubAllGlobals());

const ana = { publicKey: "aa", name: "Ana" };
const call = (over: Record<string, unknown>) => ({
  id: "01",
  publicKey: "aa",
  incoming: false,
  video: false,
  state: "ringing",
  since: Date.now(),
  ...over,
});

function open() {
  render(<CallBar people={[ana]} />);
}

test("a call ringing for this person is answered or declined, and told of when hidden", async () => {
  answers.CurrentCall = () => Promise.resolve(call({ incoming: true }));
  open();
  expect(await screen.findByText("Ana is calling")).toBeTruthy();
  await waitFor(() =>
    expect(calls.Notify).toEqual([["aa", "Ana is calling", ""]]),
  );
  fireEvent.click(screen.getByRole("button", { name: "Answer" }));
  await waitFor(() => expect(calls.AcceptCall).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "Decline" }));
  await waitFor(() => expect(calls.DeclineCall).toHaveLength(1));
});

test("a call this person makes rings until cancelled, and says how it ended", async () => {
  open();
  emit("call", call({}));
  expect(await screen.findByText("Calling Ana…")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(calls.HangUp).toHaveLength(1));
  emit("call", call({ state: "ended", outcome: "unanswered" }));
  expect(await screen.findByText("Nobody answered.")).toBeTruthy();
});

test("a call in progress sends the microphone, mutes it, and turns the camera on and off", async () => {
  open();
  emit("call", call({ state: "active" }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio"]));
  expect(screen.getByText("Microphone on")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Mute" }));
  expect(opened[0].enabled).toBe(false);
  expect(screen.getByText("Microphone muted")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Unmute" }));
  expect(opened[0].enabled).toBe(true);

  fireEvent.click(screen.getByRole("button", { name: "Turn camera on" }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio", "add c1 video"]));
  expect(await screen.findByText("Microphone on · camera on")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Turn camera off" }));
  expect(sent.at(-1)).toBe("remove c1 video");
  expect(opened[1].stopped).toBe(true);

  // Hung up, nothing is sent any more, and every device is let go.
  emit("call", call({ state: "ended", outcome: "ended" }));
  await waitFor(() => expect(sent.at(-1)).toBe("remove c1 audio"));
  expect(opened[0].stopped).toBe(true);
  fireEvent.click(document.body);
  expect(screen.queryByText("Microphone on")).toBeNull();
});

test("a video call sends the camera from the start", async () => {
  open();
  emit("call", call({ state: "active", video: true }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio", "add c1 video"]));
});

test("a microphone Windows does not let Convia use is said so, with where to allow it", async () => {
  refuse = "NotAllowedError";
  open();
  emit("call", call({ state: "active" }));
  expect(
    await screen.findByText(
      "Windows does not let Convia use the microphone: allow desktop apps in Settings › Privacy & security.",
    ),
  ).toBeTruthy();
});

test("a screen is shared with the computer's sound if chosen, and stops from the interface or the system", async () => {
  open();
  emit("call", call({ state: "active" }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio"]));

  fireEvent.click(screen.getByLabelText("With the computer's sound"));
  fireEvent.click(screen.getByRole("button", { name: "Share screen" }));
  await waitFor(() => expect(calls.ShareScreen).toHaveLength(1));
  expect(picked[0].audio).toBe(true);
  expect(sent.slice(1)).toEqual(["add c1 video", "add c1 audio"]);
  const stream = calls.ShareScreen[0][0];
  expect(stream).toMatch(/^stream/);
  expect(
    screen.getByText("Microphone on · screen and sound shared"),
  ).toBeTruthy();
  expect(screen.getByLabelText("With the computer's sound")).toHaveProperty(
    "disabled",
    true,
  );

  // The system's own control stops it.
  opened[1].ended?.();
  await waitFor(() => expect(calls.ShareScreen.at(-1)).toEqual([""]));
  expect(sent.slice(3)).toEqual(["remove c1 video", "remove c1 audio"]);
  expect(opened[1].stopped && opened[2].stopped).toBe(true);

  // Without sound, and stopped from the interface.
  fireEvent.click(screen.getByLabelText("With the computer's sound"));
  fireEvent.click(screen.getByRole("button", { name: "Share screen" }));
  await waitFor(() => expect(calls.ShareScreen).toHaveLength(3));
  expect(picked[1].audio).toBe(false);
  expect(screen.getByText("Microphone on · screen shared")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Stop sharing" }));
  await waitFor(() => expect(calls.ShareScreen.at(-1)).toEqual([""]));
  expect(sent.at(-1)).toBe("remove c1 video");
});

test("closing the system's picker shares nothing and says nothing", async () => {
  open();
  emit("call", call({ state: "active" }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio"]));
  refuse = "NotAllowedError";
  fireEvent.click(screen.getByRole("button", { name: "Share screen" }));
  await waitFor(() => expect(picked).toHaveLength(1));
  expect(calls.ShareScreen ?? []).toHaveLength(0);
  expect(screen.queryByRole("alert")).toBeNull();
});

test("the other side's screen is shown apart from their camera", async () => {
  open();
  emit("call", call({ state: "active" }));
  await waitFor(() => expect(sent).toEqual(["add c1 audio"]));
  const voice = new FakeStream();
  const shown = new FakeStream();
  fake.track?.("c1", new FakeTrack("audio"), [voice]);
  fake.track?.("c1", new FakeTrack("video"), [shown]);
  const theirScreen = screen.getByLabelText("Ana's screen");
  expect(theirScreen.className).toContain("hidden");

  emit("call", call({ state: "active", screen: shown.id }));
  await waitFor(() => expect(theirScreen.className).not.toContain("hidden"));
  expect((theirScreen as HTMLVideoElement).srcObject).toBe(shown);
  expect(
    (screen.getByLabelText("Ana's camera") as HTMLVideoElement).srcObject,
  ).toBe(voice);

  emit("call", call({ state: "active" }));
  await waitFor(() => expect(theirScreen.className).toContain("hidden"));
});
