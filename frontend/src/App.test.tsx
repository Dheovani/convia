import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import App from "./App";

test("the window shows the application's name", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: "Convia" })).toBeTruthy();
});
