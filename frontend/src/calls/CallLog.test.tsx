import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { emit, resetEvents } from "../test/runtime";
import { CallLog } from "./CallLog";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);
vi.mock(
  "../../wailsjs/runtime/runtime",
  async () => (await import("../test/runtime")).runtime,
);

beforeEach(() => {
  reset();
  resetEvents();
  answers.CallLog = () =>
    Promise.resolve([
      {
        publicKey: "aa",
        name: "Ana",
        incoming: false,
        video: true,
        outcome: "ended",
        rang: 3000,
        seconds: 125,
      },
      {
        publicKey: "bb",
        name: "Bruno",
        incoming: true,
        video: false,
        outcome: "missed",
        rang: 2000,
        seconds: 0,
      },
      {
        publicKey: "aa",
        name: "Ana",
        incoming: false,
        video: false,
        outcome: "unanswered",
        rang: 1000,
        seconds: 0,
      },
    ]);
});

test("every call made or received is listed, the missed ones apart, and called back", async () => {
  const opened: string[] = [];
  render(<CallLog onOpen={(key) => opened.push(key)} />);
  expect(await screen.findByText(/Video · 2:05/)).toBeTruthy();
  expect(screen.getByText(/Missed/)).toBeTruthy();
  expect(screen.getByText(/No answer/)).toBeTruthy();
  expect(screen.getAllByRole("img", { name: "Made" })).toHaveLength(2);
  expect(screen.getByRole("img", { name: "Received" })).toBeTruthy();

  fireEvent.click(screen.getByText("Bruno"));
  expect(opened).toEqual(["bb"]);
  fireEvent.click(screen.getByRole("button", { name: "Call Bruno" }));
  fireEvent.click(screen.getAllByRole("button", { name: "Video call Ana" })[0]);
  await waitFor(() =>
    expect(calls.StartCall).toEqual([
      ["bb", false],
      ["aa", true],
    ]),
  );

  const loads = calls.CallLog.length;
  emit("call-log");
  await waitFor(() => expect(calls.CallLog.length).toBe(loads + 1));
});

test("an empty log says so", async () => {
  answers.CallLog = () => Promise.resolve([]);
  render(<CallLog onOpen={() => {}} />);
  expect(await screen.findByText("No call made or received yet.")).toBeTruthy();
});
