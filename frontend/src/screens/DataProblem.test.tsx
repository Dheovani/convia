import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { answers, calls, reset } from "../test/bridge";
import { DataProblem } from "./DataProblem";

vi.mock(
  "../../wailsjs/go/app/Bridge",
  async () => (await import("../test/bridge")).bridge,
);

beforeEach(reset);

test("a newer Convia's data is never offered to be started over from or replaced", () => {
  render(<DataProblem problem="newer" onResolved={() => {}} />);
  expect(screen.queryByRole("button")).toBeNull();
});

test("starting over from damaged data asks the core, then carries on", async () => {
  const resolved = vi.fn();
  render(<DataProblem problem="damaged" onResolved={resolved} />);

  fireEvent.click(screen.getByRole("button", { name: "Start over" }));

  await waitFor(() => expect(resolved).toHaveBeenCalled());
  expect(calls.StartOver).toHaveLength(1);
});

test("a start over that fails says so and stays", async () => {
  answers.StartOver = () =>
    Promise.reject(new Error("the folder is read-only"));
  const resolved = vi.fn();
  render(<DataProblem problem="key-unreadable" onResolved={resolved} />);

  fireEvent.click(screen.getByRole("button", { name: "Start over" }));

  expect(await screen.findByText("Something went wrong.")).toBeTruthy();
  expect(resolved).not.toHaveBeenCalled();
});

test("an exported identity can be imported instead", () => {
  render(<DataProblem problem="damaged" onResolved={() => {}} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Import an exported identity instead" }),
  );
  expect(screen.getByText("Import an identity")).toBeTruthy();
});
