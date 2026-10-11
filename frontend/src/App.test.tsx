import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import App from "./App";

test("the window shows Convia's mark and name", () => {
  render(<App />);
  expect(screen.getByText("Convia")).toBeTruthy();
});
