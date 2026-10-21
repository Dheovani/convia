import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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

const bruno = {
  publicKey: "bb".repeat(32),
  name: "Bruno",
  verified: false,
  safetyCode: "2",
  unread: 0,
};
const casa = {
  id: "c1",
  name: "Casa da Ana",
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
};
const said = [
  {
    id: "01",
    mine: false,
    author: "Bruno Silva",
    text: "bom dia",
    status: "delivered",
    at: 1,
    reactions: [],
  },
  {
    id: "02",
    mine: true,
    author: "Eu",
    text: "oi",
    status: "delivered",
    at: 2,
    reactions: [],
    replyTo: {
      id: "01",
      mine: false,
      author: "Bruno Silva",
      text: "bom dia",
      missing: false,
    },
  },
];

beforeEach(() => {
  reset();
  resetEvents();
  localStorage.clear();
  answers.Conversations = () => Promise.resolve([bruno]);
  answers.Requests = () => Promise.resolve({ received: [], sent: [] });
  answers.Spaces = () => Promise.resolve([casa]);
  answers.SpaceInvitations = () => Promise.resolve([]);
  answers.ChannelMessages = () => Promise.resolve(said);
});

function open() {
  render(
    <Workspace me={{ name: "Eu", publicKey: "00" }} onChanged={() => {}} />,
  );
}

test("a space is in the rail, its channels in the sidebar, and a channel's conversation in the middle", async () => {
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  expect(screen.getByText("CD")).toBeTruthy();
  expect(await screen.findByRole("button", { name: /geral/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /equipe/ })).toBeTruthy();
  expect(await screen.findByText("bom dia", { selector: "p" })).toBeTruthy();
  expect(screen.getAllByText("Bruno Silva").length).toBeGreaterThan(0);
  expect(calls.ChannelMessages).toContainEqual(["c1", "g1"]);

  // Only one's own message is offered to delete, and a channel has no statuses.
  expect(screen.getAllByRole("button", { name: "Delete" })).toHaveLength(1);
  expect(screen.queryByText("Delivered")).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: /equipe/ }));
  await waitFor(() =>
    expect(calls.ChannelMessages).toContainEqual(["c1", "t1"]),
  );
});

test("a message is written in a channel, answered and reacted to", async () => {
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  const field = await screen.findByPlaceholderText("Message #geral");
  fireEvent.click(screen.getAllByRole("button", { name: "Reply" })[0]);
  fireEvent.change(field, { target: { value: "tudo bem?" } });
  fireEvent.keyDown(field, { key: "Enter" });
  await waitFor(() =>
    expect(calls.SayInChannel).toEqual([["c1", "g1", "tudo bem?", "01"]]),
  );
  fireEvent.click(screen.getAllByRole("button", { name: "React with 👍" })[0]);
  await waitFor(() =>
    expect(calls.ReactInChannel).toEqual([["c1", "g1", "01", "👍", true]]),
  );

  const loads = calls.ChannelMessages.length;
  emit("spaces", "other");
  emit("spaces", "c1");
  await waitFor(() => expect(calls.ChannelMessages).toHaveLength(loads + 1));
});

test("a space is created, and an invitation answered", async () => {
  answers.SpaceInvitations = () =>
    Promise.resolve([{ id: "d1", name: "Trabalho", inviter: "Bruno Silva" }]);
  answers.CreateSpace = () => Promise.resolve(casa);
  open();
  expect(await screen.findByLabelText("1 invitation waiting")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Add a space" }));
  expect(
    await screen.findByText("Bruno Silva invited you to Trabalho"),
  ).toBeTruthy();

  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Casa da Ana" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create" }));
  await waitFor(() =>
    expect(calls.CreateSpace).toEqual([["Casa da Ana", "general"]]),
  );
  expect(await screen.findByPlaceholderText("Message #geral")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Add a space" }));
  fireEvent.click(await screen.findByRole("button", { name: "Accept" }));
  await waitFor(() => expect(calls.AcceptSpace).toEqual([["d1"]]));
});

