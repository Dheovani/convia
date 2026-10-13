import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { Recoverable } from "./Recoverable";

function Broken(): never {
  throw new Error("the interface broke");
}

test("a failure shows a way back rather than a blank window", () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  render(
    <Recoverable>
      <Broken />
    </Recoverable>,
  );
  expect(screen.getByRole("alert").textContent).toContain(
    "Something went wrong.",
  );
  expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
});
