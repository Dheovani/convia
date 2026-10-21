# 0030 — Messages are written in a subset of Markdown

## Status

Accepted

## Date

2026-10-16

## Context

People format what they write, as they do on Discord, whose look Convia takes ([`README.md`](../../README.md)). A message is text from another peer, and whatever the interface does with it — render it, fetch what it names, open what it links to — another person controls.

## Decision

**A message is written in a subset of Markdown**: `**bold**`, `*italic*` and `_italic_`, `~~strikethrough~~`, `` `code` ``, fenced blocks of code, and lines quoted with `>`. Anything else is shown as the text it is.

- **It is drawn as the interface's own elements, never as HTML**: nothing a message holds is ever handed to the page as markup, so no message can run code or change the page.
- **Nothing is fetched to show a message.** An image or anything else a message names stays text, since fetching it would tell whoever serves it that the reader read it, and from where.
- **Addresses beginning `http://` or `https://` become links.** Following one opens it in the system's browser only after the person confirms, seeing the whole address first.

## Consequences

- The parser is Convia's own, small, and tested against what it must refuse, rather than a general Markdown library made safe after the fact.
- Headings, tables, lists and images are not formatting in a message; they may be added one at a time, each keeping these rules.
