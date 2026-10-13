import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { ImportIdentity } from "../components/ImportIdentity";
import { answers, calls, reset } from "../test/bridge";
import { Me } from "./Me";
import { Welcome } from "./Welcome";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);

beforeEach(reset);

function type(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

test("a first run makes an identity with the name given", async () => {
  const ready = vi.fn();
  render(<Welcome onReady={ready} />);

  type("Your name", "Alice");
  fireEvent.click(screen.getByRole("button", { name: "Create my identity" }));

  await waitFor(() => expect(ready).toHaveBeenCalled());
  expect(calls.CreateIdentity).toEqual([["Alice"]]);
});

test("a refused name is explained in the interface's words", async () => {
  answers.CreateIdentity = () => Promise.reject(new Error("name"));
  render(<Welcome onReady={() => {}} />);

  type("Your name", "x");
  fireEvent.click(screen.getByRole("button", { name: "Create my identity" }));

  expect(
    await screen.findByText("A name has 1 to 32 characters."),
  ).toBeTruthy();
});

test("importing over an identity asks for consent before replacing it", async () => {
  answers.ChooseIdentityFile = () => Promise.resolve("C:\\alice.cvid");
  answers.ImportIdentity = (_path, _password, replace) =>
    replace
      ? Promise.resolve({ name: "Alice", publicKey: "00" })
      : Promise.reject(new Error("needs-consent"));
  const imported = vi.fn();
  render(<ImportIdentity onImported={imported} />);

  fireEvent.click(screen.getByRole("button", { name: "Choose the file" }));
  await screen.findByText("C:\\alice.cvid");
  type("Its password", "a long enough password");
  fireEvent.click(screen.getByRole("button", { name: "Import" }));

  const consent = await screen.findByRole("button", {
    name: "Set mine aside and import",
  });
  expect(imported).not.toHaveBeenCalled();
  fireEvent.click(consent);

  await waitFor(() => expect(imported).toHaveBeenCalled());
  expect(calls.ImportIdentity?.map((args) => args[2])).toEqual([false, true]);
});

test("deleting an identity needs the person to confirm first", async () => {
  const changed = vi.fn();
  render(<Me me={{ name: "Alice", publicKey: "00" }} onChanged={changed} />);

  const remove = screen.getByRole("button", { name: "Delete my identity" });
  expect((remove as HTMLButtonElement).disabled).toBe(true);

  fireEvent.click(screen.getByLabelText("I understand this cannot be undone"));
  fireEvent.click(remove);

  await waitFor(() => expect(changed).toHaveBeenCalled());
  expect(calls.DeleteIdentity).toHaveLength(1);
});

test("an export needs the same password twice", async () => {
  render(<Me me={{ name: "Alice", publicKey: "00" }} onChanged={() => {}} />);

  type("Password", "a long enough password");
  type("The same password again", "a different password");
  fireEvent.click(screen.getByRole("button", { name: "Export" }));

  expect(await screen.findByText("The two passwords differ.")).toBeTruthy();
  expect(calls.ExportIdentity).toBeUndefined();
});
