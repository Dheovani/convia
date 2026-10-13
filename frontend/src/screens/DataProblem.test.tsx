import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import { DataProblem } from "./DataProblem";

// A plain function rather than vi.fn: Vitest fails a test when a spy's promise rejects, even one the code handles.
let startOver: () => Promise<void>;
let calls = 0;
vi.mock("../../wailsjs/go/app/Bridge", () => ({
  StartOver: () => {
    calls++;
    return startOver();
  },
}));

beforeEach(() => {
  calls = 0;
});

test("a newer Convia's data is never offered to be started over from", () => {
  render(<DataProblem problem="newer" onResolved={() => {}} />);
  expect(screen.queryByRole("button")).toBeNull();
});

test("starting over from damaged data asks the core, then carries on", async () => {
  startOver = () => Promise.resolve();
  const resolved = vi.fn();
  render(<DataProblem problem="damaged" onResolved={resolved} />);

  fireEvent.click(screen.getByRole("button", { name: "Start over" }));

  await waitFor(() => expect(resolved).toHaveBeenCalled());
  expect(calls).toBe(1);
});

test("a start over that fails says so and stays", async () => {
  startOver = () => Promise.reject(new Error("the folder is read-only"));
  const resolved = vi.fn();
  render(<DataProblem problem="key-unreadable" onResolved={resolved} />);

  fireEvent.click(screen.getByRole("button", { name: "Start over" }));

  expect(await screen.findByText("the folder is read-only")).toBeTruthy();
  expect(resolved).not.toHaveBeenCalled();
});
