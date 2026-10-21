import { Fragment } from "react";
import { parse, type Inline } from "./markdown";

// Formatted draws a message's text in the subset of ADR 0030, as the interface's own elements.
export function Formatted({
  text,
  onLink,
}: {
  text: string;
  onLink: (url: string) => void;
}) {
  return (
    <>
      {parse(text).map((block, i) => {
        switch (block.kind) {
          case "code":
            return (
              <pre
                key={i}
                className="my-1 overflow-x-auto rounded-sm bg-surface-sunken p-2 font-mono text-[0.82rem]"
              >
                <code>{block.text}</code>
              </pre>
            );
          case "quote":
            return (
              <blockquote
                key={i}
                className="my-0.5 border-l-4 border-line-strong pl-2 text-ink-dim"
              >
                <Lines lines={block.lines} onLink={onLink} />
              </blockquote>
            );
          case "paragraph":
            return (
              <p key={i}>
                <Lines lines={block.lines} onLink={onLink} />
              </p>
            );
        }
      })}
    </>
  );
}

function Lines({
  lines,
  onLink,
}: {
  lines: Inline[][];
  onLink: (url: string) => void;
}) {
  return lines.map((line, i) => (
    <Fragment key={i}>
      {i > 0 && <br />}
      <Inlines items={line} onLink={onLink} />
    </Fragment>
  ));
}

function Inlines({
  items,
  onLink,
}: {
  items: Inline[];
  onLink: (url: string) => void;
}) {
  return items.map((item, i) => {
    switch (item.kind) {
      case "text":
        return <Fragment key={i}>{item.text}</Fragment>;
      case "code":
        return (
          <code
            key={i}
            className="rounded-sm bg-surface-sunken px-1 font-mono text-[0.85em]"
          >
            {item.text}
          </code>
        );
      case "link":
        return (
          <a
            key={i}
            href={item.url}
            className="break-all text-accent-ink underline"
            onClick={(event) => {
              event.preventDefault();
              onLink(item.url);
            }}
          >
            {item.url}
          </a>
        );
      case "strong":
        return (
          <strong key={i}>
            <Inlines items={item.children} onLink={onLink} />
          </strong>
        );
      case "em":
        return (
          <em key={i}>
            <Inlines items={item.children} onLink={onLink} />
          </em>
        );
      case "strike":
        return (
          <s key={i}>
            <Inlines items={item.children} onLink={onLink} />
          </s>
        );
    }
  });
}
