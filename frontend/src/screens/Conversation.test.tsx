import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { emit, resetEvents } from "../test/runtime";
import { Conversation, MaxCharacters } from "./Conversation";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);
vi.mock(
  "../../wailsjs/runtime/runtime",
  async () => (await import("../test/runtime")).runtime,
);

const bruno = {
  publicKey: "bb".repeat(32),
  name: "Bruno",
  verified: false,
  safetyCode: "1",
};

const said = [
  { id: "01", mine: true, text: "waits", status: "waiting", at: 1 },
  { id: "02", mine: true, text: "went", status: "sent", at: 2 },
  { id: "03", mine: true, text: "arrived", status: "delivered", at: 3 },
  { id: "04", mine: false, text: "an answer", status: "received", at: 4 },
];

beforeEach(() => {
  reset();
  resetEvents();
  answers.Conversation = () => Promise.resolve(said);
  answers.SendMessage = () => Promise.resolve(said[0]);
});

function open() {
  render(<Conversation contact={bruno} meName="Ana" />);
}

test("each message of ours says whether it waits, went or arrived", async () => {
  open();
  expect(await screen.findByText("an answer")).toBeTruthy();
  expect(screen.getByText("Waiting")).toBeTruthy();
  expect(screen.getByText("Sent")).toBeTruthy();
  expect(screen.getByText("Delivered")).toBeTruthy();
});

test("Enter sends, and Shift+Enter does not", async () => {
  open();
  const field = await screen.findByLabelText("Message to Bruno");
  fireEvent.change(field, { target: { value: "hello" } });
  fireEvent.keyDown(field, { key: "Enter", shiftKey: true });
  expect(calls.SendMessage).toBeUndefined();
  fireEvent.keyDown(field, { key: "Enter" });
  await waitFor(() =>
    expect(calls.SendMessage).toEqual([[bruno.publicKey, "hello"]]),
  );
});

test("a message above the limit cannot be sent, and says by how much", async () => {
  open();
  const field = await screen.findByLabelText("Message to Bruno");
  fireEvent.change(field, { target: { value: "🙂".repeat(MaxCharacters) } });
  expect(screen.getByText("0 characters left")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Send" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);

  fireEvent.change(field, { target: { value: "a".repeat(MaxCharacters + 3) } });
  expect(screen.getByText("3 characters too many")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Send" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.keyDown(field, { key: "Enter" });
  expect(calls.SendMessage).toBeUndefined();
});

test("the counter appears only as the limit nears", async () => {
  open();
  const field = await screen.findByLabelText("Message to Bruno");
  fireEvent.change(field, { target: { value: "a".repeat(100) } });
  expect(screen.queryByText(/characters left/)).toBeNull();
  fireEvent.change(field, { target: { value: "a".repeat(3700) } });
  expect(screen.getByText("300 characters left")).toBeTruthy();
});

test("the conversation follows the core, for this contact only", async () => {
  open();
  await screen.findByText("an answer");
  answers.Conversation = () =>
    Promise.resolve([
      ...said,
      { id: "05", mine: false, text: "news", status: "received", at: 5 },
    ]);
  emit("conversation", "cc".repeat(32));
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.queryByText("news")).toBeNull();
  emit("conversation", bruno.publicKey);
  expect(await screen.findByText("news")).toBeTruthy();
});

test("a refusal is said in the interface's words", async () => {
  answers.SendMessage = () => Promise.reject(new Error("message-too-long"));
  open();
  const field = await screen.findByLabelText("Message to Bruno");
  fireEvent.change(field, { target: { value: "hello" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(
    await screen.findByText("A message holds at most 4,000 characters."),
  ).toBeTruthy();
});
