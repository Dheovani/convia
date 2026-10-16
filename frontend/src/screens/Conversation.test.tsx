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
    expect(calls.SendMessage).toEqual([[bruno.publicKey, "hello", ""]]),
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

test("a reply names the message it answers, and shows what it quotes", async () => {
  answers.Conversation = () =>
    Promise.resolve([
      ...said,
      {
        id: "05",
        mine: false,
        text: "about that",
        status: "received",
        at: 5,
        reactions: [],
        replyTo: { id: "03", mine: true, text: "**arrived**", missing: false },
      },
      {
        id: "06",
        mine: false,
        text: "and this",
        status: "received",
        at: 6,
        reactions: [],
        replyTo: { id: "99", mine: false, text: "", missing: true },
      },
    ]);
  open();
  expect(await screen.findByText("arrived", { selector: "span" })).toBeTruthy();
  expect(screen.getByText("The message replied to is not here.")).toBeTruthy();

  fireEvent.click(screen.getAllByRole("button", { name: "Reply" })[3]);
  expect(screen.getByText("Replying to Bruno:")).toBeTruthy();
  const field = screen.getByLabelText("Message to Bruno");
  fireEvent.change(field, { target: { value: "yes" } });
  fireEvent.keyDown(field, { key: "Enter" });
  await waitFor(() =>
    expect(calls.SendMessage).toEqual([[bruno.publicKey, "yes", "04"]]),
  );
  await waitFor(() =>
    expect(screen.queryByText("Replying to Bruno:")).toBeNull(),
  );
});

test("Escape gives up replying", async () => {
  open();
  fireEvent.click((await screen.findAllByRole("button", { name: "Reply" }))[0]);
  fireEvent.keyDown(screen.getByLabelText("Message to Bruno"), {
    key: "Escape",
  });
  expect(screen.queryByText(/Replying to/)).toBeNull();
});

test("a reaction is put on, and a reaction of one's own taken off", async () => {
  answers.Conversation = () =>
    Promise.resolve([
      {
        ...said[3],
        reactions: [
          { emoji: "👍", count: 2, mine: true },
          { emoji: "🎉", count: 1, mine: false },
        ],
      },
    ]);
  open();
  const mine = await screen.findByRole("button", {
    name: "👍 2, including you",
  });
  expect(mine.getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(mine);
  await waitFor(() =>
    expect(calls.React).toEqual([[bruno.publicKey, "04", "👍", false]]),
  );
  fireEvent.click(screen.getByRole("button", { name: "🎉 1" }));
  await waitFor(() =>
    expect(calls.React?.at(-1)).toEqual([bruno.publicKey, "04", "🎉", true]),
  );

  fireEvent.click(screen.getByRole("button", { name: "React" }));
  fireEvent.click(screen.getByRole("button", { name: "React with 😂" }));
  await waitFor(() =>
    expect(calls.React?.at(-1)).toEqual([bruno.publicKey, "04", "😂", true]),
  );
  expect(screen.queryByRole("group", { name: "Choose a reaction" })).toBeNull();
});
