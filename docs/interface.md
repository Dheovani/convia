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
| `POST /v1/accounts` | create an account and sign in; the cookie comes back on the response |
| `POST /v1/sessions` | sign in; the cookie comes back on the response |
| `GET /v1/me` | who is signed in — asked **before the first paint** |
| `GET /v1/me/rooms` | the sidebar: rooms and unread counts in one request |
| `GET/POST /v1/me/rooms/{id}/messages` | read a room, say something |
| `PUT /v1/me/rooms/{id}/read_state` | how far it has been read |
| `PATCH`/`POST …/delete` on a message | change or withdraw your own words |
| `POST /v1/me/rooms` | open a room, with a name and nothing else |
| `GET /v1/me/rooms/{id}/members` | who is here, by name — also how speakers are named |
| `GET /v1/me/people` | who could be added |
| `PUT /v1/me/rooms/{id}/members/{user_id}` | add somebody |
| `POST /v1/me/rooms/{id}/leave` | leave |
| `POST /v1/me/rooms/{id}/invitations` | invite a handle, on any installation |
| `POST /v1/me/invitation-previews` | what an invitation link is for |
| `GET/POST /v1/me/remote-rooms` | rooms elsewhere; join one by link |
| `…/me/remote-rooms/{id}/…` | a room elsewhere, with the same operations as a room here |
| `DELETE /v1/sessions/current` | sign out |

There is **no token in the page**. The session is a cookie the script cannot read, which is what makes it survive an XSS in this very bundle — and also what means the page cannot answer "who am I" by itself. It asks. That is why `GET /v1/me` happens before anything is drawn: guessing would flash the sign-in form at somebody already signed in, on every reload.

## Three things the interface must not undo

**A failed sign-in says one thing.** Convia answers identically whether the username is unknown or the password is wrong. Wording the two differently in the client would give away exactly what the server refused to. A test asserts the message, and asserts that it never contains the words that would leak it. The form never repeats the server's own prose either: the status decides the words.

**Creating an account warns before, not after.** The password seals the account's key, so a forgotten one cannot be reset by anybody. The registration form says so above the button, checks the username's rule, the password's length and the confirmation before sending anything, and a taken username keeps what was typed.

**An unreachable server is not a wrong password.** A network failure and a refusal are different types in the client for this reason: telling somebody their password is wrong when the connection dropped is a lie that costs them their next ten minutes.

**What was typed survives a failure.** The composer clears only once Convia has taken the message. Clearing on submit is the common shortcut and it loses the words on every failure — and the words are the one thing on the screen that cannot be fetched again.

## Who a person can add, and who they can invite

