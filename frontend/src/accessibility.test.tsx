/*
Every screen, checked against WCAG 2.2 A and AA by axe-core (M12-010). Contrast is the theme's own test,
since jsdom lays nothing out and so cannot see a colour.
*/
import { fireEvent, render, screen } from "@testing-library/react";
import axe from "axe-core";
import { beforeEach, expect, test, vi } from "vitest";
import App from "./App";
import { DeleteDialog } from "./components/DeleteDialog";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { LinkDialog } from "./components/LinkDialog";
import { DataProblem } from "./screens/DataProblem";
import { SpaceSettings } from "./screens/SpaceSettings";
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

test("a conversation with files", async () => {
  answers.Conversation = () =>
    Promise.resolve([
      {
        id: "07",
        mine: false,
        text: "beach.jpg",
        status: "received",
        at: 7,
        reactions: [],
        file: {
          size: 3,
          type: "image/png",
          held: 3,
          complete: true,
          picture: true,
          orientation: 1,
          url: "/files/07",
        },
      },
      {
        id: "08",
        mine: false,
        text: "report.pdf",
        status: "received",
        at: 8,
        reactions: [],
        file: {
          size: 10,
          type: "application/octet-stream",
          held: 4,
          complete: false,
          picture: false,
          orientation: 1,
        },
      },
    ]);
  const { container } = workspace();
  fireEvent.click(await screen.findByText("Ana"));
  await screen.findByText("report.pdf");
  expect(await violations(container)).toEqual([]);
});

test("a space, its channels and one of them open", async () => {
  answers.Spaces = () =>
    Promise.resolve([
      {
        id: "c1",
        name: "Casa",
        rank: "owner",
        channels: [
          {
            id: "g1",
            name: "geral",
            restricted: false,
            roles: [],
            guarded: false,
            writers: [],
            writes: true,
          },
          {
            id: "t1",
            name: "equipe",
            restricted: true,
            roles: [],
            guarded: false,
            writers: [],
            writes: true,
          },
        ],
      },
    ]);
  answers.SpaceInvitations = () => Promise.resolve([]);
  answers.ChannelMessages = () =>
    Promise.resolve([
      {
        id: "01",
        mine: false,
        author: "Bruno",
        text: "bom dia",
        status: "delivered",
        at: 1,
        reactions: [{ emoji: "👍", count: 2, mine: true }],
      },
    ]);
  const { container } = workspace();
  fireEvent.click(await screen.findByRole("button", { name: "Casa" }));
  await screen.findByText("bom dia");
  fireEvent.click(screen.getByRole("button", { name: "Invite a contact" }));
  expect(await violations(container)).toEqual([]);
});

test("creating a space and answering invitations", async () => {
  answers.Spaces = () => Promise.resolve([]);
  answers.SpaceInvitations = () =>
    Promise.resolve([{ id: "d1", name: "Trabalho", inviter: "Bruno" }]);
  const { container } = workspace();
  fireEvent.click(await screen.findByRole("button", { name: "Add a space" }));
  await screen.findByText("Bruno invited you to Trabalho");
  expect(await violations(container)).toEqual([]);
});

test("a space's settings, as its owner sees them", async () => {
  answers.SpaceRoles = () => Promise.resolve([{ id: "r1", name: "Equipe" }]);
  answers.SpaceMembers = () =>
    Promise.resolve([
      { publicKey: "00", name: "Eu", rank: "owner", roles: [], me: true },
      {
        publicKey: "bb",
        name: "Bruno",
        rank: "member",
        roles: ["Equipe"],
        me: false,
      },
    ]);
  const { container } = render(
    <SpaceSettings
      space={{
        id: "s1",
        name: "Casa",
        rank: "owner",
        muted: false,
        channels: [
          {
            id: "g1",
            name: "geral",
            restricted: true,
            roles: ["r1"],
            guarded: false,
            writers: [],
            writes: true,
          },
        ],
      }}
      onLeft={() => {}}
    />,
  );
  await screen.findByRole("group", { name: "Bruno's roles" });
  expect(await violations(container, false)).toEqual([]);
});

test("a confirmation", async () => {
  const { container } = render(
    <ConfirmDialog
      title="Remove Bruno?"
      explanation="He is sent nothing more."
      confirm="Remove"
      onConfirm={() => {}}
      onClose={() => {}}
    />,
  );
  expect(await violations(container, false)).toEqual([]);
});
