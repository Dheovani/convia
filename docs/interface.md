# Convia's own interface

Convia's standalone product: the page people open, sign in to, and talk in. It lives in `web/`, is built by Vite with React, TypeScript and Tailwind, and is compiled into the Convia binary and served from the same origin as the API.

It is built on the **public session surface and nothing else**. There is no privileged path from the page to the database, no internal endpoint it alone may call, and no shortcut where it acts with the first-party application's key. Everything on the screen came from a request any browser could have made with the same cookie. That is `M18-015`, and it is the only thing that makes this product evidence that the platform works.

## Running it

```bash
cd web
npm install
npm run build   # writes into internal/web/assets/dist
cd ..
go build ./cmd/convia && ./convia serve
```

Then open `http://127.0.0.1:8080`.

For working on the interface itself, `npm run dev` serves it with hot reloading and **proxies `/v1` to a Convia on port 8080**, so the browser still sees one origin. That proxy is not a convenience: without it the session cookie would be cross-site in development and same-site in production, which is the one difference that would make every cookie and CSRF decision untestable until deployment.

A binary built with `go build` alone has no interface, because the bundle needs Node. It says so at startup and answers **503** with a page naming the command that fixes it — not 404, because the page is not missing: this deployment does not have one.

## The three zones

| Zone | What it answers |
| --- | --- |
| **Rail** | where in Convia you are — chat, calls, settings |
| **Sidebar** | which conversation, and what you have not read |
| **Stage** | the conversation itself |

Calls and settings are shown and disabled rather than hidden. An interface that grows new top-level destinations as they are built teaches people that its shape is unreliable; one that shows where they will be teaches them where to look later. Each says what it is waiting for.

They are a CSS grid rather than nested boxes, because they are peers — the rail does not contain the sidebar — and a layout that says so is one a narrow screen can rearrange by changing the grid alone.

## What it talks to

