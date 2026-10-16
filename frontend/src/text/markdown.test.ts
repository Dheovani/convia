import { describe, expect, it } from "vitest";
import { parse, parseInline, plain, type Inline } from "./markdown";

const text = (t: string): Inline => ({ kind: "text", text: t });

describe("inline formatting", () => {
  it("reads the subset", () => {
    expect(parseInline("a **b** *c* _d_ ~~e~~ `f`")).toEqual([
      text("a "),
      { kind: "strong", children: [text("b")] },
      text(" "),
      { kind: "em", children: [text("c")] },
      text(" "),
      { kind: "em", children: [text("d")] },
      text(" "),
      { kind: "strike", children: [text("e")] },
      text(" "),
      { kind: "code", text: "f" },
    ]);
  });

  it("nests, but not inside code", () => {
    expect(parseInline("**a *b***")).toEqual([
      {
        kind: "strong",
        children: [text("a "), { kind: "em", children: [text("b")] }],
      },
    ]);
    expect(parseInline("`**not bold**`")).toEqual([
      { kind: "code", text: "**not bold**" },
    ]);
  });

  it("leaves what does not close, and what is against a space, as it is", () => {
    for (const plain of [
      "2 * 3 * 4",
      "**unclosed",
      "a ** b **",
      "snake_case_name",
      "``",
      "*",
      "price: 5* off*",
    ]) {
      expect(parseInline(plain)).toEqual([text(plain)]);
    }
  });

  it("lets a backslash keep a character literal", () => {
    expect(parseInline(String.raw`\*not em\*`)).toEqual([text("*not em*")]);
  });

  it("links only http and https addresses, without the punctuation after them", () => {
    expect(parseInline("see https://convia.example/a?b=1, then.")).toEqual([
      text("see "),
      { kind: "link", url: "https://convia.example/a?b=1" },
      text(", then."),
    ]);
    expect(parseInline("(http://x.example)")).toEqual([
      text("("),
      { kind: "link", url: "http://x.example" },
      text(")"),
    ]);
    for (const not of [
      "javascript:alert(1)",
      "data:text/html,<b>",
      "file:///C:/Windows",
      "https://",
      "xhttps://convia.example",
      "ftp://convia.example",
    ]) {
      expect(
        parseInline(not).every((i) => i.kind === "text"),
        not,
      ).toBe(true);
    }
  });

  it("keeps markup as the text it is", () => {
    const markup = '<img src=x onerror="alert(1)"><script>alert(2)</script>';
    expect(parseInline(markup)).toEqual([text(markup)]);
  });
});

describe("blocks", () => {
  it("reads quotes, fenced code and lines", () => {
    expect(
      parse(
        "> said\n> twice\nanswer\nsecond line\n```\nif (a) **b**\n```\nend",
      ),
    ).toEqual([
      { kind: "quote", lines: [[text("said")], [text("twice")]] },
      { kind: "paragraph", lines: [[text("answer")], [text("second line")]] },
      { kind: "code", text: "if (a) **b**" },
      { kind: "paragraph", lines: [[text("end")]] },
    ]);
  });

  it("leaves a fence that never closes as text", () => {
    expect(parse("```\nopen")).toEqual([
      { kind: "paragraph", lines: [[text("```")], [text("open")]] },
    ]);
  });

  it("never fails, whatever it is given", () => {
    const pieces = [
      "*",
      "**",
      "_",
      "~~",
      "`",
      "```",
      "\\",
      "> ",
      "\n",
      "a",
      " ",
      "https://a.b",
      "<",
      "é",
    ];
    let seed = 7;
    for (let n = 0; n < 2000; n++) {
      let s = "";
      for (let k = 0; k < 20; k++) {
        seed = (seed * 1103515245 + 12345) % 2147483648;
        s += pieces[seed % pieces.length];
      }
      expect(() => parse(s)).not.toThrow();
    }
  });
});

describe("a preview", () => {
  it("is the text without its formatting, on one line", () => {
    expect(
      plain(
        "**negrito** *itálico* e link: https://convia.example\n> citado\n```\ncódigo\n```",
      ),
    ).toBe("negrito itálico e link: https://convia.example citado código");
    expect(plain("2 * 3 = 6")).toBe("2 * 3 = 6");
  });
});
