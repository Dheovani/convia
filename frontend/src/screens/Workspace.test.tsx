import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { emit, resetEvents } from "../test/runtime";
import { Workspace } from "./Workspace";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);
vi.mock(
  "../../wailsjs/runtime/runtime",
  async () => (await import("../test/runtime")).runtime,
);

const ana = {
  publicKey: "aa".repeat(32),
  name: "Ana",
  verified: true,
  safetyCode: "1",
  unread: 2,
  last: {
    id: "01",
    mine: false,
    text: "see **this** https://convia.example",
    status: "received",
    at: 1,
  },
};
const bruno = {
  publicKey: "bb".repeat(32),
  name: "Bruno",
  verified: false,
  safetyCode: "2",
  unread: 0,
};

beforeEach(() => {
  reset();
  resetEvents();
  localStorage.clear();
  answers.Conversations = () => Promise.resolve([ana, bruno]);
  answers.Requests = () =>
    Promise.resolve({ received: [{ publicKey: "cc" }], sent: [] });
  answers.Conversation = () => Promise.resolve([ana.last]);
});

function open() {
  render(
    <Workspace me={{ name: "Eu", publicKey: "00" }} onChanged={() => {}} />,
  );
}

test("the sidebar lists every conversation, its unread count and the requests waiting", async () => {
  open();
  expect(await screen.findByText("Ana")).toBeTruthy();
  expect(screen.getByText("Bruno")).toBeTruthy();
  expect(screen.getByLabelText("2 unread")).toBeTruthy();
  expect(screen.getByLabelText("1 waiting for your answer")).toBeTruthy();
  expect(screen.getByText("see this https://convia.example")).toBeTruthy();
});

test("opening a conversation says it is on screen, and shows it formatted", async () => {
  open();
  fireEvent.click(await screen.findByText("Ana"));
  await waitFor(() => expect(calls.Viewing?.at(-1)?.[0]).toBe(ana.publicKey));
  expect(await screen.findByText("this")).toBeTruthy();
  expect(screen.getByText("this").tagName).toBe("STRONG");
});

test("a link is opened only once confirmed, showing the whole address", async () => {
  open();
  fireEvent.click(await screen.findByText("Ana"));
  fireEvent.click(await screen.findByText("https://convia.example"));
  expect(calls.OpenLink).toBeUndefined();
  expect(screen.getByText("Open this link?")).toBeTruthy();
  fireEvent.click(screen.getByText("Open"));
  expect(calls.OpenLink).toEqual([["https://convia.example"]]);
});

test("a message away from its conversation is told of by name only, unless chosen", async () => {
  open();
  await screen.findByText("Ana");
  emit("received", { publicKey: ana.publicKey, text: "a secret" });
  await waitFor(() =>
    expect(calls.Notify).toEqual([[ana.publicKey, "New message from Ana", ""]]),
  );

  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(
    screen.getByLabelText("Show what a message says in its notification"),
  );
  emit("received", { publicKey: ana.publicKey, text: "now shown" });
  await waitFor(() =>
    expect(calls.Notify?.at(-1)).toEqual([
      ana.publicKey,
      "New message from Ana",
      "now shown",
    ]),
  );
});

test("choosing a notification opens its conversation", async () => {
  open();
  await screen.findByText("Bruno");
  emit("open-conversation", bruno.publicKey);
  expect(await screen.findByRole("region", { name: "Bruno" })).toBeTruthy();
});

test("Alt+Down and Alt+Up move between conversations", async () => {
  open();
  fireEvent.click(await screen.findByText("Ana"));
  fireEvent.keyDown(window, { key: "ArrowDown", altKey: true });
  expect(await screen.findByRole("region", { name: "Bruno" })).toBeTruthy();
  fireEvent.keyDown(window, { key: "ArrowUp", altKey: true });
  expect(await screen.findByRole("region", { name: "Ana" })).toBeTruthy();
});

test("leaving for the settings says no conversation is on screen", async () => {
  open();
  fireEvent.click(await screen.findByText("Ana"));
  await waitFor(() => expect(calls.Viewing?.at(-1)?.[0]).toBe(ana.publicKey));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  await waitFor(() => expect(calls.Viewing?.at(-1)?.[0]).toBe(""));
  expect(screen.getByLabelText("Language")).toBeTruthy();
});

test("a search shows what it found, and opens the conversation at the message", async () => {
  answers.SearchMessages = (query) =>
    Promise.resolve(
      query === "beach"
        ? [
            {
              publicKey: ana.publicKey,
              name: "Ana",
              message: { ...ana.last, id: "01", text: "to the **beach**" },
            },
          ]
        : [],
    );
  open();
  await screen.findByText("Bruno");
  fireEvent.change(screen.getByLabelText("Search messages"), {
    target: { value: "beach" },
  });
  const result = await screen.findByText("to the beach");
  expect(screen.queryByText("Bruno")).toBeNull();
  fireEvent.click(result);
  expect(await screen.findByRole("region", { name: "Ana" })).toBeTruthy();
  await waitFor(() =>
    expect(
      document.getElementById("message-01")?.getAttribute("aria-current"),
    ).toBe("true"),
  );

  fireEvent.change(screen.getByLabelText("Search messages"), {
    target: { value: "nothing like it" },
  });
  expect(await screen.findByText("Nothing found.")).toBeTruthy();
  fireEvent.keyDown(screen.getByLabelText("Search messages"), {
    key: "Escape",
  });
  expect(await screen.findByText("Bruno")).toBeTruthy();
});