test("who administers a space adds channels and invites contacts; a member does not", async () => {
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  fireEvent.click(await screen.findByRole("button", { name: "New channel" }));
  fireEvent.change(screen.getByLabelText("Channel name"), {
    target: { value: "avisos" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  await waitFor(() => expect(calls.CreateChannel).toEqual([["c1", "avisos"]]));

  fireEvent.click(
    await screen.findByRole("button", { name: "Invite a contact" }),
  );
  fireEvent.change(screen.getByLabelText("Whom to invite"), {
    target: { value: bruno.publicKey },
  });
  fireEvent.click(screen.getByRole("button", { name: "Invite" }));
  await waitFor(() =>
    expect(calls.InviteToSpace).toEqual([["c1", bruno.publicKey]]),
  );
});

test("a member is offered no administration", async () => {
  answers.Spaces = () => Promise.resolve([{ ...casa, rank: "member" }]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  await screen.findByPlaceholderText("Message #geral");
  expect(screen.queryByRole("button", { name: "New channel" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Invite a contact" })).toBeNull();
});

test("a space refused is said so in the interface's words", async () => {
  answers.CreateSpace = () => Promise.reject(new Error("space-name"));
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Add a space" }));
  fireEvent.change(await screen.findByLabelText("Name"), {
    target: { value: "x" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create" }));
  expect(
    await screen.findByText("A space or channel name has 1 to 100 characters."),
  ).toBeTruthy();
});

test("a space's members are listed by rank, with their roles", async () => {
  answers.SpaceMembers = () =>
    Promise.resolve([
      { publicKey: "00", name: "Eu", rank: "owner", roles: [], me: true },
      {
        publicKey: "aa",
        name: "Ana",
        rank: "administrator",
        roles: ["Equipe"],
        me: false,
      },
      {
        publicKey: "bb",
        name: "Bruno Silva",
        rank: "member",
        roles: [],
        me: false,
      },
      {
        publicKey: "cc",
        name: "Carla",
        rank: "member",
        roles: ["Equipe", "Som"],
        me: false,
      },
    ]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  const list = await screen.findByRole("complementary", { name: "Members" });
  expect(await within(list).findByText("Owner — 1")).toBeTruthy();
  expect(within(list).getByText("Administrators — 1")).toBeTruthy();
  expect(within(list).getByText("Members — 2")).toBeTruthy();
  expect(within(list).getByText("(You)")).toBeTruthy();
  expect(within(list).getAllByText("Equipe")).toHaveLength(2);
  expect(calls.SpaceMembers).toContainEqual(["c1"]);

  const loads = calls.SpaceMembers.length;
  emit("spaces", "c1");
  await waitFor(() => expect(calls.SpaceMembers.length).toBeGreaterThan(loads));
});

test("a moderator deletes another's message, which shows as deleted by a moderator", async () => {
  answers.ChannelMessages = () =>
    Promise.resolve([
      { ...said[0], deletable: true },
      {
        id: "03",
        mine: false,
        author: "Carla",
        text: "",
        status: "delivered",
        at: 3,
        reactions: [],
        deleted: true,
        moderated: true,
      },
    ]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  expect(
    await screen.findByText("Message deleted by a moderator."),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  const dialog = screen.getByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
  await waitFor(() =>
    expect(calls.DeleteInChannel).toEqual([["c1", "g1", "01"]]),
  );
});

test("a muted member reads a channel, and is told why they cannot write", async () => {
  answers.Spaces = () => Promise.resolve([{ ...casa, muted: true }]);
  answers.SpaceMembers = () =>
    Promise.resolve([
      {
        publicKey: "00",
        name: "Eu",
        rank: "member",
        roles: [],
        me: true,
        muted: true,
      },
    ]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  expect(await screen.findByText("bom dia", { selector: "p" })).toBeTruthy();
  expect(
    screen.getByText(
      "A moderator muted you: you read this space, and write in none of its channels.",
    ),
  ).toBeTruthy();
  expect(screen.queryByPlaceholderText("Message #geral")).toBeNull();
  expect(await screen.findByText("· muted")).toBeTruthy();
});

test("a channel only some write in is read, reacted to, and said so", async () => {
  answers.Spaces = () =>
    Promise.resolve([
      {
        ...casa,
        rank: "member",
        channels: [
          {
            id: "g1",
            name: "avisos",
            restricted: false,
            roles: [],
            guarded: true,
            writers: [],
            writes: false,
          },
        ],
      },
    ]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  expect(
    await screen.findByText(
      "Only some roles write here; you read it, and may react.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("📢")).toBeTruthy();
  fireEvent.click(screen.getAllByRole("button", { name: "React with 👍" })[0]);
  await waitFor(() =>
    expect(calls.ReactInChannel).toEqual([["c1", "g1", "01", "👍", true]]),
  );
});

test("a file is attached in a channel, and one not pulled at once is downloaded when asked for", async () => {
  answers.ChooseFile = () => Promise.resolve("C:\\docs\\plano.pdf");
  answers.ChannelMessages = () =>
    Promise.resolve([
      {
        id: "05",
        mine: false,
        author: "Bruno Silva",
        text: "plano.pdf",
        status: "delivered",
        at: 5,
        reactions: [],
        file: {
          size: 3 << 20,
          type: "application/octet-stream",
          held: 0,
          complete: false,
          picture: false,
          orientation: 1,
        },
      },
      {
        id: "06",
        mine: false,
        author: "Bruno Silva",
        text: "outro.pdf",
        status: "delivered",
        at: 6,
        reactions: [],
        file: {
          size: 3 << 20,
          type: "application/octet-stream",
          held: 0,
          complete: false,
          picture: false,
          orientation: 1,
          wanted: true,
        },
      },
    ]);
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Casa da Ana" }));
  fireEvent.click(await screen.findByRole("button", { name: "Attach a file" }));
  await waitFor(() =>
    expect(calls.AttachInChannel).toEqual([
      ["c1", "g1", "C:\\docs\\plano.pdf"],
    ]),
  );
  // Only the one not yet asked for is offered to download; the other waits.
  const downloads = await screen.findAllByRole("button", { name: "Download" });
  expect(downloads).toHaveLength(1);
  expect(screen.getByText(/Waiting for/)).toBeTruthy();
  fireEvent.click(downloads[0]);
  await waitFor(() => expect(calls.FetchInChannel).toEqual([["05"]]));
});
