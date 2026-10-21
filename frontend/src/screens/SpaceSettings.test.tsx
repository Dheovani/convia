import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { resetEvents } from "../test/runtime";
import { SpaceSettings } from "./SpaceSettings";
import type { SpaceInfo } from "./SpaceSidebar";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);
vi.mock(
  "../../wailsjs/runtime/runtime",
  async () => (await import("../test/runtime")).runtime,
);

const space = (rank: SpaceInfo["rank"]): SpaceInfo => ({
  id: "s1",
  name: "Casa",
  rank,
  muted: false,
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
      roles: ["r1"],
      guarded: false,
      writers: [],
      writes: true,
    },
  ],
});

beforeEach(() => {
  reset();
  resetEvents();
  answers.SpaceRoles = () =>
    Promise.resolve([
      { id: "r1", name: "Equipe" },
      { id: "r2", name: "Som" },
    ]);
  answers.SpaceMembers = () =>
    Promise.resolve([
      { publicKey: "00", name: "Eu", rank: "owner", roles: [], me: true },
      {
        publicKey: "aa",
        name: "Ana",
        rank: "administrator",
        roles: [],
        me: false,
      },
      {
        publicKey: "bb",
        name: "Bruno",
        rank: "member",
        roles: ["Equipe"],
        me: false,
      },
    ]);
});

function open(rank: SpaceInfo["rank"], onLeft = () => {}) {
  render(<SpaceSettings space={space(rank)} onLeft={onLeft} />);
}

function confirm(label: string) {
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: label }),
  );
}

