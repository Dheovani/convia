import { render, screen } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";
import App from "./App";
import { answers, reset } from "./test/bridge";

vi.mock(
  "../wailsjs/go/app/Bridge",
  async () => (await import("./test/bridge")).bridge,
);

beforeEach(reset);

test("a first run welcomes the person", async () => {
  answers.StorageProblem = () => Promise.resolve("");
  answers.Me = () => Promise.resolve(null);
  render(<App />);
  expect(await screen.findByText("Welcome to Convia")).toBeTruthy();
});

test("a person with an identity sees it", async () => {
  answers.StorageProblem = () => Promise.resolve("");
  answers.Me = () =>
    Promise.resolve({ name: "Alice", publicKey: "00112233aabbccdd" });
  render(<App />);
  expect(await screen.findByText("0011 2233 aabb ccdd")).toBeTruthy();
});

test("data that does not open stops at the problem", async () => {
  answers.StorageProblem = () => Promise.resolve("damaged");
  render(<App />);
  expect(await screen.findByRole("alert")).toBeTruthy();
});
