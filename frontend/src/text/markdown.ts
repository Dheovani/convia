/*
The subset of Markdown a message is written in (ADR 0030), read into a tree the interface draws as its
own elements. Nothing here ever becomes markup: what is not formatting stays the text it is.
*/

export type Inline =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "link"; url: string }
  | { kind: "strong" | "em" | "strike"; children: Inline[] };

export type Block =
  | { kind: "paragraph"; lines: Inline[][] }
  | { kind: "quote"; lines: Inline[][] }
  | { kind: "code"; text: string };

const fence = "```";

export function parse(text: string): Block[] {
  const blocks: Block[] = [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    if (line.trimStart().startsWith(fence)) {
      const end = lines.findIndex(
        (l, j) => j > i && l.trimStart().startsWith(fence),
      );
      if (end > i) {
        blocks.push({ kind: "code", text: lines.slice(i + 1, end).join("\n") });
        i = end;
        continue;
      }
    }

    const quoted = /^> ?(.*)$/.exec(line);
    const kind = quoted ? "quote" : "paragraph";
    const content = parseInline(quoted ? quoted[1] : line);
    const previous = blocks.at(-1);
    if (previous && previous.kind === kind) {
      previous.lines.push(content);
    } else {
      blocks.push({ kind, lines: [content] });
    }
  }

  return blocks;
}

const link = /^https?:\/\/[^\s<>"]+/i;
// Punctuation that ends a sentence rather than an address.
const trailing = /[.,;:!?'")\]]+$/;

const delimiters = [
  { mark: "**", kind: "strong" },
  { mark: "~~", kind: "strike" },
  { mark: "*", kind: "em" },
  { mark: "_", kind: "em" },
] as const;

function isWordCharacter(c: string | undefined): boolean {
  return c !== undefined && /[\p{L}\p{N}]/u.test(c);
}

export function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  const push = (item: Inline) => {
    const last = out.at(-1);
    if (item.kind === "text" && last?.kind === "text") {
      last.text += item.text;
    } else {
      out.push(item);
    }
  };

  let i = 0;
  while (i < text.length) {
    const rest = text.slice(i);

    if (rest[0] === "\\" && rest.length > 1 && /[\\`*_~>]/.test(rest[1])) {
      push({ kind: "text", text: rest[1] });
      i += 2;
      continue;
    }

    if (rest[0] === "`") {
      const end = rest.indexOf("`", 1);
      if (end > 1) {
        push({ kind: "code", text: rest.slice(1, end) });
        i += end + 1;
        continue;
      }
    }

    const address = link.exec(rest);
    if (address && !isWordCharacter(text[i - 1])) {
      const url = address[0].replace(trailing, "");
      if (/^https?:\/\/[^/]/i.test(url)) {
        push({ kind: "link", url });
        i += url.length;
        continue;
      }
    }

    const delimiter = delimiters.find(({ mark }) => rest.startsWith(mark));
    if (delimiter) {
      const { mark, kind } = delimiter;
      const close = closing(text, i + mark.length, mark);
      // An underscore inside a word, as in snake_case, is not emphasis.
      const insideWord = mark === "_" && isWordCharacter(text[i - 1]);
      if (close > 0 && !insideWord) {
        push({
          kind,
          children: parseInline(text.slice(i + mark.length, close)),
        });
        i = close + mark.length;
        continue;
      }
    }

    push({ kind: "text", text: rest[0] });
    i += 1;
  }

  return out;
}

// closing is where mark closes what opens at from, or -1: never empty, never against a space.
function closing(text: string, from: number, mark: string): number {
  if (from >= text.length || /\s/.test(text[from])) return -1;
  for (let j = from + 1; j <= text.length - mark.length; j++) {
    if (text[j] === "\\") {
      j++;
      continue;
    }
    if (text.startsWith(mark, j) && !/\s/.test(text[j - 1])) {
      if (mark === "_" && isWordCharacter(text[j + 1])) continue;
      // Of a run of asterisks, emphasis closes with its last two.
      if (mark === "**" && text[j + 2] === "*") continue;
      if (mark === "*" && text[j + 1] === "*") {
        j++;
        continue;
      }
      return j;
    }
  }
  return -1;
}