Only the session surface, documented in [`messages.md`](messages.md#acting-as-yourself) and [`sessions.md`](sessions.md).

| | |
| --- | --- |
| `POST /v1/sessions` | sign in; the cookie comes back on the response |
| `GET /v1/me` | who is signed in — asked **before the first paint** |
| `GET /v1/me/rooms` | the sidebar: rooms and unread counts in one request |
| `GET/POST /v1/me/rooms/{id}/messages` | read a room, say something |
| `PUT /v1/me/rooms/{id}/read_state` | how far it has been read |
| `PATCH`/`POST …/delete` on a message | change or withdraw your own words |
| `DELETE /v1/sessions/current` | sign out |

There is **no token in the page**. The session is a cookie the script cannot read, which is what makes it survive an XSS in this very bundle — and also what means the page cannot answer "who am I" by itself. It asks. That is why `GET /v1/me` happens before anything is drawn: guessing would flash the sign-in form at somebody already signed in, on every reload.

## Three things the interface must not undo

**A failed sign-in says one thing.** Convia answers identically whether the address is unknown or the password is wrong, so that the form is not a way to find out who has an account. Wording the two differently in the client would give away exactly what the server refused to. A test asserts the message, and asserts that it never contains the words that would leak it.

**An unreachable server is not a wrong password.** A network failure and a refusal are different types in the client for this reason: telling somebody their password is wrong when the connection dropped is a lie that costs them their next ten minutes.

**What was typed survives a failure.** The composer clears only once Convia has taken the message. Clearing on submit is the common shortcut and it loses the words on every failure — and the words are the one thing on the screen that cannot be fetched again.

## It polls, and that is a gap rather than a design

The sidebar is asked again every fifteen seconds and an open room every five.

Convia has a real-time event stream. It is on the surface an **application** reaches with its key, and a person holding a session has no way to subscribe to their own rooms. So there is nothing to subscribe to yet, and polling is the honest cost of that. It is recorded as `M18-018` rather than hidden behind a wrapper that looks like a subscription.

## What is served, and how it is cached

| | Cache-Control |
| --- | --- |
| `/assets/*` | `public, max-age=31536000, immutable` |
| the page | `no-cache`, with an `ETag` |

Every asset is named by a digest of its own content, so a changed asset is a different URL and a year-long cache can never serve a stale one. The page is the one file whose name never changes and the one that names all the others, so it is revalidated every time — a cached index is how a deployment appears not to have happened.

A path the API has not claimed is the page: `/rooms/abc` is a screen, and answering 404 there is the failure people describe as "it works until you refresh". **Anything under `/v1` is never the page**, so a mistyped API call gets a refusal a client can parse rather than HTML.

## The policy

The page is served under:

```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self';
font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none';
frame-ancestors 'none'
```

`'none'` and then back up, so anything added later has to be allowed deliberately.

**There is no `unsafe-inline`**, which is the keyword that makes most policies decorative. The build emits no inline script and no inline style, which is what lets it stay out — and a test asserts the policy never grows it, because the day somebody adds a `<style>` to `index.html` the right outcome is a failing build rather than a quietly weakened policy.

Alongside it: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, and a `Permissions-Policy` that turns off what this product has no use for.

`connect-src 'self'` will have to change when calls arrive, because joining one means a WebSocket to the media server and that is a different origin. It is left alone rather than widened in advance, so that somebody decides.

## The design

Dark first, because this is a place people keep open beside their work for hours and a light surface at that size is the one they turn off. The light palette is the same roles at the same contrasts, not a second design.

Tokens are named for the job they do — `--color-surface-sunken`, `--color-ink-dim` — rather than for what they look like. A token called `--color-gray-700` has to be renamed the day it stops being gray.

They are declared in `@theme`, which is Tailwind's, so `bg-surface-deep` and `var(--color-surface-deep)` are the same value rather than two that have to be kept in step. **The light palette is a redefinition of those same variables under a media query, and there is no `dark:` variant anywhere in the interface** — every utility Tailwind generates refers to the token by `var()`, so the colour is decided once instead of at every element that uses one.

Tailwind runs as a Vite plugin and emits one static stylesheet. That is not a packaging preference: the play CDN generates styles in the browser and injects them inline, which would need `unsafe-inline` and a third-party origin in the policy below — undoing most of what it is for.

**One accent, used for two things:** the mark, and the thing that is currently true — the selected room, the focused field, the unread count. A second accent would make both of those mean less.

The mark is an open ring with a point at the opening: a conversation that is not closed, and somebody about to join it. It is drawn as SVG rather than fetched, so it inherits the current colour and depends on no origin.

Inter and Space Grotesk are the intended faces and are **not bundled**. The policy allows no third-party origin, so a webfont has to be committed to the repository rather than fetched — a decision about repository weight, not about design. The stack in the tokens is what a system actually has, in the same proportions.

## Accessibility

Not a later pass. `M18-010` is an exit criterion, and these are the parts already load-bearing:

- One focus ring, defined once, never removed. The usual `outline: none` is how interfaces lose keyboard navigation silently.
- Row actions appear on **hover and focus**. Focus is the half that is usually forgotten, and without it they cannot be reached from the keyboard at all.
- Unread badges carry their meaning in text, not only in a coloured circle — and say "1 unread message", not "1 unread messages".
- A failed sign-in is a live region, so it is heard and not only seen.
- `prefers-reduced-motion` removes every transition. Nothing here animates information.

## Known gaps

Named here rather than discovered later.

- **No real-time.** See above: there is nothing for a person to subscribe to. `M18-018`.
- **No router.** There is one screen and a selected room, and the URL does not change. It works because the server answers every path with the page, so adding a router later is additive.
- **No room creation, and no way to add anybody.** A person can read and write in the rooms they are already in. Putting somebody in a room is an application's decision today, made with a key — `M18-003` is what makes it a person's.
- **No calls.** `M18-004` through `M18-008`, and the policy change that comes with them.
- **No error boundary.** A component that throws takes the screen with it. It wants deciding alongside what a recoverable failure looks like, rather than a blank page with a generic apology.
- **The webfonts are not bundled.** See *The design*.
- **The bundle is not split.** One chunk, roughly 76 kB compressed, nearly all of it React, plus about 5 kB of stylesheet. Splitting is worth doing when there is a second screen heavy enough to defer, which calls will be.
- **Tailwind costs a little at this size.** Its reset and the utilities in use come to roughly three kilobytes more, compressed, than the hand-written CSS it replaced. That is the expected shape of the trade: a utility system pays for itself once the same spacing and colour decisions are being repeated across many screens, and this interface currently has two.
