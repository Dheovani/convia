# Rules for working on Convia

These apply to people and to AI agents alike.

## 1. The README is the definition of the product

Read the whole of [`README.md`](README.md) before any work. It is not an introduction: it is what Convia is. No other document, no existing code and no habit overrides it.

## 2. Every proposal passes the README's question

> Does this solution preserve the fact that users are running applications that communicate peer-to-peer, or is it quietly turning Convia into a client-server platform?

If the answer is the second, the proposal is wrong, however familiar or convenient it is.

## 3. No decision is inferred

None of the following is decided by whoever implements. Each is decided by the product owner, explicitly, before any code that depends on it. [`ROADMAP.md`](ROADMAP.md) M02 lists the ones still open.

- the language and the interface framework — decided in [ADR 0001](docs/adr/0001-a-go-core-in-a-wails-window-for-windows.md);
- the installer, updates and code signing;
- identity and authentication between peers;
- peer discovery and NAT traversal, and any auxiliary infrastructure;
- the communication protocol between peers;
- local storage, synchronization, ordering and offline delivery;
- groups and conversations with more than two people;
- media: voice, video and screen sharing;
- cryptography.

When a piece of work depends on an item not yet decided, **stop and ask**. Do not pick a provisional option, do not implement "for now", and do not leave the choice implicit in the code.

## 4. A decision exists only once it is written down

A decision made by the product owner is recorded in [`docs/adr`](docs/adr/README.md), with its date and with what it rules out, before the implementation that depends on it. What is not recorded has not been decided. [`ROADMAP.md`](ROADMAP.md) says what is built; an ADR says why.

## 5. Everything in the repository is written in English

Documentation, decisions, code, comments, identifiers, commit messages and pull requests are written in English — always, whatever language the conversation that produced them was held in. A document found in any other language is translated, not extended.

## 6. Every change is held to the same rules

- It passes every check of [ADR 0002](docs/adr/0002-what-a-change-must-pass.md), and is approved by somebody other than its author.
- Relevant code has tests that would fail if it broke. There is no coverage percentage to meet.
- A new dependency carries a license [ADR 0003](docs/adr/0003-dependencies-carry-permissive-licenses.md) permits. Check it before adding it.
- Commits are [Conventional Commits](https://www.conventionalcommits.org/). [`CONTRIBUTING.md`](CONTRIBUTING.md) has the rest.
