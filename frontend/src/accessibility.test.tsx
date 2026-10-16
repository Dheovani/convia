/*
Every screen, checked against WCAG 2.2 A and AA by axe-core (M12-010). Contrast is the theme's own test,
since jsdom lays nothing out and so cannot see a colour.
*/
import { fireEvent, render, screen } from "@testing-library/react";
import axe from "axe-core";
import { beforeEach, expect, test, vi } from "vitest";
import App from "./App";
import { DeleteDialog } from "./components/DeleteDialog";
import { LinkDialog } from "./components/LinkDialog";
import { DataProblem } from "./screens/DataProblem";
import { Workspace } from "./screens/Workspace";
import { answers, reset } from "./test/bridge";
import { resetEvents } from "./test/runtime";

vi.mock(
  "../wailsjs/go/app/Bridge",
  async () => (await import("./test/bridge")).bridge,
);
vi.mock(
  "../wailsjs/runtime/runtime",
  async () => (await import("./test/runtime")).runtime,
);

async function violations(container: Element, page = true): Promise<string[]> {
  const results = await axe.run(container, {
    runOnly: {
      type: "tag",
      values: [
        "wcag2a",
        "wcag2aa",
        "wcag21a",
        "wcag21aa",
        "wcag22aa",
        "best-practice",
      ],
    },
    rules: {
      "color-contrast": { enabled: false },
      // A part of a page is not a page: its landmarks are the page's.
      region: { enabled: page },
      "landmark-one-main": { enabled: page },
      "page-has-heading-one": { enabled: false },
    },
  });
  return results.violations.map(
    (v) =>
      `${v.id}: ${v.help} — ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`,
  );
}

const ana = {
  publicKey: "aa".repeat(32),
  name: "Ana",
  verified: true,
  safetyCode: "11111 22222",
  unread: 1,
  last: {
    id: "01",
    mine: false,
    text: "**hi** https://convia.example",
    status: "received",
    at: 1,
  },
};

beforeEach(() => {
  reset();
  resetEvents();
  answers.Conversations = () => Promise.resolve([ana]);
  answers.Requests = () =>
    Promise.resolve({
      received: [
        {
          ...ana,
          publicKey: "cc".repeat(32),
          name: "Carla",
          at: 0,
          expired: false,
        },
      ],
      sent: [
        {
          ...ana,
          publicKey: "dd".repeat(32),
          name: "Davi",
          at: 0,
          expired: false,
        },
      ],
    });
  answers.Contacts = () => Promise.resolve([ana]);
  answers.MyInvitation = () => Promise.resolve("convia1example");
  answers.Blocked = () => Promise.resolve(["ee".repeat(32)]);
  answers.Conversation = () =>
    Promise.resolve([
      ana.last,
      {
        id: "02",
        mine: true,
        text: "hello",
        status: "delivered",
        at: 2,
        reactions: [{ emoji: "👍", count: 1, mine: false }],
        replyTo: { id: "01", mine: false, text: "hi", missing: false },
      },
    ]);
});

function workspace() {
  return render(
    <Workspace me={{ name: "Eu", publicKey: "00" }} onChanged={() => {}} />,
  );
}

test("the welcome", async () => {
  answers.StorageProblem = () => Promise.resolve("");
  answers.Me = () => Promise.resolve(null);
  const { container } = render(<App />);
  await screen.findByText("Welcome to Convia");
  expect(await violations(container)).toEqual([]);
});

test("data that does not open", async () => {
  const { container } = render(
    <DataProblem problem="damaged" onResolved={() => {}} />,
  );
  expect(await violations(container, false)).toEqual([]);
});

test("the workspace, with nothing open", async () => {
  const { container } = workspace();
  await screen.findByText("Ana");
  expect(await violations(container)).toEqual([]);
});

test("a conversation", async () => {
  const { container } = workspace();
  fireEvent.click(await screen.findByText("Ana"));
  await screen.findByText("hello");
  expect(await violations(container)).toEqual([]);
});

test("the contacts", async () => {
  const { container } = workspace();
  fireEvent.click(await screen.findByRole("button", { name: /Contacts/ }));
  await screen.findByText("Carla");
  expect(await violations(container)).toEqual([]);
});

test("the settings", async () => {
  const { container } = workspace();
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  await screen.findByLabelText("Language");
  expect(await violations(container)).toEqual([]);
});

test("the question before a link opens", async () => {
  const { container } = render(
    <LinkDialog url="https://convia.example" onClose={() => {}} />,
  );
  expect(await violations(container, false)).toEqual([]);
});

test("a search's results", async () => {
  answers.SearchMessages = () =>
    Promise.resolve([
      { publicKey: ana.publicKey, name: "Ana", message: ana.last },
    ]);
  const { container } = workspace();
  await screen.findByText("Ana");
  fireEvent.change(screen.getByLabelText("Search messages"), {
    target: { value: "hi" },
  });
  await screen.findByText("hi https://convia.example");
  expect(await violations(container)).toEqual([]);
});

test("the question before a message is deleted", async () => {
  const { container } = render(
    <DeleteDialog
      mine={true}
      delivered={true}
      onBoth={() => {}}
      onHere={() => {}}
      onClose={() => {}}
    />,
  );
  expect(await violations(container, false)).toEqual([]);
});

test("a message being edited", async () => {
  const { container } = workspace();
  fireEvent.click(await screen.findByText("Ana"));
  await screen.findByText("hello");
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  await screen.findByLabelText("The message's new text");
  expect(await violations(container)).toEqual([]);
});
