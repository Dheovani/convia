import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { Formatted } from "./Formatted";

test("markup in a message is shown as the text it is, never run", () => {
  const markup = '<img src=x onerror="alert(1)"><script>alert(2)</script>';
  const { container } = render(<Formatted text={markup} onLink={() => {}} />);
  expect(container.querySelector("img, script")).toBeNull();
  expect(container.textContent).toBe(markup);
});

test("a link asks before it goes anywhere", () => {
  const onLink = vi.fn();
  render(<Formatted text="go https://convia.example now" onLink={onLink} />);
  const link = screen.getByRole("link", { name: "https://convia.example" });
  const event = new MouseEvent("click", { bubbles: true, cancelable: true });
  fireEvent(link, event);
  expect(event.defaultPrevented).toBe(true);
  expect(onLink).toHaveBeenCalledWith("https://convia.example");
});

test("the subset is drawn as its elements", () => {
  const { container } = render(
    <Formatted
      text={"**b** *i* ~~s~~ `c`\n> q\n```\npre\n```"}
      onLink={() => {}}
    />,
  );
  for (const tag of ["strong", "em", "s", "code", "blockquote", "pre"]) {
    expect(container.querySelector(tag), tag).not.toBeNull();
  }
});