The people panel lists everybody Convia lists under `GET /v1/me/people` who is not already in the room, as soon as it opens and in a section that folds away, and offers **no field to add anybody else by typing**. That is the client half of a rule the server makes: a person *adds* only somebody they already share a room with, because a lookup by address would confirm who has an account. See [`rooms.md`](rooms.md#acting-as-yourself).

Anybody else is **invited by handle**, and that field is safe to have because a handle is not a lookup. Typing one makes an invitation and a link, and says nothing about whether the handle names an account anywhere. The link works only for the key the handle's identifier is the fingerprint of. The panel shows the link to send and warns when it names `localhost`, which nobody on another machine can follow. See [`peers.md`](peers.md).

A link somebody was sent is pasted into **Join with a link** in the sidebar, which shows the room, the inviter and the installation it lives on before anything is accepted. Rooms on other installations are listed apart, under **Elsewhere**, with where they live. They are read through this installation on a timer, have no unread count, and offer no way to add or invite anybody, because that belongs to the room's home.

When somebody cannot be added, the panel says one sentence. Convia gives one answer for a stranger, an identifier that names nobody, and somebody suspended, and wording them differently here would be inventing the distinction the server refused to make.

**Only the owner of a room sees how to moderate it**, because Convia refuses everybody else and a button that could only ever fail is worse than no button ([ADR 0013](adr/0013-a-room-a-person-opens-has-an-owner.md)). The owner is marked in the people panel. For the owner, every other member has **Remove** and **Ban**, and a folded **Banned** section lists who is banned with **Unban**. The conversation header gains **Room**, with **Rename**, **Close** or **Reopen**, and **Delete room**, which asks first. The owner can **Remove** anybody else's message, and its tombstone reads *Removed by the room's owner.*, where a message its author withdrew reads *This message was withdrawn.*

Leaving asks first, because getting back in needs somebody still inside. An owner who leaves passes the room on.

Speakers are named from the member list, which is read when a conversation opens rather than polled. A message from somebody the list does not know asks for it again, once per person: that is how somebody added a moment ago gets a name, and the once is what keeps an author who has since left from turning into a request loop.

## It is told, and asks only when it cannot be

The workspace holds one connection to `GET /v1/me/events` for the whole page, and the sidebar, the open room, and its member list all listen to it. One rather than one each, because every connection is a place against a ceiling Convia keeps per person.

While the stream is open **nothing asks on a timer.** Each event is a read of exactly what it names:

| Event | What is read |
| --- | --- |
| `message.posted` in the open room | what is newer than the newest message held |
| `message.edited`, `message.deleted` in the open room | that one message, by its sequence |
| `room.member_*` for the open room | its member list |
| anything about any room | the sidebar, gathered over a quarter of a second so a burst is one read |

Losing one's own place is acted on before the read: the room leaves the sidebar at once, and if it was open, nothing stays open that the person can no longer read. Marking a room read also reads the sidebar again, because that is when the room's badge changed and no event says so.

**While the stream is not open, the interface asks as it did before there was one** — the sidebar every fifteen seconds, an open room every five. Being told is an improvement on asking, never a replacement for being able to ask. When the stream opens again, what happened meanwhile was announced to nobody, so the sidebar is read, the open room reads forward for what is new, and then its newest window again for what was edited or withdrawn.

It reconnects after a second, doubling to half a minute, so that every tab of every person does not reconnect at once into a Convia that is still down.

**A refused handshake says nothing about why.** A browser hides the status of a failed WebSocket upgrade from scripts, so an expired session and an unreachable server look the same, and the interface does not guess: it keeps retrying, falls back to its timers, and the next ordinary request is what notices a session that is gone. The one reason Convia can still give is `4001`, sent on a stream that was open when its session ended; the interface asks `/v1/me` to confirm, and returns to the sign-in form.

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

`connect-src 'self'` already admits the event stream, which is a WebSocket to this same host. It will have to change when calls arrive, because joining one means a WebSocket to the media server and that is a different origin. It is left alone rather than widened in advance, so that somebody decides.

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

- **Being told lags by up to a minute at the edges.** Convia rechecks a person's stream every minute, so a room just left elsewhere, or a session just ended, can still produce events within that minute. What arrives is identifiers, and every read they cause asks for membership again. See [`events.md`](events.md#a-persons-stream).
- **Unread counts for rooms that are not open arrive with the sidebar's read**, which an event triggers. There is no event for a read state, because marking read is the person's own act, so a second tab of the same person learns it only when something else changes the sidebar.
- **No router.** There is one screen and a selected room, and the URL does not change. It works because the server answers every path with the page, so adding a router later is additive.
- **The lists of people are one page.** Somebody who shares rooms with more than a hundred people sees the first hundred. Paging wants a screen where it matters.
- **Opening a room twice opens two rooms.** The session surface has no `Idempotency-Key`, so the button is disabled while a request is in flight and that is the whole of the protection.
- **No calls.** `M18-004` through `M18-008`, and the policy change that comes with them.
- **No error boundary.** A component that throws takes the screen with it. It wants deciding alongside what a recoverable failure looks like, rather than a blank page with a generic apology.
- **The webfonts are not bundled.** See *The design*.
- **The bundle is not split.** One chunk, roughly 76 kB compressed, nearly all of it React, plus about 5 kB of stylesheet. Splitting is worth doing when there is a second screen heavy enough to defer, which calls will be.
- **Tailwind costs a little at this size.** Its reset and the utilities in use come to roughly three kilobytes more, compressed, than the hand-written CSS it replaced. That is the expected shape of the trade: a utility system pays for itself once the same spacing and colour decisions are being repeated across many screens, and this interface currently has two.
