# Convia

Convia is a **peer-to-peer (P2P) desktop communication application**.

That sentence must be read literally. Convia is not, at its core, a web communication service with a desktop client. It is not a central backend that different clients connect to. It is not a hosted platform whose desktop application works merely as an interface to remote infrastructure.

**Convia is the application itself, running on the user's machine.**

Each user runs their own instance of Convia on their computer. When two users want to communicate, the Convia instances running on their respective machines establish a connection with each other and communicate directly between the machines.

In its conceptually simplest form:

```text
Alice's computer                        Bob's computer

┌─────────────────┐                     ┌─────────────────┐
│     Convia      │◄────── P2P ───────►│     Convia      │
└─────────────────┘                     └─────────────────┘
```

The fundamental element of the architecture is therefore **peer ↔ peer**, not **client ↔ server**.

# What it means to be a local application

Convia is installed and runs locally.

The program the user opens contains the logic needed to represent that user on the Convia network, manage their conversations, keep their local state and communicate with other instances of the application.

The local application must not be understood as a mere frontend for a "Convia server". The user's machine is an active participant in the system.

When Alice uses Convia, there is an instance of the program running on Alice's computer. When Bob uses Convia, there is another, independent instance running on Bob's computer.

When Alice talks to Bob, the system must think first of:

```text
Alice's Convia ↔ Bob's Convia
```

and not of:

```text
Alice's Convia → Convia server ← Bob's Convia
```

This distinction is essential to understanding the product.

# What peer-to-peer means in Convia

The goal of Convia is to let users' machines establish connections with each other in order to communicate.

A peer is an instance of Convia belonging to a user.

So Alice is not merely "an account stored on a server". Alice's instance takes part in the network directly. The same is true of Bob.

When an interaction between the two begins, the software must be able to make the two peers find each other, establish a connection, authenticate each other's identity according to the model that is yet to be defined, and exchange the data the communication needs.

Communication between these peers is the core of the system.

That chiefly includes real-time communication.

# The comparison with Signal

Signal is a useful reference for understanding part of the idea of Convia, because it represents an application for private communication between people.

Convia does not, however, intend simply to reproduce Signal's user experience.

The comparison is mainly about the idea of an application for communication between users, not about adopting Signal's architecture, protocol, infrastructure or account model wholesale.

The interface and the set of features intended for Convia are closer to a rich desktop communication application.

# The comparison with Discord

Convia's interface should follow an organization similar to Discord's.

This means the application must not be conceived merely as a simple list of contacts followed by a message window, as in many traditional messengers.

Convia should provide a rich desktop communication experience, in which the user can move between conversations and other features while staying inside a single visual workspace.

The reference to Discord is mainly a reference for **experience and the organization of the interface**, not for its backend architecture.

Discord is fundamentally a centralized platform.

Convia is fundamentally P2P.

Therefore:

```text
User experience:
Discord is an important reference.

Communication architecture:
Discord is NOT a reference for Convia's core.
```

# Conversation

One of Convia's fundamental functions is to let users converse.

Messages must be part of a conversation that persists from the application's point of view, not an ephemeral sequence of HTTP requests against some central server.

When Alice opens a conversation with Bob, the interface shows the relevant history of that conversation and lets new messages be exchanged between them.

When both are connected, messages can be transmitted in real time between their machines.

Exactly how messages are stored locally, synchronized, delivered when a peer is offline and reconciled across several devices still has to be defined explicitly before it is implemented.

These decisions must not be invented by the implementation.

# Real-time communication

Convia is not only an asynchronous messaging application.

An important characteristic of the product is real-time communication between peers.

This means the connection established between users must be able to serve interactive features that require low latency.

Convia's architecture must therefore be designed from the start to support continuous communication between machines, not just isolated calls to an API.

# Screen sharing

Convia must let one user share their screen with another.

Screen sharing is one of the application's native communication features.

This means that, during a communication session between Alice and Bob, Alice must be able to start sharing her screen and Bob must be able to watch that stream inside Convia itself.

This feature must respect the P2P nature of the product.

It must not be conceived, by default, as "Alice sends her screen to a Convia server and Bob receives it from that server".

The first conceptual model must be:

```text
Alice captures her screen
        │
        ▼
Alice's Convia
        │
        │ P2P communication
        ▼
Bob's Convia
        │
        ▼
Bob watches the stream
```

The future existence of auxiliary infrastructure for connectivity does not change this principle.

# Convia is not a centralized SaaS platform

This is an important conceptual constraint.

Convia must not be reinterpreted as a communication SaaS platform.

There is no conceptual entity called "the Convia server" that owns everybody's conversations, as a fundamental requirement of the product.

Nor is there, as a fundamental requirement, a central backend responsible for storing every message, creating every room, authenticating every person, ordering every message and mediating every communication.

Building that system would mean building a different product.

