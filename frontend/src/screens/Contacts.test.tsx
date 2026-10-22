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
import { Contacts } from "./Contacts";
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
  safetyCode: "11111 22222",
};
const carla = {
  publicKey: "cc".repeat(32),
  name: "Carla",
  safetyCode: "33333 44444",
  at: 0,
  expired: false,
};
const davi = { ...carla, publicKey: "dd".repeat(32), name: "Davi" };

beforeEach(() => {
  reset();
  resetEvents();
  answers.MyInvitation = () => Promise.resolve("convia1example");
  answers.Contacts = () => Promise.resolve([bruno]);
  answers.Requests = () =>
    Promise.resolve({
      received: [carla],
      sent: [
        davi,
        { ...davi, publicKey: "ee".repeat(32), name: "Eva", expired: true },
      ],
    });
  answers.Blocked = () => Promise.resolve([]);
});

test("shows the contacts, the requests and the invitation", async () => {
  render(<Contacts onOpen={() => {}} />);
  expect(await screen.findByText("Bruno")).toBeTruthy();
  expect(screen.getByText("Carla")).toBeTruthy();
  expect(screen.queryByText("33333 44444")).toBeNull();
  expect(screen.getByText("Waiting for an answer")).toBeTruthy();
  expect(screen.getByText("Not accepted")).toBeTruthy();
  expect(screen.getByText("convia1example")).toBeTruthy();
});

test("a received request is accepted, declined or blocked by its key", async () => {
  render(<Contacts onOpen={() => {}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Accept" }));
  await waitFor(() => expect(calls.AcceptRequest).toEqual([[carla.publicKey]]));
  fireEvent.click(screen.getByRole("button", { name: "Decline" }));
  await waitFor(() =>
    expect(calls.DeclineRequest).toEqual([[carla.publicKey]]),
  );
  fireEvent.click(screen.getAllByRole("button", { name: "Block" })[0]);
  await waitFor(() =>
    expect(calls.BlockContact).toEqual([[carla.publicKey, true]]),
  );
});

test("a pasted invitation sends a request and says to whom", async () => {
  answers.AddContact = () =>
    Promise.resolve({ ...davi, name: "Fábio", publicKey: "ff".repeat(32) });
  render(<Contacts onOpen={() => {}} />);
  fireEvent.change(await screen.findByLabelText("Their invitation"), {
    target: { value: "convia1fabio" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send the request" }));
  expect(await screen.findByText("Request sent to Fábio.")).toBeTruthy();
  expect(calls.AddContact).toEqual([["convia1fabio"]]);
});

test("an invitation that does not read says why", async () => {
  answers.AddContact = () => Promise.reject(new Error("invitation-checksum"));
  render(<Contacts onOpen={() => {}} />);
  fireEvent.change(await screen.findByLabelText("Their invitation"), {
    target: { value: "convia1typo" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send the request" }));
  expect(
    await screen.findByText("This code was not copied whole, or not exactly."),
  ).toBeTruthy();
});

test("a contact is verified, and removed with or without the conversation", async () => {
  render(<Contacts onOpen={() => {}} />);
  expect(screen.queryByText("11111 22222")).toBeNull();
  for (const b of await screen.findAllByRole("button", { name: "Show" }))
    fireEvent.click(b);
  expect(screen.getByText("11111 22222")).toBeTruthy();
  for (const b of screen.getAllByRole("button", { name: "Hide" }))
    fireEvent.click(b);
  expect(screen.queryByText("11111 22222")).toBeNull();

  fireEvent.click(
    screen.getByRole("button", { name: "I compared this code with Bruno" }),
  );
  await waitFor(() =>
    expect(calls.SetVerified).toEqual([[bruno.publicKey, true]]),
  );

  expect(screen.queryByLabelText("Also delete our conversation")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(screen.getByText("Remove Bruno?")).toBeTruthy();
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Remove" }),
  );
  await waitFor(() => expect(calls.RemoveContact).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  fireEvent.click(screen.getByLabelText("Also delete our conversation"));
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Remove" }),
  );
  await waitFor(() =>
    expect(calls.RemoveContact).toEqual([
      [bruno.publicKey, false],
      [bruno.publicKey, true],
    ]),
  );
});

test("a sent request is dismissed", async () => {
  render(<Contacts onOpen={() => {}} />);
  fireEvent.click(
    (await screen.findAllByRole("button", { name: "Dismiss" }))[1],
  );
  await waitFor(() =>
    expect(calls.DismissRequest).toEqual([["ee".repeat(32)]]),
  );
});

test("the list follows the core when it changes", async () => {
  render(<Contacts onOpen={() => {}} />);
  await screen.findByText("Bruno");
  answers.Contacts = () =>
    Promise.resolve([
      bruno,
      { ...bruno, publicKey: "aa".repeat(32), name: "Ana" },
    ]);
  emit("contacts");
  expect(await screen.findByText("Ana")).toBeTruthy();
});

test("the person is told which contact has to update", async () => {
  answers.Conversations = () => Promise.resolve([bruno]);
  render(
    <Workspace me={{ name: "Me", publicKey: "00" }} onChanged={() => {}} />,
  );
  await screen.findByText("Bruno");
  emit("outdated", { publicKey: bruno.publicKey, theirs: true });
  expect(
    await screen.findByText(
      "Bruno uses an older Convia. You can talk again once they update it.",
    ),
  ).toBeTruthy();
  emit("outdated", { publicKey: "99".repeat(32), theirs: false });
  expect(
    await screen.findByText(
      "A contact uses a newer Convia. Update Convia to talk to them.",
    ),
  ).toBeTruthy();
});
