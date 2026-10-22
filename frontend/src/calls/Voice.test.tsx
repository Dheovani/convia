import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { emit, resetEvents } from "../test/runtime";
import { VoiceBar } from "./VoiceBar";
import { VoiceRoom } from "./VoiceRoom";

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
  constructor(readonly kind: string) {}
  stop() {
    this.stopped = true;
  }
}

class FakeStream {
  tracks: FakeTrack[];
  id = "local";
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
  track: undefined as unknown,
  addTrack: (id: string, t: FakeTrack) => sent.push(`add ${id} ${t.kind}`),
  removeTrack: (id: string, t: FakeTrack) =>
    sent.push(`remove ${id} ${t.kind}`),
};
vi.mock("../transport/wails", () => ({ transport: () => fake }));

let opened: FakeTrack[];

beforeEach(() => {
  reset();
  resetEvents();
  sent.length = 0;
  opened = [];
  vi.stubGlobal("MediaStream", FakeStream);
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: {
      getUserMedia: (c: MediaStreamConstraints) => {
        const track = new FakeTrack(c.audio ? "audio" : "video");
        opened.push(track);
        return Promise.resolve(new FakeStream([track]));
      },
    },
  });
});

afterEach(() => vi.unstubAllGlobals());

const me = {
  publicKey: "aa",
  name: "Ana",
  video: false,
  speaks: true,
  me: true,
};
const bruno = {
  publicKey: "bb",
  name: "Bruno",
  video: false,
  speaks: true,
  me: false,
  connection: "c1",
};
const carla = {
  publicKey: "cc",
  name: "Carla",
  video: false,
  speaks: false,
  me: false,
  connection: "c2",
};
const spaces = [
  { id: "s1", name: "Casa", channels: [{ id: "v1", name: "sala" }] },
];

function seated(...participants: object[]) {
  answers.VoiceSeat = () => Promise.resolve({ space: "s1", channel: "v1" });
  answers.Voice = () =>
    Promise.resolve([{ channel: "v1", participants: [...participants] }]);
}

test("a voice channel shows who is in it, and is joined", async () => {
  answers.Voice = () =>
    Promise.resolve([{ channel: "v1", participants: [bruno, carla] }]);
  render(<VoiceRoom space="s1" channel={{ id: "v1", name: "sala" }} />);
  expect(await screen.findByText("Carla")).toBeTruthy();
  expect(screen.getByText("listening only")).toBeTruthy();

  answers.JoinVoice = () => Promise.reject(new Error("voice-full"));
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
  expect(
    await screen.findByText(
      "This voice channel is full: 6 people while a camera is on, 12 by voice alone.",
    ),
  ).toBeTruthy();

  answers.JoinVoice = () => Promise.resolve(undefined);
  seated(me, bruno, carla);
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
  await waitFor(() =>
    expect(calls.JoinVoice.at(-1)).toEqual(["s1", "v1", false]),
  );
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Join" })).toBeNull(),
  );
  expect(screen.getByText("Ana (you)")).toBeTruthy();
});

test("in a voice channel, the microphone goes to each other there, and the camera when on", async () => {
  seated(me, bruno, carla);
  render(<VoiceBar spaces={spaces} />);
  expect(await screen.findByText("In sala · Casa")).toBeTruthy();
  await waitFor(() => expect(sent).toEqual(["add c1 audio", "add c2 audio"]));

  fireEvent.click(screen.getByRole("button", { name: "Turn camera on" }));
  await waitFor(() =>
    expect(sent.slice(2).sort()).toEqual(["add c1 video", "add c2 video"]),
  );
  expect(calls.VoiceVideo).toEqual([[true]]);

  // Carla leaves: nothing more goes to her.
  seated(me, bruno);
  emit("voice", "s1");
  await waitFor(() =>
    expect(sent.slice(4).sort()).toEqual([
      "remove c2 audio",
      "remove c2 video",
    ]),
  );

  fireEvent.click(screen.getByRole("button", { name: "Mute" }));
  expect(opened[0].enabled).toBe(false);

  answers.VoiceSeat = () => Promise.resolve(null);
  fireEvent.click(screen.getByRole("button", { name: "Leave" }));
  await waitFor(() => expect(calls.LeaveVoice).toHaveLength(1));
  await waitFor(() => expect(screen.queryByText("In sala · Casa")).toBeNull());
  expect(sent.slice(6).sort()).toEqual(["remove c1 audio", "remove c1 video"]);
  expect(opened.every((t) => t.stopped)).toBe(true);
});

test("a camera refused for a full channel is not opened", async () => {
  seated(me, bruno);
  answers.VoiceVideo = () => Promise.reject(new Error("voice-full"));
  render(<VoiceBar spaces={spaces} />);
  await waitFor(() => expect(sent).toEqual(["add c1 audio"]));
  fireEvent.click(screen.getByRole("button", { name: "Turn camera on" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(opened).toHaveLength(1);
});

test("one who may only listen sends nothing", async () => {
  seated({ ...me, speaks: false }, bruno);
  render(<VoiceBar spaces={spaces} />);
  expect(await screen.findByText("You may only listen here.")).toBeTruthy();
  await new Promise((r) => setTimeout(r, 20));
  expect(opened).toHaveLength(0);
  expect(sent).toEqual([]);
});