# Convia is not a federation of personal servers

It is also important to distinguish P2P from a federated architecture.

Running a small HTTP server next to each user and having those servers talk to each other over the Internet does not automatically turn the system into the intended model.

For example:

```text
Alice's desktop
       │
       ▼
Alice's server
       │
       ▼
Bob's server
       │
       ▼
Bob's desktop
```

is not simply another way of drawing the original architecture.

In that model, the main element becomes a federation of server installations.

Convia must be thought of first in terms of the users' own applications as peers:

```text
Alice's application ↔ Bob's application
```

Any auxiliary local process must exist only because it is an internal implementation detail of the application, not because each user has started operating their own web platform.

# Convia must not depend on heavy infrastructure to exist locally

The user should not need to operate server infrastructure to be able to use the application.

Installing Convia must not mean installing and administering PostgreSQL, Redis, a media server, a reverse proxy and other production services.

The application must ship the local dependencies it really needs as part of its distribution.

If it needs to persist information on the machine, the persistence must suit the nature of a desktop application.

If it needs to keep secrets, it must use the appropriate mechanisms for secure local storage.

If it needs to keep state in memory, that belongs to the local process.

Installing it should feel like installing an application, not like deploying a server stack.

# Auxiliary infrastructure is not necessarily centralization

Being P2P does not necessarily mean that no server at all may exist anywhere in the ecosystem.

This point must be handled precisely.

Depending on future protocol and connectivity decisions, there may be auxiliary services whose only function is to help peers find each other or establish a connection.

For example, signaling, rendezvous, discovery, STUN or TURN mechanisms may eventually be needed.

Using something of that kind would not automatically change Convia's P2P character.

The fundamental distinction is who is responsible for the communication and for the product's state.

A service that only helps Alice and Bob establish a connection is conceptually very different from a server that becomes the owner of Alice and Bob's conversation.

So, if auxiliary infrastructure is needed, it must be treated as **connectivity infrastructure**, not as the logical center of the product.

Which auxiliary services will exist must still be defined explicitly.

# Authority of the peers

An important consequence of Convia's P2P nature is that a single global authority determining the whole state of the system must not be presumed.

When Alice talks to Bob, there are at least two independent participants, each with its own storage and execution.

Questions such as identity, message ordering, delivery confirmation, history, editing, deletion, conflicts, reconnection and synchronization must be defined according to that reality.

These questions must not be resolved implicitly by adding a central database merely because it simplifies the implementation.

When a feature really does require a specific authority, that authority must be defined deliberately by the protocol.

# The interface and the network core are different things

Convia has two distinct concerns.

One of them is the user experience.

The other is the mechanism through which peers communicate.

The interface can look and behave like Discord without the protocol or the architecture bearing any resemblance to Discord's architecture.

Likewise, the technologies used to build the desktop interface must not determine the architecture of the network.

The right question for every feature is first:

```text
How do two instances of Convia do this between themselves?
```

Only afterwards should the way the interface presents that feature be chosen.

# The role of the local backend

If the application uses Go or another language to implement its native logic, that code must be understood as the **local core of the desktop application**.

It may manage networking, cryptography, storage, the operating system, media capture, the P2P protocol and other local responsibilities.

It must not be treated automatically as a "web backend" separate from the product.

In other words:

```text
Window frontend
        │
        ▼
local core of the application
        │
        ▼
P2P network
```

is compatible with Convia.

Turning that local core into an independent web service that the application merely consumes is a different architectural decision, and must not be presumed.

# Fundamental principle

Every architectural decision in Convia must pass a simple question:

**Does this solution preserve the fact that users are running applications that communicate peer-to-peer, or is it quietly turning Convia into a client-server platform?**

If the solution creates a central server responsible for the conversations merely because that is more familiar or convenient, the solution violates the fundamental idea of the product.

# Short definition

Convia is a P2P desktop communication application with a user experience inspired by Discord.

Each user runs Convia on their own machine. The instances of the application establish connections with each other to allow real-time communication, messaging and additional features such as screen sharing.

The application must work as a complete local program, not as a mere frontend of a centralized service.

External infrastructure may exist when it is strictly necessary to make discovery or connectivity between peers possible, but it must not replace the peers as the core of the system, nor take ownership of the conversations or their state by default.

Decisions about identity, discovery, storage, synchronization, offline delivery, groups, media and NAT traversal must be specified explicitly before implementation, and never inferred by the team or by AI agents.

# License

Convia is source-available, not open source. It is licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE.md).

- **Free for noncommercial use.** Personal use, hobby projects, private study, experimentation, and use by charities, schools, public research organizations, and government institutions are all permitted. Use it, modify it, and share your changes.
- **Commercial rights are reserved.** Using Convia in or for a commercial product or service requires a separate license from the copyright holder. Open a discussion in the repository to request one.