test("the owner renames the space and its channels, and sets who sees a channel", async () => {
  open("owner");
  const name = await screen.findByLabelText("Name");
  fireEvent.change(name, { target: { value: "Lar" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls.RenameSpace).toEqual([["s1", "Lar"]]));

  fireEvent.change(screen.getAllByLabelText("Channel name")[0], {
    target: { value: "avisos" },
  });
  fireEvent.submit(screen.getAllByLabelText("Channel name")[0]);
  await waitFor(() =>
    expect(calls.RenameChannel).toEqual([["s1", "g1", "avisos"]]),
  );

  const general = screen.getByRole("group", { name: "Who sees #geral" });
  fireEvent.click(general.querySelectorAll("input")[2]);
  await waitFor(() =>
    expect(calls.SetChannelRoles).toEqual([["s1", "g1", ["r2"]]]),
  );
  const team = screen.getByRole("group", { name: "Who sees #equipe" });
  fireEvent.click(team.querySelectorAll("input")[0]);
  await waitFor(() =>
    expect(calls.SetChannelRoles?.at(-1)).toEqual(["s1", "t1", []]),
  );
  fireEvent.click(team.querySelectorAll("input")[1]);
  await waitFor(() =>
    expect(calls.SetChannelRoles?.at(-1)).toEqual(["s1", "t1", []]),
  );

  fireEvent.click(screen.getAllByRole("button", { name: "Delete" })[0]);
  expect(screen.getByText("Delete #geral?")).toBeTruthy();
  confirm("Delete");
  await waitFor(() => expect(calls.DeleteChannel).toEqual([["s1", "g1"]]));
});

test("the owner creates and deletes roles, and gives them", async () => {
  open("owner");
  fireEvent.change(await screen.findByLabelText("New role"), {
    target: { value: "Moderação" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  await waitFor(() => expect(calls.CreateRole).toEqual([["s1", "Moderação"]]));

  const bruno = await screen.findByRole("group", { name: "Bruno's roles" });
  const [equipe, som] = bruno.querySelectorAll("input");
  expect((equipe as HTMLInputElement).checked).toBe(true);
  fireEvent.click(equipe);
  fireEvent.click(som);
  await waitFor(() =>
    expect(calls.SetMemberRole).toEqual([
      ["s1", "bb", "r1", false],
      ["s1", "bb", "r2", true],
    ]),
  );

  // The channels' Delete come first, then the roles'.
  fireEvent.click(screen.getAllByRole("button", { name: "Delete" })[2]);
  expect(screen.getByText("Delete Equipe?")).toBeTruthy();
  confirm("Delete");
  await waitFor(() => expect(calls.DeleteRole).toEqual([["s1", "r1"]]));
});

test("the owner makes administrators, removes members and transfers ownership", async () => {
  open("owner");
  fireEvent.click(
    await screen.findByRole("button", { name: "Make administrator" }),
  );
  await waitFor(() =>
    expect(calls.SetAdministrator).toEqual([["s1", "bb", true]]),
  );
  fireEvent.click(screen.getByRole("button", { name: "Make member" }));
  await waitFor(() =>
    expect(calls.SetAdministrator?.at(-1)).toEqual(["s1", "aa", false]),
  );

  // The owner removes an administrator too, and is never offered to remove themselves.
  expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(2);
  fireEvent.click(
    screen.getAllByRole("button", { name: "Transfer ownership" })[0],
  );
  expect(screen.getByText("Make Ana the owner?")).toBeTruthy();
  confirm("Transfer ownership");
  await waitFor(() => expect(calls.TransferOwnership).toEqual([["s1", "aa"]]));

  fireEvent.click(screen.getAllByRole("button", { name: "Remove" })[1]);
  confirm("Remove");
  await waitFor(() => expect(calls.RemoveMember).toEqual([["s1", "bb"]]));
  expect(screen.queryByRole("button", { name: "Leave the space" })).toBeNull();
});

test("an administrator removes members, not administrators, and may leave", async () => {
  const left = vi.fn();
  open("administrator", left);
  await screen.findByRole("group", { name: "Bruno's roles" });
  expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "Make administrator" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Transfer ownership" }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "Delete the space" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Leave the space" }));
  confirm("Leave the space");
  await waitFor(() => expect(calls.LeaveSpace).toEqual([["s1"]]));
  await waitFor(() => expect(left).toHaveBeenCalled());
});

test("a member is offered only to leave", async () => {
  open("member");
  expect(
    await screen.findByRole("button", { name: "Leave the space" }),
  ).toBeTruthy();
  expect(screen.queryByLabelText("Name")).toBeNull();
  expect(screen.queryByText("Roles")).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
});

test("the owner deletes the space, after saying so", async () => {
  const left = vi.fn();
  open("owner", left);
  fireEvent.click(
    await screen.findByRole("button", { name: "Delete the space" }),
  );
  expect(
    screen.getByText("The space ends for every member. This cannot be undone."),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(calls.DeleteSpace).toBeUndefined();
  fireEvent.click(screen.getByRole("button", { name: "Delete the space" }));
  confirm("Delete");
  await waitFor(() => expect(calls.DeleteSpace).toEqual([["s1"]]));
  await waitFor(() => expect(left).toHaveBeenCalled());
});

test("what the space refuses is said in the interface's words", async () => {
  answers.RenameSpace = () => Promise.reject(new Error("not-allowed"));
  open("owner");
  fireEvent.change(await screen.findByLabelText("Name"), {
    target: { value: "Lar" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(
    await screen.findByText("Your place in this space does not allow that."),
  ).toBeTruthy();
});

test("a moderator mutes and unmutes those they moderate", async () => {
  answers.SpaceMembers = () =>
    Promise.resolve([
      {
        publicKey: "00",
        name: "Eu",
        rank: "administrator",
        roles: [],
        me: true,
        muted: false,
      },
      {
        publicKey: "aa",
        name: "Ana",
        rank: "owner",
        roles: [],
        me: false,
        muted: false,
      },
      {
        publicKey: "bb",
        name: "Bruno",
        rank: "member",
        roles: [],
        me: false,
        muted: false,
      },
      {
        publicKey: "cc",
        name: "Carla",
        rank: "member",
        roles: [],
        me: false,
        muted: true,
      },
    ]);
  open("administrator");
  fireEvent.click(await screen.findByRole("button", { name: "Mute" }));
  await waitFor(() => expect(calls.MuteMember).toEqual([["s1", "bb", true]]));
  fireEvent.click(screen.getByRole("button", { name: "Unmute" }));
  await waitFor(() =>
    expect(calls.MuteMember?.at(-1)).toEqual(["s1", "cc", false]),
  );
  expect(
    screen.getAllByRole("button", { name: /^(Mute|Unmute)$/ }),
  ).toHaveLength(2);
});

test("an administrator sets who writes in a channel every reader reads", async () => {
  open("administrator");
  const writes = await screen.findByRole("group", {
    name: "Who writes in #geral",
  });
  const [everyone, equipe] = writes.querySelectorAll("input");
  fireEvent.click(everyone);
  await waitFor(() =>
    expect(calls.SetChannelWriters).toEqual([["s1", "g1", false, []]]),
  );
  fireEvent.click(equipe);
  await waitFor(() =>
    expect(calls.SetChannelWriters?.at(-1)).toEqual([
      "s1",
      "g1",
      false,
      ["r1"],
    ]),
  );
});
