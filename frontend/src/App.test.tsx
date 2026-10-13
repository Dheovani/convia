import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import App from "./App";

const problem = vi.fn();
vi.mock("../wailsjs/go/app/Bridge", () => ({
  StorageProblem: () => problem(),
  StartOver: vi.fn(),
}));

test("the window shows Convia's mark and name when its data opens", async () => {
  problem.mockResolvedValue("");
  render(<App />);
  expect(await screen.findByText("Convia")).toBeTruthy();
});

test("the window stops at the problem when its data does not open", async () => {
  problem.mockResolvedValue("damaged");
  render(<App />);
  expect(await screen.findByRole("alert")).toBeTruthy();
});
